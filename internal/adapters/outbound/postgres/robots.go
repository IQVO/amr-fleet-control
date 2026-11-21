package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/amr-fleet-control/internal/application/ports"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// Robots is ports.Robots.
type Robots struct{ pool *pgxpool.Pool }

func NewRobots(pool *pgxpool.Pool) *Robots { return &Robots{pool: pool} }

func (r *Robots) List(ctx context.Context) ([]*robot.Robot, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT snapshot FROM robots ORDER BY robot_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*robot.Robot
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var s robot.Snapshot
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, err
		}
		out = append(out, robot.Rehydrate(s))
	}
	return out, rows.Err()
}

func (r *Robots) Get(ctx context.Context, id robot.ID) (*robot.Robot, error) {
	var b []byte
	err := queryFor(ctx, r.pool).QueryRow(ctx, `SELECT snapshot FROM robots WHERE robot_key = $1`, id.Key()).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ports.ErrRobotNotFound, id.Key())
	}
	if err != nil {
		return nil, err
	}
	var s robot.Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return robot.Rehydrate(s), nil
}

func (r *Robots) Save(ctx context.Context, rb *robot.Robot) error {
	b, err := json.Marshal(rb.Snapshot())
	if err != nil {
		return err
	}
	_, err = queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO robots (robot_key, manufacturer, serial_number, state, snapshot)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (robot_key) DO UPDATE SET state = $4, snapshot = $5, updated_at = now()`,
		rb.ID().Key(), rb.ID().Manufacturer, rb.ID().SerialNumber, string(rb.State()), b)
	return err
}
