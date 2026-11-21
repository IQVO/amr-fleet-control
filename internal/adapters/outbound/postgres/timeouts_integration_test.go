//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/postgres/pgtx"
	"github.com/IQVO/amr-fleet-control/internal/application/fleetrow"
	"github.com/IQVO/amr-fleet-control/internal/application/outbox"
	"github.com/IQVO/amr-fleet-control/internal/testing/pgtest"
)

// carry (Task 13 review, plan 04 fleet item): PgBouncer runs pool_mode=transaction, so the
// session-level SET in NewPool's AfterConnect does not reach the server connection a later
// transaction runs on. The pool here has NO AfterConnect (what a transaction pooler leaves
// you): only the unit of work's own SET LOCAL bounds a unit waiting on a fleet row another
// transaction holds.
func TestAUnitWaitingOnAHeldFleetRowGivesUpWithinTheTimeouts(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.BarePool(t)
	uow, fleets := postgres.NewUnitOfWork(pool), postgres.NewFleets(pool)
	if err := fleets.Save(ctx, "AMR1", fleetrow.Record{MapVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	holding, release, held := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		held <- uow.Do(ctx, func(ctx context.Context) error {
			if _, _, err := fleets.Lock(ctx, "AMR1"); err != nil {
				return err
			}
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second) // the backstop, well past the timeouts
	defer cancel()
	start := time.Now()
	err := uow.Do(wctx, func(ctx context.Context) error {
		_, _, err := fleets.Lock(ctx, "AMR1")
		return err
	})
	elapsed := time.Since(start)
	close(release)
	if herr := <-held; herr != nil {
		t.Fatalf("holder: %v", herr)
	}
	var pg interface{ SQLState() string } // *pgconn.PgError
	if !errors.As(err, &pg) || (pg.SQLState() != "55P03" && pg.SQLState() != "57014") || elapsed > 6500*time.Millisecond {
		t.Fatalf("waiter = %v after %v; want SQLSTATE 55P03 (lock_timeout) or 57014 (statement_timeout) within ~6 s", err, elapsed)
	}
	for _, setting := range []string{"statement_timeout", "lock_timeout"} {
		var v string
		if err := pool.QueryRow(ctx, "SHOW "+setting).Scan(&v); err != nil || v != "0" {
			t.Fatalf("%s outside a unit = %q (%v): the unit's SET must be LOCAL, never leak to the session", setting, v, err)
		}
	}
}

// carry (Task 12 review): the relay's pass (Drain) and a stand-alone Insert (inTx) begin their
// own transactions, so they must state the same SET LOCAL timeouts. An ACCESS EXCLUSIVE lock
// on outbox_events makes both wait; on a pool with no session settings only their own
// lock_timeout ends the wait.
func TestDrainAndAnInsertOutsideAUnitCarryTheTimeouts(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.BarePool(t)
	repo := postgres.NewOutboxRepo(pool)
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `LOCK TABLE outbox_events IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func(context.Context) error{
		"Drain": func(c context.Context) error {
			_, err := repo.Drain(c, 10, 0, func(context.Context, outbox.Message) error { return nil })
			return err
		},
		"Insert outside a unit (inTx)": func(c context.Context) error {
			return repo.Insert(c, outbox.Message{EventID: "e", Topic: "t", EventType: "x", Subject: "s", DataSchema: "u", Value: []byte("{}")})
		},
	} {
		wctx, cancel := context.WithTimeout(ctx, 12*time.Second) // the backstop, well past the timeouts
		start := time.Now()
		err := run(wctx)
		elapsed := time.Since(start)
		cancel()
		var pg interface{ SQLState() string } // *pgconn.PgError
		if !errors.As(err, &pg) || (pg.SQLState() != "55P03" && pg.SQLState() != "57014") || elapsed > 6500*time.Millisecond {
			t.Errorf("%s = %v after %v; want SQLSTATE 55P03 (lock_timeout) or 57014 (statement_timeout) within ~6 s", name, err, elapsed)
		}
	}
}

// lesson (plan 04 Task 19 review, finding 4): both timeouts are 5 s and the waits above accept
// 55P03 or 57014, so a unit that dropped SET LOCAL lock_timeout would still pass them. Read both
// settings inside a unit on a pool with no session settings.
func TestAUnitStatesBothTimeouts(t *testing.T) {
	pool := pgtest.BarePool(t)
	err := postgres.NewUnitOfWork(pool).Do(context.Background(), func(ctx context.Context) error {
		tx, ok := pgtx.From(ctx)
		if !ok {
			t.Fatal("no tx in the unit's ctx")
		}
		for _, s := range []string{"statement_timeout", "lock_timeout"} {
			var v string
			if err := tx.QueryRow(ctx, "SHOW "+s).Scan(&v); err != nil || v != "5s" {
				t.Errorf("%s inside a unit = %q (%v), want 5s", s, v, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
