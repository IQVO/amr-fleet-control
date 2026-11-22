package capacity_test

import (
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// carry (Task 6 review, brief Task 22 row "battery threshold"): a robot at exactly the charge threshold is
// available, one at 19.99 is not; every other count is asserted with it.
func TestARobotAtExactlyTheThresholdIsAvailable(t *testing.T) {
	at := bot(t, "R1", robot.Report{Battery: 20}, "P1")
	below := bot(t, "R2", robot.Report{Battery: 19.99}, "P2")
	got := capacity.Compute([]*robot.Robot{at, below}, 20, nil, t0)
	want := capacity.Snapshot{RobotsTotal: 2, RobotsOnline: 2, RobotsAvailable: 1, At: t0}
	if got != want {
		t.Fatalf("Compute at 20 and 19.99 = %+v, want %+v", got, want)
	}
	if got := capacity.Compute([]*robot.Robot{below}, 20, nil, t0); got != (capacity.Snapshot{RobotsTotal: 1, RobotsOnline: 1, At: t0}) {
		t.Fatalf("Compute at 19.99 = %+v, want nothing available", got)
	}
}
