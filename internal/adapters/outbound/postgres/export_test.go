package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exported to the external tests only (ruling P4-R17 parity probes, Task 19): the pool type, and the three
// statement helpers every repository reads and writes through outside or inside a unit of work.
type Pool = pgxpool.Pool

// ExecFor exposes exec.
func ExecFor(ctx context.Context, pool *pgxpool.Pool, sql string) error {
	_, err := exec(ctx, pool, sql)
	return err
}

// QueryRowFor exposes queryRow.
func QueryRowFor(ctx context.Context, pool *pgxpool.Pool, sql string, dest ...any) error {
	return queryRow(ctx, pool, sql, nil, dest...)
}

// QueryFor exposes query; every row's callback returns rowErr (nil reads them all). The external tests name no pgx
// type (internal/architecture's testcontainers sensor flags a pgx import in an integration test).
func QueryFor(ctx context.Context, pool *pgxpool.Pool, sql string, rowErr error) error {
	return query(ctx, pool, sql, func(pgx.Rows) error { return rowErr })
}
