//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/amr-fleet-control/internal/application/fleetrow"
	"github.com/IQVO/amr-fleet-control/internal/application/idempotency"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
	"github.com/IQVO/amr-fleet-control/internal/testing/pgtest"
)

// carry (Task 12 review): behind PgBouncer (pool_mode=transaction) NewPool's session SET does not
// reach the server connection a stand-alone statement runs on, so every pool-path statement runs in
// a transaction of its own that states SET LOCAL statement_timeout/lock_timeout (begin). With every
// table ACCESS EXCLUSIVE locked by another transaction, each call gives up with SQLSTATE 55P03 or
// 57014 within ~6 s on a pool with no session settings; before the fix they waited for the 15 s
// backstop.
func TestPoolPathStatementsCarryTheTimeouts(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.BarePoolSized(t, 16) // every call below holds its own connection at once
	idem, fleets := postgres.NewIdempotencyStore(pool), postgres.NewFleets(pool)
	robots, missions := postgres.NewRobots(pool), postgres.NewMissions(pool)
	t0 := time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC)
	r1, _ := robot.New(robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}, t0)
	leg, _ := mission.NewLeg(mission.LegSpec{ID: "leg-t", MapVersion: "v1", ContainerID: "C1", From: "ST1", To: "ST2",
		PickupNode: "ST1", DropNode: "ST2", At: t0})
	// The repository statements a caller can run OUTSIDE a unit of work: the idempotency middleware's
	// Lookup and finishRefused Save, the hourly Purge, and the read-only queries and single writes.
	calls := map[string]func(context.Context) error{
		"IdempotencyStore.Lookup": func(c context.Context) error { _, _, err := idem.Lookup(c, "k", t0); return err },
		"IdempotencyStore.Save": func(c context.Context) error {
			return idem.Save(c, idempotency.IdempotencyRecord{Key: "k", Fingerprint: "f", Status: 422, CreatedAt: t0}, t0)
		},
		"IdempotencyStore.Purge": func(c context.Context) error { _, err := idem.Purge(c, t0); return err },
		"Fleets.Get":             func(c context.Context) error { _, _, err := fleets.Get(c, "AMR1"); return err },
		"Fleets.Save":            func(c context.Context) error { return fleets.Save(c, "AMR1", fleetrow.Record{MapVersion: "v1"}) },
		"Robots.List":            func(c context.Context) error { _, err := robots.List(c); return err },
		"Robots.Get":             func(c context.Context) error { _, err := robots.Get(c, r1.ID()); return err },
		"Robots.Save":            func(c context.Context) error { return robots.Save(c, r1) },
		"Missions.Insert":        func(c context.Context) error { return missions.Insert(c, leg) },
		"Missions.Get":           func(c context.Context) error { _, err := missions.Get(c, "leg-t"); return err },
		"Missions.Open":          func(c context.Context) error { _, err := missions.Open(c); return err },
	}
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `LOCK TABLE idempotency_keys, fleets, robots, missions IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var failures []string
	var wg sync.WaitGroup
	for name, call := range calls {
		wg.Go(func() {
			wctx, cancel := context.WithTimeout(ctx, 15*time.Second) // the backstop, well past the timeouts
			defer cancel()
			start := time.Now()
			err := call(wctx)
			elapsed := time.Since(start)
			var pg interface{ SQLState() string } // *pgconn.PgError
			if !errors.As(err, &pg) || (pg.SQLState() != "55P03" && pg.SQLState() != "57014") || elapsed > 6500*time.Millisecond {
				mu.Lock()
				defer mu.Unlock()
				failures = append(failures, name+": "+errString(err)+" after "+elapsed.Round(time.Millisecond).String())
			}
		})
	}
	wg.Wait()
	sort.Strings(failures)
	for _, f := range failures {
		t.Errorf("%s; want SQLSTATE 55P03 (lock_timeout) or 57014 (statement_timeout) within ~6 s", f)
	}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
