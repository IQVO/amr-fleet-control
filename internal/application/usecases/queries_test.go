package usecases_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// (The body of the brief's test is split, in its order, into checkFleetQuery, checkRobotQuery,
// checkLegQueries and checkReaderAndOtherFleets to stay under this repo's cyclop limit of 15 and
// gocognit limit of 20; the assertions are the brief's, unchanged.)
func TestQueries(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	h.online("R2", "P2", 15)
	h.must(h.svc.HandleFactsheet(ctx, vda.Factsheet{Robot: rid("R1"), Factsheet: robot.Factsheet{SeriesName: "EMU-AMR", MaxSpeed: 1.5, MaxLoadMass: 50}}))
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	checkFleetQuery(t, h)
	checkRobotQuery(t, h, v)
	checkLegQueries(t, h, v)
	checkReaderAndOtherFleets(t, h, v)
}

func checkReaderAndOtherFleets(t *testing.T, h *harness, v usecases.LegView) {
	t.Helper()
	ro, err := usecases.NewReader(h.m, usecases.DefaultConfig(), h.robots, h.missions, h.clock)
	if err != nil {
		t.Fatal(err)
	}
	if rf, err := ro.Fleet(ctx, "AMR1"); err != nil || len(rf.Robots) != 2 {
		t.Fatalf("the reader sees the same fleet: %+v %v", rf, err)
	}
	if _, err := ro.RequestLeg(ctx, usecases.LegRequest{FleetID: "AMR1", ContainerID: "C9", From: "ST1", To: "ST2"}); !errors.Is(err, usecases.ErrReadOnly) {
		t.Fatalf("a write through the reader = %v", err)
	}
	if err := ro.Tick(ctx); !errors.Is(err, usecases.ErrReadOnly) {
		t.Fatalf("a tick through the reader = %v", err)
	}
	if _, err := usecases.NewReader(nil, usecases.DefaultConfig(), h.robots, h.missions, h.clock); !errors.Is(err, usecases.ErrInvalidConfig) {
		t.Fatalf("a reader without a map = %v", err)
	}
	for _, call := range []func() error{
		func() error { _, err := h.svc.Fleet(ctx, "AMR9"); return err },
		func() error { _, err := h.svc.Leg(ctx, "AMR9", v.LegID); return err },
		func() error { _, err := h.svc.OpenLegs(ctx, "AMR9"); return err },
		func() error { _, err := h.svc.Robot(ctx, "AMR9", rid("R1")); return err },
	} {
		if err := call(); !errors.Is(err, usecases.ErrFleetNotFound) {
			t.Fatalf("another fleet = %v", err)
		}
	}
}

func checkFleetQuery(t *testing.T, h *harness) {
	t.Helper()
	f, err := h.svc.Fleet(ctx, "AMR1")
	if err != nil || f.FleetID != "AMR1" || f.MapID != "S1" || f.MapVersion != "v1" || len(f.Robots) != 2 {
		t.Fatalf("Fleet = %+v %v", f, err)
	}
	want := []string{"ASRS1-IN1", "ASRS1-IN2", "ASRS1-OUT1", "ASRS1-OUT2", "ST1", "ST2", "ST9"}
	if !reflect.DeepEqual(f.HandoffPoints, want) {
		t.Fatalf("handoff points = %v", f.HandoffPoints)
	}
	if f.Capacity != (capacity.Snapshot{RobotsTotal: 2, RobotsOnline: 2, RobotsAvailable: 0, LegsActive: 1, At: t0}) {
		t.Fatalf("capacity = %+v (R1 on the leg, R2 charging-bound)", f.Capacity)
	}
}

func checkRobotQuery(t *testing.T, h *harness, v usecases.LegView) {
	t.Helper()
	r, err := h.svc.Robot(ctx, "AMR1", rid("R1"))
	if err != nil || r.State != "EXECUTE" || r.Connection != "ONLINE" || r.Battery != 90 || r.Available || r.Node != "P1" ||
		r.MissionID != v.LegID || !r.HasFactsheet || r.Factsheet.MaxLoadMass != 50 || r.ErrorTypes == nil || !r.LastSeenAt.Equal(t0) {
		t.Fatalf("Robot = %+v %v", r, err)
	}
	if _, err := h.svc.Robot(ctx, "AMR1", rid("R9")); !errors.Is(err, usecases.ErrRobotNotFound) {
		t.Fatalf("unknown robot = %v", err)
	}
}

func checkLegQueries(t *testing.T, h *harness, v usecases.LegView) {
	t.Helper()
	got, err := h.svc.Leg(ctx, "AMR1", v.LegID)
	if err != nil || got.Status != "ASSIGNED" || got.Robot != rid("R1") {
		t.Fatalf("Leg = %+v %v", got, err)
	}
	open, _ := h.svc.OpenLegs(ctx, "AMR1")
	if len(open) != 1 || open[0].LegID != v.LegID {
		t.Fatalf("OpenLegs = %+v (internal missions are not legs)", open)
	}
	r2, _ := h.robots.Get(ctx, rid("R2"))
	if _, err := h.svc.Leg(ctx, "AMR1", r2.MissionID()); !errors.Is(err, usecases.ErrLegNotFound) {
		t.Fatalf("an internal mission read as a leg = %v", err)
	}
}
