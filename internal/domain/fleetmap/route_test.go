package fleetmap_test

import (
	"reflect"
	"testing"
)

func TestRouteIsTheShortestPathWithItsEdgesAndLength(t *testing.T) {
	m := mustMap(t, floor())
	p, ok := m.Route("P1", "ST1", nil)
	if !ok {
		t.Fatal("no route P1 -> ST1")
	}
	if want := []string{"P1", "A", "ASRS1-OUT1", "E", "ST1"}; !reflect.DeepEqual(p.Nodes, want) {
		t.Fatalf("nodes = %v", p.Nodes)
	}
	if want := []string{"P1-A", "A-ASRS1-OUT1", "ASRS1-OUT1-E", "E-ST1"}; !reflect.DeepEqual(p.Edges, want) {
		t.Fatalf("edges = %v", p.Edges)
	}
	if p.Length != 8 {
		t.Fatalf("length = %v, want 8", p.Length)
	}
}

func TestRouteTraversesUndirectedEdgesBothWays(t *testing.T) {
	m := mustMap(t, floor())
	p, ok := m.Route("ST1", "ASRS1-IN1", nil)
	if !ok || !reflect.DeepEqual(p.Nodes, []string{"ST1", "E", "F", "ASRS1-IN1"}) || p.Length != 6 {
		t.Fatalf("route = %+v %v", p, ok)
	}
	if !reflect.DeepEqual(p.Edges, []string{"E-ST1", "E-F", "ASRS1-IN1-F"}) {
		t.Fatalf("edges = %v", p.Edges)
	}
}

func TestRouteAvoidsNodesButNeverItsOwnEnds(t *testing.T) {
	m := mustMap(t, floor())
	p, ok := m.Route("ST1", "ASRS1-IN1", map[string]bool{"F": true})
	if !ok || !reflect.DeepEqual(p.Nodes, []string{"ST1", "E", "ASRS1-OUT1", "A", "B", "ASRS1-IN1"}) || p.Length != 10 {
		t.Fatalf("detour = %+v %v", p, ok)
	}
	p, ok = m.Route("ST1", "E", map[string]bool{"E": true, "ST1": true})
	if !ok || !reflect.DeepEqual(p.Nodes, []string{"ST1", "E"}) {
		t.Fatalf("avoiding the ends themselves = %+v %v", p, ok)
	}
	if _, ok := m.Route("ST1", "ASRS1-IN1", map[string]bool{"E": true}); ok {
		t.Fatal("ST1's only neighbour E is avoided, yet a route was found")
	}
}

func TestRouteToItselfIsOneNodeAndUnknownEndsHaveNoRoute(t *testing.T) {
	m := mustMap(t, floor())
	if p, ok := m.Route("B", "B", nil); !ok || !reflect.DeepEqual(p.Nodes, []string{"B"}) || len(p.Edges) != 0 || p.Length != 0 {
		t.Fatalf("Route(B,B) = %+v %v", p, ok)
	}
	if _, ok := m.Route("Z", "B", nil); ok {
		t.Fatal("unknown start routed")
	}
	if _, ok := m.Route("B", "Z", nil); ok {
		t.Fatal("unknown end routed")
	}
}

// plan 06 D3 supersedes TestDisconnectedNodesHaveNoRoute (it appended an island ISLE and asserted
// Route refused it): fleetmap.New now refuses a disconnected map at load
// (TestNewRefusesADisconnectedMap), so the fixture can no longer be built and the failure class
// is pinned one level earlier, at New.

func TestEqualLengthRoutesAreChosenDeterministically(t *testing.T) {
	m := mustMap(t, floor())
	first, ok := m.Route("P2", "ST2", nil)
	if !ok || first.Length != 12 {
		t.Fatalf("P2 -> ST2 = %+v %v", first, ok)
	}
	// Three routes are 12 m long (via C-D-ASRS1-IN2, via C-ASRS1-OUT2-G, via ASRS1-IN1-F-G).
	// Lower ids settle first, so D settles at 6 m before F and reaches ASRS1-IN2 at 8 m; when
	// ASRS1-IN2 settles first among the 8 m ties it gives H its predecessor, and G's equal-length
	// offer at 10 m does not replace it (only a strictly shorter one would).
	if want := []string{"P2", "B", "C", "D", "ASRS1-IN2", "H", "ST2"}; !reflect.DeepEqual(first.Nodes, want) {
		t.Fatalf("tie-break chose %v, want %v", first.Nodes, want)
	}
	if want := []string{"P2-B", "B-C", "C-D", "D-ASRS1-IN2", "ASRS1-IN2-H", "H-ST2"}; !reflect.DeepEqual(first.Edges, want) {
		t.Fatalf("tie-break edges %v, want %v", first.Edges, want)
	}
	for i := 0; i < 20; i++ {
		again, _ := m.Route("P2", "ST2", nil)
		if !reflect.DeepEqual(again, first) {
			t.Fatalf("run %d chose %v, first chose %v", i, again.Nodes, first.Nodes)
		}
	}
}
