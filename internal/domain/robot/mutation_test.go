package robot_test

import (
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// Mutation work (Task 22): an ONLINE connection revives a stale robot and stamps LastSeen; any other connection
// leaves LastSeen and the stale flag alone (robot.go ApplyConnection, CONDITIONALS_NEGATION).
func TestOnlyAnOnlineConnectionRevivesAStaleRobot(t *testing.T) {
	r := online(t)
	r.MarkStale(t0.Add(2 * time.Minute))
	r.ApplyConnection(robot.ConnectionOffline, t0.Add(3*time.Minute), time.Time{})
	if s := r.Snapshot(); !s.Stale || !r.LastSeen().Equal(t0) {
		t.Fatalf("OFFLINE changed the stale flag or LastSeen: stale %v, last seen %v", s.Stale, r.LastSeen())
	}
	e := changed(t, r.ApplyConnection(robot.ConnectionOnline, t0.Add(4*time.Minute), time.Time{}))
	if s := r.Snapshot(); s.Stale || !r.LastSeen().Equal(t0.Add(4*time.Minute)) || !r.Online() || e.State != robot.Idle {
		t.Fatalf("ONLINE after stale: stale %v, last seen %v, online %v, event %+v", s.Stale, r.LastSeen(), r.Online(), e)
	}
}

// Mutation work (Task 22): RobotStateChanged is raised when only the connection changes (both STOPPED), and never
// when neither the state nor the connection changed (robot.go settle, CONDITIONALS_NEGATION).
func TestAConnectionChangeAloneIsPublishedAndARepeatIsNot(t *testing.T) {
	r := online(t)
	changed(t, r.ApplyConnection(robot.ConnectionOffline, t0.Add(time.Minute), time.Time{}))
	e := changed(t, r.ApplyConnection(robot.ConnectionBroken, t0.Add(2*time.Minute), time.Time{}))
	if e.State != robot.Stopped || e.Previous != robot.Stopped || e.Connection != robot.ConnectionBroken {
		t.Fatalf("OFFLINE -> CONNECTION_BROKEN = %+v, want STOPPED/STOPPED/CONNECTION_BROKEN", e)
	}
	if evs := r.ApplyConnection(robot.ConnectionBroken, t0.Add(3*time.Minute), time.Time{}); len(evs) != 0 {
		t.Fatalf("a repeated CONNECTION_BROKEN raised %+v", evs)
	}
}
