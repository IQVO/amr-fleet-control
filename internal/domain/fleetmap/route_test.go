package fleetmap_test

import (
	"reflect"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
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

func TestDisconnectedNodesHaveNoRoute(t *testing.T) {
	s := floor()
	s.Nodes = append(s.Nodes, fleetmap.Node{ID: "ISLE", X: 20, Y: 20, Kind: fleetmap.Parking})
	m := mustMap(t, s)
	if _, ok := m.Route("P1", "ISLE", nil); ok {
		t.Fatal("an isolated node was reached")
	}
}

func TestEqualLengthRoutesAreChosenDeterministically(t *testing.T) {
	m := mustMap(t, floor())
	first, ok := m.Route("P2", "ST2", nil)
	if !ok || first.Length != 12 {
		t.Fatalf("P2 -> ST2 = %+v %v", first, ok)
	}
	for i := 0; i < 20; i++ {
		again, _ := m.Route("P2", "ST2", nil)
		if !reflect.DeepEqual(again, first) {
			t.Fatalf("run %d chose %v, first chose %v", i, again.Nodes, first.Nodes)
		}
	}
}
