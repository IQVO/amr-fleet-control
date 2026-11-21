package usecases

import (
	"context"
	"fmt"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/ports"
	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// RobotView is the read model of one robot.
type RobotView struct {
	ID                  robot.ID
	Connection, State   string
	Battery             float64
	Charging, Available bool
	Node, MissionID     string
	ErrorTypes          []string
	LastSeenAt          time.Time
	Factsheet           robot.Factsheet
	HasFactsheet        bool
}

// FleetView is the read model of the fleet.
type FleetView struct {
	FleetID, MapID, MapVersion string
	HandoffPoints              []string
	Robots                     []RobotView
	Capacity                   capacity.Snapshot
}

// NewReader builds the read-only Service of cmd/mcp and cmd/a2a: the queries only; every write
// (RequestLeg, CancelLeg, the uplink, Tick, Recover) answers ErrReadOnly.
func NewReader(m *fleetmap.Map, cfg Config, robots ports.Robots, missions ports.Missions, clock ports.Clock) (*Service, error) {
	if m == nil || robots == nil || missions == nil || clock == nil {
		return nil, fmt.Errorf("%w: a reader needs the map, the robots, the missions and a clock", ErrInvalidConfig)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Service{m: m, cfg: cfg, d: Deps{Robots: robots, Missions: missions, Clock: clock}}, nil
}

func (s *Service) fleet(fleetID string) error {
	if fleetID != s.m.FleetID() {
		return fmt.Errorf("%w: %s", ErrFleetNotFound, fleetID)
	}
	return nil
}

// Leg reads one leg (internal missions are not legs).
func (s *Service) Leg(ctx context.Context, fleetID, legID string) (LegView, error) {
	if err := s.fleet(fleetID); err != nil {
		return LegView{}, err
	}
	m, err := s.d.Missions.Get(ctx, legID)
	if err != nil {
		return LegView{}, err
	}
	if !m.External() {
		return LegView{}, fmt.Errorf("%w: %s", ErrLegNotFound, legID)
	}
	return legView(s.m.FleetID(), m), nil
}

// OpenLegs lists the open legs in acceptance order.
func (s *Service) OpenLegs(ctx context.Context, fleetID string) ([]LegView, error) {
	if err := s.fleet(fleetID); err != nil {
		return nil, err
	}
	ms, err := s.d.Missions.Open(ctx)
	if err != nil {
		return nil, err
	}
	out := []LegView{}
	for _, m := range ms {
		if m.External() {
			out = append(out, legView(s.m.FleetID(), m))
		}
	}
	return out, nil
}

// Fleet reads the fleet with live capacity counts.
func (s *Service) Fleet(ctx context.Context, fleetID string) (FleetView, error) {
	if err := s.fleet(fleetID); err != nil {
		return FleetView{}, err
	}
	rs, err := s.d.Robots.List(ctx)
	if err != nil {
		return FleetView{}, err
	}
	ms, err := s.d.Missions.Open(ctx)
	if err != nil {
		return FleetView{}, err
	}
	f := FleetView{FleetID: s.m.FleetID(), MapID: s.m.MapID(), MapVersion: s.m.Version(), HandoffPoints: s.m.HandoffPoints(),
		Robots: []RobotView{}, Capacity: capacity.Compute(rs, s.cfg.ChargeLow, ms, s.d.Clock.Now().UTC())}
	for _, r := range rs {
		f.Robots = append(f.Robots, s.robotView(r))
	}
	return f, nil
}

// Robot reads one robot of the registry.
func (s *Service) Robot(ctx context.Context, fleetID string, id robot.ID) (RobotView, error) {
	if err := s.fleet(fleetID); err != nil {
		return RobotView{}, err
	}
	r, err := s.d.Robots.Get(ctx, id)
	if err != nil {
		return RobotView{}, err
	}
	return s.robotView(r), nil
}

func (s *Service) robotView(r *robot.Robot) RobotView {
	rep := r.Report()
	return RobotView{ID: r.ID(), Connection: string(r.Connection()), State: string(r.State()), Battery: rep.Battery,
		Charging: rep.Charging, Available: r.MissionID() == "" && r.Assignable(s.cfg.ChargeLow), Node: r.Node(),
		MissionID: r.MissionID(), ErrorTypes: rep.ErrorTypes(), LastSeenAt: r.LastSeen(), Factsheet: r.Factsheet(),
		HasFactsheet: r.HasFactsheet()}
}
