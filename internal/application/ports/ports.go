// Package ports are the application's outbound ports: repositories, the unit of work, the
// clock, ids, the event sink (the outbox writer), the robot downlink and the leg metrics.
package ports

import (
	"context"
	"errors"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/fleetrow"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

var (
	// ErrLegNotFound is answered by Missions.Get for an unknown mission id.
	ErrLegNotFound = errors.New("leg not found")
	// ErrRobotNotFound is answered by Robots.Get for a robot never seen.
	ErrRobotNotFound = errors.New("robot not found")
	// ErrOpenLegExists is answered by Missions.Insert when an open leg already moves the container.
	ErrOpenLegExists = errors.New("an open leg already exists for the container")
)

type Clock interface{ Now() time.Time }

type IDs interface{ NewID() string }

// UnitOfWork runs fn atomically: every repository call made with the ctx fn receives commits
// together or rolls back together. Do joins an outer unit of work carried by ctx (the
// Idempotency-Key middleware opens one around every POST).
//
// AfterCommit registers fn to run after the OUTERMOST unit of work carried by ctx committed, in
// registration order; the hooks are dropped when it rolls back or panics. Outside a unit of work
// fn runs at once. Robot commands are sent only from such a hook.
//
// carry (Task 11 review): never open a unit from a ctx of a finished unit or from a goroutine
// spawned inside one: Do would join a frame that already committed (or is still running on
// another goroutine), so its writes and hooks escape the unit they belong to.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
	AfterCommit(ctx context.Context, fn func())
}

// Fleets persists one row per fleet (fleetrow.Record). Lock takes the row lock (SELECT ... FOR
// UPDATE) for the unit of work: every write holds it (decision D18). false = no row yet; Save
// upserts.
type Fleets interface {
	Lock(ctx context.Context, fleetID string) (fleetrow.Record, bool, error)
	Get(ctx context.Context, fleetID string) (fleetrow.Record, bool, error)
	Save(ctx context.Context, fleetID string, r fleetrow.Record) error
}

// Robots persists the registry; List is sorted by robot key.
type Robots interface {
	List(ctx context.Context) ([]*robot.Robot, error)
	Get(ctx context.Context, id robot.ID) (*robot.Robot, error)
	Save(ctx context.Context, r *robot.Robot) error
}

// Missions persists missions. Insert assigns Seq (acceptance order) and answers ErrOpenLegExists
// for a second open leg of one container; Open lists every open mission (internal ones included)
// in ascending Seq.
type Missions interface {
	Insert(ctx context.Context, m *mission.Mission) error
	Update(ctx context.Context, m *mission.Mission) error
	Get(ctx context.Context, id string) (*mission.Mission, error)
	Open(ctx context.Context) ([]*mission.Mission, error)
}

// EventSink writes domain events to the transactional outbox, in the caller's unit of work.
type EventSink interface {
	Append(ctx context.Context, events ...any) error
}

// Downlink publishes VDA 5050 commands to robots (the MQTT link).
type Downlink interface {
	SendOrder(ctx context.Context, o vda.Order) error
	SendInstantActions(ctx context.Context, a vda.InstantActions) error
}

// Metrics records Tier 2 business metrics (decision D21). outcome is "completed" or "failed";
// reason is the LegFailed reason ("" for completed).
type Metrics interface {
	LegEnded(ctx context.Context, outcome, reason string)
}
