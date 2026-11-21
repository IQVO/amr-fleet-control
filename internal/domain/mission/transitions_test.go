package mission_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
)

func one[T any](t *testing.T, evs []any) T {
	t.Helper()
	if len(evs) != 1 {
		t.Fatalf("events = %+v", evs)
	}
	e, ok := evs[0].(T)
	if !ok {
		t.Fatalf("event %T", evs[0])
	}
	return e
}

func TestAssignRaisesLegAssignedAndStartsAtUpdateZero(t *testing.T) {
	m := mustLeg(t)
	evs, err := m.Assign(r1, legRoute(), 2, t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	e := one[mission.LegAssigned](t, evs)
	if e != (mission.LegAssigned{LegID: "leg-1", ContainerID: "C1", From: "ST1", To: "ASRS1-IN1", Reference: "tl-9", Robot: r1, MapVersion: "v1", At: t0.Add(time.Second)}) {
		t.Fatalf("LegAssigned = %+v", e)
	}
	if m.Status() != mission.Assigned || m.Robot() != r1 || m.BaseEnd() != 2 || m.Reached() != 0 || m.OrderStart() != 0 ||
		m.UpdateID() != 0 || m.Sent() || !m.Active() || !m.AssignedAt().Equal(t0.Add(time.Second)) {
		t.Fatalf("assigned = %+v", m.State())
	}
	if _, err := m.Assign(r1, legRoute(), 2, t0); !errors.Is(err, mission.ErrInvalidTransition) {
		t.Fatalf("a second Assign = %v", err)
	}
}

// (The body of the brief's test is split into checkSeenAndResends to stay under this repo's
// cyclop limit of 15; the assertions are the brief's, unchanged.)
func TestOrderUpdateIDOnlyIncreases(t *testing.T) {
	m := assigned(t)
	m.MarkSent(t0)
	if !m.Unacknowledged() || !m.Sent() || !m.SentAt().Equal(t0) {
		t.Fatal("a sent, unseen order is unacknowledged")
	}
	m.Seen(0)
	if m.Unacknowledged() {
		t.Fatal("update 0 was seen")
	}
	if err := m.Extend(4); err != nil {
		t.Fatal(err)
	}
	if m.UpdateID() != 1 || m.OrderStart() != 2 || m.BaseEnd() != 4 || m.Sent() {
		t.Fatalf("after Extend(4): update %d start %d end %d sent %v", m.UpdateID(), m.OrderStart(), m.BaseEnd(), m.Sent())
	}
	for _, bad := range []int{4, 3, 8, 9} {
		if err := m.Extend(bad); !errors.Is(err, mission.ErrInvalidMission) {
			t.Fatalf("Extend(%d) = %v", bad, err)
		}
	}
	if m.UpdateID() != 1 {
		t.Fatal("a refused Extend changed the update id")
	}
	checkSeenAndResends(t, m)
}

func checkSeenAndResends(t *testing.T, m *mission.Mission) {
	t.Helper()
	m.MarkSent(t0)
	m.Seen(0)
	if !m.Unacknowledged() {
		t.Fatal("seeing an older update does not acknowledge the newer one")
	}
	m.Seen(1)
	m.Seen(0)
	if m.Unacknowledged() || m.State().SeenUpdate != 1 {
		t.Fatalf("Seen must keep the highest update: %d", m.State().SeenUpdate)
	}
	m.Resent(t0.Add(5 * time.Second))
	m.Resent(t0.Add(10 * time.Second))
	if m.Resends() != 2 || !m.SentAt().Equal(t0.Add(10*time.Second)) || !m.Sent() {
		t.Fatalf("Resent = %d at %v", m.Resends(), m.SentAt())
	}
	if err := m.Extend(5); err != nil || m.Resends() != 0 {
		t.Fatalf("a new update id starts its resends at 0: %v %d", err, m.Resends())
	}
}

func TestProgressGrowsWithinTheBase(t *testing.T) {
	m := assigned(t)
	if err := m.Progress(2); err != nil || m.Reached() != 2 {
		t.Fatalf("Progress(2) = %v, reached %d", err, m.Reached())
	}
	if err := m.Progress(1); err != nil || m.Reached() != 2 {
		t.Fatal("Progress went backwards")
	}
	if err := m.Progress(3); !errors.Is(err, mission.ErrInvalidMission) {
		t.Fatalf("Progress past the base = %v", err)
	}
	if err := m.Progress(-1); !errors.Is(err, mission.ErrInvalidMission) {
		t.Fatalf("Progress(-1) = %v", err)
	}
	if err := mustLeg(t).Progress(0); !errors.Is(err, mission.ErrInvalidTransition) {
		t.Fatalf("Progress of a queued leg = %v", err)
	}
}

func TestRerouteReplacesTheHorizonAndKeepsTheActions(t *testing.T) {
	m := assigned(t) // base end 2 = ASRS1-OUT1
	if got := m.Targets(); !reflect.DeepEqual(got, []mission.Step{legRoute()[4], legRoute()[7]}) {
		t.Fatalf("Targets = %+v", got)
	}
	tail := []mission.Step{{Node: "ASRS1-OUT1"}, {Node: "E", Edge: "ASRS1-OUT1-E"}, {Node: "ST1", Edge: "E-ST1", Action: mission.ActionPick},
		{Node: "E", Edge: "E-ST1"}, {Node: "ASRS1-OUT1", Edge: "ASRS1-OUT1-E"}, {Node: "A", Edge: "A-ASRS1-OUT1"},
		{Node: "B", Edge: "A-B"}, {Node: "ASRS1-IN1", Edge: "B-ASRS1-IN1", Action: mission.ActionDrop}}
	if err := m.Reroute(tail); err != nil {
		t.Fatal(err)
	}
	r := m.Route()
	if len(r) != 10 || r[2] != legRoute()[2] || r[9].Node != "ASRS1-IN1" || r[9].Action != mission.ActionDrop || m.UpdateID() != 1 || m.OrderStart() != 2 || m.BaseEnd() != 2 {
		t.Fatalf("rerouted = %+v update %d start %d", r, m.UpdateID(), m.OrderStart())
	}
	if err := m.Reroute([]mission.Step{{Node: "E"}}); !errors.Is(err, mission.ErrInvalidMission) {
		t.Fatalf("a re-route not starting at the base end = %v", err)
	}
	if err := m.Reroute([]mission.Step{{Node: "ASRS1-OUT1"}, {Node: "A", Edge: "A-ASRS1-OUT1"}}); !errors.Is(err, mission.ErrInvalidMission) {
		t.Fatalf("a re-route dropping the pick and drop = %v", err)
	}
	if err := m.Reroute(nil); !errors.Is(err, mission.ErrInvalidMission) {
		t.Fatalf("an empty re-route = %v", err)
	}
	if m.UpdateID() != 1 {
		t.Fatal("a refused re-route changed the update id")
	}
	if err := mustLeg(t).Reroute(tail); !errors.Is(err, mission.ErrInvalidTransition) {
		t.Fatalf("re-routing a queued leg = %v", err)
	}
}

func TestALegEndsInExactlyOneOutcome(t *testing.T) {
	m := assigned(t)
	if _, err := m.Complete(t0, t0); !errors.Is(err, mission.ErrInvalidTransition) {
		t.Fatalf("Complete before the pickup = %v", err)
	}
	p := one[mission.ContainerPickedUp](t, must(m.PickUp(t0.Add(time.Minute), t0.Add(time.Hour))))
	if p != (mission.ContainerPickedUp{LegID: "leg-1", ContainerID: "C1", From: "ST1", Reference: "tl-9", Robot: r1, At: t0.Add(time.Minute), RobotTime: t0.Add(time.Hour)}) {
		t.Fatalf("ContainerPickedUp = %+v", p)
	}
	if m.Status() != mission.PickedUp || !m.PickedUpAt().Equal(t0.Add(time.Minute)) {
		t.Fatalf("picked = %+v", m.State())
	}
	if _, err := m.PickUp(t0, t0); !errors.Is(err, mission.ErrInvalidTransition) {
		t.Fatalf("a second pickup = %v", err)
	}
	c := one[mission.LegCompleted](t, must(m.Complete(t0.Add(2*time.Minute), t0.Add(2*time.Hour))))
	if c != (mission.LegCompleted{LegID: "leg-1", ContainerID: "C1", To: "ASRS1-IN1", Reference: "tl-9", Robot: r1, At: t0.Add(2 * time.Minute), RobotTime: t0.Add(2 * time.Hour)}) {
		t.Fatalf("LegCompleted = %+v", c)
	}
	if m.Status() != mission.Done || m.Open() || m.Active() || !m.EndedAt().Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("done = %+v", m.State())
	}
	if _, err := m.Fail(mission.ReasonRobotFault, "", t0); !errors.Is(err, mission.ErrLegEnded) {
		t.Fatalf("Fail after Done = %v", err)
	}
	if _, err := m.Complete(t0, t0); !errors.Is(err, mission.ErrInvalidTransition) {
		t.Fatalf("a second Complete = %v", err)
	}
}

func must(evs []any, err error) []any {
	if err != nil {
		panic(err)
	}
	return evs
}

func TestFailCarriesTheReasonAndWhetherTheContainerIsOnTheRobot(t *testing.T) {
	m := assigned(t)
	f := one[mission.LegFailed](t, must(m.Fail(mission.ReasonPickFailed, "LOAD_NOT_AVAILABLE", t0)))
	if f != (mission.LegFailed{LegID: "leg-1", ContainerID: "C1", From: "ST1", To: "ASRS1-IN1", Reference: "tl-9", Robot: r1,
		Reason: mission.ReasonPickFailed, RobotError: "LOAD_NOT_AVAILABLE", At: t0}) {
		t.Fatalf("LegFailed = %+v", f)
	}
	if m.Status() != mission.Failed || m.Reason() != mission.ReasonPickFailed || m.RobotError() != "LOAD_NOT_AVAILABLE" {
		t.Fatalf("failed = %+v", m.State())
	}
	p := assigned(t)
	_, _ = p.PickUp(t0, t0)
	if f := one[mission.LegFailed](t, must(p.Fail(mission.ReasonRobotBlocked, "ROBOT_BLOCKED", t0))); !f.ContainerOnRobot {
		t.Fatal("a leg failed after the pickup must say the container is on the robot")
	}
	q := mustLeg(t)
	if f := one[mission.LegFailed](t, must(q.Fail(mission.ReasonMapVersionRetired, "", t0))); !f.Robot.IsZero() || f.ContainerOnRobot {
		t.Fatalf("a queued leg failed = %+v", f)
	}
	if _, err := mustLeg(t).Fail("", "", t0); !errors.Is(err, mission.ErrInvalidMission) {
		t.Fatalf("Fail without a reason = %v", err)
	}
}

// (The body of the brief's test is split into checkAssignedCancel to stay under this repo's
// cyclop limit of 15; the assertions are the brief's, unchanged. The helper declares
// imm/evs/err up front so the brief's `imm, evs, err = a.RequestCancel(t0)` line stays as is.)
func TestCancellationMatrix(t *testing.T) {
	q := mustLeg(t)
	imm, evs, err := q.RequestCancel(t0)
	if err != nil || !imm || q.Status() != mission.Cancelled || q.Reason() != mission.ReasonCancelled {
		t.Fatalf("cancel queued = %v %v %+v", imm, err, q.State())
	}
	if f := one[mission.LegFailed](t, evs); f.Reason != mission.ReasonCancelled || f.ContainerOnRobot {
		t.Fatalf("queued cancel event = %+v", f)
	}
	if _, _, err := q.RequestCancel(t0); !errors.Is(err, mission.ErrLegEnded) {
		t.Fatalf("cancel an ended leg = %v", err)
	}
	checkAssignedCancel(t)
	p := assigned(t)
	_, _ = p.PickUp(t0, t0)
	if _, _, err := p.RequestCancel(t0); !errors.Is(err, mission.ErrNotCancellable) || p.Status() != mission.PickedUp {
		t.Fatalf("cancel picked up = %v", err)
	}
	park, _ := mission.NewInternal(mission.Park, "ms-1", "v1", r1, []mission.Step{{Node: "B"}, {Node: "P2", Edge: "P2-B"}}, 1, t0)
	if _, _, err := park.RequestCancel(t0); !errors.Is(err, mission.ErrInvalidTransition) {
		t.Fatalf("cancelling an internal mission = %v", err)
	}
}

func checkAssignedCancel(t *testing.T) {
	t.Helper()
	var (
		imm bool
		evs []any
		err error
	)
	a := assigned(t)
	imm, evs, err = a.RequestCancel(t0)
	if err != nil || imm || len(evs) != 0 || a.Status() != mission.CancelRequested || !a.Active() {
		t.Fatalf("cancel assigned = %v %v %v %s", imm, evs, err, a.Status())
	}
	if imm, evs, err := a.RequestCancel(t0); err != nil || imm || len(evs) != 0 {
		t.Fatal("a repeated cancel request is a no-op")
	}
	if err := a.Reroute([]mission.Step{{Node: "ASRS1-OUT1"}}); !errors.Is(err, mission.ErrInvalidTransition) {
		t.Fatalf("re-routing a cancelled order = %v", err)
	}
	f := one[mission.LegFailed](t, must(a.ConfirmCancel(true, t0.Add(time.Second))))
	if f.Reason != mission.ReasonCancelled || !f.ContainerOnRobot || a.Status() != mission.Cancelled {
		t.Fatalf("ConfirmCancel = %+v", f)
	}
	if _, err := a.ConfirmCancel(false, t0); !errors.Is(err, mission.ErrInvalidTransition) {
		t.Fatalf("a second ConfirmCancel = %v", err)
	}
}

func TestInternalMissionsEndSilently(t *testing.T) {
	park, _ := mission.NewInternal(mission.Park, "ms-1", "v1", r1, []mission.Step{{Node: "B"}, {Node: "P2", Edge: "P2-B"}}, 0, t0)
	if got := park.Targets(); !reflect.DeepEqual(got, []mission.Step{{Node: "P2", Edge: "P2-B"}}) {
		t.Fatalf("park Targets = %+v", got)
	}
	evs, err := park.Complete(t0, t0)
	if err != nil || len(evs) != 0 || park.Status() != mission.Done {
		t.Fatalf("park Complete = %v %v", evs, err)
	}
	if _, err := park.PickUp(t0, t0); !errors.Is(err, mission.ErrInvalidTransition) {
		t.Fatal("a park picks nothing up")
	}
	ch, _ := mission.NewInternal(mission.Charge, "ms-2", "v1", r1, []mission.Step{{Node: "D"}, {Node: "CH1", Edge: "CH1-D", Action: mission.ActionCharge}}, 1, t0)
	evs, err = ch.Fail(mission.ReasonRobotOffline, "", t0)
	if err != nil || len(evs) != 0 || ch.Status() != mission.Failed {
		t.Fatalf("charge Fail = %v %v", evs, err)
	}
}
