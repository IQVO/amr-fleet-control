package mission_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

var (
	t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	r1 = robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}
)

func legSpec() mission.LegSpec {
	return mission.LegSpec{ID: "leg-1", MapVersion: "v1", ContainerID: "C1", From: "ST1", To: "ASRS1-IN1",
		PickupNode: "ST1", DropNode: "ASRS1-IN1", Reference: "tl-9", At: t0}
}

// legRoute: R1 at P1 drives to ST1, picks, drives to ASRS1-IN1, drops.
func legRoute() []mission.Step {
	return []mission.Step{
		{Node: "P1"}, {Node: "A", Edge: "P1-A"}, {Node: "ASRS1-OUT1", Edge: "A-ASRS1-OUT1"}, {Node: "E", Edge: "ASRS1-OUT1-E"},
		{Node: "ST1", Edge: "E-ST1", Action: mission.ActionPick}, {Node: "E", Edge: "E-ST1"}, {Node: "F", Edge: "E-F"},
		{Node: "ASRS1-IN1", Edge: "ASRS1-IN1-F", Action: mission.ActionDrop},
	}
}

func mustLeg(t *testing.T) *mission.Mission {
	t.Helper()
	m, err := mission.NewLeg(legSpec())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func assigned(t *testing.T) *mission.Mission {
	t.Helper()
	m := mustLeg(t)
	if _, err := m.Assign(r1, legRoute(), 2, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	return m
}

// (The brief's single 15-term spec condition is split at its line boundaries into
// checkLegIdentity/checkLegFreshState to stay under this repo's cyclop limit of 15: one `if`
// over 15 terms alone scores 16. Every term and the Fatalf message are the brief's, unchanged.)
func TestNewLegIsQueuedWithItsSpec(t *testing.T) {
	m := mustLeg(t)
	checkLegIdentity(t, m)
	checkLegFreshState(t, m)
	if !m.External() || !m.Open() || m.Active() {
		t.Fatal("a queued leg is external, open and not active")
	}
	if m.ActionID(mission.ActionPick) != "leg-1-pick" || m.CancelActionID() != "leg-1-cancel" {
		t.Fatalf("action ids %s %s", m.ActionID(mission.ActionPick), m.CancelActionID())
	}
	m.AssignSeq(7)
	if m.Seq() != 7 {
		t.Fatal("AssignSeq")
	}
}

func checkLegIdentity(t *testing.T, m *mission.Mission) {
	t.Helper()
	if m.ID() != "leg-1" || m.Kind() != mission.Leg || m.Status() != mission.Queued || m.MapVersion() != "v1" ||
		m.ContainerID() != "C1" || m.From() != "ST1" || m.To() != "ASRS1-IN1" || m.PickupNode() != "ST1" {
		t.Fatalf("leg = %+v", m.State())
	}
}

func checkLegFreshState(t *testing.T, m *mission.Mission) {
	t.Helper()
	if m.DropNode() != "ASRS1-IN1" || m.Reference() != "tl-9" || !m.AcceptedAt().Equal(t0) || !m.Robot().IsZero() ||
		len(m.Route()) != 0 || m.Sent() || m.Unacknowledged() {
		t.Fatalf("leg = %+v", m.State())
	}
}

func TestNewLegRefusesIncompleteOrStillLegs(t *testing.T) {
	cases := map[string]func(*mission.LegSpec){
		"no id":          func(s *mission.LegSpec) { s.ID = "" },
		"no map version": func(s *mission.LegSpec) { s.MapVersion = "" },
		"no container":   func(s *mission.LegSpec) { s.ContainerID = "" },
		"no from":        func(s *mission.LegSpec) { s.From = "" },
		"no to":          func(s *mission.LegSpec) { s.To = "" },
		"no pickup node": func(s *mission.LegSpec) { s.PickupNode = "" },
		"no drop node":   func(s *mission.LegSpec) { s.DropNode = "" },
		"no time":        func(s *mission.LegSpec) { s.At = time.Time{} },
		"same handoff":   func(s *mission.LegSpec) { s.To = s.From },
		"same node":      func(s *mission.LegSpec) { s.DropNode = s.PickupNode },
	}
	for name, bad := range cases {
		s := legSpec()
		bad(&s)
		if _, err := mission.NewLeg(s); !errors.Is(err, mission.ErrInvalidMission) {
			t.Errorf("%s: NewLeg = %v", name, err)
		}
	}
}

func TestInternalMissionsStartAssignedAndValidateTheirRoute(t *testing.T) {
	park := []mission.Step{{Node: "ST1"}, {Node: "E", Edge: "E-ST1"}, {Node: "ASRS1-OUT1", Edge: "ASRS1-OUT1-E"}, {Node: "A", Edge: "A-ASRS1-OUT1"}, {Node: "P1", Edge: "P1-A"}}
	m, err := mission.NewInternal(mission.Park, "ms-1", "v1", r1, park, 2, t0)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status() != mission.Assigned || m.External() || !m.Active() || m.BaseEnd() != 2 || m.Robot() != r1 || !m.AssignedAt().Equal(t0) {
		t.Fatalf("park = %+v", m.State())
	}
	charge := []mission.Step{{Node: "CH1", Action: mission.ActionCharge}}
	if _, err := mission.NewInternal(mission.Charge, "ms-2", "v1", r1, charge, 0, t0); err != nil {
		t.Fatalf("charging in place: %v", err)
	}
	bad := []struct {
		name  string
		kind  mission.Kind
		id    string
		r     robot.ID
		route []mission.Step
		base  int
	}{
		{"leg kind", mission.Leg, "ms-3", r1, park, 0},
		{"no id", mission.Park, "", r1, park, 0},
		{"no robot", mission.Park, "ms-3", robot.ID{}, park, 0},
		{"park of one step", mission.Park, "ms-3", r1, park[:1], 0},
		{"park with an action", mission.Park, "ms-3", r1, charge, 0},
		{"charge without the action", mission.Charge, "ms-3", r1, park, 0},
		{"charge action not last", mission.Charge, "ms-3", r1, []mission.Step{{Node: "D", Action: mission.ActionCharge}, {Node: "CH1", Edge: "CH1-D"}}, 0},
		{"base past the end", mission.Park, "ms-3", r1, park, 5},
		{"negative base", mission.Park, "ms-3", r1, park, -1},
	}
	for _, c := range bad {
		if _, err := mission.NewInternal(c.kind, c.id, "v1", c.r, c.route, c.base, t0); !errors.Is(err, mission.ErrInvalidMission) {
			t.Errorf("%s: NewInternal = %v", c.name, err)
		}
	}
	if _, err := mission.NewInternal(mission.Park, "ms-3", "", r1, park, 0, t0); !errors.Is(err, mission.ErrInvalidMission) {
		t.Error("no map version accepted")
	}
	if _, err := mission.NewInternal(mission.Park, "ms-3", "v1", r1, park, 0, time.Time{}); !errors.Is(err, mission.ErrInvalidMission) {
		t.Error("no time accepted")
	}
}

func TestAssignValidatesTheLegRoute(t *testing.T) {
	route := legRoute
	bad := map[string]func() []mission.Step{
		"first step has an edge": func() []mission.Step { r := route(); r[0].Edge = "X"; return r },
		"later step has no edge": func() []mission.Step { r := route(); r[3].Edge = ""; return r },
		"empty node":             func() []mission.Step { r := route(); r[2].Node = ""; return r },
		"standing still":         func() []mission.Step { r := route(); r[2].Node = r[1].Node; return r },
		"no pick":                func() []mission.Step { r := route(); r[4].Action = ""; return r },
		"pick at the wrong node": func() []mission.Step { r := route(); r[4].Action, r[3].Action = "", mission.ActionPick; return r },
		"drop before the pick":   func() []mission.Step { r := route(); r[7].Action, r[2].Action = "", mission.ActionDrop; return r },
		"drop at the wrong node": func() []mission.Step { r := route(); r[7].Action, r[6].Action = "", mission.ActionDrop; return r },
		"two picks":              func() []mission.Step { r := route(); r[1].Action = mission.ActionPick; return r },
		"two drops":              func() []mission.Step { r := route(); r[6].Action = mission.ActionDrop; return r },
		"a charge on a leg":      func() []mission.Step { r := route(); r[6].Action = mission.ActionCharge; return r },
		"an unknown action":      func() []mission.Step { r := route(); r[6].Action = "dance"; return r },
		"one step":               func() []mission.Step { return route()[:1] },
	}
	for name, r := range bad {
		m := mustLeg(t)
		if _, err := m.Assign(r1, r(), 0, t0); !errors.Is(err, mission.ErrInvalidMission) {
			t.Errorf("%s: Assign = %v", name, err)
		}
		if m.Status() != mission.Queued {
			t.Errorf("%s: a refused Assign changed the status to %s", name, m.Status())
		}
	}
	m := mustLeg(t)
	if _, err := m.Assign(robot.ID{}, legRoute(), 0, t0); !errors.Is(err, mission.ErrInvalidMission) {
		t.Errorf("Assign without a robot = %v", err)
	}
	if _, err := m.Assign(r1, legRoute(), 8, t0); !errors.Is(err, mission.ErrInvalidMission) {
		t.Errorf("Assign with the base past the route = %v", err)
	}
}

func TestStateRoundTripsAndRouteIsACopy(t *testing.T) {
	m := assigned(t)
	m.AssignSeq(3)
	m.MarkSent(t0)
	s := m.State()
	if !reflect.DeepEqual(mission.Rehydrate(s).State(), s) {
		t.Fatalf("round trip = %+v", s)
	}
	s.Route[0].Node = "Z"
	m.Route()[1].Node = "Z"
	if m.Route()[0].Node != "P1" || m.Route()[1].Node != "A" {
		t.Fatal("State or Route shares the mission's route")
	}
}
