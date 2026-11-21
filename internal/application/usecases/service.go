// Package usecases is the application layer of amr-fleet-control. Every write runs in one fleet
// unit of work that locks the fleet row (decision D18); robot commands leave only after it
// committed.
package usecases

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/ports"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
)

var (
	ErrInvalidConfig      = errors.New("invalid configuration")
	ErrFleetNotFound      = errors.New("fleet not found")
	ErrInvalidRequest     = errors.New("invalid request")
	ErrUnknownHandoff     = errors.New("unknown handoff point")
	ErrHandoffUnreachable = errors.New("handoff point unreachable")
	ErrContainerBusy      = errors.New("an open leg already moves the container")
	ErrFleetUnavailable   = errors.New("no robot of the fleet is online")
	ErrReadOnly           = errors.New("this service instance is read-only (cmd/mcp, cmd/a2a)")

	ErrLegNotFound    = ports.ErrLegNotFound
	ErrRobotNotFound  = ports.ErrRobotNotFound
	ErrNotCancellable = mission.ErrNotCancellable
	ErrLegEnded       = mission.ErrLegEnded
)

// Config holds the fleet policies (decisions D6-D12, D19).
type Config struct {
	ChargeLow       float64
	ChargeResume    float64
	ReleaseAhead    int
	NodeTolerance   float64
	StaleAfter      time.Duration
	ResendAfter     time.Duration
	ResendLimit     int
	RerouteAfter    time.Duration
	DeadlockTimeout time.Duration
	BlockedTimeout  time.Duration
	OfflineTimeout  time.Duration
}

func DefaultConfig() Config {
	return Config{ChargeLow: 20, ChargeResume: 80, ReleaseAhead: 2, NodeTolerance: 0.25,
		StaleAfter: 90 * time.Second, ResendAfter: 5 * time.Second, ResendLimit: 3, RerouteAfter: 10 * time.Second,
		DeadlockTimeout: 60 * time.Second, BlockedTimeout: 60 * time.Second, OfflineTimeout: 60 * time.Second}
}

// Validate refuses a configuration the policies cannot run with.
func (c Config) Validate() error {
	if c.ChargeLow <= 0 || c.ChargeLow >= c.ChargeResume || c.ChargeResume > 100 {
		return fmt.Errorf("%w: need 0 < CHARGE_LOW_PERCENT (%v) < CHARGE_RESUME_PERCENT (%v) <= 100", ErrInvalidConfig, c.ChargeLow, c.ChargeResume)
	}
	if c.ReleaseAhead < 1 || c.ResendLimit < 1 || c.NodeTolerance <= 0 {
		return fmt.Errorf("%w: RELEASE_AHEAD_NODES and ORDER_RESEND_LIMIT must be >= 1 and NODE_TOLERANCE_METRES > 0", ErrInvalidConfig)
	}
	for _, d := range []struct {
		name string
		v    time.Duration
	}{{"ROBOT_STALE_AFTER", c.StaleAfter}, {"ORDER_RESEND_AFTER", c.ResendAfter}, {"REROUTE_AFTER", c.RerouteAfter},
		{"DEADLOCK_TIMEOUT", c.DeadlockTimeout}, {"ROBOT_BLOCKED_TIMEOUT", c.BlockedTimeout}, {"ROBOT_OFFLINE_TIMEOUT", c.OfflineTimeout}} {
		if d.v <= 0 {
			return fmt.Errorf("%w: %s must be > 0", ErrInvalidConfig, d.name)
		}
	}
	return nil
}

// Deps are the outbound ports.
type Deps struct {
	UoW      ports.UnitOfWork
	Fleets   ports.Fleets
	Robots   ports.Robots
	Missions ports.Missions
	Sink     ports.EventSink
	Downlink ports.Downlink
	Clock    ports.Clock
	IDs      ports.IDs
	Metrics  ports.Metrics
	Log      *slog.Logger
}

// Service runs one fleet on one map version (decision D14).
type Service struct {
	m   *fleetmap.Map
	cfg Config
	d   Deps
}

func NewService(m *fleetmap.Map, cfg Config, d Deps) (*Service, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: no map", ErrInvalidConfig)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if d.UoW == nil || d.Fleets == nil || d.Robots == nil || d.Missions == nil || d.Sink == nil || d.Downlink == nil ||
		d.Clock == nil || d.IDs == nil || d.Metrics == nil || d.Log == nil {
		return nil, fmt.Errorf("%w: every dependency is required", ErrInvalidConfig)
	}
	return &Service{m: m, cfg: cfg, d: d}, nil
}

// Map is the loaded map.
func (s *Service) Map() *fleetmap.Map { return s.m }
