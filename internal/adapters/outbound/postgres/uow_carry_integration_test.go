//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/amr-fleet-control/internal/application/fleetrow"
	"github.com/IQVO/amr-fleet-control/internal/application/outbox"
	"github.com/IQVO/amr-fleet-control/internal/testing/pgtest"
)

// carry (Task 11 review): a ctx kept from a finished unit still carries its committed
// transaction. Do joins only an OPEN frame, so a unit opened from that ctx begins its own
// transaction: its write commits, its hook runs, and an error rolls its write back.
func TestADoFromAFinishedUnitsCtxBeginsItsOwnTransaction(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	uow, fleets := postgres.NewUnitOfWork(pool), postgres.NewFleets(pool)
	var kept context.Context
	if err := uow.Do(ctx, func(c context.Context) error { kept = c; return nil }); err != nil {
		t.Fatal(err)
	}
	ran := false
	if err := uow.Do(kept, func(c context.Context) error {
		uow.AfterCommit(c, func() { ran = true })
		return fleets.Save(c, "AMR1", fleetrow.Record{MapVersion: "v1"})
	}); err != nil || !ran {
		t.Fatalf("err = %v, hook ran = %v: a unit opened from a finished unit's ctx must commit and run its hooks", err, ran)
	}
	_ = uow.Do(kept, func(c context.Context) error {
		if err := fleets.Save(c, "AMR2", fleetrow.Record{MapVersion: "v1"}); err != nil {
			return err
		}
		return errors.New("roll back")
	})
	if _, ok, err := fleets.Get(ctx, "AMR1"); !ok || err != nil {
		t.Fatalf("AMR1 committed = %v %v", ok, err)
	}
	if _, ok, err := fleets.Get(ctx, "AMR2"); ok || err != nil {
		t.Fatalf("AMR2 must be rolled back: %v %v", ok, err)
	}
	hooked := 0
	uow.AfterCommit(kept, func() { hooked++ })
	if hooked != 1 {
		t.Fatal("AfterCommit on a finished frame must run at once")
	}
}

// carry (Task 12 review): an AfterCommit hook runs after the commit, so a Do it opens with the
// unit's own ctx must begin a transaction of its own, never join the committed one.
func TestAHooksOwnDoBeginsItsOwnTransaction(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	uow, fleets := postgres.NewUnitOfWork(pool), postgres.NewFleets(pool)
	var herr error
	err := uow.Do(ctx, func(c context.Context) error {
		uow.AfterCommit(c, func() {
			herr = uow.Do(c, func(c2 context.Context) error { return fleets.Save(c2, "AMR9", fleetrow.Record{MapVersion: "v1"}) })
		})
		return nil
	})
	if err != nil || herr != nil {
		t.Fatalf("unit = %v, hook's Do = %v", err, herr)
	}
	if _, ok, err := fleets.Get(ctx, "AMR9"); !ok || err != nil {
		t.Fatalf("the hook's write did not commit: %v %v", ok, err)
	}
}

// carry (Task 12 review): a repository call made outside Do with a ctx kept from a finished
// unit must not run on that unit's closed transaction: queryFor falls back to the pool and
// inTx begins a transaction of its own.
func TestARepositoryCallOnAFinishedUnitsCtxRunsOutsideIt(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	uow, fleets, repo := postgres.NewUnitOfWork(pool), postgres.NewFleets(pool), postgres.NewOutboxRepo(pool)
	var kept context.Context
	if err := uow.Do(ctx, func(c context.Context) error { kept = c; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fleets.Get(kept, "AMR1"); err != nil {
		t.Errorf("Fleets.Get on a finished unit's ctx = %v", err)
	}
	if err := repo.Insert(kept, outbox.Message{EventID: "e", Topic: "t", EventType: "x", Subject: "s", DataSchema: "u", Value: []byte("{}")}); err != nil {
		t.Errorf("OutboxRepo.Insert on a finished unit's ctx = %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_id = 'e'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("outbox rows committed = %d (%v), want 1", n, err)
	}
}
