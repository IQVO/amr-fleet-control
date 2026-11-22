package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/amr-fleet-control/internal/application/idempotency"
)

// IdempotencyStore is the pgx ports.IdempotencyStore (24-hour window enforced by notBefore).
type IdempotencyStore struct{ pool *pgxpool.Pool }

func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore { return &IdempotencyStore{pool: pool} }

func (s *IdempotencyStore) Lookup(ctx context.Context, key string, notBefore time.Time) (idempotency.IdempotencyRecord, bool, error) {
	rec := idempotency.IdempotencyRecord{Key: key}
	err := queryRow(ctx, s.pool, `SELECT fingerprint, status, content_type, body, created_at
		FROM idempotency_keys WHERE key = $1 AND created_at >= $2`, []any{key, notBefore.UTC()},
		&rec.Fingerprint, &rec.Status, &rec.ContentType, &rec.Body, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return idempotency.IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return idempotency.IdempotencyRecord{}, false, fmt.Errorf("lookup idempotency key: %w", err)
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	return rec, true, nil
}

// Save keeps the first writer of a key; a row older than notBefore is replaced.
func (s *IdempotencyStore) Save(ctx context.Context, rec idempotency.IdempotencyRecord, notBefore time.Time) error {
	body := rec.Body
	if body == nil {
		body = []byte{}
	}
	_, err := exec(ctx, s.pool, `INSERT INTO idempotency_keys (key, fingerprint, status, content_type, body, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (key) DO UPDATE SET fingerprint = EXCLUDED.fingerprint, status = EXCLUDED.status,
			content_type = EXCLUDED.content_type, body = EXCLUDED.body, created_at = EXCLUDED.created_at
		WHERE idempotency_keys.created_at < $7`,
		rec.Key, rec.Fingerprint, rec.Status, rec.ContentType, body, rec.CreatedAt.UTC(), notBefore.UTC())
	if err != nil {
		return fmt.Errorf("save idempotency key: %w", err)
	}
	return nil
}

// Purge drops the answers created before before. carry (Task 12 review): in a transaction of its
// own (exec), so the SET LOCAL timeouts bound it.
func (s *IdempotencyStore) Purge(ctx context.Context, before time.Time) (int64, error) {
	tag, err := exec(ctx, s.pool, `DELETE FROM idempotency_keys WHERE created_at < $1`, before.UTC())
	return tag.RowsAffected(), err
}
