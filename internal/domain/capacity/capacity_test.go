package capacity_test

import (
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

func bot(t *testing.T, serial string, rep robot.Report, node string) *robot.Robot {
	t.Helper()
	r, err := robot.New(robot.ID{Manufacturer: "EMU", SerialNumber: serial}, t0)
	if err != nil {
		t.Fatal(err)
	}
	r.ApplyReport(rep, node, t0)
	return r
}

func TestComputeCountsRobotsByStateAndLegsByStatus(t *testing.T) {
	idle := bot(t, "R1", robot.Report{Battery: 90}, "P1")
	low := bot(t, "R2", robot.Report{Battery: 10}, "P2")
	charging := bot(t, "R3", robot.Report{Battery: 50, Charging: true}, "CH1")
	held := bot(t, "R4", robot.Report{Battery: 90, Errors: []robot.Error{{Type: robot.ErrTypeBlocked, Level: robot.LevelCritical}}}, "A")
	offline, _ := robot.New(robot.ID{Manufacturer: "EMU", SerialNumber: "R5"}, t0)
	queued, _ := mission.NewLeg(mission.LegSpec{ID: "leg-1", MapVersion: "v1", ContainerID: "C1", From: "ST1", To: "ST2", PickupNode: "ST1", DropNode: "ST2", At: t0})
	active, _ := mission.NewLeg(mission.LegSpec{ID: "leg-2", MapVersion: "v1", ContainerID: "C2", From: "ST1", To: "ST2", PickupNode: "ST1", DropNode: "ST2", At: t0})
	_, _ = active.Assign(idle.ID(), []mission.Step{{Node: "ST1", Action: mission.ActionPick}, {Node: "E", Edge: "E-ST1"}, {Node: "ST2", Edge: "x", Action: mission.ActionDrop}}, 0, t0)
	ended, _ := mission.NewLeg(mission.LegSpec{ID: "leg-3", MapVersion: "v1", ContainerID: "C3", From: "ST1", To: "ST2", PickupNode: "ST1", DropNode: "ST2", At: t0})
	_, _, _ = ended.RequestCancel(t0)
	park, _ := mission.NewInternal(mission.Park, "ms-1", "v1", low.ID(), []mission.Step{{Node: "A"}, {Node: "P1", Edge: "P1-A"}}, 1, t0)
	got := capacity.Compute([]*robot.Robot{idle, low, charging, held, offline}, 20, []*mission.Mission{queued, active, ended, park}, t0)
	want := capacity.Snapshot{RobotsTotal: 5, RobotsOnline: 4, RobotsAvailable: 1, RobotsCharging: 1, RobotsHeld: 1, LegsQueued: 1, LegsActive: 1, At: t0}
	if got != want {
		t.Fatalf("Compute = %+v, want %+v", got, want)
	}
}

func TestNextPublishesOnlyWhenACountChanges(t *testing.T) {
	a := capacity.Snapshot{RobotsTotal: 3, RobotsOnline: 3, At: t0}
	if e, ok := capacity.Next(capacity.Snapshot{}, false, a); !ok || e.Snapshot != a {
		t.Fatalf("the first snapshot is always published: %+v %v", e, ok)
	}
	later := a
	later.At = t0.Add(time.Hour)
	if _, ok := capacity.Next(a, true, later); ok {
		t.Fatal("a snapshot differing only in time was published")
	}
	if !a.SameCounts(later) {
		t.Fatal("SameCounts must ignore At")
	}
	for i, mutate := range []func(*capacity.Snapshot){
		func(s *capacity.Snapshot) { s.RobotsTotal++ }, func(s *capacity.Snapshot) { s.RobotsOnline-- },
		func(s *capacity.Snapshot) { s.RobotsAvailable++ }, func(s *capacity.Snapshot) { s.RobotsCharging++ },
		func(s *capacity.Snapshot) { s.RobotsHeld++ }, func(s *capacity.Snapshot) { s.LegsQueued++ },
		func(s *capacity.Snapshot) { s.LegsActive++ },
	} {
		b := later
		mutate(&b)
		if e, ok := capacity.Next(a, true, b); !ok || e.Snapshot != b {
			t.Fatalf("count %d changed but nothing was published", i)
		}
	}
}
