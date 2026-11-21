package usecases_test

import (
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// reportAt publishes the robot's previous state changed by edit at x, y (not snapped to a node).
func (h *harness) reportAt(serial string, x, y float64, edit func(*robot.Report)) {
	h.t.Helper()
	rep := h.seen[serial]
	edit(&rep)
	rep.X, rep.Y, rep.MapID = x, y, h.m.MapID()
	h.seen[serial] = rep
	h.must(h.svc.HandleState(ctx, vda.State{Robot: rid(serial), Report: rep}))
}

// carry (Task 8 review): cancelOrder stops the robot where it is (emulator: "the robot stops where
// it is; orderID and lastNodeID are kept"), possibly mid-edge. While it is driving or away from its
// lastNodeId, it keeps the edge it stands on and that edge's far node; back on a node it releases
// them.
func TestARobotStoppedMidEdgeKeepsTheEdgeAndItsFarNode(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	h.ack("R1")
	h.reach("R1", 1) // on A; the base now runs to E
	if _, now, err := h.svc.CancelLeg(ctx, "AMR1", v.LegID); err != nil || now {
		t.Fatalf("cancel = %v %v", now, err)
	}
	h.reportAt("R1", 0, 3, func(r *robot.Report) { r.Driving = true }) // halfway A -> ASRS1-OUT1
	h.reportAt("R1", 0, 3, func(r *robot.Report) {
		r.Driving, r.Pending = false, false
		setAction(r, robot.ActionState{ID: v.LegID + "-cancel", Status: robot.ActionFinished}, true)
	})
	rec, _, _ := h.fleets.Get(ctx, "AMR1")
	if rec.Reservations.Edges["A-ASRS1-OUT1"] != "EMU/R1" || rec.Reservations.Nodes["ASRS1-OUT1"] != "EMU/R1" ||
		rec.Reservations.Nodes["A"] != "EMU/R1" {
		t.Fatalf("a robot stopped mid-edge must keep its edge and far node: %+v", rec.Reservations)
	}
	if rec.Reservations.Nodes["E"] != "" || rec.Reservations.Edges["ASRS1-OUT1-E"] != "" {
		t.Fatalf("the rest of the cancelled base must be released: %+v", rec.Reservations)
	}
	h.reportAt("R1", 0, 2, func(*robot.Report) {}) // back on A, not driving
	rec, _, _ = h.fleets.Get(ctx, "AMR1")
	if rec.Reservations.Edges["A-ASRS1-OUT1"] != "" || rec.Reservations.Nodes["ASRS1-OUT1"] != "" {
		t.Fatalf("back on its node the robot releases the edge: %+v", rec.Reservations)
	}
}
