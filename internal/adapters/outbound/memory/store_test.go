package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/memory"
	"github.com/IQVO/amr-fleet-control/internal/application/fleetrow"
	"github.com/IQVO/amr-fleet-control/internal/application/ports"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func leg(id, container string) *mission.Mission {
	m, err := mission.NewLeg(mission.LegSpec{ID: id, MapVersion: "v1", ContainerID: container, From: "ST1", To: "ST2", PickupNode: "ST1", DropNode: "ST2", At: t0})
	if err != nil {
		panic(err)
	}
	return m
}

func TestUnitOfWorkCommitsRollsBackAndRunsHooksAfterTheOutermostCommit(t *testing.T) {
	s := memory.NewStore()
	uow, missions, events := memory.NewUnitOfWork(s), memory.NewMissions(s), memory.NewEvents(s)
	ctx := context.Background()
	var ran []string
	boom := errors.New("boom")
	err := uow.Do(ctx, func(ctx context.Context) error {
		_ = missions.Insert(ctx, leg("leg-1", "C1"))
		_ = events.Append(ctx, "e1")
		uow.AfterCommit(ctx, func() { ran = append(ran, "rolled-back") })
		return boom
	})
	if !errors.Is(err, boom) || len(ran) != 0 || len(events.All()) != 0 {
		t.Fatalf("rollback: err %v hooks %v events %v", err, ran, events.All())
	}
	if _, err := missions.Get(ctx, "leg-1"); !errors.Is(err, ports.ErrLegNotFound) {
		t.Fatalf("a rolled-back insert survived: %v", err)
	}
	err = uow.Do(ctx, func(ctx context.Context) error {
		uow.AfterCommit(ctx, func() { ran = append(ran, "outer") })
		return uow.Do(ctx, func(ctx context.Context) error {
			uow.AfterCommit(ctx, func() { ran = append(ran, "inner") })
			if len(ran) != 0 {
				t.Fatal("a hook ran before the outermost commit")
			}
			return missions.Insert(ctx, leg("leg-1", "C1"))
		})
	})
	if err != nil || len(ran) != 2 || ran[0] != "outer" || ran[1] != "inner" {
		t.Fatalf("commit: err %v hooks %v", err, ran)
	}
	uow.AfterCommit(ctx, func() { ran = append(ran, "now") })
	if ran[2] != "now" {
		t.Fatal("outside a unit of work a hook runs at once")
	}
}

func TestMissionsKeepSeqOrderAndOneOpenLegPerContainer(t *testing.T) {
	s := memory.NewStore()
	missions, ctx := memory.NewMissions(s), context.Background()
	a, b := leg("leg-1", "C1"), leg("leg-2", "C2")
	_ = missions.Insert(ctx, a)
	_ = missions.Insert(ctx, b)
	if a.Seq() != 1 || b.Seq() != 2 {
		t.Fatalf("seq = %d %d", a.Seq(), b.Seq())
	}
	if err := missions.Insert(ctx, leg("leg-3", "C1")); !errors.Is(err, ports.ErrOpenLegExists) {
		t.Fatalf("a second open leg for C1 = %v", err)
	}
	_, _, _ = a.RequestCancel(t0)
	_ = missions.Update(ctx, a)
	if err := missions.Insert(ctx, leg("leg-3", "C1")); err != nil {
		t.Fatalf("an ended leg frees its container: %v", err)
	}
	open, _ := missions.Open(ctx)
	if len(open) != 2 || open[0].ID() != "leg-2" || open[1].ID() != "leg-3" {
		t.Fatalf("Open = %v", open)
	}
	got, _ := missions.Get(ctx, "leg-1")
	if got.Status() != mission.Cancelled {
		t.Fatalf("Get = %s", got.Status())
	}
}

func TestFleetsAndRobotsRoundTrip(t *testing.T) {
	s := memory.NewStore()
	fleets, robots, ctx := memory.NewFleets(s), memory.NewRobots(s), context.Background()
	if _, ok, _ := fleets.Lock(ctx, "AMR1"); ok {
		t.Fatal("a fleet row before any save")
	}
	rec := fleetrow.Record{MapVersion: "v1", Reservations: fleetmap.State{Nodes: map[string]string{"P1": "EMU/R1"}}}
	_ = fleets.Save(ctx, "AMR1", rec)
	got, ok, _ := fleets.Get(ctx, "AMR1")
	if !ok || got.MapVersion != "v1" || got.Reservations.Nodes["P1"] != "EMU/R1" {
		t.Fatalf("fleet = %+v", got)
	}
	got.Reservations.Nodes["P1"] = "X"
	again, _, _ := fleets.Lock(ctx, "AMR1")
	if again.Reservations.Nodes["P1"] != "EMU/R1" {
		t.Fatal("the store shares the reservation map")
	}
	r2, _ := robot.New(robot.ID{Manufacturer: "EMU", SerialNumber: "R2"}, t0)
	r1, _ := robot.New(robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}, t0)
	_ = robots.Save(ctx, r2)
	_ = robots.Save(ctx, r1)
	list, _ := robots.List(ctx)
	if len(list) != 2 || list[0].ID().SerialNumber != "R1" {
		t.Fatalf("List = %v", list)
	}
	if _, err := robots.Get(ctx, robot.ID{Manufacturer: "EMU", SerialNumber: "R9"}); !errors.Is(err, ports.ErrRobotNotFound) {
		t.Fatalf("Get unknown = %v", err)
	}
}
