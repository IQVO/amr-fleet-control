//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/amr-fleet-control/internal/application/ports"
	"github.com/IQVO/amr-fleet-control/internal/testing/pgtest"
)

// carry (Task 12 review): an UPDATE that touches no row is ErrLegNotFound (memory.Missions refuses an unknown id the
// same way), never a silent success for a mission that was never inserted.
func TestMissionsUpdateOfAnUnknownIDIsLegNotFound(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	missions := postgres.NewMissions(pool)
	if err := missions.Update(ctx, leg("leg-update-9", "C-update-9")); !errors.Is(err, ports.ErrLegNotFound) {
		t.Fatalf("Update of an unknown mission = %v, want ErrLegNotFound", err)
	}
	if _, err := missions.Get(ctx, "leg-update-9"); !errors.Is(err, ports.ErrLegNotFound) {
		t.Fatalf("the refused Update stored leg-update-9: %v", err)
	}
	a := leg("leg-update-1", "C-update-1")
	if err := missions.Insert(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := missions.Update(ctx, a); err != nil {
		t.Fatalf("Update of an inserted mission = %v", err)
	}
}
