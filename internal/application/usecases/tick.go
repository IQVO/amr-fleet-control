package usecases

import (
	"context"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// Tick runs the time-based rules once; the MQTT link's single worker calls it every second, in
// line with the robot messages (decision D18).
func (s *Service) Tick(ctx context.Context) error {
	return s.inFleet(ctx, func(u *unit) error {
		u.stale()
		if err := u.timeouts(); err != nil {
			return err
		}
		if err := u.resends(); err != nil {
			return err
		}
		if err := u.cancelResends(); err != nil { // carry (Task 8 review)
			return err
		}
		u.waits()
		if err := u.deadlockTimeouts(); err != nil {
			return err
		}
		return u.schedule()
	})
}

// Recover runs once at boot, before the link starts (decisions D14, D19).
func (s *Service) Recover(ctx context.Context) error {
	return s.inFleet(ctx, func(u *unit) error {
		for _, ms := range u.missions {
			if ms.MapVersion() == s.m.Version() {
				continue
			}
			if err := u.failActive(ms, mission.ReasonMapVersionRetired, "", ms.Active()); err != nil {
				return err
			}
		}
		for _, k := range u.keys {
			u.instant(u.robots[k].ID(), vda.Action{Type: vda.StateRequest, ID: "state-" + s.d.IDs.NewID(), Blocking: vda.BlockingNone})
		}
		return u.schedule()
	})
}

// stale stops every robot that sent no state for ROBOT_STALE_AFTER (decision D12).
func (u *unit) stale() {
	for _, k := range u.keys {
		r := u.robots[k]
		if r.Online() && u.now.Sub(r.LastSeen()) > u.s.cfg.StaleAfter {
			u.raise(r.MarkStale(u.now)...)
			u.touchR(r)
		}
	}
}

// timeouts fail an active mission whose robot stayed STOPPED or HELD too long (decision D16).
func (u *unit) timeouts() error {
	for _, ms := range u.missions {
		r := u.robots[ms.Robot().Key()]
		if !ms.Active() || r == nil {
			continue
		}
		switch {
		case r.State() == robot.Stopped && u.now.Sub(r.OfflineSince()) > u.s.cfg.OfflineTimeout:
			if err := u.failActive(ms, mission.ReasonRobotOffline, "", false); err != nil {
				return err
			}
		case r.State() == robot.Held && u.now.Sub(r.HeldSince()) > u.s.cfg.BlockedTimeout:
			e, _ := r.Report().Blocking()
			if err := u.failActive(ms, mission.ReasonRobotBlocked, e, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// resends repeats an unacknowledged order after ORDER_RESEND_AFTER and fails it ORDER_REJECTED
// after ORDER_RESEND_LIMIT resends (decision D19).
//
// carry (Task 8 review): a CANCEL_REQUESTED leg's order is never resent nor failed ORDER_REJECTED,
// and no order is resent while its robot awaits a cancelOrder answer (a robot with a cancel in
// flight is never offered an order, D19).
func (u *unit) resends() error {
	for _, ms := range u.missions {
		r := u.robots[ms.Robot().Key()]
		if !ms.Unacknowledged() || r == nil || !r.Online() || u.now.Sub(ms.SentAt()) < u.s.cfg.ResendAfter {
			continue
		}
		if ms.Status() == mission.CancelRequested || r.AwaitingAction() != "" {
			continue
		}
		if ms.Resends() >= u.s.cfg.ResendLimit {
			// carry (Task 8 review): robot_error is the robot's newest rejection, if it lists one (D16).
			if err := u.failActive(ms, mission.ReasonOrderRejected, lastError(r.Report(), true), true); err != nil {
				return err
			}
			continue
		}
		u.resend(ms)
	}
	return nil
}

// waits re-routes a robot that waited REROUTE_AFTER for a node another robot holds (D9).
func (u *unit) waits() {
	for _, ms := range u.missions {
		if ms.Status() != mission.Assigned && ms.Status() != mission.PickedUp {
			continue
		}
		if w, ok := u.fm.WaitOf(ms.Robot().Key()); ok && u.now.Sub(w.Since) >= u.s.cfg.RerouteAfter {
			u.reroute(ms)
		}
	}
}

// deadlockTimeouts fail the youngest mission of a cycle older than DEADLOCK_TIMEOUT (D9).
func (u *unit) deadlockTimeouts() error {
	for _, cycle := range u.fm.Cycles() {
		if u.now.Sub(u.fm.CycleSince(cycle)) <= u.s.cfg.DeadlockTimeout {
			continue
		}
		if ms := u.youngest(cycle); ms != nil {
			if err := u.failActive(ms, mission.ReasonRobotBlocked, "", true); err != nil {
				return err
			}
		}
	}
	return nil
}
