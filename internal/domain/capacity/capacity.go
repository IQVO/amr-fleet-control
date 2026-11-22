// Package capacity is the fleet's capacity snapshot (decision D20): what material-flow-control
// aggregates into SubsystemCapacityChanged for the WES.
package capacity

import (
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// Snapshot counts robots and legs at At.
type Snapshot struct {
	RobotsTotal, RobotsOnline, RobotsAvailable, RobotsCharging, RobotsHeld int
	LegsQueued, LegsActive                                                 int
	At                                                                     time.Time
}

// FleetCapacityChanged is raised when a count changes.
type FleetCapacityChanged struct{ Snapshot }

// Compute counts: available = assignable at the charge threshold low; charging = SUSPENDED;
// held = HELD; legs only (internal missions are not capacity).
func Compute(robots []*robot.Robot, low float64, missions []*mission.Mission, at time.Time) Snapshot {
	s := Snapshot{RobotsTotal: len(robots), At: at}
	for _, r := range robots {
		if r.Online() {
			s.RobotsOnline++
		}
		if r.Assignable(low) {
			s.RobotsAvailable++
		}
		switch r.State() {
		case robot.Suspended:
			s.RobotsCharging++
		case robot.Held:
			s.RobotsHeld++
		}
	}
	for _, m := range missions {
		if !m.External() {
			continue
		}
		if m.Status() == mission.Queued { // an if-chain: cover does not instrument tagless-switch cases
			s.LegsQueued++
		} else if m.Active() {
			s.LegsActive++
		}
	}
	return s
}

// SameCounts compares everything but the time.
func (s Snapshot) SameCounts(o Snapshot) bool {
	s.At, o.At = time.Time{}, time.Time{}
	return s == o
}

// Next returns the event to publish for next, if any.
func Next(prev Snapshot, hadPrev bool, next Snapshot) (FleetCapacityChanged, bool) {
	if hadPrev && prev.SameCounts(next) {
		return FleetCapacityChanged{}, false
	}
	return FleetCapacityChanged{Snapshot: next}, true
}
