package usecases_test

import (
	"strings"
	"testing"
	"time"

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
	for range 4 {
		h.tick(5 * time.Second)
	}
	if n := len(h.orders("R1")); n != 1 {
		t.Fatalf("orders = %d, a cancel-requested leg's order was resent", n)
	}
	for _, lf := range ofType[mission.LegFailed](h.events.All()) {
		if lf.Reason == mission.ReasonOrderRejected {
			t.Fatalf("an unanswered cancel failed ORDER_REJECTED: %+v", lf)
		}
	}
	if ia := h.instants("R1"); len(ia) != 1 || ia[0].ID != v.LegID+"-cancel" {
		t.Fatalf("instant actions = %+v", ia)
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
