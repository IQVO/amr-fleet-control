//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/amr-fleet-control/internal/application/fleetrow"
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
