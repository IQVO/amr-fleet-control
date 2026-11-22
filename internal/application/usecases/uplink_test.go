package usecases_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

func TestTheRegistryIsLearnedFromTheRobots(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.must(h.svc.HandleFactsheet(ctx, vda.Factsheet{Robot: rid("R1"), Factsheet: robot.Factsheet{SeriesName: "EMU-AMR", MaxSpeed: 1.5}}))
	r, err := h.robots.Get(ctx, rid("R1"))
	if err != nil || r.State() != robot.Stopped || !r.HasFactsheet() || r.Factsheet().MaxSpeed != 1.5 {
		t.Fatalf("robot from a factsheet = %+v %v", r, err)
	}
	h.must(h.svc.HandleState(ctx, vda.State{Robot: robot.ID{Manufacturer: "EMU"}, Report: robot.Report{}}))
	if list, _ := h.robots.List(ctx); len(list) != 1 {
		t.Fatalf("an invalid robot id was registered: %d robots", len(list))
	}
	h.report("R1", func(r *robot.Report) { r.X, r.Y, r.Battery = 0.1, 0.1, 90 }) // no lastNodeId: the nearest node within 0.25 m
	if r, _ := h.robots.Get(ctx, rid("R1")); r.Node() != "P1" || r.State() != robot.Idle {
		t.Fatalf("robot = node %q state %s", r.Node(), r.State())
	}
}

// (The body of the brief's test is split into checkFirstExtension to stay under this repo's
// cyclop limit of 15; the assertions are the brief's, unchanged.)
func TestAnAcknowledgedOrderIsExtendedAsTheRobotAdvances(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	h.ack("R1")
	if n := len(h.orders("R1")); n != 1 {
		t.Fatalf("an ack at index 0 extended the base: %d orders", n)
	}
	checkFirstExtension(t, h)
	h.reach("R1", 2)
	if n := len(h.orders("R1")); n != 2 {
		t.Fatalf("an unacknowledged update was extended again: %d orders", n)
	}
	h.ack("R1")
	if o := h.lastOrder("R1"); o.UpdateID != 2 || o.Nodes[0].ID != "E" || o.Nodes[0].Seq != 6 {
		t.Fatalf("second update = %+v", o)
	}
	if lf := ofType[mission.LegFailed](h.events.All()); len(lf) != 0 || v.LegID == "" {
		t.Fatalf("unexpected failures %+v", lf)
	}
}

// checkFirstExtension is the brief's first base extension of TestAnAcknowledgedOrderIsExtendedAsTheRobotAdvances.
func checkFirstExtension(t *testing.T, h *harness) {
	t.Helper()
	h.reach("R1", 1)
	o := h.lastOrder("R1")
	if o.UpdateID != 1 || !reflect.DeepEqual(nodeIDs(o), []string{"ASRS1-OUT1", "E", "ST1", "E", "F", "ASRS1-IN1"}) || o.Nodes[0].Seq != 4 {
		t.Fatalf("update = %+v", o)
	}
	if n, e := released(o); n != 2 || e != 1 || o.Edges[0].ID != "ASRS1-OUT1-E" || o.Edges[0].Seq != 5 {
		t.Fatalf("update released %d/%d, edges %+v", n, e, o.Edges)
	}
	rec, _, _ := h.fleets.Get(ctx, "AMR1")
	if rec.Reservations.Nodes["P1"] != "" || rec.Reservations.Edges["P1-A"] != "" || rec.Reservations.Nodes["E"] != "EMU/R1" || rec.Reservations.Nodes["A"] != "EMU/R1" {
		t.Fatalf("passed elements must be released, new ones reserved: %+v", rec.Reservations)
	}
}

// (The body of the brief's test is split into checkParked to stay under this repo's cyclop
// limit of 15; the assertions are the brief's, unchanged.)
func TestALegIsPickedUpCompletedAndTheRobotParked(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	h.drive("R1", 0, 4)
	h.action("R1", v.LegID+"-pick", robot.ActionFinished, false)
	pu := ofType[mission.ContainerPickedUp](h.events.All())
	if len(pu) != 1 || pu[0] != (mission.ContainerPickedUp{LegID: v.LegID, ContainerID: "C1", From: "ST1", Reference: "tl-C1", Robot: rid("R1"), At: t0, RobotTime: t0.Add(time.Hour)}) {
		t.Fatalf("ContainerPickedUp = %+v", pu)
	}
	h.drive("R1", 5, 7)
	h.report("R1", func(r *robot.Report) { r.Pending = false })
	h.action("R1", v.LegID+"-drop", robot.ActionFinished, false)
	lc := ofType[mission.LegCompleted](h.events.All())
	if len(lc) != 1 || lc[0].LegID != v.LegID || lc[0].To != "ASRS1-IN1" || lc[0].Robot != rid("R1") {
		t.Fatalf("LegCompleted = %+v", lc)
	}
	if got := h.metrics.Ended(); !reflect.DeepEqual(got, []string{"completed/"}) {
		t.Fatalf("metrics = %v", got)
	}
	checkParked(t, h, v)
}

// checkParked is the brief's parking tail of TestALegIsPickedUpCompletedAndTheRobotParked.
func checkParked(t *testing.T, h *harness, v usecases.LegView) {
	t.Helper()
	park := h.lastOrder("R1")
	if park.OrderID == v.LegID || !reflect.DeepEqual(nodeIDs(park), []string{"ASRS1-IN1", "B", "P2"}) {
		t.Fatalf("park order = %+v (the nearest free parking from the port is P2)", park)
	}
	h.drive("R1", 0, 2)
	h.report("R1", func(r *robot.Report) { r.Pending = false })
	if m, _ := h.missions.Get(ctx, park.OrderID); m.Status() != mission.Done || m.Kind() != mission.Park {
		t.Fatalf("park mission = %s %s", m.Kind(), m.Status())
	}
	if n := len(h.orders("R1")); n != 7 { // leg updates 0-5, then the park order
		t.Fatalf("orders = %d", n)
	}
	if r, _ := h.robots.Get(ctx, rid("R1")); r.State() != robot.Idle || r.MissionID() != "" || r.Node() != "P2" {
		t.Fatalf("robot after parking = %+v", r.Snapshot())
	}
}

func TestARejectedOrderFailsItsLegOnceAndAStaleRejectionIsIgnored(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	h.report("R1", func(r *robot.Report) {
		r.Errors = []robot.Error{{Type: robot.ErrTypeInvalidOrder, Level: robot.LevelWarning}}
	})
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].LegID != v.LegID || lf[0].Reason != mission.ReasonOrderRejected || lf[0].RobotError != "INVALID_ORDER" || lf[0].ContainerOnRobot {
		t.Fatalf("LegFailed = %+v", lf)
	}
	if len(h.instants("R1")) != 0 {
		t.Fatal("a rejected NEW order has nothing to cancel")
	}
	w := h.leg("C2", "ST1", "ASRS1-IN1")
	if w.Status != "ASSIGNED" {
		t.Fatalf("the robot must take the next leg: %+v", w)
	}
	h.report("R1", func(r *robot.Report) { r.Battery = 89 }) // the old INVALID_ORDER is still listed
	if n := len(ofType[mission.LegFailed](h.events.All())); n != 1 {
		t.Fatalf("a stale rejection failed the next leg: %d failures", n)
	}
}

func TestAFailedPickFailsTheLegAndCancelsTheOrder(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	h.drive("R1", 0, 4)
	h.report("R1", func(r *robot.Report) {
		setAction(r, robot.ActionState{ID: v.LegID + "-pick", Status: robot.ActionFailed}, false)
		r.Errors = append(r.Errors, robot.Error{Type: "LOAD_NOT_AVAILABLE", Level: robot.LevelWarning})
	})
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].Reason != mission.ReasonPickFailed || lf[0].RobotError != "LOAD_NOT_AVAILABLE" {
		t.Fatalf("LegFailed = %+v", lf)
	}
	if ia := h.instants("R1"); len(ia) != 1 || ia[0].Type != vda.CancelOrder || ia[0].ID != v.LegID+"-cancel" {
		t.Fatalf("instant = %+v", ia)
	}
	h.report("R1", func(r *robot.Report) {
		setAction(r, robot.ActionState{ID: v.LegID + "-cancel", Status: robot.ActionFinished}, true)
		r.Pending = false
	})
	if r, _ := h.robots.Get(ctx, rid("R1")); r.AwaitingAction() != "" || r.MissionID() == "" {
		t.Fatalf("after the cancel the robot leaves the station for parking: %+v", r.Snapshot())
	}
}

func TestAFatalErrorFailsTheLegAsRobotFault(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	w := h.leg("C2", "ST1", "ASRS1-IN1")
	h.ack("R1")
	h.report("R1", func(r *robot.Report) {
		r.Errors = []robot.Error{{Type: robot.ErrTypeBatteryEmpty, Level: robot.LevelFatal}}
	})
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].LegID != w.LegID || lf[0].Reason != mission.ReasonRobotFault || lf[0].RobotError != "BATTERY_EMPTY" {
		t.Fatalf("LegFailed = %+v", lf)
	}
	if ia := h.instants("R1"); len(ia) != 1 || ia[0].ID != w.LegID+"-cancel" {
		t.Fatalf("instant = %+v", ia)
	}
	held := ofType[robot.RobotStateChanged](h.events.All())
	if last := held[len(held)-1]; last.State != robot.Held || !reflect.DeepEqual(last.ErrorTypes, []string{"BATTERY_EMPTY"}) {
		t.Fatalf("RobotStateChanged = %+v", last)
	}
}

func TestAnOrderNoMissionOwnsIsCancelledBeforeTheRobotIsUsed(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	h.report("R1", func(r *robot.Report) { r.OrderID, r.Pending = "before-restart", true })
	ia := h.instants("R1")
	if len(ia) != 1 || ia[0].Type != vda.CancelOrder || ia[0].ID[:8] != "recover-" {
		t.Fatalf("instant = %+v", ia)
	}
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	if v.Status != "QUEUED" {
		t.Fatalf("a robot with a cancel in flight took a leg: %+v", v)
	}
	h.report("R1", func(r *robot.Report) {
		r.Pending = false
		setAction(r, robot.ActionState{ID: ia[0].ID, Status: robot.ActionFinished}, true)
	})
	if o := h.lastOrder("R1"); o.OrderID != v.LegID {
		t.Fatalf("after the cancel the leg must be assigned: %+v", o)
	}
}
