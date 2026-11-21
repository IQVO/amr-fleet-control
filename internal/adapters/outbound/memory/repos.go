package memory

import (
	"context"
	"fmt"
	"sort"

	"github.com/IQVO/amr-fleet-control/internal/application/fleetrow"
	"github.com/IQVO/amr-fleet-control/internal/application/ports"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

type Fleets struct{ s *Store }

func NewFleets(s *Store) *Fleets { return &Fleets{s: s} }

func (r *Fleets) Lock(ctx context.Context, fleetID string) (fleetrow.Record, bool, error) {
	return r.Get(ctx, fleetID)
}

func (r *Fleets) Get(_ context.Context, fleetID string) (fleetrow.Record, bool, error) {
	var rec fleetrow.Record
	var ok bool
	r.s.with(func(t *tables) { rec, ok = t.fleets[fleetID] })
	return cloneRecord(rec), ok, nil
}

func (r *Fleets) Save(_ context.Context, fleetID string, rec fleetrow.Record) error {
	r.s.with(func(t *tables) { t.fleets[fleetID] = cloneRecord(rec) })
	return nil
}

type Robots struct{ s *Store }

func NewRobots(s *Store) *Robots { return &Robots{s: s} }

func (r *Robots) List(context.Context) ([]*robot.Robot, error) {
	var out []*robot.Robot
	r.s.with(func(t *tables) {
		for _, snap := range t.robots {
			out = append(out, robot.Rehydrate(snap))
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID().Key() < out[j].ID().Key() })
	return out, nil
}

func (r *Robots) Get(_ context.Context, id robot.ID) (*robot.Robot, error) {
	var snap robot.Snapshot
	var ok bool
	r.s.with(func(t *tables) { snap, ok = t.robots[id.Key()] })
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrRobotNotFound, id.Key())
	}
	return robot.Rehydrate(snap), nil
}

func (r *Robots) Save(_ context.Context, rb *robot.Robot) error {
	r.s.with(func(t *tables) { t.robots[rb.ID().Key()] = rb.Snapshot() })
	return nil
}

type Missions struct{ s *Store }

func NewMissions(s *Store) *Missions { return &Missions{s: s} }

func (r *Missions) Insert(_ context.Context, m *mission.Mission) error {
	var err error
	r.s.with(func(t *tables) {
		for _, st := range t.missions {
			if m.External() && st.Kind == mission.Leg && st.ContainerID == m.ContainerID() && mission.Rehydrate(st).Open() {
				err = fmt.Errorf("%w: %s", ports.ErrOpenLegExists, m.ContainerID())
				return
			}
		}
		t.seq++
		m.AssignSeq(t.seq)
		t.missions[m.ID()] = m.State()
	})
	return err
}

func (r *Missions) Update(_ context.Context, m *mission.Mission) error {
	r.s.with(func(t *tables) { t.missions[m.ID()] = m.State() })
	return nil
}

func (r *Missions) Get(_ context.Context, id string) (*mission.Mission, error) {
	var st mission.State
	var ok bool
	r.s.with(func(t *tables) { st, ok = t.missions[id] })
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrLegNotFound, id)
	}
	return mission.Rehydrate(st), nil
}

func (r *Missions) Open(context.Context) ([]*mission.Mission, error) {
	var out []*mission.Mission
	r.s.with(func(t *tables) {
		for _, st := range t.missions {
			if m := mission.Rehydrate(st); m.Open() {
				out = append(out, m)
			}
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Seq() < out[j].Seq() })
	return out, nil
}
