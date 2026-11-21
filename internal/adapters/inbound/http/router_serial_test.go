package http_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	ahttp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/http"
	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/memory"
	"github.com/IQVO/amr-fleet-control/internal/application/ports"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/config"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// gateDownlink records the wire sequence of every command and holds the FIRST order until
// release is closed: the after-commit hook of the leg's unit is still running while the test
// starts a cancellation and the link worker's Tick.
type gateDownlink struct {
	mu      sync.Mutex
	got     []string
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (g *gateDownlink) SendOrder(_ context.Context, o vda.Order) error {
	g.once.Do(func() {
		close(g.entered)
		<-g.release
	})
	g.add("order " + o.OrderID)
	return nil
}

func (g *gateDownlink) SendInstantActions(_ context.Context, a vda.InstantActions) error {
	for _, x := range a.Actions {
		g.add(x.Type + " " + x.ID)
	}
	return nil
}

func (g *gateDownlink) add(s string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.got = append(g.got, s)
}

func (g *gateDownlink) of(legID string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, s := range g.got {
		if strings.Contains(s, legID) {
			out = append(out, s)
		}
	}
	return out
}

// hooksAfterCommit is a unit of work that, like Postgres, runs its AfterCommit hooks only after
// the commit released the store (the memory unit of work runs them while it still holds it, which
// would hide an after-commit command leaving the serial lock).
type hooksAfterCommit struct{ inner ports.UnitOfWork }

type hacKey struct{}

type hacFrame struct {
	hooks []func()
	done  bool // set by the unit's own goroutine once its commit returned
}

func (u *hooksAfterCommit) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if f, ok := ctx.Value(hacKey{}).(*hacFrame); ok && !f.done {
		return u.inner.Do(ctx, fn)
	}
	f := &hacFrame{}
	err := u.inner.Do(context.WithValue(ctx, hacKey{}, f), fn)
	f.done = true
	if err != nil {
		return err
	}
	for _, h := range f.hooks {
		h()
	}
	return nil
}

func (u *hooksAfterCommit) AfterCommit(ctx context.Context, fn func()) {
	if f, ok := ctx.Value(hacKey{}).(*hacFrame); ok && !f.done {
		f.hooks = append(f.hooks, fn)
		return
	}
	fn()
}

// newGatedAPI is newAPI with one online robot R1 at P1, down as the Downlink and a Postgres-like
// unit of work; the router is wired exactly as cmd/api wires it: the middleware over
// svc.UnitOfWork() (ruling P3-R10). inside wraps the handlers INSIDE the middleware's unit.
func newGatedAPI(t *testing.T, down ports.Downlink, inside func(http.Handler) http.Handler) (http.Handler, *usecases.Service) {
	t.Helper()
	m, err := config.LoadMap("../../../../config/map.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := memory.NewStore()
	clock, uow := memory.NewClock(t0), &hooksAfterCommit{inner: memory.NewUnitOfWork(s)}
	svc, err := usecases.NewService(m, usecases.DefaultConfig(), usecases.Deps{UoW: uow, Fleets: memory.NewFleets(s),
		Robots: memory.NewRobots(s), Missions: memory.NewMissions(s), Sink: memory.NewEvents(s), Downlink: down, Clock: clock,
		IDs: memory.NewIDs("u"), Metrics: memory.NewMetrics(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	id := robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}
	n, _ := m.Node("P1")
	if err := svc.HandleConnection(context.Background(), vda.Connection{Robot: id, State: robot.ConnectionOnline, At: t0}); err != nil {
		t.Fatal(err)
	}
	if err := svc.HandleState(context.Background(), vda.State{Robot: id, Report: robot.Report{At: t0, LastNodeID: "P1", X: n.X, Y: n.Y,
		MapID: m.MapID(), Battery: 90}}); err != nil {
		t.Fatal(err)
	}
	idem := ahttp.NewIdempotency(memory.NewIdempotency(s), svc.UnitOfWork(), clock)
	mw := func(next http.Handler) http.Handler { return idem.Middleware(inside(next)) }
	return ahttp.NewRouter(&ahttp.Server{Service: svc, Idempotency: mw}, nil), svc
}

func postKeyed(h http.Handler, path, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// awaitOrDump waits for n results on done; on the deadline it dumps every goroutine and fails
// with what it did observe.
func awaitOrDump(t *testing.T, done <-chan string, n int, observed func() string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for i := 0; i < n; i++ {
		select {
		case r := <-done:
			if r != "" {
				t.Fatal(r)
			}
		case <-deadline:
			_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
			t.Fatalf("DEADLOCK: %d of %d units finished; observed %s", i, n, observed())
		}
	}
}

// carry (Task 14 brief amendment, ruling P3-R10 amended): through the REAL router and
// Idempotency middleware, an HTTP RequestLeg whose after-commit order is still being handed to
// the Downlink runs concurrently with the link worker's Tick and an HTTP cancellation of the
// same leg. Nothing deadlocks, and the wire carries the order first, then its cancelOrder.
func TestAnHTTPLegConcurrentWithTickAndACancelKeepsCommitOrderOnTheWire(t *testing.T) {
	gate := &gateDownlink{entered: make(chan struct{}), release: make(chan struct{})}
	h, svc := newGatedAPI(t, gate, func(next http.Handler) http.Handler { return next })
	accepted := make(chan *httptest.ResponseRecorder, 1)
	go func() { accepted <- postKeyed(h, "/fleets/AMR1/legs", legBody, "k1") }()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the leg's order never reached the Downlink")
	}
	open, err := svc.OpenLegs(context.Background(), "AMR1")
	if err != nil || len(open) != 1 {
		t.Fatalf("open legs while the order hook runs = %v, %v", open, err)
	}
	legID := open[0].LegID
	done := make(chan string, 4)
	for i := 0; i < 3; i++ {
		go func() {
			if err := svc.Tick(context.Background()); err != nil {
				done <- "tick: " + err.Error()
				return
			}
			done <- ""
		}()
	}
	cancelled := make(chan *httptest.ResponseRecorder, 1)
	go func() { cancelled <- postKeyed(h, "/fleets/AMR1/legs/"+legID+"/cancellation", "", "c1"); done <- "" }()
	time.Sleep(100 * time.Millisecond) // a unit that is not serialized behind the hook overtakes it here
	close(gate.release)
	awaitOrDump(t, done, 4, func() string {
		return fmt.Sprintf("wire %v (HTTP RequestLeg hook vs HTTP CancelLeg vs Tick)", gate.of(legID))
	})
	if rec := <-accepted; rec.Code != http.StatusAccepted || decode(t, rec)["leg_id"] != legID {
		t.Fatalf("POST leg = %d %s", rec.Code, rec.Body)
	}
	if rec := <-cancelled; rec.Code != http.StatusAccepted || decode(t, rec)["status"] != "CANCEL_REQUESTED" {
		t.Fatalf("POST cancellation = %d %s", rec.Code, rec.Body)
	}
	if got := gate.of(legID); len(got) != 2 || got[0] != "order "+legID || got[1] != "cancelOrder "+legID+"-cancel" {
		t.Fatalf("wire = %v, want the order of %s then its cancelOrder", got, legID)
	}
}

// carry (Task 14 brief amendment, ruling P3-R10 amended): the middleware's unit of work is the
// OUTERMOST one of a POST. While it runs (here: paused before the handler, standing for the
// middleware's own work), the link worker's Tick must wait for it, never take the serial lock
// first and then wait for the unit's transaction (the ABBA deadlock of a raw unit of work).
func TestAnHTTPLegConcurrentWithTheTickDoesNotDeadlock(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	pause := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() {
				close(entered)
				<-release
			})
			next.ServeHTTP(w, r)
		})
	}
	down := memory.NewDownlink()
	h, svc := newGatedAPI(t, down, pause)
	accepted := make(chan *httptest.ResponseRecorder, 1)
	done := make(chan string, 2)
	go func() { accepted <- postKeyed(h, "/fleets/AMR1/legs", legBody, "k1"); done <- "" }()
	<-entered
	go func() {
		if err := svc.Tick(context.Background()); err != nil {
			done <- "tick: " + err.Error()
			return
		}
		done <- ""
	}()
	time.Sleep(50 * time.Millisecond) // the Tick reaches the serial lock while the POST's unit is open
	close(release)
	awaitOrDump(t, done, 2, func() string { return fmt.Sprintf("%d orders sent (HTTP RequestLeg unit vs Tick)", len(down.Orders())) })
	if rec := <-accepted; rec.Code != http.StatusAccepted {
		t.Fatalf("POST leg = %d %s", rec.Code, rec.Body)
	}
	if n := len(down.Orders()); n != 1 {
		t.Fatalf("orders sent = %d, want 1", n)
	}
}

// carry (Task 14 review): a duplicate (same key, same body) that passed the first Lookup while the
// original held the serial lock replays the original's 202 once it committed; it never re-runs
// RequestLeg (which would answer the RETRYABLE 422 container-busy).
func TestAConcurrentDuplicateOfASuccessReplaysTheAnswer(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	pause := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() { close(entered); <-release })
			next.ServeHTTP(w, r)
		})
	}
	h, _ := newGatedAPI(t, memory.NewDownlink(), pause)
	first := make(chan int, 1)
	go func() { first <- postKeyed(h, "/fleets/AMR1/legs", legBody, "k1").Code }()
	<-entered
	second := make(chan [3]string, 1)
	go func() {
		rec := postKeyed(h, "/fleets/AMR1/legs", legBody, "k1")
		second <- [3]string{http.StatusText(rec.Code), rec.Header().Get("Idempotency-Replayed"), rec.Body.String()}
	}()
	time.Sleep(50 * time.Millisecond) // the duplicate passed Lookup and waits on the serial lock
	close(release)
	if c := <-first; c != http.StatusAccepted {
		t.Fatalf("first = %d", c)
	}
	got := <-second
	if got[0] != http.StatusText(http.StatusAccepted) || got[1] != "true" {
		t.Fatalf("concurrent duplicate = %s replayed=%q %s, want the replayed 202", got[0], got[1], got[2])
	}
}

// carry (Task 14 review): a duplicate cancellation of a QUEUED leg waiting on the serial lock
// replays the original's 200; it never re-runs CancelLeg (which would answer 409 leg-ended, a
// refusal plan 04 treats as terminal although the leg WAS cancelled).
func TestAConcurrentDuplicateOfAnImmediateCancelReplaysTheAnswer(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	pause := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/cancellation") {
				once.Do(func() { close(entered); <-release })
			}
			next.ServeHTTP(w, r)
		})
	}
	h, _ := newGatedAPI(t, memory.NewDownlink(), pause)
	_ = postKeyed(h, "/fleets/AMR1/legs", legBody, "k1") // C1 takes R1
	q := postKeyed(h, "/fleets/AMR1/legs", strings.Replace(legBody, "C1", "C2", 1), "k2")
	queued := decode(t, q)["leg_id"].(string)
	if decode(t, q)["status"] != "QUEUED" {
		t.Fatalf("second leg = %s", q.Body)
	}
	path := "/fleets/AMR1/legs/" + queued + "/cancellation"
	first := make(chan int, 1)
	go func() { first <- postKeyed(h, path, "", "c1").Code }()
	<-entered
	second := make(chan [3]string, 1)
	go func() {
		rec := postKeyed(h, path, "", "c1")
		second <- [3]string{http.StatusText(rec.Code), rec.Header().Get("Idempotency-Replayed"), rec.Body.String()}
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	if c := <-first; c != http.StatusOK {
		t.Fatalf("first cancel = %d", c)
	}
	got := <-second
	if got[0] != http.StatusText(http.StatusOK) || got[1] != "true" {
		t.Fatalf("concurrent duplicate cancel = %s replayed=%q %s, want the replayed 200", got[0], got[1], got[2])
	}
}
