package robot_test

import (
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// carry (Task 11 review): a stopCharging still unanswered after the resend delay is due again,
// for as long as the robot keeps charging; a stop clears it.
func TestAnUnansweredStopChargingIsDueAgainAfterTheResendDelay(t *testing.T) {
	r, err := robot.New(robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	rep := robot.Report{Battery: 85, Charging: true}
	r.ApplyReport(rep, "CH1", t0)
	if !r.Charged(80) || r.StopChargeOverdue(80, t0, 5*time.Second) {
		t.Fatalf("a first stopCharging is Charged, never overdue: state %s", r.State())
	}
	r.MarkStopChargeSentAt(t0)
	if s := r.Snapshot(); !s.StopChargeSent || !s.StopChargeSentAt.Equal(t0) {
		t.Fatalf("snapshot = %+v", s)
	}
	if r.Charged(80) || r.StopChargeOverdue(80, t0.Add(4*time.Second), 5*time.Second) {
		t.Fatal("stopCharging is not due again before the resend delay")
	}
	if !r.StopChargeOverdue(80, t0.Add(5*time.Second), 5*time.Second) {
		t.Fatal("an unanswered stopCharging is due again after the resend delay")
	}
	if r.StopChargeOverdue(90, t0.Add(5*time.Second), 5*time.Second) {
		t.Fatal("below the resume level nothing is due")
	}
	rep.Charging = false
	r.ApplyReport(rep, "CH1", t0.Add(6*time.Second))
	if s := r.Snapshot(); s.StopChargeSent || !s.StopChargeSentAt.IsZero() || r.StopChargeOverdue(80, t0.Add(time.Hour), 5*time.Second) {
		t.Fatalf("a robot that stopped charging owes no stopCharging: %+v", s)
	}
}
