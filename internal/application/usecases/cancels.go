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

// failActive is the tick's and the recovery's way to end a mission: a CANCEL_REQUESTED leg ends
// through endCancel (CANCELLED, with the robot's reported load), every other mission fails with
// reason (decision D17).
//
// carry (Task 8 review): mission.Fail derives container_on_robot from PICKED_UP only, so a
// cancel-requested leg the robot already carries would otherwise report false.
func (u *unit) failActive(ms *mission.Mission, reason mission.Reason, robotErr string, cancel bool) error {
	if ms.Status() == mission.CancelRequested {
		return u.endCancel(ms, u.robots[ms.Robot().Key()], cancel)
	}
	return u.fail(ms, reason, robotErr, cancel)
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
	// carry (Task 9 review): the recover- cancel is queued before release holds the robot, so a
	// robot still reporting the pending order keeps its whole base (hold).
	if r != nil && !cancel && r.Online() {
		u.unowned(r, r.Report())
	}
	u.release(ms, cancel)
	return nil
}

// lostCancels forgets a cancelOrder no open CANCEL_REQUESTED leg owns (one sent as a mission ended,
// or a recover- cancel) once the robot's latest report neither shows a pending order nor lists that
// action: there is nothing left to cancel, and anything sent later reaches the robot after the
// cancel (decision D18). It runs first in the tick, so only a cancel queued by an earlier unit of
// work is forgotten; a CANCEL_REQUESTED leg's own cancel is left to cancelResends.
//
// carry (Task 9 review): such a cancel is never re-sent, so a lost one left the robot awaiting it,
// and unassignable, for good.
func (u *unit) lostCancels() {
	for _, k := range u.keys {
		r := u.robots[k]
		id := r.AwaitingAction()
		if id == "" || !r.Online() || u.cancelOwned(id) {
			continue
		}
		rep := r.Report()
		if _, listed := rep.Action(id); rep.Pending || listed {
			continue
		}
		r.AwaitAction("")
		u.touchR(r)
	}
}

// cancelOwned: an open CANCEL_REQUESTED leg awaits the cancelOrder with this action id.
func (u *unit) cancelOwned(id string) bool {
	for _, ms := range u.missions {
		if ms.Status() == mission.CancelRequested && ms.CancelActionID() == id {
			return true
		}
	}
	return false
}
