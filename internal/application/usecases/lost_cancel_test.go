package usecases_test

import (
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// rejectedByTheResendLimit: R1 never acknowledged leg C1, the 4th tick failed it ORDER_REJECTED
// and sent its cancelOrder, which R1 has not answered; it returns the cancel's action id.
func rejectedByTheResendLimit(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	for range 4 {
		h.tick(5 * time.Second)
	}
	lf := ofType[mission.LegFailed](h.events.All())
	ia := h.instants("R1")
	if len(lf) != 1 || lf[0].Reason != mission.ReasonOrderRejected || len(ia) != 1 || ia[0].ID != v.LegID+"-cancel" {
		t.Fatalf("LegFailed = %+v, instant actions = %+v", lf, ia)
	}
	if r, _ := h.robots.Get(ctx, rid("R1")); r.AwaitingAction() != ia[0].ID {
		t.Fatalf("robot = %+v", r.Snapshot())
	}
	return h, ia[0].ID
}

// carry (Task 9 review) (c): a cancelOrder sent as a mission ends is never re-sent, so a lost one
// left the robot unassignable for good. A robot whose latest report is not pending and does not
// list the awaited action has nothing to cancel: the next tick stops awaiting it (anything sent
// later reaches the robot after that cancel, D18).
func TestALostCancelOfANonPendingRobotIsForgottenByTheNextTick(t *testing.T) {
	h, _ := rejectedByTheResendLimit(t)
	h.tick(time.Second)
	if r, _ := h.robots.Get(ctx, rid("R1")); r.AwaitingAction() != "" || !r.Assignable(20) {
		t.Fatalf("robot = %+v, want the lost cancel forgotten", r.Snapshot())
	}
}

// carry (Task 9 review) (c): a robot that lists the awaited cancel, still reports a pending order,
// or is not online keeps awaiting it.
func TestACancelTheRobotListsOrAPendingOrOfflineRobotIsStillAwaited(t *testing.T) {
	for name, then := range map[string]func(h *harness, id string){
		"listed": func(h *harness, id string) {
			h.report("R1", func(r *robot.Report) { setAction(r, robot.ActionState{ID: id, Status: robot.ActionWaiting}, true) })
		},
		"pending": func(h *harness, _ string) {
			h.report("R1", func(r *robot.Report) { r.OrderID, r.Pending = "ghost", true })
		},
		"offline": func(h *harness, _ string) {
			h.must(h.svc.HandleConnection(ctx, vda.Connection{Robot: rid("R1"), State: robot.ConnectionOffline, At: h.clock.Now()}))
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, id := rejectedByTheResendLimit(t)
			then(h, id)
			h.tick(time.Second)
			if r, _ := h.robots.Get(ctx, rid("R1")); r.AwaitingAction() != id || r.Assignable(20) {
				t.Fatalf("robot = %+v, want it still awaiting %s", r.Snapshot(), id)
			}
		})
	}
}

// carry (Task 9 review) (c): an awaited cancel an open CANCEL_REQUESTED leg owns is left to that
// leg's re-send and give-up (cancelResends), never forgotten by this rule.
func TestACancelRequestedLegsCancelIsNotForgotten(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1") // never acknowledged: R1 is not pending
	if _, now, err := h.svc.CancelLeg(ctx, "AMR1", v.LegID); err != nil || now {
		t.Fatalf("cancel = %v %v", now, err)
	}
	h.tick(time.Second)
	if r, _ := h.robots.Get(ctx, rid("R1")); r.AwaitingAction() != v.LegID+"-cancel" {
		t.Fatalf("robot = %+v", r.Snapshot())
	}
}

// carry (Task 10 review): the unanswered cancelOrder of a failed leg, while the robot still reports
// a pending order without listing it, goes out again with the same action id once
// ORDER_RESEND_AFTER has passed since it was sent; never earlier, never to a robot that lists it,
// and never to one that is not online.
func TestAnUnansweredUnownedCancelOfAPendingRobotIsResent(t *testing.T) {
	pending := func(h *harness) { h.report("R1", func(r *robot.Report) { r.OrderID, r.Pending = "ghost", true }) }
	for name, c := range map[string]struct {
		then  func(h *harness, id string)
		after time.Duration
		sent  int
	}{
		"due":     {func(h *harness, _ string) { pending(h) }, 5 * time.Second, 2},
		"not due": {func(h *harness, _ string) { pending(h) }, 4 * time.Second, 1},
		"listed": {func(h *harness, id string) {
			pending(h)
			h.report("R1", func(r *robot.Report) { setAction(r, robot.ActionState{ID: id, Status: robot.ActionWaiting}, true) })
		}, 5 * time.Second, 1},
		"offline": {func(h *harness, _ string) {
			pending(h)
			h.must(h.svc.HandleConnection(ctx, vda.Connection{Robot: rid("R1"), State: robot.ConnectionOffline, At: h.clock.Now()}))
		}, 5 * time.Second, 1},
	} {
		t.Run(name, func(t *testing.T) {
			h, id := rejectedByTheResendLimit(t)
			c.then(h, id)
			h.tick(c.after)
			ia := h.instants("R1")
			if len(ia) != c.sent || ia[len(ia)-1].ID != id {
				t.Fatalf("instant actions = %+v, want %d cancel(s) %s", ia, c.sent, id)
			}
			if r, _ := h.robots.Get(ctx, rid("R1")); r.AwaitingAction() != id || r.Assignable(20) {
				t.Fatalf("robot = %+v, want it still awaiting %s", r.Snapshot(), id)
			}
		})
	}
}
