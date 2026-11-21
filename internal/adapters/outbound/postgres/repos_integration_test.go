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
