package mission_test

import (
	"errors"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
)

// carry (Task 5 review): Fail takes only a failure reason of the published list (D16). CANCELLED is the outcome
// of RequestCancel / ConfirmCancel, never of Fail, and an off-list reason would publish a LegFailed the contract
// refuses. A refused Fail leaves the leg open and unchanged.
func TestFailRefusesCancelledAndOffListReasons(t *testing.T) {
	for _, bad := range []mission.Reason{"", mission.ReasonCancelled, "TIMEOUT", "robot_blocked"} {
		m := assigned(t)
		if evs, err := m.Fail(bad, "", t0); !errors.Is(err, mission.ErrInvalidMission) || evs != nil {
			t.Fatalf("Fail(%q) = %v %v, want ErrInvalidMission", bad, evs, err)
		}
		if m.Status() != mission.Assigned || m.Reason() != "" || !m.EndedAt().IsZero() {
			t.Fatalf("a refused Fail(%q) changed the leg: %+v", bad, m.State())
		}
	}
	for _, ok := range []mission.Reason{mission.ReasonRobotBlocked, mission.ReasonRobotFault, mission.ReasonRobotOffline,
		mission.ReasonOrderRejected, mission.ReasonPickFailed, mission.ReasonDropFailed, mission.ReasonMapVersionRetired} {
		m := assigned(t)
		if e := one[mission.LegFailed](t, must(m.Fail(ok, "", t0))); e.Reason != ok || m.Status() != mission.Failed {
			t.Fatalf("Fail(%s) = %+v (%s)", ok, e, m.Status())
		}
	}
	park, _ := mission.NewInternal(mission.Park, "ms-1", "v1", r1, []mission.Step{{Node: "B"}, {Node: "P2", Edge: "P2-B"}}, 1, t0)
	if _, err := park.Fail(mission.ReasonCancelled, "", t0); !errors.Is(err, mission.ErrInvalidMission) || !park.Open() {
		t.Fatalf("an internal mission failed CANCELLED = %v", err)
	}
}
