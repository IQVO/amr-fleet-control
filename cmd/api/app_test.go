package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	inboundhttp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/http"
)

// journal records what the shutdown did, in order.
type journal struct {
	mu  sync.Mutex
	got []string
}

func (e *journal) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, s)
}

func (e *journal) list() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.got...)
}

func readyzOf(rd *inboundhttp.Readiness) int {
	rec := httptest.NewRecorder()
	inboundhttp.NewRouter(&inboundhttp.Server{Readiness: rd}, slog.New(slog.DiscardHandler)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	return rec.Code
}

// testApp is an app with no infrastructure: a NotFound server, the given loops and the three
// closers of the real wiring, journalled.
func testApp(ev *journal, handler http.Handler, loops ...func(context.Context)) *app {
	if handler == nil {
		handler = http.NotFoundHandler()
	}
	return &app{log: slog.New(slog.DiscardHandler), readiness: inboundhttp.NewReadiness(time.Second, time.Second),
		server: &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}, loops: loops,
		closers: []func(){func() { ev.add("disconnect link") }, func() { ev.add("close sink") }, func() { ev.add("close pool") }}}
}

func runAsync(a *app, ctx context.Context, ln net.Listener) chan error {
	done := make(chan error, 1)
	go func() { done <- a.run(ctx, ln) }()
	return done
}

// waitRun is run's result, or a failure with every goroutine's stack when it has not returned
// within 15 s (a loop that never saw the cancel).
func waitRun(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
		t.Fatal("run did not return within 15 s of the shutdown")
		return nil
	}
}

// lesson (plan 04 Task 19 review, finding 2): every loop (the link's worker/tick/sender, the relay,
// the purger) has returned before the link disconnects and the sink and the pool close, in that
// order; kills "closers before the loops' Wait" and "no Wait".
func TestTheLoopsStopBeforeTheLinkTheSinkAndThePoolClose(t *testing.T) {
	ev := &journal{}
	a := testApp(ev, nil, func(ctx context.Context) {
		<-ctx.Done()
		time.Sleep(100 * time.Millisecond) // a pass finishing after the cancel
		ev.add("loop stopped")
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(a, ctx, listen(t))
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := waitRun(t, done); err != nil {
		t.Fatal(err)
	}
	a.close() // what main's defer and Task 17's cleanup do: a no-op after run
	want := "loop stopped,disconnect link,close sink,close pool"
	if got := strings.Join(ev.list(), ","); got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
}

// lesson (plan 04 Task 19 review, finding 3): readiness reads not-ready before any loop sees the
// cancel, and an in-flight request still sees it while HTTP drains; kills "no SetNotReady" and a
// flip after the loops were told to stop.
func TestReadinessFlipsBeforeTheLoopsStopAndStaysOffWhileHTTPDrains(t *testing.T) {
	ev := &journal{}
	rd := inboundhttp.NewReadiness(time.Second, time.Second)
	inFlight, status := make(chan struct{}), make(chan int, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(inFlight)
		code := 0
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if code = readyzOf(rd); code == http.StatusServiceUnavailable {
				break
			}
		}
		status <- code
		w.WriteHeader(http.StatusOK)
	})
	a := testApp(ev, handler)
	a.readiness = rd
	a.loops = []func(context.Context){func(ctx context.Context) {
		<-ctx.Done()
		ev.add("loop saw /readyz " + http.StatusText(readyzOf(a.readiness)))
	}}
	ln := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(a, ctx, ln)
	go func() {
		if resp, err := http.Get("http://" + ln.Addr().String() + "/"); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-inFlight
	cancel()
	if code := <-status; code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz during the drain = %d, want 503", code)
	}
	if err := waitRun(t, done); err != nil {
		t.Fatal(err)
	}
	if got := ev.list(); len(got) == 0 || got[0] != "loop saw /readyz Service Unavailable" {
		t.Fatalf("events = %v, want the loop to see 503 at its cancel", got)
	}
}

// A listener that fails (Serve returns an error) stops every loop, closes everything and is
// returned, as a signal would end the process.
func TestAServeErrorStopsEveryLoopAndIsReturned(t *testing.T) {
	ev := &journal{}
	a := testApp(ev, nil, func(ctx context.Context) { <-ctx.Done(); ev.add("loop stopped") })
	ln := listen(t)
	done := runAsync(a, context.Background(), ln)
	time.Sleep(50 * time.Millisecond)
	_ = ln.Close()
	if err := waitRun(t, done); err == nil || errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("run = %v, want the Serve error", err)
	}
	if got := strings.Join(ev.list(), ","); got != "loop stopped,disconnect link,close sink,close pool" {
		t.Fatalf("events = %s", got)
	}
}

// A boot step that fails (the MQTT connect, Recover) closes the listener and everything wired, starts
// no loop and is returned.
func TestAFailedStartClosesTheListenerAndEverythingWired(t *testing.T) {
	ev := &journal{}
	a := testApp(ev, nil, func(context.Context) { ev.add("loop started") })
	boom := errors.New("mqtt down")
	a.starts = []func(context.Context) error{func(context.Context) error { ev.add("connect"); return boom },
		func(context.Context) error { ev.add("recover"); return nil }}
	ln := listen(t)
	addr := ln.Addr().String()
	if err := a.run(context.Background(), ln); !errors.Is(err, boom) {
		t.Fatalf("run = %v, want %v", err, boom)
	}
	if got := strings.Join(ev.list(), ","); got != "connect,disconnect link,close sink,close pool" {
		t.Fatalf("events = %s", got)
	}
	again, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the failed start kept %s: %v", addr, err)
	}
	_ = again.Close()
}
