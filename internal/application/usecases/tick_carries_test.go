package usecases_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
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
	recoverCancelKeepsTheBase(t, h, ia[4].ID)
}

// recoverCancelKeepsTheBase continues TestAnUnansweredCancelOfAPendingOrderHandsTheRobotToARecoverCancel
// (split out for cyclop).
//
// carry (Task 9 review) (b): the robot may still run the order whose cancel went unanswered, so
// until it reports the recover- cancel FINISHED and no pending order it keeps everything it held.
func recoverCancelKeepsTheBase(t *testing.T, h *harness, recoverID string) {
	t.Helper()
	base := [2][]string{{"A", "ASRS1-OUT1", "P1"}, {"A-ASRS1-OUT1", "P1-A"}}
	if got := heldBy(h, "EMU/R1"); got != fmt.Sprint(base) {
		t.Fatalf("after the give-up R1 holds %s, want its whole base %v", got, base)
	}
	h.report("R1", func(r *robot.Report) { r.Battery = 89 }) // still pending, recover- unanswered
	if got := heldBy(h, "EMU/R1"); got != fmt.Sprint(base) {
		t.Fatalf("a pending robot awaiting its recover- cancel holds %s, want %v", got, base)
	}
	h.report("R1", func(r *robot.Report) {
		r.Pending = false
		setAction(r, robot.ActionState{ID: recoverID, Status: robot.ActionFinished}, true)
	})
	if got, want := heldBy(h, "EMU/R1"), fmt.Sprint([2][]string{{"P1"}, nil}); got != want {
		t.Fatalf("after recover- FINISHED R1 holds %s, want %s", got, want)
	}
}

// heldBy renders the nodes and edges the fleet record reserves for robot, sorted.
func heldBy(h *harness, robot string) string {
	rec, _, _ := h.fleets.Get(ctx, "AMR1")
	var out [2][]string
	for id, k := range rec.Reservations.Nodes {
		if k == robot {
			out[0] = append(out[0], id)
		}
	}
	for id, k := range rec.Reservations.Edges {
		if k == robot {
			out[1] = append(out[1], id)
		}
	}
	slices.Sort(out[0])
	slices.Sort(out[1])
	return fmt.Sprint(out)
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

// cancelling: R1 runs leg C1, fleet control asked it to cancel and it reported C1 as its load
// without answering the cancel yet.
func cancelling(t *testing.T, tune func(*usecases.Config)) (*harness, usecases.LegView) {
	t.Helper()
	h := newHarness(t, floor(t), tune)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	h.ack("R1")
	if _, now, err := h.svc.CancelLeg(ctx, "AMR1", v.LegID); err != nil || now {
		t.Fatalf("cancel = %v %v", now, err)
	}
	h.report("R1", func(r *robot.Report) { r.LoadIDs = []string{"C1"} })
	return h, v
}

// carry (Task 8 review): the offline timeout ends a CANCEL_REQUESTED leg CANCELLED with the robot's
// last reported load (not ROBOT_OFFLINE with container_on_robot=false), sending nothing offline.
func TestTheOfflineTimeoutEndsACancelRequestedLegCancelledWithTheRobotsLoad(t *testing.T) {
	h, v := cancelling(t, nil)
	h.tick(91 * time.Second) // stale before any cancel re-send
	h.tick(61 * time.Second)
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].LegID != v.LegID || lf[0].Reason != mission.ReasonCancelled || !lf[0].ContainerOnRobot {
		t.Fatalf("LegFailed = %+v", lf)
	}
	if ia := h.instants("R1"); len(ia) != 1 {
		t.Fatalf("instant actions = %+v (nothing is sent to an offline robot)", ia)
	}
	if r, _ := h.robots.Get(ctx, rid("R1")); r.MissionID() != "" || r.AwaitingAction() != "" {
		t.Fatalf("robot = %+v", r.Snapshot())
	}
}

// carry (Task 8 review): the blocked timeout ends a CANCEL_REQUESTED leg CANCELLED with the robot's
// load and sends its cancelOrder again (same action id), which the robot must still answer.
func TestTheBlockedTimeoutEndsACancelRequestedLegCancelledWithTheRobotsLoad(t *testing.T) {
	h, v := cancelling(t, func(c *usecases.Config) { c.ResendAfter = time.Hour })
	h.report("R1", func(r *robot.Report) {
		r.Errors = []robot.Error{{Type: robot.ErrTypeBlocked, Level: robot.LevelCritical}}
	})
	h.tick(61 * time.Second)
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].Reason != mission.ReasonCancelled || !lf[0].ContainerOnRobot || lf[0].RobotError != "" {
		t.Fatalf("LegFailed = %+v", lf)
	}
	ia := h.instants("R1")
	if len(ia) != 2 || ia[1].ID != v.LegID+"-cancel" || ia[1].Type != vda.CancelOrder {
		t.Fatalf("instant actions = %+v", ia)
	}
	if r, _ := h.robots.Get(ctx, rid("R1")); r.AwaitingAction() != v.LegID+"-cancel" {
		t.Fatalf("robot = %+v", r.Snapshot())
	}
}

// carry (Task 8 review): Recover ends a CANCEL_REQUESTED leg of a retired map version CANCELLED
// with the robot's load and cancels its order again.
func TestRecoverEndsACancelRequestedLegOfARetiredVersionCancelled(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	h.report("R1", func(r *robot.Report) { r.LoadIDs = []string{"C9"} })
	old, _ := mission.NewLeg(mission.LegSpec{ID: "leg-old", MapVersion: "v0", ContainerID: "C9", From: "ST1", To: "ST2", PickupNode: "ST1", DropNode: "ST2", At: t0})
	_, _ = old.Assign(rid("R1"), []mission.Step{{Node: "ST1", Action: mission.ActionPick}, {Node: "E", Edge: "E-ST1"}, {Node: "ST2", Edge: "x", Action: mission.ActionDrop}}, 0, t0)
	_, _, _ = old.RequestCancel(t0)
	h.must(h.missions.Insert(ctx, old))
	h.must(h.svc.Recover(ctx))
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].LegID != "leg-old" || lf[0].Reason != mission.ReasonCancelled || !lf[0].ContainerOnRobot {
		t.Fatalf("LegFailed = %+v", lf)
	}
	if ia := h.instants("R1"); len(ia) != 2 || ia[0].ID != "leg-old-cancel" || ia[1].Type != vda.StateRequest {
		t.Fatalf("instant = %+v", ia)
	}
}

// carry (Task 4 review): LastSeen deliberately counts connection messages too: a (retained,
// replayed) ONLINE connection makes a stale robot fresh again, and it goes stale once more after
// ROBOT_STALE_AFTER without a state, so a replay delays the stale check by one period at most.
func TestAReplayedOnlineConnectionDelaysTheStaleCheckByOnePeriodAtMost(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	h.tick(91 * time.Second)
	if r, _ := h.robots.Get(ctx, rid("R1")); r.State() != robot.Stopped {
		t.Fatalf("not stale: %s", r.State())
	}
	h.must(h.svc.HandleConnection(ctx, vda.Connection{Robot: rid("R1"), State: robot.ConnectionOnline, At: h.clock.Now()}))
	if r, _ := h.robots.Get(ctx, rid("R1")); r.State() != robot.Idle || !r.LastSeen().Equal(h.clock.Now()) {
		t.Fatalf("after the replay: %s %v", r.State(), r.LastSeen())
	}
	h.tick(90 * time.Second)
	if r, _ := h.robots.Get(ctx, rid("R1")); r.State() != robot.Idle {
		t.Fatalf("stale before ROBOT_STALE_AFTER: %s", r.State())
	}
	h.tick(time.Second)
	if r, _ := h.robots.Get(ctx, rid("R1")); r.State() != robot.Stopped {
		t.Fatalf("a replayed connection kept a silent robot online: %s", r.State())
	}
}
