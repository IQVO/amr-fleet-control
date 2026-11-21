package usecases

import (
	"slices"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// cancelResends re-sends an unanswered cancelOrder with the same action id after
// ORDER_RESEND_AFTER; after ORDER_RESEND_LIMIT re-sends the leg ends CANCELLED through its own
// timeout (decisions D17, D19).
//
// carry (Task 8 review): a cancel is never ended ORDER_REJECTED; mission.RequestCancel restarts
// SentAt and Resends for the cancelOrder, so the cancel has the same budget as an order.
func (u *unit) cancelResends() error {
	for _, ms := range u.missions {
		r := u.robots[ms.Robot().Key()]
		if ms.Status() != mission.CancelRequested || r == nil || !r.Online() || r.AwaitingAction() != ms.CancelActionID() ||
			u.now.Sub(ms.SentAt()) < u.s.cfg.ResendAfter {
			continue
		}
		if ms.Resends() >= u.s.cfg.ResendLimit {
			if err := u.endCancel(ms, r, false); err != nil {
				return err
			}
			continue
		}
		u.instant(r.ID(), vda.Action{Type: vda.CancelOrder, ID: ms.CancelActionID(), Blocking: vda.BlockingHard})
		ms.Resent(u.now)
		u.touchM(ms)
	}
	return nil
}

// endCancel ends a CANCEL_REQUESTED leg CANCELLED without the robot's answer, passing the load
// the robot last reported (never container_on_robot=false blindly). cancel sends its cancelOrder
// again; otherwise the robot stops awaiting it and, when it is online and still reports a pending
// order, gets a recover- cancel instead, so it is never offered an order while that one may run.
func (u *unit) endCancel(ms *mission.Mission, r *robot.Robot, cancel bool) error {
	onRobot := r != nil && slices.Contains(r.Report().LoadIDs, ms.ContainerID())
	evs, err := ms.ConfirmCancel(onRobot, u.now)
	if err != nil {
		return err
	}
	u.raise(evs...)
	u.ended = append(u.ended, endedLeg{outcome: "failed", reason: string(mission.ReasonCancelled)})
	if r != nil && !cancel && r.AwaitingAction() == ms.CancelActionID() {
		r.AwaitAction("")
		u.touchR(r)
	}
	u.release(ms, cancel)
	if r != nil && !cancel && r.Online() {
		u.unowned(r, r.Report())
	}
	return nil
}
