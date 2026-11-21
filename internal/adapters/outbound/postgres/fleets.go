package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/amr-fleet-control/internal/application/fleetrow"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
)

// Fleets is ports.Fleets: one row per fleet, locked by every write.
type Fleets struct{ pool *pgxpool.Pool }

func NewFleets(pool *pgxpool.Pool) *Fleets { return &Fleets{pool: pool} }

func (r *Fleets) Lock(ctx context.Context, fleetID string) (fleetrow.Record, bool, error) {
	return r.read(ctx, fleetID, " FOR UPDATE")
}

func (r *Fleets) Get(ctx context.Context, fleetID string) (fleetrow.Record, bool, error) {
	return r.read(ctx, fleetID, "")
}

func (r *Fleets) read(ctx context.Context, fleetID, lock string) (fleetrow.Record, bool, error) {
	var rec fleetrow.Record
	var res, cpt []byte
	err := queryFor(ctx, r.pool).QueryRow(ctx,
		`SELECT map_version, reservations, capacity FROM fleets WHERE fleet_id = $1`+lock, fleetID).Scan(&rec.MapVersion, &res, &cpt)
	if errors.Is(err, pgx.ErrNoRows) {
		return fleetrow.Record{}, false, nil
	}
	if err != nil {
		return fleetrow.Record{}, false, err
	}
	if err := json.Unmarshal(res, &rec.Reservations); err != nil {
		return fleetrow.Record{}, false, err
	}
	if cpt != nil {
		if err := json.Unmarshal(cpt, &rec.Capacity); err != nil {
			return fleetrow.Record{}, false, err
		}
		rec.HasCapacity = true
	}
	return rec, true, nil
}

func (r *Fleets) Save(ctx context.Context, fleetID string, rec fleetrow.Record) error {
	res, err := json.Marshal(nonNil(rec.Reservations))
	if err != nil {
		return err
	}
	var cpt []byte
	if rec.HasCapacity {
		if cpt, err = json.Marshal(rec.Capacity); err != nil {
			return err
		}
	}
	_, err = queryFor(ctx, r.pool).Exec(ctx, `INSERT INTO fleets (fleet_id, map_version, reservations, capacity) VALUES ($1, $2, $3, $4)
		ON CONFLICT (fleet_id) DO UPDATE SET map_version = $2, reservations = $3, capacity = $4, updated_at = now()`,
		fleetID, rec.MapVersion, res, cpt)
	return err
}

func nonNil(s fleetmap.State) fleetmap.State {
	if s.Nodes == nil {
		s.Nodes = map[string]string{}
	}
	if s.Edges == nil {
		s.Edges = map[string]string{}
	}
	if s.Waits == nil {
		s.Waits = map[string]fleetmap.Wait{}
	}
	return s
}
