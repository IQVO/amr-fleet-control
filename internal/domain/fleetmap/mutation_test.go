package fleetmap_test

import (
	"reflect"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
)

// tol is the 1e-9 tie tolerance of route.go and map.go as a float64 VARIABLE: Go evaluates constant expressions
// exactly, so a boundary coordinate must be computed with the same float64 rounding the code applies at run time.
var tol float64 = 1e-9

func pointMap(t *testing.T, nodes []fleetmap.Node, edges ...[2]string) *fleetmap.Map {
	t.Helper()
	var es []fleetmap.Edge
	for _, e := range edges {
		es = append(es, fleetmap.Edge{ID: e[0] + "-" + e[1], A: e[0], B: e[1]})
	}
	return mustMap(t, fleetmap.Spec{FleetID: "AMR1", MapID: "S1", Version: "v1", Nodes: nodes, Edges: es})
}

func wp(id string, x, y float64) fleetmap.Node {
	return fleetmap.Node{ID: id, X: x, Y: y, Kind: fleetmap.Waypoint}
}

// Mutation work (Task 22, map.go Nearest): a node exactly at the tolerance, or exactly 1e-9 past it, is within it;
// a later id exactly 1e-9 nearer is "equidistant" and loses to the lower id.
func TestNearestAtTheExactBoundaries(t *testing.T) {
	half := 0.5
	if n, ok := pointMap(t, []fleetmap.Node{wp("A", half, 0)}).Nearest(0, 0, half); !ok || n != "A" {
		t.Fatalf("a node exactly at the tolerance = %q %v, want A", n, ok)
	}
	if n, ok := pointMap(t, []fleetmap.Node{wp("A", half+tol, 0)}).Nearest(0, 0, half); !ok || n != "A" {
		t.Fatalf("a node exactly 1e-9 past the tolerance = %q %v, want A (the tolerance has 1e-9 slack)", n, ok)
	}
	if n, ok := pointMap(t, []fleetmap.Node{wp("A", 0, 3), wp("B", 0, -3)}).Nearest(0, 3, half); !ok || n != "A" {
		t.Fatalf("Nearest(0, 3) = %q %v, want A at (0, 3) (the distance subtracts the query's y)", n, ok)
	}
	one := 1.0
	if n, _ := pointMap(t, []fleetmap.Node{wp("A", one, 0), wp("B", -(one - tol), 0)}).Nearest(0, 0, 2); n != "A" {
		t.Fatalf("B exactly 1e-9 nearer than A = %q, want A (equidistant within 1e-9: the lower id)", n)
	}
}

// Mutation work (Task 22, route.go Route): a predecessor is replaced only by a path shorter by MORE than 1e-9.
// A is settled first (1 m) and offers T at 11 m (F-A-T); C, settled later (10 m), offers T at exactly 11 - 1e-9.
// Every edge is axis-aligned, so each length is exact and the sums round like the code's.
func TestRouteKeepsTheFirstPathAgainstOneExactlyAToleranceShorter(t *testing.T) {
	m := pointMap(t, []fleetmap.Node{wp("F", 0, 0), wp("A", 0, 1), wp("T", 10, 1), wp("B", 2, 0), wp("C", 10, tol)},
		[2]string{"F", "A"}, [2]string{"A", "T"}, [2]string{"F", "B"}, [2]string{"B", "C"}, [2]string{"C", "T"})
	if via := 2.0 + 8.0 + (1 - tol); via != 11-tol {
		t.Fatalf("fixture: F-B-C-T = %v, want exactly 11 - 1e-9 = %v", via, 11-tol)
	}
	p, ok := m.Route("F", "T", nil)
	if !ok || !reflect.DeepEqual(p.Nodes, []string{"F", "A", "T"}) || p.Length != 11 {
		t.Fatalf("Route F -> T = %+v, want F A T of 11 m", p)
	}
}

// Mutation work (Task 22, route.go closest): among unsettled nodes, a later id exactly 1e-9 nearer is equidistant,
// so the lower id is settled first and its relaxation of T stands. A (2 m) and B (2 - 1e-9 m) offer T at exactly
// the same length (the same two exact edges, added in the other order).
func TestRouteSettlesEquidistantNodesByTheLowerID(t *testing.T) {
	two := 2.0
	m := pointMap(t, []fleetmap.Node{wp("F", 0, 0), wp("A", two, 0), wp("B", 0, -(two - tol)), wp("T", two, -(two - tol))},
		[2]string{"F", "A"}, [2]string{"F", "B"}, [2]string{"A", "T"}, [2]string{"B", "T"})
	p, ok := m.Route("F", "T", nil)
	if !ok || !reflect.DeepEqual(p.Nodes, []string{"F", "A", "T"}) {
		t.Fatalf("Route F -> T = %+v, want F A T (A and B are equidistant within 1e-9; A is the lower id)", p)
	}
}
