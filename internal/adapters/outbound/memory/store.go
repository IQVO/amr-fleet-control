// Package memory holds in-memory TEST DOUBLES of the application ports. cmd/ never imports it
// (TestRuntimeBinaryHasNoInMemoryAdapters). One Store holds every table; a UnitOfWork serialises
// units of work and restores the whole store when fn fails, so the doubles commit or roll back
// like Postgres.
package memory

import (
	"context"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/IQVO/amr-fleet-control/internal/application/fleetrow"
	"github.com/IQVO/amr-fleet-control/internal/application/idempotency"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

type tables struct {
	fleets   map[string]fleetrow.Record
	robots   map[string]robot.Snapshot
	missions map[string]mission.State
	seq      int64
	events   []any
	idem     map[string]idempotency.IdempotencyRecord
	tags     map[string]int
}

// Store is the in-memory database.
type Store struct {
	tx sync.Mutex // serialises units of work
	mu sync.Mutex // guards t
	t  tables
}

func NewStore() *Store {
	return &Store{t: tables{fleets: map[string]fleetrow.Record{}, robots: map[string]robot.Snapshot{},
		missions: map[string]mission.State{}, idem: map[string]idempotency.IdempotencyRecord{}, tags: map[string]int{}}}
}

// Tag records one scratch write inside the caller's unit of work (the replay-class test's Write).
func (s *Store) Tag(_ context.Context, tag string) error {
	s.with(func(t *tables) { t.tags[tag]++ })
	return nil
}

// Tagged counts the committed scratch writes of tag.
func (s *Store) Tagged(tag string) int {
	n := 0
	s.with(func(t *tables) { n = t.tags[tag] })
	return n
}

func (s *Store) with(fn func(t *tables)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.t)
}

func (t tables) clone() tables {
	c := tables{fleets: map[string]fleetrow.Record{}, robots: maps.Clone(t.robots), missions: maps.Clone(t.missions),
		seq: t.seq, events: append([]any(nil), t.events...), idem: maps.Clone(t.idem), tags: maps.Clone(t.tags)}
	for k, v := range t.fleets {
		c.fleets[k] = cloneRecord(v)
	}
	return c
}

func cloneRecord(r fleetrow.Record) fleetrow.Record {
	r.Reservations = fleetmap.State{Nodes: maps.Clone(r.Reservations.Nodes), Edges: maps.Clone(r.Reservations.Edges),
		Waits: maps.Clone(r.Reservations.Waits), Deadlocks: append([]string(nil), r.Reservations.Deadlocks...)}
	return r
}

type uowKey struct{}

// frame is one outermost unit of work. carry (Task 11 review): done is set once its hooks ran
// (or it rolled back), so a ctx kept from a finished unit never joins it.
type frame struct {
	hooks []func()
	done  atomic.Bool
}

// open returns the unfinished frame ctx carries.
func open(ctx context.Context) (*frame, bool) {
	f, ok := ctx.Value(uowKey{}).(*frame)
	return f, ok && !f.done.Load()
}

// UnitOfWork is the in-memory ports.UnitOfWork.
type UnitOfWork struct{ s *Store }

func NewUnitOfWork(s *Store) *UnitOfWork { return &UnitOfWork{s: s} }

func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if _, inner := open(ctx); inner {
		return fn(ctx)
	}
	u.s.tx.Lock()
	defer u.s.tx.Unlock()
	var before tables
	u.s.with(func(t *tables) { before = t.clone() })
	f := &frame{}
	defer f.done.Store(true) // runs after the hooks: a hook's own Do still joins this unit
	defer func() {
		if p := recover(); p != nil {
			u.s.with(func(t *tables) { *t = before })
			panic(p)
		}
	}()
	if err = fn(context.WithValue(ctx, uowKey{}, f)); err != nil {
		u.s.with(func(t *tables) { *t = before })
		return err
	}
	// carry (Task 12 review): by index, so a hook an AfterCommit registers from inside a
	// running hook (the frame is still open) runs too, exactly once.
	for i := 0; i < len(f.hooks); i++ {
		f.hooks[i]()
	}
	return nil
}

func (u *UnitOfWork) AfterCommit(ctx context.Context, fn func()) {
	if f, ok := open(ctx); ok {
		f.hooks = append(f.hooks, fn)
		return
	}
	fn()
}
