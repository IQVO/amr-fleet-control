package usecases_test

import (
	"strings"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// carry (Task 8 review): an order whose leg is CANCEL_REQUESTED is never resent and never failed
// ORDER_REJECTED by the resend limit: the leg waits for its cancelOrder answer.
func TestAnUnansweredCancelIsNeverResentAsAnOrderNorFailedRejected(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1") // never acknowledged
	if _, now, err := h.svc.CancelLeg(ctx, "AMR1", v.LegID); err != nil || now {
		t.Fatalf("cancel = %v %v", now, err)
	}
	h.report("R1", func(r *robot.Report) { r.LoadIDs = []string{"C1"} }) // the cancel stays unanswered
	for range 4 {
		h.tick(5 * time.Second)
	}
	if n := len(h.orders("R1")); n != 1 {
		t.Fatalf("orders = %d, a cancel-requested leg's order was resent", n)
	}
	// carry (Task 8 review, cancel re-send): the cancelOrder went out once and was re-sent
	// ORDER_RESEND_LIMIT times (5 s, 10 s, 15 s); the 4th tick (20 s) ends the leg through its own
	// timeout, CANCELLED with the robot's reported load, never ORDER_REJECTED.
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].Reason != mission.ReasonCancelled || !lf[0].ContainerOnRobot || lf[0].RobotError != "" {
		t.Fatalf("LegFailed = %+v", lf)
	}
	ia := h.instants("R1")
	if len(ia) != 4 {
		t.Fatalf("instant actions = %+v", ia)
	}
	for _, a := range ia {
		if a.ID != v.LegID+"-cancel" || a.Type != vda.CancelOrder {
			t.Fatalf("instant actions = %+v", ia)
		}
	}
	if r, _ := h.robots.Get(ctx, rid("R1")); r.MissionID() != "" || r.AwaitingAction() != "" {
		t.Fatalf("robot = %+v (not pending: nothing left to cancel)", r.Snapshot())
	}
}

// carry (Task 8 review): no order is resent while its robot awaits a cancelOrder answer (here the
// cancel of an order fleet control does not own); the resend resumes once the cancel is answered.
func TestNoOrderIsResentWhileItsRobotAwaitsACancel(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	h.report("R1", func(r *robot.Report) { r.OrderID, r.Pending = "ghost", true }) // a pre-restart order
	ia := h.instants("R1")
	if len(ia) != 1 || !strings.HasPrefix(ia[0].ID, "recover-") {
		t.Fatalf("instant actions = %+v", ia)
	}
	for range 4 {
		h.tick(5 * time.Second)
	}
	if n := len(h.orders("R1")); n != 1 || len(ofType[mission.LegFailed](h.events.All())) != 0 {
		t.Fatalf("orders = %d, LegFailed = %+v while the robot awaits its cancel", n, ofType[mission.LegFailed](h.events.All()))
	}
	h.report("R1", func(r *robot.Report) {
		setAction(r, robot.ActionState{ID: ia[0].ID, Status: robot.ActionFinished}, true)
		r.Pending = false
	})
	h.tick(time.Second)
	if os := h.orders("R1"); len(os) != 2 || os[1].OrderID != v.LegID || os[1].UpdateID != 0 {
		t.Fatalf("orders = %+v, want the resend once the cancel was answered", os)
	}
}

// carry (Task 8 review): an unanswered cancelOrder is re-sent with the SAME action id after
// ORDER_RESEND_AFTER; its answer (FAILED: no order to cancel) still ends the leg CANCELLED once.
func TestAnUnansweredCancelIsResentWithTheSameActionID(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	if _, now, err := h.svc.CancelLeg(ctx, "AMR1", v.LegID); err != nil || now {
		t.Fatalf("cancel = %v %v", now, err)
	}
	h.tick(4 * time.Second)
	if n := len(h.instants("R1")); n != 1 {
		t.Fatalf("cancel re-sent before ORDER_RESEND_AFTER: %d", n)
	}
	h.tick(time.Second)
	ia := h.instants("R1")
	if len(ia) != 2 || ia[0].ID != v.LegID+"-cancel" || ia[1].ID != v.LegID+"-cancel" || ia[1].Type != vda.CancelOrder {
		t.Fatalf("instant actions = %+v", ia)
	}
	h.action("R1", v.LegID+"-cancel", robot.ActionFailed, true)
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].Reason != mission.ReasonCancelled || lf[0].ContainerOnRobot {
		t.Fatalf("LegFailed = %+v", lf)
	}
	h.tick(5 * time.Second)
	if n := len(h.instants("R1")); n != 2 {
		t.Fatalf("an answered cancel was re-sent: %d", n)
	}
}

// carry (Task 8 review): a cancel the robot never answers to a still-pending order ends the leg
// CANCELLED after ORDER_RESEND_LIMIT re-sends and hands the robot over to a recover- cancel, so it
// is never offered a new order while that order may still run (D19).
func TestAnUnansweredCancelOfAPendingOrderHandsTheRobotToARecoverCancel(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	h.ack("R1")
	if _, now, err := h.svc.CancelLeg(ctx, "AMR1", v.LegID); err != nil || now {
		t.Fatalf("cancel = %v %v", now, err)
	}
	for range 4 {
		h.tick(5 * time.Second)
	}
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].Reason != mission.ReasonCancelled || lf[0].ContainerOnRobot {
		t.Fatalf("LegFailed = %+v", lf)
	}
	ia := h.instants("R1")
	if len(ia) != 5 || ia[3].ID != v.LegID+"-cancel" || !strings.HasPrefix(ia[4].ID, "recover-") {
		t.Fatalf("instant actions = %+v", ia)
	}
	if r, _ := h.robots.Get(ctx, rid("R1")); r.MissionID() != "" || r.AwaitingAction() != ia[4].ID || r.Assignable(20) {
		t.Fatalf("robot = %+v", r.Snapshot())
	}
}

// carry (Task 8 review): the resend limit's ORDER_REJECTED carries the robot's newest rejection
// as robot_error (D16), here one listed before the order (so the count never rose).
func TestTheResendLimitReportsTheRobotsRejection(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	h.report("R1", func(r *robot.Report) {
		r.Errors = []robot.Error{{Type: robot.ErrTypeInvalidOrder, Level: robot.LevelWarning}, {Type: "LOAD_NOT_AVAILABLE", Level: robot.LevelWarning}}
	})
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	for range 4 {
		h.tick(5 * time.Second)
	}
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].LegID != v.LegID || lf[0].Reason != mission.ReasonOrderRejected || lf[0].RobotError != "INVALID_ORDER" {
		t.Fatalf("LegFailed = %+v", lf)
	}
}
