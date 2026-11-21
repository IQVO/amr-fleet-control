package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/amr-fleet-control/internal/application/ports"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
)

// Missions is ports.Missions.
type Missions struct{ pool *pgxpool.Pool }

func NewMissions(pool *pgxpool.Pool) *Missions { return &Missions{pool: pool} }

const uniqueViolation = "23505"

func (r *Missions) Insert(ctx context.Context, m *mission.Mission) error {
	b, err := json.Marshal(m.State())
	if err != nil {
		return err
	}
	var seq int64
	err = queryFor(ctx, r.pool).QueryRow(ctx, `INSERT INTO missions (mission_id, kind, status, open, container_id, map_version, state, accepted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING seq`,
		m.ID(), string(m.Kind()), string(m.Status()), m.Open(), m.ContainerID(), m.MapVersion(), b, m.AcceptedAt()).Scan(&seq)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == uniqueViolation && pg.ConstraintName == "uq_missions_open_leg_container" {
		return fmt.Errorf("%w: %s", ports.ErrOpenLegExists, m.ContainerID())
	}
	if err != nil {
		return err
	}
	m.AssignSeq(seq)
	return r.Update(ctx, m) // the stored state carries the seq
}

func (r *Missions) Update(ctx context.Context, m *mission.Mission) error {
	b, err := json.Marshal(m.State())
	if err != nil {
		return err
	}
	_, err = queryFor(ctx, r.pool).Exec(ctx, `UPDATE missions SET status = $2, open = $3, state = $4, updated_at = now() WHERE mission_id = $1`,
		m.ID(), string(m.Status()), m.Open(), b)
	return err
}

func (r *Missions) Get(ctx context.Context, id string) (*mission.Mission, error) {
	var b []byte
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT state FROM missions WHERE mission_id = $1`, id).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ports.ErrLegNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	return decodeMission(b)
}

func (r *Missions) Open(ctx context.Context) ([]*mission.Mission, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT state FROM missions WHERE open ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*mission.Mission
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		m, err := decodeMission(b)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func decodeMission(b []byte) (*mission.Mission, error) {
	var s mission.State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return mission.Rehydrate(s), nil
}
