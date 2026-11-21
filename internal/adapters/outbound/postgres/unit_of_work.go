package postgres

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/postgres/pgtx"
)

// querier is the subset of pgx shared by *pgxpool.Pool and pgx.Tx the repositories need.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type hooksKey struct{}

// hooks are the after-commit functions of the outermost unit of work. carry (Task 11 review):
// done is set once its transaction committed or rolled back, so a ctx kept from a finished
// unit (it still carries the closed transaction) never joins it.
type hooks struct {
	fns  []func()
	done atomic.Bool
}

// joinable reports whether ctx carries a transaction of a unit of work that is still open.
func joinable(ctx context.Context) bool {
	if _, ok := pgtx.From(ctx); !ok {
		return false
	}
	h, ok := ctx.Value(hooksKey{}).(*hooks)
	return !ok || !h.done.Load()
}

// UnitOfWork is the pgx ports.UnitOfWork: Do begins one READ COMMITTED transaction carried in
// ctx (pgtx), commits on a nil return and rolls back otherwise; a ctx that already carries a
// transaction is joined. After a successful outermost commit the AfterCommit hooks run in
// order; a rollback or a panic drops them.
type UnitOfWork struct{ pool *pgxpool.Pool }

func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork { return &UnitOfWork{pool: pool} }

func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if joinable(ctx) {
		return fn(ctx)
	}
	// READ COMMITTED is stated, not inherited: the fleet row lock (SELECT ... FOR UPDATE,
	// decision D18) and the partial unique index serialise writers; no snapshot isolation is
	// wanted. carry (Task 12 review): comment reworded from the asrs-control original.
	tx, err := begin(ctx, u.pool)
	if err != nil {
		return fmt.Errorf("begin unit of work: %w", err)
	}
	// Rollback after Commit is a harmless no-op; it also covers a panic in fn.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	h := &hooks{}
	defer h.done.Store(true) // a rollback (error or panic) finishes the frame too
	if err := fn(context.WithValue(pgtx.With(ctx, tx), hooksKey{}, h)); err != nil {
		return err
	}
	err = tx.Commit(ctx)
	h.done.Store(true)
	if err != nil {
		return fmt.Errorf("commit unit of work: %w", err)
	}
	for _, f := range h.fns {
		f()
	}
	return nil
}

// AfterCommit implements ports.UnitOfWork.
func (u *UnitOfWork) AfterCommit(ctx context.Context, fn func()) {
	if h, ok := ctx.Value(hooksKey{}).(*hooks); ok && !h.done.Load() {
		h.fns = append(h.fns, fn)
		return
	}
	fn()
}

// carry (Task 12 review, Task 16): there is no pool path. A statement outside a unit of work (the
// idempotency middleware's Lookup and refusal Save, the hourly Purge, the read-only queries) runs
// in a transaction of its own through inTx, so begin's SET LOCAL timeouts bound it behind
// PgBouncer (pool_mode=transaction) too, where NewPool's session SET may sit on another server
// connection. Inside an open unit the unit's transaction is joined, as before.

// exec runs one statement on the open unit's transaction, or in a transaction of its own.
func exec(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) (pgconn.CommandTag, error) {
	var tag pgconn.CommandTag
	err := inTx(ctx, pool, func(q querier) error {
		var err error
		tag, err = q.Exec(ctx, sql, args...)
		return err
	})
	return tag, err
}

// queryRow runs a single-row query and scans it into dest, like exec.
func queryRow(ctx context.Context, pool *pgxpool.Pool, sql string, args []any, dest ...any) error {
	return inTx(ctx, pool, func(q querier) error { return q.QueryRow(ctx, sql, args...).Scan(dest...) })
}

// query runs a multi-row query like exec and hands every row to each, inside the transaction.
func query(ctx context.Context, pool *pgxpool.Pool, sql string, each func(pgx.Rows) error) error {
	return inTx(ctx, pool, func(q querier) error {
		rows, err := q.Query(ctx, sql)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := each(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	})
}

// inTx runs fn on the open unit's transaction, or on a transaction of its own. carry (Task 12
// review): a finished unit's ctx begins a transaction of its own.
func inTx(ctx context.Context, pool *pgxpool.Pool, fn func(q querier) error) error {
	if tx, ok := pgtx.From(ctx); ok && joinable(ctx) {
		return fn(tx)
	}
	tx, err := begin(ctx, pool)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// LockTimeout bounds how long a statement waits for a row lock (the fleet row, decision D18).
const LockTimeout = "5s"

// carry (Task 13 review, plan 04 fleet item): PgBouncer runs pool_mode=transaction, so a
// session-level SET (NewPool's AfterConnect) may sit on a server connection this transaction
// never runs on. Every transaction therefore states its own timeouts with SET LOCAL: they end
// with it and never leak to the next client of the server connection.
const setLocalTimeouts = "SET LOCAL statement_timeout = '" + StatementTimeout + "'; SET LOCAL lock_timeout = '" + LockTimeout + "'"

// begin starts a READ COMMITTED transaction with the timeouts set LOCAL to it.
func begin(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, setLocalTimeouts); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("set transaction timeouts: %w", err)
	}
	return tx, nil
}
