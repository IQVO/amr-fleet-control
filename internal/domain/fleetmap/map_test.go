package fleetmap_test

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
)

// floor is the example floor of config/map.example.yaml, built in code.
func floor() fleetmap.Spec {
	n := func(id string, x, y float64, k fleetmap.NodeKind, h string) fleetmap.Node {
		return fleetmap.Node{ID: id, X: x, Y: y, Kind: k, HandoffPoint: h}
	}
	e := func(a, b string) fleetmap.Edge { return fleetmap.Edge{ID: a + "-" + b, A: a, B: b} }
	return fleetmap.Spec{FleetID: "AMR1", MapID: "S1", Version: "2026-11-20.1",
		Nodes: []fleetmap.Node{
			n("P1", 0, 0, fleetmap.Parking, ""), n("P2", 2, 0, fleetmap.Parking, ""), n("P3", 4, 0, fleetmap.Parking, ""),
			n("CH1", 6, 0, fleetmap.Charger, ""),
			n("A", 0, 2, fleetmap.Waypoint, ""), n("B", 2, 2, fleetmap.Waypoint, ""), n("C", 4, 2, fleetmap.Waypoint, ""), n("D", 6, 2, fleetmap.Waypoint, ""),
			n("ASRS1-OUT1", 0, 4, fleetmap.Port, "ASRS1-OUT1"), n("ASRS1-IN1", 2, 4, fleetmap.Port, "ASRS1-IN1"),
			n("ASRS1-OUT2", 4, 4, fleetmap.Port, "ASRS1-OUT2"), n("ASRS1-IN2", 6, 4, fleetmap.Port, "ASRS1-IN2"),
			n("E", 0, 6, fleetmap.Waypoint, ""), n("F", 2, 6, fleetmap.Waypoint, ""), n("G", 4, 6, fleetmap.Waypoint, ""), n("H", 6, 6, fleetmap.Waypoint, ""),
			n("ST1", 0, 8, fleetmap.Station, "ST1"), n("ST2", 6, 8, fleetmap.Station, "ST2"),
		},
		Edges: []fleetmap.Edge{
			e("P1", "A"), e("P2", "B"), e("P3", "C"), e("CH1", "D"),
			e("A", "B"), e("B", "C"), e("C", "D"),
			e("A", "ASRS1-OUT1"), e("ASRS1-OUT1", "E"), e("B", "ASRS1-IN1"), e("ASRS1-IN1", "F"),
			e("C", "ASRS1-OUT2"), e("ASRS1-OUT2", "G"), e("D", "ASRS1-IN2"), e("ASRS1-IN2", "H"),
			e("E", "F"), e("F", "G"), e("G", "H"), e("E", "ST1"), e("H", "ST2"),
		}}
}

func mustMap(t *testing.T, s fleetmap.Spec) *fleetmap.Map {
	t.Helper()
	m, err := fleetmap.New(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// (The body of the brief's test is split into checkHandoffPoints/checkNodesAndEdges to stay
// under this repo's cyclop limit of 15; the assertions are the brief's, unchanged.)
func TestTheExampleFloorBuildsWithItsIdentityAndHandoffPoints(t *testing.T) {
	m := mustMap(t, floor())
	if m.FleetID() != "AMR1" || m.MapID() != "S1" || m.Version() != "2026-11-20.1" {
		t.Fatalf("identity = %s %s %s", m.FleetID(), m.MapID(), m.Version())
	}
	checkHandoffPoints(t, m)
	checkNodesAndEdges(t, m)
}

func checkHandoffPoints(t *testing.T, m *fleetmap.Map) {
	t.Helper()
	want := []string{"ASRS1-IN1", "ASRS1-IN2", "ASRS1-OUT1", "ASRS1-OUT2", "ST1", "ST2"}
	if got := m.HandoffPoints(); !reflect.DeepEqual(got, want) {
		t.Fatalf("HandoffPoints = %v", got)
	}
	if n, ok := m.NodeOfHandoff("ST1"); !ok || n != "ST1" {
		t.Fatalf("NodeOfHandoff(ST1) = %q %v", n, ok)
	}
	if _, ok := m.NodeOfHandoff("ST9"); ok {
		t.Fatal("an unknown handoff point resolved")
	}
}

func checkNodesAndEdges(t *testing.T, m *fleetmap.Map) {
	t.Helper()
	if len(m.NodeIDs()) != 18 || m.NodeIDs()[0] != "A" || m.NodeIDs()[17] != "ST2" {
		t.Fatalf("NodeIDs = %v", m.NodeIDs())
	}
	if n, ok := m.Node("CH1"); !ok || n.Kind != fleetmap.Charger || n.X != 6 || n.Y != 0 {
		t.Fatalf("Node(CH1) = %+v %v", n, ok)
	}
	if e, ok := m.Edge("E-ST1"); !ok || e.A != "E" || e.B != "ST1" {
		t.Fatalf("Edge(E-ST1) = %+v %v", e, ok)
	}
	if _, ok := m.Edge("nope"); ok {
		t.Fatal("unknown edge found")
	}
}

func TestNearestFindsTheClosestNodeWithinTheTolerance(t *testing.T) {
	m := mustMap(t, floor())
	if n, ok := m.Nearest(0.3, 0.1, 0.5); !ok || n != "P1" {
		t.Fatalf("Nearest = %q %v", n, ok)
	}
	if n, ok := m.Nearest(2, 0.5, 0.5); !ok || n != "P2" {
		t.Fatalf("Nearest at exactly the tolerance = %q %v", n, ok)
	}
	if _, ok := m.Nearest(1, 1, 0.5); ok {
		t.Fatal("a pose 1.4 m from every node matched")
	}
	if n, ok := m.Nearest(1, 0, 1); !ok || n != "P1" {
		t.Fatalf("an equidistant pose must pick the lower id, got %q", n)
	}
}

func TestNewRefusesInvalidMaps(t *testing.T) {
	cases := []struct {
		name string
		bad  func(*fleetmap.Spec)
	}{
		{"no fleet id", func(s *fleetmap.Spec) { s.FleetID = "" }},
		{"no map id", func(s *fleetmap.Spec) { s.MapID = "" }},
		{"no version", func(s *fleetmap.Spec) { s.Version = "" }},
		{"no node", func(s *fleetmap.Spec) { s.Nodes, s.Edges = nil, nil }},
		{"node without id", func(s *fleetmap.Spec) { s.Nodes[0].ID = "" }},
		{"duplicate node", func(s *fleetmap.Spec) { s.Nodes[1].ID = "P1" }},
		{"unknown kind", func(s *fleetmap.Spec) { s.Nodes[0].Kind = "DOCK" }},
		{"handoff on a waypoint", func(s *fleetmap.Spec) { s.Nodes[4].HandoffPoint = "X" }},
		{"duplicate handoff", func(s *fleetmap.Spec) { s.Nodes[17].HandoffPoint = "ST1" }},
		{"edge without id", func(s *fleetmap.Spec) { s.Edges[0].ID = "" }},
		{"duplicate edge id", func(s *fleetmap.Spec) { s.Edges[1].ID = "P1-A" }},
		{"edge to unknown node", func(s *fleetmap.Spec) { s.Edges[0].B = "Z" }},
		{"edge from unknown node", func(s *fleetmap.Spec) { s.Edges[0].A = "Z" }},
		{"loop", func(s *fleetmap.Spec) { s.Edges[0].B = "P1" }},
		{"negative speed", func(s *fleetmap.Spec) { s.Edges[0].MaxSpeed = -1 }},
		{"NaN x", func(s *fleetmap.Spec) { s.Nodes[0].X = math.NaN() }},
		{"infinite x", func(s *fleetmap.Spec) { s.Nodes[0].X = math.Inf(1) }},
		{"NaN y", func(s *fleetmap.Spec) { s.Nodes[0].Y = math.NaN() }},
		{"infinite y", func(s *fleetmap.Spec) { s.Nodes[0].Y = math.Inf(-1) }},
		{"NaN speed", func(s *fleetmap.Spec) { s.Edges[0].MaxSpeed = math.NaN() }},
		{"infinite speed", func(s *fleetmap.Spec) { s.Edges[0].MaxSpeed = math.Inf(1) }},
		{"second corridor between a pair", func(s *fleetmap.Spec) {
			s.Edges = append(s.Edges, fleetmap.Edge{ID: "A-P1", A: "A", B: "P1"})
		}},
		{"zero length", func(s *fleetmap.Spec) {
			s.Nodes = append(s.Nodes, fleetmap.Node{ID: "P1b", Kind: fleetmap.Waypoint})
			s.Edges = append(s.Edges, fleetmap.Edge{ID: "P1-P1b", A: "P1", B: "P1b"})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := floor()
			c.bad(&s)
			if _, err := fleetmap.New(s); !errors.Is(err, fleetmap.ErrInvalidMap) {
				t.Fatalf("New = %v, want ErrInvalidMap", err)
			}
		})
	}
}
