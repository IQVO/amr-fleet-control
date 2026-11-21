package dispatch_test

import (
	"reflect"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/domain/dispatch"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// floor is config/map.example.yaml (Task 2), built in code.
func floor(t *testing.T) *fleetmap.Map {
	t.Helper()
	n := func(id string, x, y float64, k fleetmap.NodeKind) fleetmap.Node {
		h := ""
		if k == fleetmap.Port || k == fleetmap.Station {
			h = id
		}
		return fleetmap.Node{ID: id, X: x, Y: y, Kind: k, HandoffPoint: h}
	}
	e := func(a, b string) fleetmap.Edge { return fleetmap.Edge{ID: a + "-" + b, A: a, B: b} }
	m, err := fleetmap.New(fleetmap.Spec{FleetID: "AMR1", MapID: "S1", Version: "v1",
		Nodes: []fleetmap.Node{n("P1", 0, 0, fleetmap.Parking), n("P2", 2, 0, fleetmap.Parking), n("P3", 4, 0, fleetmap.Parking),
			n("CH1", 6, 0, fleetmap.Charger), n("A", 0, 2, fleetmap.Waypoint), n("B", 2, 2, fleetmap.Waypoint),
			n("C", 4, 2, fleetmap.Waypoint), n("D", 6, 2, fleetmap.Waypoint), n("ASRS1-OUT1", 0, 4, fleetmap.Port),
			n("ASRS1-IN1", 2, 4, fleetmap.Port), n("ASRS1-OUT2", 4, 4, fleetmap.Port), n("ASRS1-IN2", 6, 4, fleetmap.Port),
			n("E", 0, 6, fleetmap.Waypoint), n("F", 2, 6, fleetmap.Waypoint), n("G", 4, 6, fleetmap.Waypoint),
			n("H", 6, 6, fleetmap.Waypoint), n("ST1", 0, 8, fleetmap.Station), n("ST2", 6, 8, fleetmap.Station),
			n("ISLE", 20, 20, fleetmap.Parking)},
		Edges: []fleetmap.Edge{e("P1", "A"), e("P2", "B"), e("P3", "C"), e("CH1", "D"), e("A", "B"), e("B", "C"), e("C", "D"),
			e("A", "ASRS1-OUT1"), e("ASRS1-OUT1", "E"), e("B", "ASRS1-IN1"), e("ASRS1-IN1", "F"), e("C", "ASRS1-OUT2"),
			e("ASRS1-OUT2", "G"), e("D", "ASRS1-IN2"), e("ASRS1-IN2", "H"), e("E", "F"), e("F", "G"), e("G", "H"),
			e("E", "ST1"), e("H", "ST2")}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func id(s string) robot.ID { return robot.ID{Manufacturer: "EMU", SerialNumber: s} }

func TestChooseTakesTheNearestThenTheFullestThenTheLowestKey(t *testing.T) {
	m := floor(t)
	cands := []dispatch.Candidate{{Robot: id("R3"), Node: "P3", Battery: 90}, {Robot: id("R2"), Node: "P2", Battery: 90}, {Robot: id("R1"), Node: "P1", Battery: 30}}
	if c, ok := dispatch.Choose(m, "ST1", cands); !ok || c.Robot != id("R1") {
		t.Fatalf("nearest to ST1 = %+v %v (P1 is 8 m away, P2 10 m, P3 12 m)", c, ok)
	}
	tie := []dispatch.Candidate{{Robot: id("R3"), Node: "P3", Battery: 60}, {Robot: id("R1"), Node: "P1", Battery: 50}}
	if c, _ := dispatch.Choose(m, "ASRS1-IN1", tie); c.Robot != id("R3") {
		t.Fatalf("equal distance (6 m) must prefer the fuller battery, got %+v", c)
	}
	tie[0].Battery = 50
	if c, _ := dispatch.Choose(m, "ASRS1-IN1", tie); c.Robot != id("R1") {
		t.Fatalf("equal distance and battery must prefer the lower key, got %+v", c)
	}
	if c, ok := dispatch.Choose(m, "ST1", []dispatch.Candidate{{Robot: id("R9"), Node: "ISLE", Battery: 99}, cands[1]}); !ok || c.Robot != id("R2") {
		t.Fatalf("an unreachable candidate must be skipped: %+v", c)
	}
	if _, ok := dispatch.Choose(m, "ST1", nil); ok {
		t.Fatal("no candidate chose a robot")
	}
	if _, ok := dispatch.Choose(m, "ST1", []dispatch.Candidate{{Robot: id("R9"), Node: "ISLE"}}); ok {
		t.Fatal("only unreachable candidates chose a robot")
	}
}

func TestPlanChainsShortestPathsAndPutsEachActionOnItsTarget(t *testing.T) {
	m := floor(t)
	steps, ok := dispatch.Plan(m, "P1", []dispatch.Target{{Node: "ST1", Action: mission.ActionPick}, {Node: "ASRS1-IN1", Action: mission.ActionDrop}}, nil)
	want := []mission.Step{
		{Node: "P1"}, {Node: "A", Edge: "P1-A"}, {Node: "ASRS1-OUT1", Edge: "A-ASRS1-OUT1"}, {Node: "E", Edge: "ASRS1-OUT1-E"},
		{Node: "ST1", Edge: "E-ST1", Action: mission.ActionPick}, {Node: "E", Edge: "E-ST1"}, {Node: "F", Edge: "E-F"},
		{Node: "ASRS1-IN1", Edge: "ASRS1-IN1-F", Action: mission.ActionDrop},
	}
	if !ok || !reflect.DeepEqual(steps, want) {
		t.Fatalf("Plan = %+v %v", steps, ok)
	}
	steps, ok = dispatch.Plan(m, "ST1", []dispatch.Target{{Node: "ST1", Action: mission.ActionPick}, {Node: "ASRS1-IN1", Action: mission.ActionDrop}}, nil)
	if !ok || len(steps) != 4 || steps[0] != (mission.Step{Node: "ST1", Action: mission.ActionPick}) || steps[3].Action != mission.ActionDrop {
		t.Fatalf("a robot already at the pickup = %+v", steps)
	}
	steps, ok = dispatch.Plan(m, "ST1", []dispatch.Target{{Node: "ASRS1-IN1", Action: mission.ActionDrop}}, map[string]bool{"F": true})
	if !ok || len(steps) != 6 || steps[2].Node != "ASRS1-OUT1" {
		t.Fatalf("avoiding F = %+v", steps)
	}
	if _, ok := dispatch.Plan(m, "P1", []dispatch.Target{{Node: "ISLE"}}, nil); ok {
		t.Fatal("an unreachable target was planned")
	}
	if _, ok := dispatch.Plan(m, "ZZ", nil, nil); ok {
		t.Fatal("an unknown start was planned")
	}
	if _, ok := dispatch.Plan(m, "ST1", []dispatch.Target{{Node: "ST1", Action: mission.ActionPick}, {Node: "ST1", Action: mission.ActionDrop}}, nil); ok {
		t.Fatal("two actions on one step were planned")
	}
}

func holders(nodes, edges map[string]string) (func(string) (string, bool), func(string) (string, bool)) {
	return func(id string) (string, bool) { h, ok := nodes[id]; return h, ok },
		func(id string) (string, bool) { h, ok := edges[id]; return h, ok }
}

func TestWindowReleasesAheadUntilAnotherRobotHoldsTheNextElement(t *testing.T) {
	m := floor(t)
	route, _ := dispatch.Plan(m, "P1", []dispatch.Target{{Node: "ST1", Action: mission.ActionPick}, {Node: "ASRS1-IN1", Action: mission.ActionDrop}}, nil)
	free, freeE := holders(map[string]string{"P1": "EMU/R1"}, nil)
	if end, b, at := dispatch.Window(route, 0, 0, 2, "EMU/R1", free, freeE); end != 2 || b != "" || at != "" {
		t.Fatalf("free window = %d %q %q", end, b, at)
	}
	if end, _, _ := dispatch.Window(route, 2, 1, 2, "EMU/R1", free, freeE); end != 3 {
		t.Fatalf("window after reaching index 1 = %d, want 3", end)
	}
	if end, _, _ := dispatch.Window(route, 6, 6, 5, "EMU/R1", free, freeE); end != 7 {
		t.Fatalf("the window is capped at the last step, got %d", end)
	}
	if end, _, _ := dispatch.Window(route, 3, 0, 2, "EMU/R1", free, freeE); end != 3 {
		t.Fatalf("a base already past the window stays, got %d", end)
	}
	byNode, byNodeE := holders(map[string]string{"ASRS1-OUT1": "EMU/R2"}, nil)
	if end, b, at := dispatch.Window(route, 0, 0, 2, "EMU/R1", byNode, byNodeE); end != 1 || b != "EMU/R2" || at != "ASRS1-OUT1" {
		t.Fatalf("held node = %d %q %q", end, b, at)
	}
	byEdge, byEdgeE := holders(nil, map[string]string{"P1-A": "EMU/R3"})
	if end, b, at := dispatch.Window(route, 0, 0, 2, "EMU/R1", byEdge, byEdgeE); end != 0 || b != "EMU/R3" || at != "A" {
		t.Fatalf("held edge = %d %q %q", end, b, at)
	}
	own, ownE := holders(map[string]string{"A": "EMU/R1"}, map[string]string{"P1-A": "EMU/R1"})
	if end, _, _ := dispatch.Window(route, 0, 0, 2, "EMU/R1", own, ownE); end != 2 {
		t.Fatalf("its own reservations never block a robot, got %d", end)
	}
}

func TestNearestFreeIsTheClosestFreeNodeOfAKind(t *testing.T) {
	m := floor(t)
	all := func(string) bool { return true }
	if n, ok := dispatch.NearestFree(m, "ST1", fleetmap.Parking, all); !ok || n != "P1" {
		t.Fatalf("nearest parking from ST1 = %q %v", n, ok)
	}
	if n, _ := dispatch.NearestFree(m, "ST1", fleetmap.Parking, func(id string) bool { return id != "P1" }); n != "P2" {
		t.Fatalf("with P1 taken = %q", n)
	}
	if n, _ := dispatch.NearestFree(m, "P1", fleetmap.Charger, all); n != "CH1" {
		t.Fatalf("nearest charger = %q", n)
	}
	if _, ok := dispatch.NearestFree(m, "ST1", fleetmap.Charger, func(string) bool { return false }); ok {
		t.Fatal("no free charger, yet one was found")
	}
	if n, _ := dispatch.NearestFree(m, "ISLE", fleetmap.Parking, all); n != "ISLE" {
		t.Fatalf("an isolated parking robot's nearest parking is its own node, got %q", n)
	}
}
