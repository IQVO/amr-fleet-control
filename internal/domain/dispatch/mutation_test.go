package dispatch_test

import (
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/domain/dispatch"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
)

// tol is dispatch's 1e-9 tie tolerance as a float64 VARIABLE: Go evaluates constant expressions exactly, so a
// boundary distance must be computed with the run-time rounding the code applies (bestLen-eps, bestLen+eps).
var tol float64 = 1e-9

// Mutation work (Task 22, dispatch.go better): a candidate exactly 1e-9 nearer or farther is still equidistant, so
// the battery decides; equal batteries go to the lower key whatever the order the candidates are listed in.
func TestChooseAtTheExactToleranceAndTheKeyTieInBothOrders(t *testing.T) {
	two := 2.0
	m := star(t, fleetmap.Parking, map[string]float64{"A": two, "N": -(two - tol), "W": -(two + tol)})
	nearer := []dispatch.Candidate{{Robot: id("R1"), Node: "A", Battery: 90}, {Robot: id("R2"), Node: "N", Battery: 30}}
	if c, _ := dispatch.Choose(m, "X", nearer); c.Robot != id("R1") {
		t.Fatalf("exactly 1e-9 nearer with less battery = %+v, want R1 (equidistant: the fuller battery)", c)
	}
	farther := []dispatch.Candidate{{Robot: id("R1"), Node: "A", Battery: 30}, {Robot: id("R2"), Node: "W", Battery: 90}}
	if c, _ := dispatch.Choose(m, "X", farther); c.Robot != id("R2") {
		t.Fatalf("exactly 1e-9 farther with more battery = %+v, want R2 (equidistant: the fuller battery)", c)
	}
	f := floor(t)
	lowFirst := []dispatch.Candidate{{Robot: id("R1"), Node: "P1", Battery: 50}, {Robot: id("R3"), Node: "P3", Battery: 50}}
	if c, _ := dispatch.Choose(f, "ASRS1-IN1", lowFirst); c.Robot != id("R1") {
		t.Fatalf("equal distance and battery, lower key listed first = %+v, want R1", c)
	}
}

// Mutation work (Task 22, dispatch.go NearestFree): a free node exactly 1e-9 nearer than a lower id is equidistant,
// so the lower id stays.
func TestNearestFreeAtTheExactTolerance(t *testing.T) {
	two := 2.0
	m := star(t, fleetmap.Parking, map[string]float64{"A": two, "B": -(two - tol)})
	if n, ok := dispatch.NearestFree(m, "X", fleetmap.Parking, func(string) bool { return true }); !ok || n != "A" {
		t.Fatalf("B exactly 1e-9 nearer than A = %q %v, want A (equidistant: the lower id)", n, ok)
	}
}

// Mutation work (Task 22, dispatch.go Window): a window reaching exactly one or two steps past the route is capped
// at the last step.
func TestWindowCapsAWindowJustPastTheRoute(t *testing.T) {
	m := floor(t)
	route, _ := dispatch.Plan(m, "P1", []dispatch.Target{{Node: "ASRS1-OUT1"}}, nil) // P1, A, ASRS1-OUT1
	free, freeE := holders(nil, nil)
	for _, ahead := range []int{2, 3} {
		if end, b, at := dispatch.Window(route, 0, 1, ahead, "EMU/R1", free, freeE); end != 2 || b != "" || at != "" {
			t.Fatalf("Window(reached 1, ahead %d) on 3 steps = %d %q %q, want 2", ahead, end, b, at)
		}
	}
}
