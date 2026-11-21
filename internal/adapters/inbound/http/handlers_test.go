package http_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ahttp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/http"
	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/memory"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/config"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

type api struct {
	t    *testing.T
	h    http.Handler
	svc  *usecases.Service
	down *memory.Downlink
}

func newAPI(t *testing.T, robots ...string) *api {
	t.Helper()
	m, err := config.LoadMap("../../../../config/map.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := memory.NewStore()
	uow, clock, down := memory.NewUnitOfWork(s), memory.NewClock(t0), memory.NewDownlink()
	svc, err := usecases.NewService(m, usecases.DefaultConfig(), usecases.Deps{UoW: uow, Fleets: memory.NewFleets(s), Robots: memory.NewRobots(s),
		Missions: memory.NewMissions(s), Sink: memory.NewEvents(s), Downlink: down, Clock: clock, IDs: memory.NewIDs("u"),
		Metrics: memory.NewMetrics(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	for i, serial := range robots {
		id := robot.ID{Manufacturer: "EMU", SerialNumber: serial}
		node := []string{"P1", "P2", "P3"}[i]
		_ = svc.HandleConnection(context.Background(), vda.Connection{Robot: id, State: robot.ConnectionOnline, At: t0})
		n, _ := m.Node(node)
		_ = svc.HandleState(context.Background(), vda.State{Robot: id, Report: robot.Report{At: t0, LastNodeID: node, X: n.X, Y: n.Y, MapID: m.MapID(), Battery: 90}})
	}
	idem := ahttp.NewIdempotency(memory.NewIdempotency(s), svc, clock) // rulings P3-R10, P3-R15: the Service, so its serialized UoW
	return &api{t: t, svc: svc, down: down, h: ahttp.NewRouter(&ahttp.Server{Service: svc, Idempotency: idem.Middleware}, nil)}
}

func (a *api) do(method, path, body, key string) *httptest.ResponseRecorder {
	a.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return m
}

const legBody = `{"container_id":"C1","from_handoff_point_id":"ST1","to_handoff_point_id":"ASRS1-IN1","reference":"tl-9"}`

func TestALegIsAcceptedWithALocationAndNoVDA5050Vocabulary(t *testing.T) {
	a := newAPI(t, "R1")
	rec := a.do("POST", "/fleets/AMR1/legs", legBody, "k1")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body)
	}
	b := decode(t, rec)
	id, _ := b["leg_id"].(string)
	if rec.Header().Get("Location") != "/fleets/AMR1/legs/"+id || b["status"] != "ASSIGNED" || b["fleet_id"] != "AMR1" ||
		b["container_id"] != "C1" || b["from_handoff_point_id"] != "ST1" || b["to_handoff_point_id"] != "ASRS1-IN1" ||
		b["reference"] != "tl-9" || b["map_version"] != "2026-11-20.1" || b["accepted_at"] != "2026-11-20T08:00:00Z" {
		t.Fatalf("leg = %v, Location %q", b, rec.Header().Get("Location"))
	}
	checkLegAnswer(t, a, b, id)
}

// (The tail of the brief's test is split into checkLegAnswer to stay under this repo's cyclop
// limit of 15; the assertions are the brief's, unchanged.)
func checkLegAnswer(t *testing.T, a *api, b map[string]any, id string) {
	t.Helper()
	if r := b["robot"].(map[string]any); r["manufacturer"] != "EMU" || r["serial_number"] != "R1" {
		t.Fatalf("robot = %v", r)
	}
	for _, k := range []string{"nodes", "edges", "order_id", "base", "horizon", "reason", "ended_at", "picked_up_at"} {
		if _, ok := b[k]; ok {
			t.Fatalf("the leg answer carries %q", k)
		}
	}
	got := a.do("GET", "/fleets/AMR1/legs/"+id, "", "")
	if got.Code != http.StatusOK || decode(t, got)["leg_id"] != id {
		t.Fatalf("GET = %d %s", got.Code, got.Body)
	}
}

func TestARetryWithTheSameKeyIsReplayedAndAnotherBodyIsRefused(t *testing.T) {
	a := newAPI(t, "R1")
	first := a.do("POST", "/fleets/AMR1/legs", legBody, "k1")
	again := a.do("POST", "/fleets/AMR1/legs", legBody, "k1")
	if again.Code != http.StatusAccepted || again.Header().Get("Idempotency-Replayed") != "true" || again.Body.String() != first.Body.String() {
		t.Fatalf("replay = %d %q %s", again.Code, again.Header().Get("Idempotency-Replayed"), again.Body)
	}
	if n := len(a.down.Orders()); n != 1 {
		t.Fatalf("a replay sent another order: %d", n)
	}
	other := a.do("POST", "/fleets/AMR1/legs", strings.Replace(legBody, "C1", "C2", 1), "k1")
	if other.Code != http.StatusConflict || decode(t, other)["code"] != "idempotency-key-reused" {
		t.Fatalf("reuse = %d %s", other.Code, other.Body)
	}
	if rec := a.do("POST", "/fleets/AMR1/legs", legBody, ""); rec.Code != http.StatusBadRequest || decode(t, rec)["code"] != "idempotency-key-required" {
		t.Fatalf("no key = %d %s", rec.Code, rec.Body)
	}
}

func TestARetryableRefusalReleasesItsKey(t *testing.T) {
	a := newAPI(t)
	rec := a.do("POST", "/fleets/AMR1/legs", legBody, "k1")
	if rec.Code != http.StatusUnprocessableEntity || decode(t, rec)["code"] != "fleet-unavailable" {
		t.Fatalf("no robot = %d %s", rec.Code, rec.Body)
	}
	if rec := a.do("POST", "/fleets/AMR1/legs", legBody, "k1"); rec.Header().Get("Idempotency-Replayed") == "true" {
		t.Fatal("a RETRYABLE refusal was replayed")
	}
}

// ruling P3-R13: a refusal decided by the map file (unknown-handoff-point, fleet-not-found) can
// change inside the 24 h window without the client changing its request (a map fix deploy), so it
// is RETRYABLE: never stored under its key, a retry under the same key is evaluated again.
func TestAMapDependentRefusalReleasesItsKey(t *testing.T) {
	a := newAPI(t, "R1")
	unknown := strings.Replace(legBody, "ST1", "NOPE", 1)
	rec := a.do("POST", "/fleets/AMR1/legs", unknown, "K")
	if rec.Code != http.StatusUnprocessableEntity || decode(t, rec)["code"] != "unknown-handoff-point" {
		t.Fatalf("unknown handoff = %d %s", rec.Code, rec.Body)
	}
	if again := a.do("POST", "/fleets/AMR1/legs", unknown, "K"); again.Header().Get("Idempotency-Replayed") == "true" {
		t.Fatalf("an unknown-handoff-point refusal was replayed under its key: %d %s", again.Code, again.Body)
	}
	if fixed := a.do("POST", "/fleets/AMR1/legs", legBody, "K"); fixed.Code != http.StatusAccepted {
		t.Fatalf("a retry under K after unknown-handoff-point = %d %s, want a fresh 202", fixed.Code, fixed.Body)
	}
	_ = a.do("POST", "/fleets/AMR9/legs", legBody, "F")
	if again := a.do("POST", "/fleets/AMR9/legs", legBody, "F"); again.Code != http.StatusNotFound || again.Header().Get("Idempotency-Replayed") == "true" {
		t.Fatalf("fleet-not-found retried under its key = %d replayed=%q %s", again.Code, again.Header().Get("Idempotency-Replayed"), again.Body)
	}
}

func TestValidationAndNotFoundProblems(t *testing.T) {
	a := newAPI(t, "R1")
	cases := []struct {
		method, path, body, code string
		status                   int
	}{
		{"POST", "/fleets/AMR1/legs", `{`, "bad-request", 400},
		{"POST", "/fleets/AMR1/legs", `{"container_id":"C1","from_handoff_point_id":"ST1","to_handoff_point_id":"ST2","colour":"red"}`, "bad-request", 400},
		{"POST", "/fleets/AMR1/legs", `{"container_id":"","from_handoff_point_id":"ST1","to_handoff_point_id":"ST2"}`, "invalid-parameter", 400},
		{"POST", "/fleets/AMR1/legs", `{"container_id":"C1","from_handoff_point_id":"ST1","to_handoff_point_id":"ST1"}`, "invalid-parameter", 400},
		{"POST", "/fleets/AMR1/legs", `{"container_id":"C\u0000","from_handoff_point_id":"ST1","to_handoff_point_id":"ST2"}`, "invalid-parameter", 400},
		{"POST", "/fleets/AMR9/legs", legBody, "fleet-not-found", 404},
		{"POST", "/fleets/AMR1/legs", `{"container_id":"C1","from_handoff_point_id":"NOPE","to_handoff_point_id":"ST2"}`, "unknown-handoff-point", 422},
		{"GET", "/fleets/AMR9", "", "fleet-not-found", 404},
		{"GET", "/fleets/AMR1/legs/leg-nope", "", "leg-not-found", 404},
		{"GET", "/fleets/AMR1/robots/EMU/R9", "", "robot-not-found", 404},
		{"POST", "/fleets/AMR1/legs/leg-nope/cancellation", "", "leg-not-found", 404},
	}
	for i, c := range cases {
		rec := a.do(c.method, c.path, c.body, "key-"+string(rune('a'+i)))
		if rec.Code != c.status || rec.Header().Get("Content-Type") != "application/problem+json" || decode(t, rec)["code"] != c.code {
			t.Errorf("%s %s %s = %d %s, want %d %s", c.method, c.path, c.body, rec.Code, rec.Body, c.status, c.code)
		}
	}
	_ = a.do("POST", "/fleets/AMR1/legs", legBody, "busy-1")
	busy := a.do("POST", "/fleets/AMR1/legs", strings.Replace(legBody, "ST1", "ST2", 1), "busy-2")
	if busy.Code != 422 || decode(t, busy)["code"] != "container-busy" {
		t.Fatalf("container busy = %d %s", busy.Code, busy.Body)
	}
}

func TestCancellationAnswers200WhenDoneAnd202WhenTheRobotIsAsked(t *testing.T) {
	a := newAPI(t, "R1")
	assigned := decode(t, a.do("POST", "/fleets/AMR1/legs", legBody, "k1"))["leg_id"].(string)
	queued := decode(t, a.do("POST", "/fleets/AMR1/legs", strings.Replace(legBody, "C1", "C2", 1), "k2"))["leg_id"].(string)
	rec := a.do("POST", "/fleets/AMR1/legs/"+queued+"/cancellation", "", "c1")
	if b := decode(t, rec); rec.Code != http.StatusOK || b["status"] != "CANCELLED" || b["reason"] != "CANCELLED" || b["ended_at"] == nil {
		t.Fatalf("cancel queued = %d %v", rec.Code, b)
	}
	rec = a.do("POST", "/fleets/AMR1/legs/"+assigned+"/cancellation", "", "c2")
	if b := decode(t, rec); rec.Code != http.StatusAccepted || b["status"] != "CANCEL_REQUESTED" {
		t.Fatalf("cancel assigned = %d %v", rec.Code, b)
	}
	rec = a.do("POST", "/fleets/AMR1/legs/"+queued+"/cancellation", "", "c3")
	if rec.Code != http.StatusConflict || decode(t, rec)["code"] != "leg-ended" {
		t.Fatalf("cancel ended = %d %s", rec.Code, rec.Body)
	}
}

func TestTheFleetAndRobotViews(t *testing.T) {
	a := newAPI(t, "R1", "R2")
	f := decode(t, a.do("GET", "/fleets/AMR1", "", ""))
	if f["fleet_id"] != "AMR1" || f["map_id"] != "S1" || f["map_version"] != "2026-11-20.1" || len(f["robots"].([]any)) != 2 ||
		len(f["handoff_points"].([]any)) != 6 {
		t.Fatalf("fleet = %v", f)
	}
	if c := f["capacity"].(map[string]any); c["robots_total"] != 2.0 || c["robots_available"] != 2.0 || c["legs_queued"] != 0.0 {
		t.Fatalf("capacity = %v", c)
	}
	checkRobotView(t, a)
}

// (The robot half of the brief's test is split into checkRobotView to stay under this repo's
// cyclop limit of 15; the assertions are the brief's, unchanged.)
func checkRobotView(t *testing.T, a *api) {
	t.Helper()
	r := decode(t, a.do("GET", "/fleets/AMR1/robots/EMU/R1", "", ""))
	if r["manufacturer"] != "EMU" || r["serial_number"] != "R1" || r["state"] != "IDLE" || r["connection"] != "ONLINE" ||
		r["battery_percent"] != 90.0 || r["available"] != true || r["node_id"] != "P1" || r["last_seen_at"] != "2026-11-20T08:00:00Z" {
		t.Fatalf("robot = %v", r)
	}
	if _, ok := r["factsheet"]; ok {
		t.Fatal("a robot without a factsheet must omit it")
	}
}

func TestAPanicIsA500Problem(t *testing.T) {
	h := ahttp.NewRouter(&ahttp.Server{Service: nil}, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/fleets/AMR1", nil))
	if rec.Code != http.StatusInternalServerError || decode(t, rec)["code"] != "internal-error" {
		t.Fatalf("panic = %d %s", rec.Code, rec.Body)
	}
}
