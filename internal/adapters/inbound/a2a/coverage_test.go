package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// busyReads is a fleet with one robot working one open leg.
type busyReads struct{ fakeReads }

var r1 = robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}

func (busyReads) robot() usecases.RobotView {
	return usecases.RobotView{ID: r1, Connection: "ONLINE", State: "EXECUTE", Battery: 81.6, Node: "P1", MissionID: "leg-1",
		ErrorTypes: []string{"X"}, LastSeenAt: at0}
}

func (b busyReads) Fleet(context.Context, string) (usecases.FleetView, error) {
	return usecases.FleetView{FleetID: "AMR1", MapID: "S1", MapVersion: "v1", Robots: []usecases.RobotView{b.robot()},
		Capacity: capacity.Snapshot{RobotsTotal: 1, RobotsOnline: 1, LegsActive: 1}}, nil
}

func (b busyReads) OpenLegs(ctx context.Context, fleetID string) ([]usecases.LegView, error) {
	l, err := b.Leg(ctx, fleetID, "leg-1")
	return []usecases.LegView{l}, err
}

func (b busyReads) Robot(context.Context, string, robot.ID) (usecases.RobotView, error) {
	return b.robot(), nil
}

// brokenReads fails every read with an unexpected error.
type brokenReads struct{}

var errBroken = errors.New("dial tcp 10.0.0.7:5432: broken")

func (brokenReads) Fleet(context.Context, string) (usecases.FleetView, error) {
	return usecases.FleetView{}, errBroken
}

func (brokenReads) Leg(context.Context, string, string) (usecases.LegView, error) {
	return usecases.LegView{}, errBroken
}

func (brokenReads) OpenLegs(context.Context, string) ([]usecases.LegView, error) {
	return nil, errBroken
}

func (brokenReads) Robot(context.Context, string, robot.ID) (usecases.RobotView, error) {
	return usecases.RobotView{}, errBroken
}

func TestEverySkillAnswersItsDataAndSummary(t *testing.T) {
	client := agent(t, busyReads{})
	env, text := send(t, client, skill("get_fleet", map[string]any{"fleetId": "AMR1"}))
	f, _ := env["data"].(map[string]any)["fleet"].(map[string]any)
	robots, _ := f["robots"].([]any)
	if len(robots) != 1 || robots[0].(map[string]any)["robot"] != "EMU/R1" || text != "Fleet AMR1 on map S1 version v1: 1 robots, 0 available, 0 legs queued." {
		t.Errorf("get_fleet = %v %q", f, text)
	}
	env, text = send(t, client, skill("list_open_legs", map[string]any{"fleetId": "AMR1"}))
	legs, _ := env["data"].(map[string]any)["legs"].([]any)
	if len(legs) != 1 || legs[0].(map[string]any)["robot"] != "EMU/R1" || text != "1 open legs." {
		t.Errorf("list_open_legs = %v %q", env, text)
	}
	env, text = send(t, client, skill("get_robot", map[string]any{"fleetId": "AMR1", "manufacturer": "EMU", "serialNumber": "R1"}))
	r, _ := env["data"].(map[string]any)["robot"].(map[string]any)
	if r["robot"] != "EMU/R1" || r["state"] != "EXECUTE" || r["batteryPercent"] != 81.6 || r["nodeId"] != "P1" ||
		r["lastSeenAt"] != "2026-11-20T08:00:00Z" || text != "Robot EMU/R1 is EXECUTE at 82%." {
		t.Errorf("get_robot = %v %q", r, text)
	}
	if _, text = send(t, client, skill("get_leg", map[string]any{"fleetId": "AMR1", "legId": "leg-1"})); text != "Leg leg-1 (C1, ST1 to ST2) is ASSIGNED." {
		t.Errorf("get_leg summary = %q", text)
	}
}

func TestMissingArgumentsAreInvalid(t *testing.T) {
	client := agent(t, busyReads{})
	for name, part := range map[string]*a2a.Part{
		"no fleetId":         skill("get_fleet", map[string]any{}),
		"no legId":           skill("get_leg", map[string]any{"fleetId": "AMR1"}),
		"no serialNumber":    skill("get_robot", map[string]any{"fleetId": "AMR1", "manufacturer": "EMU"}),
		"arguments an array": a2a.NewDataPart(map[string]any{"skill": "get_fleet", "arguments": []any{"AMR1"}}),
	} {
		if env, _ := send(t, client, part); problemType(env) != "invalid-argument" || env["skill"] == "" {
			t.Errorf("%s: envelope %v, want invalid-argument naming the skill", name, env)
		}
	}
}

func TestAnUnexpectedReadErrorIsAnInternalErrorOnEverySkill(t *testing.T) {
	client := agent(t, brokenReads{})
	for _, part := range []*a2a.Part{
		skill("get_fleet", map[string]any{"fleetId": "AMR1"}), skill("list_open_legs", map[string]any{"fleetId": "AMR1"}),
		skill("get_leg", map[string]any{"fleetId": "AMR1", "legId": "leg-1"}),
		skill("get_robot", map[string]any{"fleetId": "AMR1", "manufacturer": "EMU", "serialNumber": "R1"}),
	} {
		env, text := send(t, client, part)
		raw, _ := json.Marshal(env)
		if problemType(env) != "internal-error" || strings.Contains(string(raw)+text, "5432") {
			t.Errorf("envelope %s %q, want internal-error without the cause", raw, text)
		}
	}
}

// A message without parts, or whose first part is neither data nor text, is a malformed request.
func TestAMessageWithoutASkillPartIsMalformed(t *testing.T) {
	exec := NewExecutor(Deps{Reads: fakeReads{}})
	for name, msg := range map[string]*a2a.Message{
		"no message": nil,
		"no parts":   a2a.NewMessage(a2a.MessageRoleUser),
		"a raw part": a2a.NewMessage(a2a.MessageRoleUser, a2a.NewRawPart([]byte("x"))),
	} {
		for ev, err := range exec.Execute(context.Background(), &a2asrv.ExecutorContext{Message: msg}) {
			m, ok := ev.(*a2a.Message)
			if err != nil || !ok {
				t.Fatalf("%s: event %T %v", name, ev, err)
			}
			raw, _ := json.Marshal(m.Parts[0].Content.(a2a.Data).Value)
			var env map[string]any
			_ = json.Unmarshal(raw, &env)
			if problemType(env) != "malformed-skill-request" {
				t.Errorf("%s: envelope %v", name, env)
			}
		}
	}
}

func TestAZeroTimeIsNull(t *testing.T) {
	if v := robotDTO(usecases.RobotView{ID: r1})["lastSeenAt"]; v != nil || stamp(at0) != "2026-11-20T08:00:00Z" {
		t.Fatalf("a zero lastSeenAt = %v, want null", v)
	}
}
