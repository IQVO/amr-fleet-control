package fleetmap_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
)

const (
	r1 = "EMU/R1"
	r2 = "EMU/R2"
	r3 = "EMU/R3"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func TestReserveHoldsEachElementForExactlyOneRobot(t *testing.T) {
	f := fleetmap.NewFleetMap(mustMap(t, floor()))
	if err := f.Reserve(r1, []string{"P1", "A"}, []string{"P1-A"}); err != nil {
		t.Fatal(err)
	}
	if h, ok := f.NodeHolder("A"); !ok || h != r1 {
		t.Fatalf("A held by %q %v", h, ok)
	}
	if h, ok := f.EdgeHolder("P1-A"); !ok || h != r1 {
		t.Fatalf("P1-A held by %q %v", h, ok)
	}
	if _, ok := f.NodeHolder("B"); ok {
		t.Fatal("B is held")
	}
	if err := f.Reserve(r1, []string{"A"}, []string{"P1-A"}); err != nil {
		t.Fatalf("re-reserving its own elements: %v", err)
	}
	err := f.Reserve(r2, []string{"B", "A"}, nil)
	if !errors.Is(err, fleetmap.ErrReserved) {
		t.Fatalf("Reserve of a held node = %v", err)
	}
	if _, ok := f.NodeHolder("B"); ok {
		t.Fatal("a refused reservation kept B: reservations are all or nothing")
	}
	if err := f.Reserve(r2, nil, []string{"P1-A"}); !errors.Is(err, fleetmap.ErrReserved) {
		t.Fatalf("Reserve of a held edge = %v", err)
	}
	if err := f.Reserve(r2, []string{"Z"}, nil); !errors.Is(err, fleetmap.ErrUnknownElement) {
		t.Fatalf("unknown node = %v", err)
	}
	if err := f.Reserve(r2, nil, []string{"Z-Z"}); !errors.Is(err, fleetmap.ErrUnknownElement) {
		t.Fatalf("unknown edge = %v", err)
	}
	if err := f.Reserve("", []string{"B"}, nil); !errors.Is(err, fleetmap.ErrUnknownElement) {
		t.Fatalf("a reservation without a robot = %v", err)
	}
}

func TestRetainReleasesEverythingNotListedAndOnlyForThatRobot(t *testing.T) {
	f := fleetmap.NewFleetMap(mustMap(t, floor()))
	_ = f.Reserve(r1, []string{"P1", "A", "ASRS1-OUT1"}, []string{"P1-A", "A-ASRS1-OUT1"})
	_ = f.Reserve(r2, []string{"P2"}, nil)
	f.Retain(r1, []string{"A", "ASRS1-OUT1"}, []string{"A-ASRS1-OUT1"})
	nodes, edges := f.Held(r1)
	if !reflect.DeepEqual(nodes, []string{"A", "ASRS1-OUT1"}) || !reflect.DeepEqual(edges, []string{"A-ASRS1-OUT1"}) {
		t.Fatalf("held = %v %v", nodes, edges)
	}
	if h, _ := f.NodeHolder("P2"); h != r2 {
		t.Fatal("Retain of r1 released r2's node")
	}
	f.SetWait(r1, "E", r2, t0)
	f.ReleaseRobot(r1)
	if n, e := f.Held(r1); len(n) != 0 || len(e) != 0 {
		t.Fatalf("after ReleaseRobot r1 holds %v %v", n, e)
	}
	if _, ok := f.WaitOf(r1); ok {
		t.Fatal("ReleaseRobot kept the wait")
	}
}

func TestSetWaitKeepsItsFirstTimeUntilTheWaitChanges(t *testing.T) {
	f := fleetmap.NewFleetMap(mustMap(t, floor()))
	f.SetWait(r1, "B", r2, t0)
	f.SetWait(r1, "B", r2, t0.Add(time.Minute))
	if w, ok := f.WaitOf(r1); !ok || !w.Since.Equal(t0) || w.Node != "B" || w.Blocker != r2 {
		t.Fatalf("wait = %+v %v", w, ok)
	}
	f.SetWait(r1, "B", r3, t0.Add(time.Minute))
	if w, _ := f.WaitOf(r1); !w.Since.Equal(t0.Add(time.Minute)) || w.Blocker != r3 {
		t.Fatalf("a new blocker kept the old time: %+v", w)
	}
	f.ClearWait(r1)
	if _, ok := f.WaitOf(r1); ok {
		t.Fatal("ClearWait kept the wait")
	}
}

func TestStateRoundTripsThroughRehydrate(t *testing.T) {
	m := mustMap(t, floor())
	f := fleetmap.NewFleetMap(m)
	_ = f.Reserve(r1, []string{"P1", "A"}, []string{"P1-A"})
	f.SetWait(r1, "B", r2, t0)
	s := f.State()
	g, err := fleetmap.Rehydrate(m, s)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g.State(), s) {
		t.Fatalf("rehydrated %+v, want %+v", g.State(), s)
	}
	s.Nodes["A"] = r3
	if h, _ := f.NodeHolder("A"); h != r1 {
		t.Fatal("State returned the aggregate's own map")
	}
	if g.Map() != m {
		t.Fatal("Map() is not the map it was built with")
	}
}

func TestRehydrateRefusesElementsOfAnotherMapVersion(t *testing.T) {
	m := mustMap(t, floor())
	bad := []fleetmap.State{
		{Nodes: map[string]string{"Z": r1}},
		{Nodes: map[string]string{"A": ""}},
		{Edges: map[string]string{"Z-Z": r1}},
		{Edges: map[string]string{"P1-A": ""}},
		{Waits: map[string]fleetmap.Wait{r1: {Node: "Z", Blocker: r2}}},
		{Waits: map[string]fleetmap.Wait{r1: {Node: "A"}}},
	}
	for i, s := range bad {
		if _, err := fleetmap.Rehydrate(m, s); !errors.Is(err, fleetmap.ErrUnknownElement) {
			t.Fatalf("case %d: Rehydrate = %v", i, err)
		}
	}
}
