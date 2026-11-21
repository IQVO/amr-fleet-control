package usecases_test

import (
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// carry (Task 9 review): the mid-edge rule (underway) is for a robot without an active mission
// only (the after-cancel case). A robot driving its mission keeps exactly the unpassed part of its
// base: reaching A with Driving=true (already leaving for the next node) releases P1 and P1-A.
func TestADrivingRobotWithAMissionReleasesWhatItPassed(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	h.leg("C1", "ST1", "ASRS1-IN1")
	h.ack("R1")
	node := h.nodeAt("R1", 1)
	h.report("R1", func(r *robot.Report) {
		r.LastNodeID, r.LastNodeSeq, r.Driving = node, vda.NodeSeq(1), true
	})
	rec, _, _ := h.fleets.Get(ctx, "AMR1")
	if node != "A" || rec.Reservations.Nodes["P1"] != "" || rec.Reservations.Edges["P1-A"] != "" {
		t.Fatalf("a robot driving its mission past P1 must release P1 and P1-A (node %s): %+v", node, rec.Reservations)
	}
	if rec.Reservations.Nodes["A"] != "EMU/R1" || rec.Reservations.Edges["A-ASRS1-OUT1"] != "EMU/R1" {
		t.Fatalf("the unpassed base must stay held: %+v", rec.Reservations)
	}
}
