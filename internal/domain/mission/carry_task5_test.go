package mission_test

import (
	"errors"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
)

// carry (Task 5 review): a resend repeats the current orderUpdateId (D8/D19 same-id resend); the next base
// extension is exactly +1 and starts at the previous base end.
func TestResentKeepsTheUpdateIDAndTheNextExtendCountsOne(t *testing.T) {
	m := assigned(t)
	m.MarkSent(t0)
	if err := m.Extend(4); err != nil {
		t.Fatal(err)
	}
	m.MarkSent(t0)
	m.Resent(t0.Add(5 * time.Second))
	m.Resent(t0.Add(10 * time.Second))
	if m.UpdateID() != 1 || m.OrderStart() != 2 || m.BaseEnd() != 4 {
		t.Fatalf("after the Resent pair: update %d start %d end %d, want 1 2 4", m.UpdateID(), m.OrderStart(), m.BaseEnd())
	}
	if err := m.Extend(5); err != nil {
		t.Fatal(err)
	}
	if m.UpdateID() != 2 || m.OrderStart() != 4 || m.BaseEnd() != 5 || m.Resends() != 0 || m.Sent() {
		t.Fatalf("after Extend(5): update %d start %d end %d resends %d sent %v, want 2 4 5 0 false",
			m.UpdateID(), m.OrderStart(), m.BaseEnd(), m.Resends(), m.Sent())
	}
}

// carry (Task 5 review): Extend refuses a mission that holds no released route (QUEUED) and an ended one (DONE),
// and a refused Extend leaves the update id, the base and the order start alone.
func TestExtendRefusesAQueuedAndAnEndedMission(t *testing.T) {
	q := mustLeg(t)
	if err := q.Extend(1); !errors.Is(err, mission.ErrInvalidTransition) || q.UpdateID() != 0 || q.BaseEnd() != 0 {
		t.Fatalf("Extend of a queued leg = %v (update %d end %d)", err, q.UpdateID(), q.BaseEnd())
	}
	d := assigned(t)
	_, _ = d.PickUp(t0, t0)
	_, _ = d.Complete(t0, t0)
	if err := d.Extend(3); !errors.Is(err, mission.ErrInvalidTransition) || d.UpdateID() != 0 || d.BaseEnd() != 2 || d.OrderStart() != 0 {
		t.Fatalf("Extend of a done leg = %v (update %d end %d start %d)", err, d.UpdateID(), d.BaseEnd(), d.OrderStart())
	}
}

// carry (Task 5 review): the outcome boundaries after a cancel. A CANCEL_REQUESTED leg is neither picked up nor
// completed; Fail ends it with container_on_robot false (only PICKED_UP means the container is on the robot); a
// CANCELLED leg is ended.
func TestOutcomesAfterACancel(t *testing.T) {
	cr := func() *mission.Mission {
		m := assigned(t)
		if _, _, err := m.RequestCancel(t0); err != nil || m.Status() != mission.CancelRequested {
			t.Fatalf("RequestCancel = %v %s", err, m.Status())
		}
		return m
	}
	if _, err := cr().PickUp(t0, t0); !errors.Is(err, mission.ErrInvalidTransition) {
		t.Fatalf("PickUp on CANCEL_REQUESTED = %v", err)
	}
	if _, err := cr().Complete(t0, t0); !errors.Is(err, mission.ErrInvalidTransition) {
		t.Fatalf("Complete on CANCEL_REQUESTED = %v", err)
	}
	f := cr()
	if e := one[mission.LegFailed](t, must(f.Fail(mission.ReasonRobotOffline, "", t0))); e.ContainerOnRobot || e.Reason != mission.ReasonRobotOffline ||
		f.Status() != mission.Failed {
		t.Fatalf("Fail on CANCEL_REQUESTED = %+v (%s)", e, f.Status())
	}
	c := cr()
	if _, err := c.ConfirmCancel(true, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fail(mission.ReasonRobotFault, "", t0); !errors.Is(err, mission.ErrLegEnded) || c.Status() != mission.Cancelled {
		t.Fatalf("Fail on CANCELLED = %v (%s)", err, c.Status())
	}
}
