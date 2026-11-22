package usecases_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

func TestRequestLegRefusesAtOnce(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	req := func(fleet, container, from, to string) error {
		_, err := h.svc.RequestLeg(ctx, usecases.LegRequest{FleetID: fleet, ContainerID: container, From: from, To: to})
		return err
	}
	if err := req("AMR1", "C1", "ST1", "ASRS1-IN1"); !errors.Is(err, usecases.ErrFleetUnavailable) {
		t.Fatalf("no robot online = %v", err)
	}
	h.online("R1", "P1", 90)
	cases := []struct {
		fleet, container, from, to string
		want                       error
	}{
		{"AMR9", "C1", "ST1", "ASRS1-IN1", usecases.ErrFleetNotFound},
		{"AMR1", "", "ST1", "ASRS1-IN1", usecases.ErrInvalidRequest},
		{"AMR1", "C1", "", "ASRS1-IN1", usecases.ErrInvalidRequest},
		{"AMR1", "C1", "ST1", "", usecases.ErrInvalidRequest},
		{"AMR1", "C1", "ST1", "ST1", usecases.ErrInvalidRequest},
		{"AMR1", "C1", "NOPE", "ASRS1-IN1", usecases.ErrUnknownHandoff},
		{"AMR1", "C1", "ST1", "NOPE", usecases.ErrUnknownHandoff},
		{"AMR1", "C1", "ST1", "ST9", usecases.ErrHandoffUnreachable},
	}
	for _, c := range cases {
		if err := req(c.fleet, c.container, c.from, c.to); !errors.Is(err, c.want) {
			t.Errorf("%+v = %v", c, err)
		}
	}
	h.leg("C1", "ST1", "ASRS1-IN1")
	if err := req("AMR1", "C1", "ST2", "ASRS1-IN2"); !errors.Is(err, usecases.ErrContainerBusy) {
		t.Fatalf("a second open leg for C1 = %v", err)
	}
	if n := len(ofType[mission.LegAssigned](h.events.All())); n != 1 {
		t.Fatalf("refusals published legs: %d LegAssigned", n)
	}
}

// (The body of the brief's test is split into checkBaseAndHorizonOrder, checkOrderDetails,
// checkR1Reservations and checkAssignedEvents to stay under this repo's gocognit limit of 20 and
// cyclop limit of 15; the assertions are the brief's, unchanged.)
func TestALegGoesToTheNearestRobotAsBaseAndHorizon(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	h.online("R2", "P2", 90)
	h.online("R3", "P3", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	if v.Status != "ASSIGNED" || v.Robot != rid("R1") || v.MapVersion != "v1" || v.From != "ST1" || v.To != "ASRS1-IN1" ||
		v.ContainerID != "C1" || v.Reference != "tl-C1" || v.FleetID != "AMR1" || !v.AcceptedAt.Equal(t0) || !v.AssignedAt.Equal(t0) {
		t.Fatalf("view = %+v", v)
	}
	checkBaseAndHorizonOrder(t, h, v)
	checkR1Reservations(t, h)
	checkAssignedEvents(t, h, v)
}

func checkBaseAndHorizonOrder(t *testing.T, h *harness, v usecases.LegView) {
	t.Helper()
	o := h.lastOrder("R1")
	if o.OrderID != v.LegID || o.UpdateID != 0 || !reflect.DeepEqual(nodeIDs(o), []string{"P1", "A", "ASRS1-OUT1", "E", "ST1", "E", "F", "ASRS1-IN1"}) {
		t.Fatalf("order = %+v", o)
	}
	if n, e := released(o); n != 3 || e != 2 {
		t.Fatalf("released %d nodes %d edges, want base P1-A-ASRS1-OUT1 (3, 2)", n, e)
	}
	for i, n := range o.Nodes {
		if n.Seq != vda.NodeSeq(i) || n.MapID != "S1" {
			t.Fatalf("node %d = %+v", i, n)
		}
	}
	checkOrderDetails(t, o, v)
}

func checkOrderDetails(t *testing.T, o vda.Order, v usecases.LegView) {
	t.Helper()
	if a, b := o.Nodes[1], o.Edges[3]; a.X != 0 || a.Y != 2 || b.ID != "E-ST1" || b.Seq != 7 || b.Start != "E" || b.End != "ST1" || b.Released {
		t.Fatalf("node A %+v, edge 4 %+v", a, b)
	}
	pick := o.Nodes[4].Actions
	if len(pick) != 1 || !reflect.DeepEqual(pick[0], vda.Action{Type: "pick", ID: v.LegID + "-pick", Blocking: vda.BlockingHard, Params: map[string]string{vda.ParamLoadID: "C1"}}) {
		t.Fatalf("pick = %+v", pick)
	}
	if d := o.Nodes[7].Actions; len(d) != 1 || d[0].Type != "drop" || d[0].ID != v.LegID+"-drop" || len(d[0].Params) != 0 {
		t.Fatalf("drop = %+v", d)
	}
}

func checkR1Reservations(t *testing.T, h *harness) {
	t.Helper()
	rec, _, _ := h.fleets.Get(ctx, "AMR1")
	for _, n := range []string{"P1", "A", "ASRS1-OUT1"} {
		if rec.Reservations.Nodes[n] != "EMU/R1" {
			t.Fatalf("node %s held by %q", n, rec.Reservations.Nodes[n])
		}
	}
	if rec.Reservations.Nodes["E"] != "" || rec.Reservations.Edges["P1-A"] != "EMU/R1" || rec.Reservations.Edges["A-ASRS1-OUT1"] != "EMU/R1" {
		t.Fatalf("reservations = %+v", rec.Reservations)
	}
}

func checkAssignedEvents(t *testing.T, h *harness, v usecases.LegView) {
	t.Helper()
	evs := h.events.All()
	la := ofType[mission.LegAssigned](evs)
	if len(la) != 1 || la[0] != (mission.LegAssigned{LegID: v.LegID, ContainerID: "C1", From: "ST1", To: "ASRS1-IN1", Reference: "tl-C1", Robot: rid("R1"), MapVersion: "v1", At: t0}) {
		t.Fatalf("LegAssigned = %+v", la)
	}
	caps := ofType[capacity.FleetCapacityChanged](evs)
	if last := caps[len(caps)-1].Snapshot; last.RobotsTotal != 3 || last.RobotsAvailable != 2 || last.LegsActive != 1 || last.LegsQueued != 0 {
		t.Fatalf("capacity = %+v", last)
	}
}

// (The body of the brief's test is split into checkCancelAssigned to stay under this repo's
// cyclop limit of 15; the assertions are the brief's, unchanged.)
func TestCancellationMatrix(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	h.report("R1", func(r *robot.Report) {
		r.Errors = []robot.Error{{Type: robot.ErrTypeBlocked, Level: robot.LevelCritical}}
	})
	q := h.leg("C1", "ST1", "ASRS1-IN1")
	if q.Status != "QUEUED" {
		t.Fatalf("a HELD robot took the leg: %+v", q)
	}
	v, now, err := h.svc.CancelLeg(ctx, "AMR1", q.LegID)
	if err != nil || !now || v.Status != "CANCELLED" || v.Reason != "CANCELLED" {
		t.Fatalf("cancel queued = %+v %v %v", v, now, err)
	}
	if _, _, err := h.svc.CancelLeg(ctx, "AMR1", q.LegID); !errors.Is(err, usecases.ErrLegEnded) {
		t.Fatalf("cancel again = %v", err)
	}
	checkCancelAssigned(t, h)
	p := h.leg("C3", "ST1", "ASRS1-IN1")
	h.drive("R1", 1, 4)
	h.action("R1", p.LegID+"-pick", robot.ActionFinished, false)
	if _, _, err := h.svc.CancelLeg(ctx, "AMR1", p.LegID); !errors.Is(err, usecases.ErrNotCancellable) {
		t.Fatalf("cancel picked up = %v", err)
	}
	if _, _, err := h.svc.CancelLeg(ctx, "AMR1", "leg-nope"); !errors.Is(err, usecases.ErrLegNotFound) {
		t.Fatalf("cancel unknown = %v", err)
	}
	if _, _, err := h.svc.CancelLeg(ctx, "AMR9", p.LegID); !errors.Is(err, usecases.ErrFleetNotFound) {
		t.Fatalf("cancel on another fleet = %v", err)
	}
}

// checkCancelAssigned is the brief's middle of TestCancellationMatrix: only "v, now, err ="
// became ":=" because the variables are local to the helper.
func checkCancelAssigned(t *testing.T, h *harness) {
	t.Helper()
	h.report("R1", func(r *robot.Report) { r.Errors = nil })
	a := h.leg("C2", "ST1", "ASRS1-IN1")
	h.ack("R1")
	v, now, err := h.svc.CancelLeg(ctx, "AMR1", a.LegID)
	if err != nil || now || v.Status != "CANCEL_REQUESTED" {
		t.Fatalf("cancel assigned = %+v %v %v", v, now, err)
	}
	_, _, _ = h.svc.CancelLeg(ctx, "AMR1", a.LegID)
	if ia := h.instants("R1"); len(ia) != 1 || !reflect.DeepEqual(ia[0], vda.Action{Type: vda.CancelOrder, ID: a.LegID + "-cancel", Blocking: vda.BlockingHard}) {
		t.Fatalf("instant actions = %+v (one cancelOrder, never repeated)", ia)
	}
	h.report("R1", func(r *robot.Report) { // cancelOrder: the emulator drops the order, so nothing is pending
		setAction(r, robot.ActionState{ID: a.LegID + "-cancel", Status: robot.ActionFinished}, true)
		r.Pending = false
	})
	failed := ofType[mission.LegFailed](h.events.All())
	if len(failed) != 2 || failed[1].LegID != a.LegID || failed[1].Reason != mission.ReasonCancelled || failed[1].ContainerOnRobot || failed[1].Robot != rid("R1") {
		t.Fatalf("LegFailed = %+v", failed)
	}
	if got := h.metrics.Ended(); !reflect.DeepEqual(got, []string{"failed/CANCELLED", "failed/CANCELLED"}) {
		t.Fatalf("metrics = %v", got)
	}
}

func TestAnInternalMissionIsNotALeg(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "ST1", 90) // a robot left on a station is parked (decision D11)
	o := h.lastOrder("R1")
	if _, _, err := h.svc.CancelLeg(ctx, "AMR1", o.OrderID); !errors.Is(err, usecases.ErrLegNotFound) {
		t.Fatalf("cancel the park mission = %v", err)
	}
}
