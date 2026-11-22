package usecases_test

import (
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// carry (Task 4 review): a robot below CHARGE_LOW_PERCENT never gets a leg, even when no charger
// is free to send it to (responsibility 4, decision D6); at the threshold it is assignable again.
func TestARobotBelowTheChargeThresholdNeverGetsALeg(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R2", "CH1", 90) // R2 holds the only charger
	h.report("R2", func(r *robot.Report) {
		r.Errors = []robot.Error{{Type: robot.ErrTypeBlocked, Level: robot.LevelCritical}}
	}) // HELD: R2 takes no leg either
	h.online("R1", "P1", 19.99)
	if n := len(h.orders("R1")); n != 0 {
		t.Fatalf("R1 got %d orders with no free charger", n)
	}
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	if v.Status != "QUEUED" || v.Robot != (robot.ID{}) {
		t.Fatalf("a robot below the threshold took a leg: %+v", v)
	}
	h.report("R1", func(r *robot.Report) { r.Battery = 19.5 }) // every report schedules again
	if m, _ := h.missions.Get(ctx, v.LegID); m.Status() != mission.Queued || len(h.orders("R1")) != 0 {
		t.Fatalf("leg %s, %d orders to R1", m.Status(), len(h.orders("R1")))
	}
	h.report("R1", func(r *robot.Report) { r.Battery = 20 })
	if o := h.lastOrder("R1"); o.OrderID != v.LegID {
		t.Fatalf("at the threshold R1 takes the queued leg: %+v", o)
	}
}

// carry (Task 5 review): a CANCEL_REQUESTED leg is never extended while its robot drives on; its
// passed nodes are still released.
func TestACancelRequestedLegIsNeverExtended(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	h.ack("R1")
	if _, now, err := h.svc.CancelLeg(ctx, "AMR1", v.LegID); err != nil || now {
		t.Fatalf("cancel = %v %v", now, err)
	}
	h.reach("R1", 1)
	h.reach("R1", 2)
	if n := len(h.orders("R1")); n != 1 {
		t.Fatalf("a cancel-requested leg was extended: %d orders", n)
	}
	m, _ := h.missions.Get(ctx, v.LegID)
	if m.Status() != mission.CancelRequested || m.UpdateID() != 0 || m.BaseEnd() != 2 || m.Reached() != 2 {
		t.Fatalf("mission = %+v", m.State())
	}
	rec, _, _ := h.fleets.Get(ctx, "AMR1")
	if rec.Reservations.Nodes["P1"] != "" || rec.Reservations.Nodes["A"] != "" || rec.Reservations.Nodes["ASRS1-OUT1"] != "EMU/R1" {
		t.Fatalf("reservations = %+v", rec.Reservations)
	}
}

// carry (Task 5 review): a CANCEL_REQUESTED leg never reaches mission.Fail (which derives
// container_on_robot from PICKED_UP only): a rejection of its pending update does not fail it
// ORDER_REJECTED, and the cancel confirmation passes the robot's load.
func TestACancelRequestedLegEndsCancelledWithTheRobotsLoad(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	h.ack("R1")
	h.reach("R1", 1) // update 1 goes out, unacknowledged
	if o := h.lastOrder("R1"); o.UpdateID != 1 {
		t.Fatalf("update = %+v", o)
	}
	if _, now, err := h.svc.CancelLeg(ctx, "AMR1", v.LegID); err != nil || now {
		t.Fatalf("cancel = %v %v", now, err)
	}
	h.report("R1", func(r *robot.Report) { // update 1 rejected; the robot lists the container as its load
		r.Errors = append(r.Errors, robot.Error{Type: robot.ErrTypeInvalidOrder, Level: robot.LevelWarning})
		r.LoadIDs = []string{"C1"}
	})
	if lf := ofType[mission.LegFailed](h.events.All()); len(lf) != 0 {
		t.Fatalf("a cancel-requested leg failed before its cancel was answered: %+v", lf)
	}
	h.report("R1", func(r *robot.Report) {
		setAction(r, robot.ActionState{ID: v.LegID + "-cancel", Status: robot.ActionFinished}, true)
		r.Pending = false
	})
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].Reason != mission.ReasonCancelled || !lf[0].ContainerOnRobot || lf[0].RobotError != "" {
		t.Fatalf("LegFailed = %+v", lf)
	}
	if ia := h.instants("R1"); len(ia) != 1 || ia[0].ID != v.LegID+"-cancel" {
		t.Fatalf("instant actions = %+v (one cancelOrder)", ia)
	}
}

// carry (Task 5 review): the cancel of an order the robot never reported is confirmed by its
// cancelOrder answer (FAILED: no order to cancel), not left CANCEL_REQUESTED.
func TestACancelOfAnOrderTheRobotNeverReportedIsConfirmed(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	if _, now, err := h.svc.CancelLeg(ctx, "AMR1", v.LegID); err != nil || now {
		t.Fatalf("cancel = %v %v", now, err)
	}
	h.report("R1", func(r *robot.Report) {
		setAction(r, robot.ActionState{ID: v.LegID + "-cancel", Status: robot.ActionFailed}, true)
	})
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].LegID != v.LegID || lf[0].Reason != mission.ReasonCancelled || lf[0].ContainerOnRobot {
		t.Fatalf("LegFailed = %+v", lf)
	}
	if r, _ := h.robots.Get(ctx, rid("R1")); r.MissionID() == v.LegID || r.AwaitingAction() != "" {
		t.Fatalf("robot = %+v", r.Snapshot())
	}
}

// carry (Task 4 review): the emulator keeps only the newest 50 errors, so a rejection reported on
// a full list of rejections does not raise the count (decision D19's count-rise misses it). The
// leg stays ASSIGNED and unacknowledged; the tick's resend limit (Task 9) fails it.
func TestARejectionOnAFullErrorListIsLeftToTheResendLimit(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	full := make([]robot.Error, 50)
	for i := range full {
		full[i] = robot.Error{Type: robot.ErrTypeInvalidOrder, Level: robot.LevelWarning}
	}
	h.report("R1", func(r *robot.Report) { r.Errors = full }) // MaxReported stale rejections
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	if v.Status != "ASSIGNED" {
		t.Fatalf("leg = %+v", v)
	}
	h.report("R1", func(r *robot.Report) { // a new rejection: the oldest entry drops out
		r.Errors = append(r.Errors[1:], robot.Error{Type: robot.ErrTypeInvalidOrder, Level: robot.LevelWarning})
	})
	if lf := ofType[mission.LegFailed](h.events.All()); len(lf) != 0 {
		t.Fatalf("the count did not rise, yet the leg failed: %+v", lf)
	}
	if m, _ := h.missions.Get(ctx, v.LegID); m.Status() != mission.Assigned || !m.Unacknowledged() {
		t.Fatalf("mission = %+v", m.State())
	}
}
