package usecases_test

import (
	"reflect"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

func TestALowRobotChargesAndIsNeverAssignedUntilCharged(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 15)
	o := h.lastOrder("R1")
	if !reflect.DeepEqual(nodeIDs(o), []string{"P1", "A", "B", "C", "D", "CH1"}) {
		t.Fatalf("charge order = %v", nodeIDs(o))
	}
	last := o.Nodes[len(o.Nodes)-1].Actions
	if len(last) != 1 || last[0].Type != mission.ActionCharge || last[0].ID != o.OrderID+"-startCharging" {
		t.Fatalf("charge action = %+v", last)
	}
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	if v.Status != "QUEUED" {
		t.Fatalf("a robot below the threshold took a leg (responsibility 4): %+v", v)
	}
	h.drive("R1", 0, 5)
	h.report("R1", func(r *robot.Report) {
		r.Pending, r.Charging, r.Battery = false, true, 50
		setAction(r, robot.ActionState{ID: o.OrderID + "-startCharging", Status: robot.ActionFinished}, false)
	})
	if m, _ := h.missions.Get(ctx, o.OrderID); m.Status() != mission.Done {
		t.Fatalf("charge mission = %s", m.Status())
	}
	if len(h.instants("R1")) != 0 {
		t.Fatal("stopCharging before the resume level")
	}
	h.report("R1", func(r *robot.Report) { r.Battery = 80 })
	h.report("R1", func(r *robot.Report) { r.Battery = 81 })
	ia := h.instants("R1")
	if len(ia) != 1 || ia[0].Type != vda.StopCharging {
		t.Fatalf("stopCharging once = %+v", ia)
	}
	if got, _ := h.missions.Get(ctx, v.LegID); got.Status() != mission.Queued {
		t.Fatal("a charging robot took the leg before it stopped charging")
	}
	h.report("R1", func(r *robot.Report) { r.Charging = false })
	if lo := h.lastOrder("R1"); lo.OrderID != v.LegID || lo.Nodes[0].ID != "CH1" {
		t.Fatalf("the charged robot takes the queued leg from the charger: %+v", lo)
	}
}

func TestARobotWaitsWhereAnotherRobotHoldsItsNextNode(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "ASRS1-OUT1", 90) // parked at once: P1 is the nearest parking, via A
	h.online("R2", "P2", 90)
	h.ack("R1")
	v := h.leg("C1", "ASRS1-OUT1", "ST1") // R2: P2, B, A, ASRS1-OUT1 (R1 is not assignable: it is parking)
	o := h.lastOrder("R2")
	if o.OrderID != v.LegID || nodeIDs(o)[2] != "A" {
		t.Fatalf("R2 order = %v", nodeIDs(o))
	}
	if n, _ := released(o); n != 2 {
		t.Fatalf("R2 base must stop before A, which R1 holds: %d nodes released", n)
	}
	h.ack("R2")
	h.reach("R2", 1)
	rec, _, _ := h.fleets.Get(ctx, "AMR1")
	if w, ok := rec.Reservations.Waits["EMU/R2"]; !ok || w.Node != "A" || w.Blocker != "EMU/R1" || !w.Since.Equal(t0) {
		t.Fatalf("wait = %+v %v", rec.Reservations.Waits, ok)
	}
	h.reach("R1", 1)
	h.reach("R1", 2)
	h.report("R1", func(r *robot.Report) { r.Pending = false })
	if lo := h.lastOrder("R2"); lo.UpdateID != 1 || lo.Nodes[0].ID != "B" {
		t.Fatalf("R2 must be released once A is free: %+v", lo)
	}
	rec, _, _ = h.fleets.Get(ctx, "AMR1")
	if _, ok := rec.Reservations.Waits["EMU/R2"]; ok {
		t.Fatal("the wait must clear")
	}
}
