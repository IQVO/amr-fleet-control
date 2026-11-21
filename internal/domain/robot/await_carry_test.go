package robot_test

import (
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// carry (Task 10 review): the robot records when its awaited action was last sent, and forgets the
// time with the action (an answer, or an empty id).
func TestTheAwaitedActionKeepsItsSendTime(t *testing.T) {
	r := online(t)
	r.ApplyReport(idleReport(90), "P1", t0)
	r.AwaitAction("c1", t0)
	if r.AwaitingAction() != "c1" || !r.AwaitingSince().Equal(t0) || !r.Snapshot().AwaitingSince.Equal(t0) {
		t.Fatalf("snapshot = %+v", r.Snapshot())
	}
	r.AwaitAction("c1", t0.Add(5*time.Second))
	if !r.AwaitingSince().Equal(t0.Add(5 * time.Second)) {
		t.Fatalf("a re-send keeps the old time: %v", r.AwaitingSince())
	}
	r.AwaitAction("", t0.Add(6*time.Second))
	if r.AwaitingAction() != "" || !r.AwaitingSince().IsZero() {
		t.Fatalf("an empty id keeps a time: %+v", r.Snapshot())
	}
	r.AwaitAction("c2", t0)
	rep := idleReport(90)
	rep.InstantActions = []robot.ActionState{{ID: "c2", Status: robot.ActionFinished}}
	r.ApplyReport(rep, "P1", t0)
	if r.AwaitingAction() != "" || !r.AwaitingSince().IsZero() {
		t.Fatalf("an answered action keeps a time: %+v", r.Snapshot())
	}
}
