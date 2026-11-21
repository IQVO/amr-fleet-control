package usecases_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/fleetrow"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// corridor: stations S1 and S2 joined by M1-M2; with bypass, also S1-B1-B2-S2 and M1-B1, M2-B2.
func corridor(t *testing.T, bypass bool) *fleetmap.Map {
	t.Helper()
	nodes := []fleetmap.Node{{ID: "S1", X: 0, Y: 0, Kind: fleetmap.Station, HandoffPoint: "S1"}, {ID: "M1", X: 2, Y: 0, Kind: fleetmap.Waypoint},
		{ID: "M2", X: 4, Y: 0, Kind: fleetmap.Waypoint}, {ID: "S2", X: 6, Y: 0, Kind: fleetmap.Station, HandoffPoint: "S2"}}
	edges := []fleetmap.Edge{{ID: "S1-M1", A: "S1", B: "M1"}, {ID: "M1-M2", A: "M1", B: "M2"}, {ID: "M2-S2", A: "M2", B: "S2"}}
	if bypass {
		nodes = append(nodes, fleetmap.Node{ID: "B1", X: 2, Y: 3, Kind: fleetmap.Waypoint}, fleetmap.Node{ID: "B2", X: 4, Y: 3, Kind: fleetmap.Waypoint})
		edges = append(edges, fleetmap.Edge{ID: "S1-B1", A: "S1", B: "B1"}, fleetmap.Edge{ID: "B1-B2", A: "B1", B: "B2"},
			fleetmap.Edge{ID: "B2-S2", A: "B2", B: "S2"}, fleetmap.Edge{ID: "M1-B1", A: "M1", B: "B1"}, fleetmap.Edge{ID: "M2-B2", A: "M2", B: "B2"})
	}
	m, err := fleetmap.New(fleetmap.Spec{FleetID: "AMR1", MapID: "S1", Version: "v1", Nodes: nodes, Edges: edges})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (h *harness) tick(d time.Duration) {
	h.t.Helper()
	h.clock.Advance(d)
	h.must(h.svc.Tick(ctx))
}

func TestAStaleRobotStopsAndItsLegFailsOfflineWithoutACancel(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	h.ack("R1")
	h.tick(90 * time.Second)
	if n := len(ofType[robot.RobotStateChanged](h.events.All())); n != 3 {
		t.Fatalf("90 s is not stale yet: %d state changes", n)
	}
	h.tick(time.Second)
	rsc := ofType[robot.RobotStateChanged](h.events.All())
	if last := rsc[len(rsc)-1]; last.State != robot.Stopped || last.Previous != robot.Execute {
		t.Fatalf("stale = %+v", last)
	}
	h.tick(60 * time.Second)
	if lf := ofType[mission.LegFailed](h.events.All()); len(lf) != 0 {
		t.Fatalf("failed after exactly 60 s offline: %+v", lf)
	}
	h.tick(time.Second)
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].LegID != v.LegID || lf[0].Reason != mission.ReasonRobotOffline || lf[0].RobotError != "" {
		t.Fatalf("LegFailed = %+v", lf)
	}
	if len(h.instants("R1")) != 0 {
		t.Fatal("a cancel was sent to an offline robot")
	}
	h.report("R1", func(r *robot.Report) { r.Pending = true }) // back online, still running the failed leg's order
	if ia := h.instants("R1"); len(ia) != 1 || !strings.HasPrefix(ia[0].ID, "recover-") {
		t.Fatalf("the returning robot's orphan order must be cancelled: %+v", ia)
	}
}

func TestAHeldRobotFailsItsLegBlockedAfterTheTimeout(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	h.ack("R1")
	h.report("R1", func(r *robot.Report) {
		r.Errors = []robot.Error{{Type: robot.ErrTypeBlocked, Level: robot.LevelCritical}}
	})
	h.tick(60 * time.Second)
	if len(ofType[mission.LegFailed](h.events.All())) != 0 {
		t.Fatal("failed at exactly the timeout")
	}
	h.tick(time.Second)
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].Reason != mission.ReasonRobotBlocked || lf[0].RobotError != "ROBOT_BLOCKED" || lf[0].LegID != v.LegID {
		t.Fatalf("LegFailed = %+v", lf)
	}
	if ia := h.instants("R1"); len(ia) != 1 || ia[0].Type != vda.CancelOrder {
		t.Fatalf("instant = %+v", ia)
	}
}

func TestAnUnacknowledgedOrderIsResentThenFailedRejected(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	h.tick(4 * time.Second)
	if n := len(h.orders("R1")); n != 1 {
		t.Fatalf("resent before ORDER_RESEND_AFTER: %d", n)
	}
	for i := 1; i <= 3; i++ {
		h.tick(time.Second)
		if n := len(h.orders("R1")); n != 1+i {
			t.Fatalf("resend %d: %d orders", i, n)
		}
		if o := h.lastOrder("R1"); o.UpdateID != 0 || o.OrderID != v.LegID {
			t.Fatalf("a resend repeats the same update: %+v", o)
		}
		h.clock.Advance(4 * time.Second)
	}
	h.tick(time.Second)
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].Reason != mission.ReasonOrderRejected || lf[0].RobotError != "" {
		t.Fatalf("after ORDER_RESEND_LIMIT resends: %+v", lf)
	}
	if n := len(h.orders("R1")); n != 4 {
		t.Fatalf("orders = %d, want the first and three resends", n)
	}
}

func TestAWaitingRobotIsReroutedAfterRerouteAfter(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "ASRS1-OUT1", 90) // R1 parks towards P1 but never moves: it holds OUT1, A and P1
	h.online("R2", "P2", 90)
	h.ack("R1")
	h.leg("C1", "ASRS1-OUT1", "ST1")
	h.ack("R2")
	h.reach("R2", 1)
	h.tick(9 * time.Second)
	if n := len(h.orders("R2")); n != 1 {
		t.Fatalf("re-routed before REROUTE_AFTER: %d orders", n)
	}
	h.tick(time.Second)
	o := h.lastOrder("R2")
	if o.UpdateID != 1 || !reflect.DeepEqual(nodeIDs(o), []string{"B", "ASRS1-IN1", "F", "E", "ASRS1-OUT1", "E", "ST1"}) {
		t.Fatalf("re-route = %+v %v", o.UpdateID, nodeIDs(o))
	}
	if o.Nodes[4].Actions[0].Type != mission.ActionPick || o.Nodes[6].Actions[0].Type != mission.ActionDrop || o.Nodes[0].Seq != 2 {
		t.Fatalf("the re-route keeps the actions and the base end's sequence id: %+v", o.Nodes)
	}
	rec, _, _ := h.fleets.Get(ctx, "AMR1")
	if _, waiting := rec.Reservations.Waits["EMU/R2"]; waiting {
		t.Fatal("a re-routed robot is no longer waiting")
	}
}

func deadlock(t *testing.T, bypass bool) *harness {
	t.Helper()
	h := newHarness(t, corridor(t, bypass), func(c *usecases.Config) { c.ReleaseAhead = 1 })
	h.online("R1", "S1", 90)
	h.online("R2", "S2", 90)
	h.leg("C1", "S1", "S2")
	h.leg("C2", "S2", "S1")
	h.ack("R1")
	h.ack("R2")
	h.reach("R1", 1)
	h.reach("R2", 1)
	return h
}

func TestADeadlockIsPublishedOnceAndItsYoungestLegRerouted(t *testing.T) {
	h := deadlock(t, true)
	dd := ofType[fleetmap.DeadlockDetected](h.events.All())
	if len(dd) != 1 || !strings.HasPrefix(dd[0].DeadlockID, "dl-") || !reflect.DeepEqual(dd[0].Robots, []string{"EMU/R1", "EMU/R2"}) ||
		!reflect.DeepEqual(dd[0].Nodes, []string{"M2", "M1"}) || !dd[0].At.Equal(t0) {
		t.Fatalf("DeadlockDetected = %+v", dd)
	}
	o := h.lastOrder("R2")
	if o.UpdateID != 1 || !reflect.DeepEqual(nodeIDs(o), []string{"M2", "B2", "B1", "S1"}) {
		t.Fatalf("R2 (the youngest leg) re-route = %d %v", o.UpdateID, nodeIDs(o))
	}
	h.tick(time.Second)
	if n := len(ofType[fleetmap.DeadlockDetected](h.events.All())); n != 1 {
		t.Fatalf("one deadlock is published once: %d", n)
	}
}

func TestADeadlockThatCannotBeReroutedFailsItsYoungestLegBlocked(t *testing.T) {
	h := deadlock(t, false)
	if n := len(ofType[fleetmap.DeadlockDetected](h.events.All())); n != 1 {
		t.Fatalf("deadlocks = %d", n)
	}
	h.tick(60 * time.Second)
	if len(ofType[mission.LegFailed](h.events.All())) != 0 {
		t.Fatal("failed at exactly DEADLOCK_TIMEOUT")
	}
	h.tick(time.Second)
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].Reason != mission.ReasonRobotBlocked || lf[0].Robot != rid("R2") || lf[0].ContainerOnRobot {
		t.Fatalf("LegFailed = %+v", lf)
	}
	if ia := h.instants("R2"); len(ia) != 1 || ia[0].Type != vda.CancelOrder {
		t.Fatalf("R2 cancel = %+v", ia)
	}
}

func TestRecoverRetiresMissionsOfAnotherMapVersionAndAsksEveryRobotForItsState(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	old, _ := mission.NewLeg(mission.LegSpec{ID: "leg-old", MapVersion: "v0", ContainerID: "C9", From: "ST1", To: "ST2", PickupNode: "ST1", DropNode: "ST2", At: t0})
	_, _ = old.Assign(rid("R1"), []mission.Step{{Node: "ST1", Action: mission.ActionPick}, {Node: "E", Edge: "E-ST1"}, {Node: "ST2", Edge: "x", Action: mission.ActionDrop}}, 0, t0)
	h.must(h.missions.Insert(ctx, old))
	h.must(h.fleets.Save(ctx, "AMR1", fleetrow.Record{MapVersion: "v0", Reservations: fleetmap.State{Nodes: map[string]string{"ZZ": "EMU/R9"}}}))
	h.must(h.svc.Recover(ctx))
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].LegID != "leg-old" || lf[0].Reason != mission.ReasonMapVersionRetired {
		t.Fatalf("LegFailed = %+v", lf)
	}
	ia := h.instants("R1")
	if len(ia) != 2 || ia[0].ID != "leg-old-cancel" || ia[1].Type != vda.StateRequest || ia[1].Blocking != vda.BlockingNone {
		t.Fatalf("instant = %+v", ia)
	}
	rec, _, _ := h.fleets.Get(ctx, "AMR1")
	if rec.MapVersion != "v1" || rec.Reservations.Nodes["ZZ"] != "" || rec.Reservations.Nodes["P1"] != "EMU/R1" {
		t.Fatalf("the old version's reservations must be dropped: %+v", rec)
	}
}
