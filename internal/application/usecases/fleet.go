package usecases

import (
	"context"
	"sort"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/fleetrow"
	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// unit is one fleet unit of work: everything loaded under the fleet row lock.
type unit struct {
	s        *Service
	ctx      context.Context
	now      time.Time
	rec      fleetrow.Record
	fm       *fleetmap.FleetMap
	robots   map[string]*robot.Robot
	keys     []string
	missions []*mission.Mission
	dirtyR   map[string]bool
	dirtyM   map[string]bool
	events   []any
	sends    []func(context.Context) error
	ended    []endedLeg
}

type endedLeg struct{ outcome, reason string }

// inFleet runs fn in one fleet unit of work. Every write path goes through here: the link
// worker's messages and ticks and the HTTP use cases alike. ruling P3-R10 (amended; carry (Task
// 11 review)): s.d.UoW is a SerialUnitOfWork, so the OUTERMOST unit (this one, or the HTTP
// middleware's that this one joins) holds the lock from before its transaction to the end of
// its after-commit hooks, and commands enqueue in commit order.
func (s *Service) inFleet(ctx context.Context, fn func(u *unit) error) error {
	if s.d.UoW == nil {
		return ErrReadOnly
	}
	return s.d.UoW.Do(ctx, func(ctx context.Context) error {
		u, err := s.load(ctx)
		if err != nil {
			return err
		}
		if err := fn(u); err != nil {
			return err
		}
		return u.flush()
	})
}

func (s *Service) load(ctx context.Context) (*unit, error) {
	rec, found, err := s.d.Fleets.Lock(ctx, s.m.FleetID())
	if err != nil {
		return nil, err
	}
	fm := fleetmap.NewFleetMap(s.m)
	if found && rec.MapVersion == s.m.Version() {
		if fm, err = fleetmap.Rehydrate(s.m, rec.Reservations); err != nil {
			return nil, err
		}
	}
	rs, err := s.d.Robots.List(ctx)
	if err != nil {
		return nil, err
	}
	ms, err := s.d.Missions.Open(ctx)
	if err != nil {
		return nil, err
	}
	u := &unit{s: s, ctx: ctx, now: s.d.Clock.Now().UTC(), rec: rec, fm: fm, robots: map[string]*robot.Robot{},
		missions: ms, dirtyR: map[string]bool{}, dirtyM: map[string]bool{}}
	for _, r := range rs {
		u.robots[r.ID().Key()] = r
		u.keys = append(u.keys, r.ID().Key())
	}
	return u, nil
}

func (u *unit) flush() error {
	for _, k := range u.keys {
		if u.dirtyR[k] {
			if err := u.s.d.Robots.Save(u.ctx, u.robots[k]); err != nil {
				return err
			}
		}
	}
	for _, m := range u.missions {
		if u.dirtyM[m.ID()] {
			if err := u.s.d.Missions.Update(u.ctx, m); err != nil {
				return err
			}
		}
	}
	u.capacity()
	u.rec.MapVersion, u.rec.Reservations = u.s.m.Version(), u.fm.State()
	if err := u.s.d.Fleets.Save(u.ctx, u.s.m.FleetID(), u.rec); err != nil {
		return err
	}
	if len(u.events) > 0 {
		if err := u.s.d.Sink.Append(u.ctx, u.events...); err != nil {
			return err
		}
	}
	u.afterCommit()
	return nil
}

func (u *unit) capacity() {
	snap := capacity.Compute(u.robotList(), u.s.cfg.ChargeLow, u.missions, u.now)
	if ev, ok := capacity.Next(u.rec.Capacity, u.rec.HasCapacity, snap); ok {
		u.events = append(u.events, ev)
		u.rec.Capacity, u.rec.HasCapacity = snap, true
	}
}

// afterCommit sends the queued robot commands in order and records the leg metrics, once.
func (u *unit) afterCommit() {
	if len(u.sends) == 0 && len(u.ended) == 0 {
		return
	}
	sends, ended, d := u.sends, u.ended, u.s.d
	ctx := context.WithoutCancel(u.ctx)
	d.UoW.AfterCommit(u.ctx, func() {
		for _, send := range sends {
			if err := send(ctx); err != nil {
				d.Log.Warn("robot command not sent; an unacknowledged order is resent by the tick (so are a cancelOrder and a stopCharging)", "err", err)
			}
		}
		for _, e := range ended {
			d.Metrics.LegEnded(ctx, e.outcome, e.reason)
		}
	})
}

func (u *unit) robotList() []*robot.Robot {
	out := make([]*robot.Robot, 0, len(u.keys))
	for _, k := range u.keys {
		out = append(out, u.robots[k])
	}
	return out
}

func (u *unit) raise(evs ...any) { u.events = append(u.events, evs...) }

func (u *unit) touchR(r *robot.Robot) { u.dirtyR[r.ID().Key()] = true }

func (u *unit) touchM(m *mission.Mission) { u.dirtyM[m.ID()] = true }

func (u *unit) insert(m *mission.Mission) error {
	if err := u.s.d.Missions.Insert(u.ctx, m); err != nil {
		return err
	}
	u.missions = append(u.missions, m)
	return nil
}

// robotFor returns the registry entry, registering a robot seen for the first time (D13).
func (u *unit) robotFor(id robot.ID) (*robot.Robot, bool) {
	if r, ok := u.robots[id.Key()]; ok {
		return r, true
	}
	r, err := robot.New(id, u.now)
	if err != nil {
		u.s.d.Log.Warn("message from an invalid robot id ignored", "manufacturer", id.Manufacturer, "serial", id.SerialNumber)
		return nil, false
	}
	u.robots[id.Key()] = r
	u.keys = append(u.keys, id.Key())
	sort.Strings(u.keys)
	u.touchR(r)
	return r, true
}

// missionOf returns the robot's active mission.
func (u *unit) missionOf(key string) *mission.Mission {
	for _, m := range u.missions {
		if m.Active() && m.Robot().Key() == key {
			return m
		}
	}
	return nil
}

// missionByID finds an open mission, else loads an ended one.
func (u *unit) missionByID(id string) (*mission.Mission, error) {
	for _, m := range u.missions {
		if m.ID() == id {
			return m, nil
		}
	}
	return u.s.d.Missions.Get(u.ctx, id)
}

func (u *unit) anyOnline() bool {
	for _, r := range u.robots {
		if r.Online() {
			return true
		}
	}
	return false
}

// hold keeps a robot's reservations to the node it stands on plus the unpassed part of its
// mission's base, releasing everything it passed (decision D7).
func (u *unit) hold(r *robot.Robot) {
	key := r.ID().Key()
	var nodes, edges []string
	if r.Node() != "" {
		nodes = append(nodes, r.Node())
	}
	if ms := u.missionOf(key); ms != nil {
		n, e := held(ms.Route(), ms.Reached(), ms.BaseEnd())
		nodes, edges = append(nodes, n...), append(edges, e...)
	} else if r.Report().Pending && r.AwaitingAction() != "" {
		// carry (Task 9 review): no active mission owns the order the robot still runs and its
		// cancel is unanswered (a recover- cancel after a give-up, or a failed leg's cancelOrder),
		// so it keeps everything it holds until it reports no pending order.
		n, e := u.fm.Held(key)
		nodes, edges = append(nodes, n...), append(edges, e...)
	} else {
		// carry (Task 8 review): a robot stopped mid-edge keeps its edge.
		// carry (Task 9 review): only without an active mission (after a cancel); a robot driving
		// its mission keeps exactly the unpassed part of its base.
		n, e := u.underway(r)
		nodes, edges = append(nodes, n...), append(edges, e...)
	}
	u.fm.Retain(key, nodes, edges)
	if r.Node() == "" {
		return
	}
	if err := u.fm.Reserve(key, []string{r.Node()}, nil); err != nil {
		u.s.d.Log.Warn("a robot stands on a node it cannot hold", "robot", key, "node", r.Node(), "err", err)
	}
}

// fail ends a mission with reason; cancel also sends cancelOrder to its robot.
func (u *unit) fail(ms *mission.Mission, reason mission.Reason, robotErr string, cancel bool) error {
	evs, err := ms.Fail(reason, robotErr, u.now)
	if err != nil {
		return err
	}
	u.raise(evs...)
	if ms.External() {
		u.ended = append(u.ended, endedLeg{outcome: "failed", reason: string(reason)})
	}
	u.release(ms, cancel)
	return nil
}

// finish records a completed mission's events.
func (u *unit) finish(ms *mission.Mission, evs []any) {
	u.raise(evs...)
	if ms.External() {
		u.ended = append(u.ended, endedLeg{outcome: "completed"})
	}
	u.release(ms, false)
}

func (u *unit) release(ms *mission.Mission, cancel bool) {
	u.touchM(ms)
	r, ok := u.robots[ms.Robot().Key()]
	if !ok {
		return
	}
	u.raise(r.Release(ms.ID(), u.now)...)
	u.touchR(r)
	u.fm.ClearWait(r.ID().Key())
	if cancel {
		u.cancelOrder(r, ms.CancelActionID())
	}
	u.hold(r)
}
