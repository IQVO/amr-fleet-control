package usecases

import (
	"context"
	"slices"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// HandleConnection applies a robot's connection message.
func (s *Service) HandleConnection(ctx context.Context, c vda.Connection) error {
	return s.inFleet(ctx, func(u *unit) error {
		r, ok := u.robotFor(c.Robot)
		if !ok {
			return nil
		}
		u.raise(r.ApplyConnection(c.State, u.now, c.At)...)
		u.touchR(r)
		u.hold(r)
		return u.schedule()
	})
}

// HandleFactsheet records a robot's factsheet.
func (s *Service) HandleFactsheet(ctx context.Context, f vda.Factsheet) error {
	return s.inFleet(ctx, func(u *unit) error {
		r, ok := u.robotFor(f.Robot)
		if !ok {
			return nil
		}
		r.ApplyFactsheet(f.Factsheet)
		u.touchR(r)
		return nil
	})
}

// HandleState applies a robot's state, tracks its mission and schedules the fleet.
func (s *Service) HandleState(ctx context.Context, st vda.State) error {
	return s.inFleet(ctx, func(u *unit) error {
		r, ok := u.robotFor(st.Robot)
		if !ok {
			return nil
		}
		before := r.Report().Rejections()
		u.raise(r.ApplyReport(st.Report, u.nodeOf(st.Report), u.now)...)
		u.touchR(r)
		if ms := u.missionOf(r.ID().Key()); ms != nil {
			if err := u.track(ms, st.Report, before); err != nil {
				return err
			}
		}
		u.unowned(r, st.Report)
		u.hold(r)
		return u.schedule()
	})
}

// nodeOf is the map node a report stands on: its lastNodeId, else the nearest node within the
// tolerance (a robot that never drove an order reports no lastNodeId); "" off this map.
func (u *unit) nodeOf(rep robot.Report) string {
	if rep.MapID != u.s.m.MapID() {
		return ""
	}
	if _, ok := u.s.m.Node(rep.LastNodeID); ok {
		return rep.LastNodeID
	}
	n, _ := u.s.m.Nearest(rep.X, rep.Y, u.s.cfg.NodeTolerance)
	return n
}

func (u *unit) track(ms *mission.Mission, rep robot.Report, before int) error {
	ours := rep.OrderID == ms.ID()
	if (!ours || rep.OrderUpdateID < ms.UpdateID()) && ms.Unacknowledged() && rep.Rejections() > before {
		return u.fail(ms, mission.ReasonOrderRejected, lastError(rep, true), ours)
	}
	if !ours {
		return nil
	}
	ms.Seen(rep.OrderUpdateID)
	u.touchM(ms)
	if i, ok := vda.IndexOfSeq(rep.LastNodeSeq); ok && i <= ms.BaseEnd() {
		if err := ms.Progress(i); err != nil {
			return err
		}
	}
	if ms.Status() == mission.CancelRequested {
		return u.confirmCancel(ms, rep)
	}
	if e, fatal := rep.Fatal(); fatal {
		return u.fail(ms, mission.ReasonRobotFault, e, true)
	}
	if ms.Kind() == mission.Leg {
		return u.legActions(ms, rep)
	}
	return u.internalActions(ms, rep)
}

func (u *unit) legActions(ms *mission.Mission, rep robot.Report) error {
	if a, ok := rep.Action(ms.ActionID(mission.ActionPick)); ok && ms.Status() == mission.Assigned {
		switch a.Status {
		case robot.ActionFinished:
			evs, err := ms.PickUp(u.now, rep.At)
			if err != nil {
				return err
			}
			u.raise(evs...)
		case robot.ActionFailed:
			return u.fail(ms, mission.ReasonPickFailed, lastError(rep, false), true)
		}
	}
	if a, ok := rep.Action(ms.ActionID(mission.ActionDrop)); ok && ms.Status() == mission.PickedUp {
		switch a.Status {
		case robot.ActionFinished:
			return u.complete(ms, rep)
		case robot.ActionFailed:
			return u.fail(ms, mission.ReasonDropFailed, lastError(rep, false), true)
		}
	}
	return nil
}

func (u *unit) internalActions(ms *mission.Mission, rep robot.Report) error {
	if ms.Kind() == mission.Charge {
		a, ok := rep.Action(ms.ActionID(mission.ActionCharge))
		switch {
		case ok && a.Status == robot.ActionFinished:
			return u.complete(ms, rep)
		case ok && a.Status == robot.ActionFailed:
			return u.fail(ms, mission.ReasonRobotFault, lastError(rep, false), true)
		}
		return nil
	}
	if ms.Reached() == len(ms.Route())-1 && rep.OrderUpdateID == ms.UpdateID() && !rep.Pending {
		return u.complete(ms, rep)
	}
	return nil
}

func (u *unit) complete(ms *mission.Mission, rep robot.Report) error {
	evs, err := ms.Complete(u.now, rep.At)
	if err != nil {
		return err
	}
	u.finish(ms, evs)
	return nil
}

// confirmCancel ends a cancel-requested leg once its cancelOrder FINISHED or FAILED.
func (u *unit) confirmCancel(ms *mission.Mission, rep robot.Report) error {
	a, ok := rep.Action(ms.CancelActionID())
	if !ok || (a.Status != robot.ActionFinished && a.Status != robot.ActionFailed) {
		return nil
	}
	evs, err := ms.ConfirmCancel(slices.Contains(rep.LoadIDs, ms.ContainerID()), u.now)
	if err != nil {
		return err
	}
	u.raise(evs...)
	u.ended = append(u.ended, endedLeg{outcome: "failed", reason: string(mission.ReasonCancelled)})
	u.release(ms, false)
	return nil
}

// unowned cancels a pending order no active mission of this robot owns (after a restart, or a
// leg failed while the robot was offline) before the robot is offered anything (decision D19).
func (u *unit) unowned(r *robot.Robot, rep robot.Report) {
	if !rep.Pending || r.AwaitingAction() != "" {
		return
	}
	if ms := u.missionOf(r.ID().Key()); ms != nil && ms.ID() == rep.OrderID {
		return
	}
	u.cancelOrder(r, "recover-"+u.s.d.IDs.NewID())
}

// lastError is the newest error type of the report (only rejections when rejections).
func lastError(rep robot.Report, rejections bool) string {
	for i := len(rep.Errors) - 1; i >= 0; i-- {
		t := rep.Errors[i].Type
		if !rejections || t == robot.ErrTypeValidation || t == robot.ErrTypeInvalidOrder {
			return t
		}
	}
	return ""
}
