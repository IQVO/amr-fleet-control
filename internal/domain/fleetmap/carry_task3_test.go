package fleetmap_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
)

// carry (Task 3 review): two disjoint cycles are reported in the order of their first robot key, and a robot
// waiting into a cycle is not part of it. The chain R0 -> R5 makes the walk find the R4/R5 cycle FIRST, so the
// order is the comparator's, not the walk's (Robots sorted by key, Nodes aligned per robot).
func TestTwoDisjointCyclesAreOrderedByTheirFirstRobot(t *testing.T) {
	const r0, r4, r5 = "EMU/R0", "EMU/R4", "EMU/R5"
	f := fleetmap.NewFleetMap(mustMap(t, floor()))
	f.SetWait(r0, "D", r5, t0) // waits into the second cycle
	f.SetWait(r4, "C", r5, t0)
	f.SetWait(r5, "G", r4, t0)
	f.SetWait(r1, "B", r2, t0)
	f.SetWait(r2, "A", r1, t0)
	if got := f.Cycles(); !reflect.DeepEqual(got, [][]string{{r1, r2}, {r4, r5}}) {
		t.Fatalf("Cycles = %v, want [[%s %s] [%s %s]]", got, r1, r2, r4, r5)
	}
	evs := f.DetectDeadlocks(t0, ids())
	want := []fleetmap.DeadlockDetected{
		{DeadlockID: "dl-1", Robots: []string{r1, r2}, Nodes: []string{"B", "A"}, At: t0},
		{DeadlockID: "dl-2", Robots: []string{r4, r5}, Nodes: []string{"C", "G"}, At: t0},
	}
	if !reflect.DeepEqual(evs, want) {
		t.Fatalf("DetectDeadlocks = %+v\nwant %+v", evs, want)
	}
}

// carry (Task 3 review): a released element is free for another robot; a reservation refused on an EDGE keeps none
// of its nodes; re-reserving its own elements plus a new node takes the new node too.
func TestReserveEdgeCases(t *testing.T) {
	f := fleetmap.NewFleetMap(mustMap(t, floor()))
	if err := f.Reserve(r1, []string{"P1", "A"}, []string{"P1-A"}); err != nil {
		t.Fatal(err)
	}
	f.Retain(r1, []string{"A"}, nil)
	if err := f.Reserve(r2, []string{"P1"}, []string{"P1-A"}); err != nil {
		t.Fatalf("elements r1 released are free for r2: %v", err)
	}
	if h, _ := f.NodeHolder("P1"); h != r2 {
		t.Fatalf("P1 held by %q, want %s", h, r2)
	}
	if err := f.Reserve(r1, []string{"B", "C"}, []string{"A-B", "P1-A"}); !errors.Is(err, fleetmap.ErrReserved) {
		t.Fatalf("Reserve over r2's edge = %v", err)
	}
	if nodes, edges := f.Held(r1); !reflect.DeepEqual(nodes, []string{"A"}) || len(edges) != 0 {
		t.Fatalf("a reservation refused on an edge kept %v %v, want only A", nodes, edges)
	}
	if err := f.Reserve(r1, []string{"A", "B"}, []string{"A-B"}); err != nil {
		t.Fatal(err)
	}
	if nodes, edges := f.Held(r1); !reflect.DeepEqual(nodes, []string{"A", "B"}) || !reflect.DeepEqual(edges, []string{"A-B"}) {
		t.Fatalf("own A plus new B = %v %v", nodes, edges)
	}
}
