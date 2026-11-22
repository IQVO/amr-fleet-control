package mission_test

import (
	"errors"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
)

// Mutation work (Task 22): an action may sit on step 0 (the robot already stands on its node), so every "no such
// action" check is "< 0" and "present" is ">= 0" (mission.go actions/fits, CONDITIONALS_BOUNDARY).
func TestActionsOnTheFirstStep(t *testing.T) {
	pickInPlace := []mission.Step{{Node: "ST1", Action: mission.ActionPick}, {Node: "E", Edge: "E-ST1"}, {Node: "F", Edge: "E-F"},
		{Node: "ASRS1-IN1", Edge: "ASRS1-IN1-F", Action: mission.ActionDrop}}
	if _, err := mustLeg(t).Assign(r1, pickInPlace, 0, t0); err != nil {
		t.Fatalf("a leg picking where the robot stands = %v", err)
	}
	repeated := []mission.Step{{Node: "ST1", Action: mission.ActionPick}, {Node: "E", Edge: "E-ST1"},
		{Node: "ST1", Edge: "E-ST1", Action: mission.ActionPick}, {Node: "E", Edge: "E-ST1"}, {Node: "F", Edge: "E-F"},
		{Node: "ASRS1-IN1", Edge: "ASRS1-IN1-F", Action: mission.ActionDrop}}
	if _, err := mustLeg(t).Assign(r1, repeated, 0, t0); !errors.Is(err, mission.ErrInvalidMission) {
		t.Fatalf("a pick on step 0 and again on step 2 = %v", err)
	}
	chargeFirst := legRoute()
	chargeFirst[0].Action = mission.ActionCharge
	if _, err := mustLeg(t).Assign(r1, chargeFirst, 0, t0); !errors.Is(err, mission.ErrInvalidMission) {
		t.Fatalf("a leg charging on step 0 = %v", err)
	}
	for _, c := range []struct {
		kind  mission.Kind
		route []mission.Step
	}{
		{mission.Charge, []mission.Step{{Node: "D", Action: mission.ActionPick}, {Node: "CH1", Edge: "CH1-D", Action: mission.ActionCharge}}},
		{mission.Charge, []mission.Step{{Node: "D", Action: mission.ActionDrop}, {Node: "CH1", Edge: "CH1-D", Action: mission.ActionCharge}}},
		{mission.Park, []mission.Step{{Node: "B", Action: mission.ActionPick}, {Node: "P2", Edge: "P2-B"}}},
		{mission.Park, []mission.Step{{Node: "B", Action: mission.ActionDrop}, {Node: "P2", Edge: "P2-B"}}},
		{mission.Park, []mission.Step{{Node: "B", Action: mission.ActionCharge}, {Node: "P2", Edge: "P2-B"}}},
	} {
		if _, err := mission.NewInternal(c.kind, "ms-1", "v1", r1, c.route, 0, t0); !errors.Is(err, mission.ErrInvalidMission) {
			t.Errorf("%s with %s on step 0 = %v", c.kind, c.route[0].Action, err)
		}
	}
}

// Mutation work (Task 22): an internal mission starts with no update seen (-1), so its update 0 is unacknowledged
// once sent until a state shows it (mission.go NewInternal SeenUpdate, INVERT_NEGATIVES / ARITHMETIC_BASE).
func TestASentInternalMissionIsUnacknowledgedUntilSeen(t *testing.T) {
	park, err := mission.NewInternal(mission.Park, "ms-1", "v1", r1, []mission.Step{{Node: "B"}, {Node: "P2", Edge: "P2-B"}}, 1, t0)
	if err != nil {
		t.Fatal(err)
	}
	park.MarkSent(t0)
	if !park.Unacknowledged() || park.State().SeenUpdate != -1 {
		t.Fatalf("a sent park order before any state: unacknowledged %v, seen %d", park.Unacknowledged(), park.State().SeenUpdate)
	}
	park.Seen(0)
	if park.Unacknowledged() {
		t.Fatal("update 0 was seen")
	}
}

// Mutation work (Task 22): index 0 is inside the base (transitions.go Progress, CONDITIONALS_BOUNDARY).
func TestProgressToTheFirstStepIsInsideTheBase(t *testing.T) {
	m := assigned(t)
	if err := m.Progress(0); err != nil || m.Reached() != 0 {
		t.Fatalf("Progress(0) = %v, reached %d", err, m.Reached())
	}
}
