package postgres

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// carry (plan 04 ruling P4-R17 amended, parity; ported from material-flow-control 62f4828 readonly.go): cmd/mcp and
// cmd/a2a read through a READ-ONLY pool. PgBouncer's transaction pooling can refuse a default_transaction_read_only
// startup parameter (and a session SET may sit on another server connection), so read-only is enforced here, per
// transaction: every transaction this adapter begins on such a pool is BEGIN READ ONLY (begin -> txOptions). This
// adapter has no autocommit pool path since Task 16 (every statement outside a unit of work runs in a transaction
// of its own through inTx), so plan 04's readOnlyQuerier is not needed: out-of-unit reads are READ ONLY through the
// same begin. A write through any path fails with SQLSTATE 25006. cmd/api keeps NewPool.

// readOnlyPools holds every pool NewReadOnlyPool opened. One per agent process; an entry is never removed (a
// closed pool begins nothing).
var readOnlyPools sync.Map

// NewReadOnlyPool opens a pool like NewPool whose every transaction this adapter begins is READ ONLY.
func NewReadOnlyPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := NewPool(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	readOnlyPools.Store(pool, struct{}{})
	return pool, nil
}

func isReadOnly(pool *pgxpool.Pool) bool {
	_, ok := readOnlyPools.Load(pool)
	return ok
}

// txOptions is READ COMMITTED (stated, not inherited), READ ONLY on a NewReadOnlyPool pool.
func txOptions(pool *pgxpool.Pool) pgx.TxOptions {
	o := pgx.TxOptions{IsoLevel: pgx.ReadCommitted}
	if isReadOnly(pool) {
		o.AccessMode = pgx.ReadOnly
	}
	return o
}
