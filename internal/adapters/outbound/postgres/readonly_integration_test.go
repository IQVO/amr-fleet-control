//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/amr-fleet-control/internal/application/outbox"
	"github.com/IQVO/amr-fleet-control/internal/application/ports"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
	"github.com/IQVO/amr-fleet-control/internal/testing/pgtest"
)

// carry (plan 04 ruling P4-R17 amended, parity; ported from material-flow-control 62f4828
// readonly_integration_test.go): cmd/mcp and cmd/a2a read through postgres.NewReadOnlyPool. PgBouncer's
// transaction pooling may refuse a default_transaction_read_only startup parameter, so the adapter itself begins
// every transaction on that pool READ ONLY: a write attempted through any of its paths (a repository outside a
// unit of work, a unit of work, the outbox, a raw statement on exec / queryRow / query) fails with SQLSTATE 25006
// and writes nothing. cmd/api's NewPool stays read-write.

const readOnlySQLTransaction = "25006"

func readOnlyPools(t *testing.T) (rw, ro *postgres.Pool) {
	t.Helper()
	ctx := context.Background()
	url := pgtest.URL(t)
	rw, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rw.Close)
	ro, err = postgres.NewReadOnlyPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeWithin(t, ro, 5*time.Second) })
	return rw, ro
}

// closeWithin closes pool, failing instead of hanging when a connection is never handed back (Close waits for
// every acquired connection).
func closeWithin(t *testing.T, pool *postgres.Pool, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { pool.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Errorf("closing the read-only pool hung for %s with %d connections acquired: a transaction never ended", d, pool.Stat().AcquiredConns())
	}
}

func assertReadOnlyRefusal(t *testing.T, what string, err error) {
	t.Helper()
	var pgErr interface{ SQLState() string } // *pgconn.PgError, named without importing pgconn
	if !errors.As(err, &pgErr) || pgErr.SQLState() != readOnlySQLTransaction {
		t.Errorf("%s through the read-only pool = %v, want SQLSTATE %s (read_only_sql_transaction)", what, err, readOnlySQLTransaction)
	}
}

func TestAWriteThroughTheReadOnlyPoolFailsWithSQLState25006(t *testing.T) {
	ctx := context.Background()
	rw, ro := readOnlyPools(t)
	if err := postgres.NewMissions(rw).Insert(ctx, leg("leg-1", "C1")); err != nil {
		t.Fatal(err)
	}
	if got, err := postgres.NewMissions(ro).Get(ctx, "leg-1"); err != nil || got.ID() != "leg-1" {
		t.Fatalf("read through the read-only pool = %+v %v", got, err)
	}
	if _, err := postgres.NewRobots(ro).Get(ctx, robot.ID{Manufacturer: "EMU", SerialNumber: "R9"}); !errors.Is(err, ports.ErrRobotNotFound) {
		t.Fatalf("unknown robot through the read-only pool = %v, want ErrRobotNotFound", err)
	}
	r, _ := robot.New(robot.ID{Manufacturer: "EMU", SerialNumber: "R2"}, t0)
	assertReadOnlyRefusal(t, "Missions.Insert outside a unit (exec in its own tx)", postgres.NewMissions(ro).Insert(ctx, leg("leg-2", "C2")))
	assertReadOnlyRefusal(t, "Robots.Save outside a unit", postgres.NewRobots(ro).Save(ctx, r))
	assertReadOnlyRefusal(t, "a unit of work", postgres.NewUnitOfWork(ro).Do(ctx, func(ctx context.Context) error {
		return postgres.NewMissions(ro).Insert(ctx, leg("leg-2", "C2"))
	}))
	assertReadOnlyRefusal(t, "OutboxRepo.Insert", postgres.NewOutboxRepo(ro).Insert(ctx, outbox.Message{EventID: "ev-1",
		Topic: "t", EventType: "t", Subject: "s", Key: []byte("k"), DataSchema: "urn", Value: []byte(`{}`)}))
	assertReadOnlyRefusal(t, "a raw exec", postgres.ExecFor(ctx, ro, `DELETE FROM missions`))
	var n int
	assertReadOnlyRefusal(t, "a raw queryRow", postgres.QueryRowFor(ctx, ro, `UPDATE missions SET mission_id = mission_id RETURNING 1`, &n))
	assertReadOnlyRefusal(t, "a raw query", postgres.QueryFor(ctx, ro, `DELETE FROM missions RETURNING mission_id`, nil))

	if _, err := postgres.NewMissions(rw).Get(ctx, "leg-1"); err != nil {
		t.Fatalf("leg-1 after the refused writes = %v: a refused write deleted it", err)
	}
	if _, err := postgres.NewMissions(rw).Get(ctx, "leg-2"); !errors.Is(err, ports.ErrLegNotFound) {
		t.Fatalf("leg-2 after the refused writes = %v, want ErrLegNotFound", err)
	}
	if _, err := postgres.NewRobots(rw).Get(ctx, r.ID()); !errors.Is(err, ports.ErrRobotNotFound) {
		t.Fatalf("EMU/R2 after the refused writes = %v, want ErrRobotNotFound", err)
	}
	// cmd/api's pool stays read-write.
	if err := postgres.NewMissions(rw).Insert(ctx, leg("leg-2", "C2")); err != nil {
		t.Fatalf("the read-write pool refused a write: %v", err)
	}
}

// Every statement on the read-only pool runs inside a READ ONLY transaction, so transaction_read_only reads "on"
// whatever the session default says; the read-write pool's transactions read "off".
func TestTheReadOnlyPoolBeginsEveryTransactionReadOnly(t *testing.T) {
	ctx := context.Background()
	rw, ro := readOnlyPools(t)
	for _, c := range []struct {
		name string
		pool *postgres.Pool
		want string
	}{{"read-only", ro, "on"}, {"read-write", rw, "off"}} {
		var v string
		if err := postgres.QueryRowFor(ctx, c.pool, `SHOW transaction_read_only`, &v); err != nil || v != c.want {
			t.Errorf("%s pool outside a unit: transaction_read_only = %q (%v), want %q", c.name, v, err, c.want)
		}
		err := postgres.NewUnitOfWork(c.pool).Do(ctx, func(ctx context.Context) error {
			return postgres.QueryRowFor(ctx, c.pool, `SHOW transaction_read_only`, &v)
		})
		if err != nil || v != c.want {
			t.Errorf("%s pool in a unit: transaction_read_only = %q (%v), want %q", c.name, v, err, c.want)
		}
	}
}

// A list read on the read-only pool holds its transaction while the rows are read; ending the read must end the
// transaction and hand the connection back, or the agent runs out of its MaxConns connections after that many reads.
func TestReadOnlyListReadsReleaseTheirConnections(t *testing.T) {
	ctx := context.Background()
	rw, ro := readOnlyPools(t)
	r, _ := robot.New(robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}, t0)
	if err := postgres.NewRobots(rw).Save(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := postgres.NewMissions(rw).Insert(ctx, leg("leg-1", "C1")); err != nil {
		t.Fatal(err)
	}
	robots, missions := postgres.NewRobots(ro), postgres.NewMissions(ro)
	for i := 0; i < 3*postgres.MaxConns; i++ {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		rs, rerr := robots.List(rctx)
		ms, merr := missions.Open(rctx)
		cancel()
		if rerr != nil || merr != nil || len(rs) != 1 || len(ms) != 1 {
			t.Fatalf("list read %d on the read-only pool: List=%d %v Open=%d %v (stat: %+v)", i, len(rs), rerr, len(ms), merr, ro.Stat())
		}
	}
	if acquired := ro.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("%d connections still acquired after the reads: a read's transaction never ended", acquired)
	}
	if err := postgres.NewRobots(ro).Save(ctx, r); err == nil {
		t.Fatal("a write succeeded on the read-only pool after the list reads")
	}
}

// carry (plan 04 P4-R17 lesson, the rollback-on-Query-error leak): a statement that FAILS on the read-only pool
// (a refused write, a bad statement, a row callback's error) must still roll its transaction back and hand the
// connection back. Without the rollback every failed read pins a connection, and the agent stalls after MaxConns
// of them.
func TestReadOnlyQueryErrorsReleaseTheirConnections(t *testing.T) {
	ctx := context.Background()
	_, ro := readOnlyPools(t)
	stop := errors.New("stop")
	for i := 0; i < 3*postgres.MaxConns; i++ {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var n int
		errs := map[string]error{
			"a refused query":     postgres.QueryFor(rctx, ro, `DELETE FROM missions RETURNING 1`, nil),
			"a bad query":         postgres.QueryFor(rctx, ro, `SELECT * FROM no_such_table`, nil),
			"a row callback":      postgres.QueryFor(rctx, ro, `SELECT 1`, stop),
			"a refused query row": postgres.QueryRowFor(rctx, ro, `UPDATE missions SET mission_id = mission_id RETURNING 1`, &n),
			"a refused exec":      postgres.ExecFor(rctx, ro, `DELETE FROM missions`),
		}
		cancel()
		for what, err := range errs {
			if err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s %d on the read-only pool = %v, want its own error (stat: %+v)", what, i, err, ro.Stat())
			}
		}
	}
	if acquired := ro.Stat().AcquiredConns(); acquired != 0 {
		t.Fatalf("%d connections still acquired after the failed statements: a transaction was never rolled back", acquired)
	}
	var v string
	if err := postgres.QueryRowFor(ctx, ro, `SHOW transaction_read_only`, &v); err != nil || v != "on" {
		t.Fatalf("a read after the failures = %q %v", v, err)
	}
}
