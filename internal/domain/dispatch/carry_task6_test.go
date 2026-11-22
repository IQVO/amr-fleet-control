package dispatch_test

import (
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/domain/dispatch"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
)

// star is a test map: the hub X at the origin and one spoke per entry, a node at (x, 0) with an edge to X, so the
// route length from a spoke to X is exactly |x| (math.Hypot of (x, 0) is exact). It lets a test place two
// candidates at distances that differ by an exact amount around the 1e-9 tie tolerance.
func star(t *testing.T, kind fleetmap.NodeKind, spokes map[string]float64) *fleetmap.Map {
	t.Helper()
	nodes := []fleetmap.Node{{ID: "X", Kind: fleetmap.Waypoint}}
	var edges []fleetmap.Edge
	for id, x := range spokes {
		nodes = append(nodes, fleetmap.Node{ID: id, X: x, Kind: kind})
		edges = append(edges, fleetmap.Edge{ID: id + "-X", A: id, B: "X"})
	}
	m, err := fleetmap.New(fleetmap.Spec{FleetID: "AMR1", MapID: "S1", Version: "v1", Nodes: nodes, Edges: edges})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// carry (Task 6 review): the nearest candidate listed first keeps the leg against a farther one with a fuller
// battery (the longer-but-fuller branch); lengths closer than the tolerance are equal, so the battery decides.
func TestChooseNeverTakesALongerRouteForAFullerBattery(t *testing.T) {
	m := floor(t)
	cands := []dispatch.Candidate{{Robot: id("R1"), Node: "P1", Battery: 30}, {Robot: id("R3"), Node: "P3", Battery: 90}}
	if c, ok := dispatch.Choose(m, "ST1", cands); !ok || c.Robot != id("R1") {
		t.Fatalf("Choose = %+v, want R1 (P1 is 8 m from ST1, P3 12 m, whatever the battery)", c)
	}
	near := star(t, fleetmap.Parking, map[string]float64{"A": 2, "B": -(2 + 0.5e-9), "C": -(2 - 0.5e-9)})
	within := []dispatch.Candidate{{Robot: id("R1"), Node: "A", Battery: 30}, {Robot: id("R2"), Node: "B", Battery: 90}}
	if c, _ := dispatch.Choose(near, "X", within); c.Robot != id("R2") {
		t.Fatalf("0.5e-9 m farther is the same distance, the fuller battery wins: got %+v", c)
	}
	nearer := []dispatch.Candidate{{Robot: id("R1"), Node: "A", Battery: 90}, {Robot: id("R2"), Node: "C", Battery: 30}}
	if c, _ := dispatch.Choose(near, "X", nearer); c.Robot != id("R1") {
		t.Fatalf("0.5e-9 m nearer is the same distance, the fuller battery wins: got %+v", c)
	}
}

// carry (Task 6 review): equal distances resolve to the lower node id. From B, P1 (B-A-P1) and P3 (B-C-P3) are
// both 4 m; P2 (2 m) is taken.
func TestNearestFreeResolvesEqualDistancesToTheLowerID(t *testing.T) {
	m := floor(t)
	if n, ok := dispatch.NearestFree(m, "B", fleetmap.Parking, func(id string) bool { return id != "P2" }); !ok || n != "P1" {
		t.Fatalf("NearestFree from B without P2 = %q %v, want P1", n, ok)
	}
}
