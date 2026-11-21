package usecases

import (
	"slices"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/dispatch"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// schedule runs the fleet policies in a fixed order after every change.
func (u *unit) schedule() error {
	u.extendAll()
	u.deadlocks()
	if err := u.chargeAll(); err != nil {
		return err
	}
	u.stopCharging()
	if err := u.assignAll(); err != nil {
		return err
	}
	return u.parkAll()
}

// extendAll releases more of every acknowledged base and records who waits (decision D7).
func (u *unit) extendAll() {
	for _, ms := range u.missions {
		r := u.robots[ms.Robot().Key()]
		// carry (Task 5 review): the only ms.Extend call site; CANCEL_REQUESTED (and every other
		// status) is skipped here, and mission.Extend refuses it too.
		if (ms.Status() != mission.Assigned && ms.Status() != mission.PickedUp) || r == nil || !r.Online() {
			continue
		}
		key := r.ID().Key()
		if ms.Sent() && !ms.Unacknowledged() {
			if end, _, _ := u.window(ms.Route(), ms.BaseEnd(), ms.Reached(), key); end > ms.BaseEnd() {
				nodes, edges := stretch(ms.Route(), ms.BaseEnd()+1, end)
				if u.fm.Reserve(key, nodes, edges) == nil && ms.Extend(end) == nil {
					u.order(ms)
				}
			}
		}
		if _, blocker, at := u.window(ms.Route(), ms.BaseEnd(), ms.Reached(), key); blocker != "" && ms.Reached() == ms.BaseEnd() {
			u.fm.SetWait(key, at, blocker, u.now)
		} else {
			u.fm.ClearWait(key)
		}
	}
}

// deadlocks publishes each new wait-for cycle once and re-routes its youngest mission (D9).
func (u *unit) deadlocks() {
	for _, d := range u.fm.DetectDeadlocks(u.now, func() string { return "dl-" + u.s.d.IDs.NewID() }) {
		u.raise(d)
		if ms := u.youngest(d.Robots); ms != nil {
			u.reroute(ms)
		}
	}
}

func (u *unit) youngest(keys []string) *mission.Mission {
	var out *mission.Mission
	for _, k := range keys {
		ms := u.missionOf(k)
		if ms == nil || ms.Status() == mission.CancelRequested {
			continue
		}
		if out == nil || ms.Seq() > out.Seq() {
			out = ms
		}
	}
	return out
}

// reroute plans the horizon again from the base end around every node another robot holds.
func (u *unit) reroute(ms *mission.Mission) bool {
	key, route := ms.Robot().Key(), ms.Route()
	avoid := map[string]bool{}
	for _, n := range u.s.m.NodeIDs() {
		if h, ok := u.fm.NodeHolder(n); ok && h != key {
			avoid[n] = true
		}
	}
	var targets []dispatch.Target
	for _, st := range ms.Targets() {
		targets = append(targets, dispatch.Target{Node: st.Node, Action: st.Action})
	}
	tail, ok := dispatch.Plan(u.s.m, route[ms.BaseEnd()].Node, targets, avoid)
	if !ok || slices.Equal(tail[1:], route[ms.BaseEnd()+1:]) || ms.Reroute(tail) != nil {
		return false
	}
	u.fm.ClearWait(key)
	u.order(ms)
	return true
}

// chargeAll sends every idle robot below the threshold to the nearest free charger (D10).
func (u *unit) chargeAll() error {
	for _, k := range u.keys {
		r := u.robots[k]
		if r.MissionID() != "" || !r.NeedsCharge(u.s.cfg.ChargeLow) {
			continue
		}
		ch, ok := dispatch.NearestFree(u.s.m, r.Node(), fleetmap.Charger, u.freeFor(k))
		if !ok {
			continue
		}
		route, ok := dispatch.Plan(u.s.m, r.Node(), []dispatch.Target{{Node: ch, Action: mission.ActionCharge}}, nil)
		if !ok {
			continue
		}
		if err := u.startInternal(r, mission.Charge, route); err != nil {
			return err
		}
	}
	return nil
}

// stopCharging ends each charge at the resume level, once. carry (Task 11 review): a
// stopCharging still unanswered after ORDER_RESEND_AFTER (refused by a full downlink, lost with
// the connection, dropped at shutdown) is sent again, so a charged robot is never stranded.
func (u *unit) stopCharging() {
	resume, after := u.s.cfg.ChargeResume, u.s.cfg.ResendAfter
	for _, k := range u.keys {
		r := u.robots[k]
		resend := r.StopChargeOverdue(resume, u.now, after)
		if !resend && !r.Charged(resume) {
			continue
		}
		if resend {
			u.s.d.Log.Warn("stopCharging unanswered; sent again", "robot", k)
		}
		u.instant(r.ID(), vda.Action{Type: vda.StopCharging, ID: "stop-" + u.s.d.IDs.NewID(), Blocking: vda.BlockingHard})
		r.MarkStopChargeSentAt(u.now)
		u.touchR(r)
	}
}

// assignAll gives queued legs, in acceptance order, to the best assignable robot (D6).
func (u *unit) assignAll() error {
	cands := u.candidates()
	for _, ms := range u.missions {
		if ms.Status() != mission.Queued || len(cands) == 0 {
			continue
		}
		c, ok := dispatch.Choose(u.s.m, ms.PickupNode(), cands)
		if !ok {
			continue
		}
		route, ok := dispatch.Plan(u.s.m, c.Node, []dispatch.Target{{Node: ms.PickupNode(), Action: mission.ActionPick},
			{Node: ms.DropNode(), Action: mission.ActionDrop}}, nil)
		if !ok {
			continue
		}
		end, _, _ := u.window(route, 0, 0, c.Robot.Key())
		evs, err := ms.Assign(c.Robot, route, end, u.now)
		if err != nil {
			return err
		}
		if err := u.launch(u.robots[c.Robot.Key()], ms, evs, false); err != nil {
			return err
		}
		cands = slices.DeleteFunc(cands, func(x dispatch.Candidate) bool { return x.Robot == c.Robot })
	}
	return nil
}

func (u *unit) candidates() []dispatch.Candidate {
	var out []dispatch.Candidate
	for _, k := range u.keys {
		if r := u.robots[k]; r.MissionID() == "" && r.Assignable(u.s.cfg.ChargeLow) {
			out = append(out, dispatch.Candidate{Robot: r.ID(), Node: r.Node(), Battery: r.Battery()})
		}
	}
	return out
}

// parkAll moves every idle robot off a port, station or waypoint to the nearest free parking (D11).
func (u *unit) parkAll() error {
	for _, k := range u.keys {
		r := u.robots[k]
		if r.MissionID() != "" || !r.Assignable(u.s.cfg.ChargeLow) {
			continue
		}
		if n, _ := u.s.m.Node(r.Node()); n.Kind == fleetmap.Parking || n.Kind == fleetmap.Charger {
			continue
		}
		p, ok := dispatch.NearestFree(u.s.m, r.Node(), fleetmap.Parking, u.freeFor(k))
		if !ok {
			continue
		}
		if route, ok := dispatch.Plan(u.s.m, r.Node(), []dispatch.Target{{Node: p}}, nil); ok && len(route) > 1 {
			if err := u.startInternal(r, mission.Park, route); err != nil {
				return err
			}
		}
	}
	return nil
}

// freeFor: a node no other robot holds and no other active mission ends at.
func (u *unit) freeFor(key string) func(string) bool {
	targeted := map[string]bool{}
	for _, ms := range u.missions {
		if ms.Active() && ms.Robot().Key() != key {
			route := ms.Route()
			targeted[route[len(route)-1].Node] = true
		}
	}
	return func(n string) bool {
		h, held := u.fm.NodeHolder(n)
		return (!held || h == key) && !targeted[n]
	}
}

func (u *unit) startInternal(r *robot.Robot, kind mission.Kind, route []mission.Step) error {
	end, _, _ := u.window(route, 0, 0, r.ID().Key())
	ms, err := mission.NewInternal(kind, "ms-"+u.s.d.IDs.NewID(), u.s.m.Version(), r.ID(), route, end, u.now)
	if err != nil {
		return err
	}
	return u.launch(r, ms, nil, true)
}

// launch reserves a mission's first base, gives the robot the mission and sends the order.
func (u *unit) launch(r *robot.Robot, ms *mission.Mission, evs []any, insert bool) error {
	if ms.BaseEnd() > 0 {
		nodes, edges := stretch(ms.Route(), 1, ms.BaseEnd())
		if err := u.fm.Reserve(r.ID().Key(), nodes, edges); err != nil {
			return err
		}
	}
	if insert {
		if err := u.insert(ms); err != nil {
			return err
		}
	}
	// carry (Task 4 review): Robot.Assign guards neither state nor battery, by design. This is its
	// only call site, and launch is reached only from assignAll (candidates: no mission and
	// Assignable(ChargeLow), so never below the threshold), chargeAll (no mission and
	// NeedsCharge(ChargeLow)) and parkAll (no mission and Assignable(ChargeLow)); each robot is
	// re-checked after the earlier policies ran (TestARobotBelowTheChargeThresholdNeverGetsALeg).
	rev, err := r.Assign(ms.ID(), u.now)
	if err != nil {
		return err
	}
	u.raise(evs...)
	u.raise(rev...)
	u.touchR(r)
	u.touchM(ms)
	u.order(ms)
	return nil
}
