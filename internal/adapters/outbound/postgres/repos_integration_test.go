//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/amr-fleet-control/internal/application/fleetrow"
	"github.com/IQVO/amr-fleet-control/internal/application/idempotency"
	"github.com/IQVO/amr-fleet-control/internal/application/ports"
	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
	"github.com/IQVO/amr-fleet-control/internal/testing/pgtest"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func leg(id, container string) *mission.Mission {
	m, _ := mission.NewLeg(mission.LegSpec{ID: id, MapVersion: "v1", ContainerID: container, From: "ST1", To: "ST2", PickupNode: "ST1", DropNode: "ST2", At: t0})
	return m
}

func TestRepositoriesRoundTripAndKeepTheirRules(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	fleets, robots, missions := postgres.NewFleets(pool), postgres.NewRobots(pool), postgres.NewMissions(pool)
	if _, ok, err := fleets.Get(ctx, "AMR1"); ok || err != nil {
		t.Fatalf("no fleet row yet: %v %v", ok, err)
	}
	rec := fleetrow.Record{MapVersion: "v1", Reservations: fleetmap.State{Nodes: map[string]string{"P1": "EMU/R1"}, Edges: map[string]string{"P1-A": "EMU/R1"},
		Waits: map[string]fleetmap.Wait{"EMU/R2": {Node: "A", Blocker: "EMU/R1", Since: t0}}, Deadlocks: []string{"EMU/R1,EMU/R2"}},
		Capacity: capacity.Snapshot{RobotsTotal: 2, RobotsOnline: 2, At: t0}, HasCapacity: true}
	if err := fleets.Save(ctx, "AMR1", rec); err != nil {
		t.Fatal(err)
	}
	got, ok, err := fleets.Lock(ctx, "AMR1")
	if err != nil || !ok || !reflect.DeepEqual(got, rec) {
		t.Fatalf("fleet = %+v %v %v", got, ok, err)
	}
	r, _ := robot.New(robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}, t0)
	r.ApplyReport(robot.Report{At: t0, LastNodeID: "P1", MapID: "S1", Battery: 80, Errors: []robot.Error{{Type: "X", Level: "WARNING"}}}, "P1", t0)
	if err := robots.Save(ctx, r); err != nil {
		t.Fatal(err)
	}
	back, err := robots.Get(ctx, r.ID())
	if err != nil || !reflect.DeepEqual(back.Snapshot(), r.Snapshot()) {
		t.Fatalf("robot = %+v %v", back, err)
	}
	if _, err := robots.Get(ctx, robot.ID{Manufacturer: "EMU", SerialNumber: "R9"}); !errors.Is(err, ports.ErrRobotNotFound) {
		t.Fatalf("unknown robot = %v", err)
	}
	a, b := leg("leg-1", "C1"), leg("leg-2", "C2")
	if err := missions.Insert(ctx, a); err != nil || a.Seq() == 0 {
		t.Fatalf("insert = %v seq %d", err, a.Seq())
	}
	_ = missions.Insert(ctx, b)
	if err := missions.Insert(ctx, leg("leg-3", "C1")); !errors.Is(err, ports.ErrOpenLegExists) {
		t.Fatalf("second open leg of C1 = %v", err)
	}
	_, _, _ = a.RequestCancel(t0)
	_ = missions.Update(ctx, a)
	if err := missions.Insert(ctx, leg("leg-3", "C1")); err != nil {
		t.Fatalf("an ended leg frees the container: %v", err)
	}
	open, _ := missions.Open(ctx)
	if len(open) != 2 || open[0].ID() != "leg-2" || open[1].ID() != "leg-3" || open[0].Seq() >= open[1].Seq() {
		t.Fatalf("open = %v", open)
	}
	gotA, _ := missions.Get(ctx, "leg-1")
	if gotA.Status() != mission.Cancelled || gotA.Seq() != a.Seq() {
		t.Fatalf("leg-1 = %+v", gotA.State())
	}
	if _, err := missions.Get(ctx, "nope"); !errors.Is(err, ports.ErrLegNotFound) {
		t.Fatalf("unknown mission = %v", err)
	}
}

func TestTheFleetRowLockSerialisesUnitsOfWork(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	fleets, uow := postgres.NewFleets(pool), postgres.NewUnitOfWork(pool)
	_ = fleets.Save(ctx, "AMR1", fleetrow.Record{MapVersion: "v1"})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = uow.Do(ctx, func(ctx context.Context) error {
				rec, _, err := fleets.Lock(ctx, "AMR1")
				if err != nil {
					return err
				}
				rec.Capacity.LegsQueued++
				rec.HasCapacity = true
				return fleets.Save(ctx, "AMR1", rec)
			})
		}()
	}
	wg.Wait()
	rec, _, _ := fleets.Get(ctx, "AMR1")
	if rec.Capacity.LegsQueued != 20 {
		t.Fatalf("lost updates: %d of 20", rec.Capacity.LegsQueued)
	}
}

// carry (Task 12 review): asrs-control da5d808 TestIdempotencyStoreWindowAndPurge, plus the
// first writer kept inside the window and two concurrent units racing on one key.
func TestIdempotencyStoreWindowAndPurge(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	idem := postgres.NewIdempotencyStore(pool)
	rec := idempotency.IdempotencyRecord{Key: "k1", Fingerprint: "f", Status: 202, ContentType: "application/json", Body: []byte("{}"), CreatedAt: t0}
	if err := idem.Save(ctx, rec, t0.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := idem.Lookup(ctx, "k1", t0.Add(-24*time.Hour)); !ok || got.Status != 202 || got.Fingerprint != "f" || string(got.Body) != "{}" || !got.CreatedAt.Equal(t0) {
		t.Fatalf("Lookup = %+v %v", got, ok)
	}
	if _, ok, _ := idem.Lookup(ctx, "k1", t0.Add(time.Second)); ok {
		t.Fatal("an answer older than the window was replayed")
	}
	other := rec
	other.Fingerprint, other.Status, other.CreatedAt = "g", 422, t0.Add(time.Minute)
	_ = idem.Save(ctx, other, t0.Add(-24*time.Hour)) // inside the window: the first writer is kept
	if got, _, _ := idem.Lookup(ctx, "k1", t0.Add(-24*time.Hour)); got.Fingerprint != "f" || got.Status != 202 {
		t.Fatalf("a second writer inside the window replaced the first: %+v", got)
	}
	newer := rec
	newer.Fingerprint, newer.CreatedAt = "h", t0.Add(25*time.Hour)
	_ = idem.Save(ctx, newer, t0.Add(time.Hour)) // the old row is outside the window: replaced
	if got, _, _ := idem.Lookup(ctx, "k1", t0); got.Fingerprint != "h" {
		t.Fatalf("an expired row was not replaced: %+v", got)
	}
	if got := raceOneKey(ctx, postgres.NewUnitOfWork(pool), idem); got[0] != got[1] || got[0] == "" {
		t.Fatalf("concurrent writers saw different answers: %v (both would commit their effects)", got)
	}
	if n, err := idem.Purge(ctx, t0.Add(26*time.Hour)); err != nil || n != 2 {
		t.Fatalf("Purge = %d %v, want 2 (k1 and race)", n, err)
	}
}

// raceOneKey runs two units that Save then Lookup the same key; it returns what each read.
func raceOneKey(ctx context.Context, uow *postgres.UnitOfWork, idem *postgres.IdempotencyStore) []string {
	var wg sync.WaitGroup
	got := make([]string, 2)
	for i, fp := range []string{"x", "y"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = uow.Do(ctx, func(c context.Context) error {
				r := idempotency.IdempotencyRecord{Key: "race", Fingerprint: fp, Status: 201, CreatedAt: t0}
				if err := idem.Save(c, r, t0.Add(-time.Hour)); err != nil {
					return err
				}
				s, _, err := idem.Lookup(c, "race", t0.Add(-time.Hour))
				got[i] = s.Fingerprint
				return err
			})
		}()
	}
	wg.Wait()
	return got
}
