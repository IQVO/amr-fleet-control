package fleetmap_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
)

func ids() func() string {
	n := 0
	return func() string {
		n++
		return "dl-" + string(rune('0'+n))
	}
}

func TestTwoRobotsWaitingOnEachOtherAreOneCycle(t *testing.T) {
	f := fleetmap.NewFleetMap(mustMap(t, floor()))
	f.SetWait(r2, "A", r1, t0.Add(time.Second))
	f.SetWait(r1, "B", r2, t0)
	f.SetWait(r3, "C", r1, t0) // waits on the cycle, is not part of it
	if got := f.Cycles(); !reflect.DeepEqual(got, [][]string{{r1, r2}}) {
		t.Fatalf("Cycles = %v", got)
	}
	if since := f.CycleSince([]string{r1, r2}); !since.Equal(t0.Add(time.Second)) {
		t.Fatalf("CycleSince = %v, want the latest wait", since)
	}
}

func TestAChainIsNotADeadlock(t *testing.T) {
	f := fleetmap.NewFleetMap(mustMap(t, floor()))
	f.SetWait(r1, "B", r2, t0)
	f.SetWait(r2, "C", r3, t0)
	if got := f.Cycles(); len(got) != 0 {
		t.Fatalf("Cycles = %v", got)
	}
}

func TestDetectDeadlocksReportsEachCycleOnceUntilItDissolves(t *testing.T) {
	f := fleetmap.NewFleetMap(mustMap(t, floor()))
	f.SetWait(r1, "B", r2, t0)
	f.SetWait(r2, "C", r3, t0)
	f.SetWait(r3, "A", r1, t0)
	next := ids()
	evs := f.DetectDeadlocks(t0, next)
	want := fleetmap.DeadlockDetected{DeadlockID: "dl-1", Robots: []string{r1, r2, r3}, Nodes: []string{"B", "C", "A"}, At: t0}
	if len(evs) != 1 || !reflect.DeepEqual(evs[0], want) {
		t.Fatalf("first detection = %+v", evs)
	}
	if again := f.DetectDeadlocks(t0.Add(time.Second), next); len(again) != 0 {
		t.Fatalf("a reported cycle was reported again: %+v", again)
	}
	if !reflect.DeepEqual(f.State().Deadlocks, []string{r1 + "," + r2 + "," + r3}) {
		t.Fatalf("State().Deadlocks = %v", f.State().Deadlocks)
	}
	f.ClearWait(r2)
	if evs := f.DetectDeadlocks(t0.Add(2*time.Second), next); len(evs) != 0 || len(f.State().Deadlocks) != 0 {
		t.Fatalf("dissolved cycle: events %+v, remembered %v", evs, f.State().Deadlocks)
	}
	f.SetWait(r2, "C", r3, t0.Add(3*time.Second))
	if evs := f.DetectDeadlocks(t0.Add(3*time.Second), next); len(evs) != 1 || evs[0].DeadlockID != "dl-2" {
		t.Fatalf("a re-formed cycle must be reported again: %+v", evs)
	}
}
