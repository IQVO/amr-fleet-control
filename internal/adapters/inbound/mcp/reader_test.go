package mcp_test

import (
	"context"
	"strings"
	"testing"

	inboundmcp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/mcp"
	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/memory"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	mapfile "github.com/IQVO/amr-fleet-control/internal/config"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// realReader is usecases.NewReader (the Reads cmd/mcp wires) over the memory repositories, holding one
// robot EMU/R1 at P1 and one QUEUED leg ST1 -> ASRS1-IN1 of container C1.
func realReader(t *testing.T) inboundmcp.Reads {
	t.Helper()
	m, err := mapfile.LoadMap("../../../../config/map.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	store := memory.NewStore()
	r, _ := robot.New(robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}, t0)
	r.ApplyReport(robot.Report{At: t0, LastNodeID: "P1", MapID: m.MapID(), Battery: 80}, "P1", t0)
	if err := memory.NewRobots(store).Save(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	leg, err := mission.NewLeg(mission.LegSpec{ID: "leg-1", MapVersion: m.Version(), ContainerID: "C1", From: "ST1", To: "ASRS1-IN1",
		PickupNode: "ST1", DropNode: "ASRS1-IN1", At: t0})
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.NewMissions(store).Insert(context.Background(), leg); err != nil {
		t.Fatal(err)
	}
	reads, err := usecases.NewReader(m, usecases.DefaultConfig(), memory.NewRobots(store), memory.NewMissions(store), memory.NewClock(t0))
	if err != nil {
		t.Fatal(err)
	}
	return reads
}

// The tools over the real Reader: what is stored reads back, and an unknown id on EVERY tool (the list
// tool included) is its not-found slug, never an empty answer.
func TestTheToolsReadThroughTheRealReader(t *testing.T) {
	s := sessionWith(t, realReader(t))
	o, isErr := call(t, s, "list_open_legs", map[string]any{"fleetId": "AMR1"})
	if legs, _ := o["legs"].([]any); isErr || len(legs) != 1 || legs[0].(map[string]any)["legId"] != "leg-1" ||
		legs[0].(map[string]any)["status"] != "QUEUED" {
		t.Fatalf("list_open_legs = %v", o)
	}
	f, isErr := call(t, s, "get_fleet", map[string]any{"fleetId": "AMR1"})
	if robots, _ := f["robots"].([]any); isErr || f["mapVersion"] != "2026-11-22.1" || len(robots) != 1 ||
		f["capacity"].(map[string]any)["legsQueued"] != 1.0 {
		t.Fatalf("get_fleet = %v", f)
	}
	for _, c := range []struct {
		tool string
		args map[string]any
		slug string
	}{
		{"list_open_legs", map[string]any{"fleetId": "AMR9"}, "fleet-not-found"},
		{"get_fleet", map[string]any{"fleetId": ""}, "fleet-not-found"},
		{"get_leg", map[string]any{"fleetId": "AMR9", "legId": "leg-1"}, "fleet-not-found"},
		{"get_leg", map[string]any{"fleetId": "AMR1", "legId": "leg-9"}, "leg-not-found"},
		{"get_robot", map[string]any{"fleetId": "AMR9", "manufacturer": "EMU", "serialNumber": "R1"}, "fleet-not-found"},
		{"get_robot", map[string]any{"fleetId": "AMR1", "manufacturer": "EMU", "serialNumber": "R9"}, "robot-not-found"},
	} {
		if e, isErr := call(t, s, c.tool, c.args); !isErr || !strings.HasPrefix(e["error"].(string), c.slug+":") {
			t.Errorf("%s %v = %v, want %s", c.tool, c.args, e, c.slug)
		}
	}
}

// ruling P3-R13: fleet-not-found and robot-not-found are RETRYABLE (a map deploy, a robot's first report),
// so a description names each not-found slug its inputs can meet and never calls one final.
func TestToolDescriptionsNameTheirNotFoundSlugsWithoutFinality(t *testing.T) {
	tools, err := session(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"get_fleet":      {"fleet-not-found"},
		"list_open_legs": {"fleet-not-found"},
		"get_leg":        {"fleet-not-found", "leg-not-found"},
		"get_robot":      {"fleet-not-found", "robot-not-found"},
	}
	for _, tool := range tools.Tools {
		for _, slug := range want[tool.Name] {
			if !strings.Contains(tool.Description, slug) {
				t.Errorf("%s does not name %s: %q", tool.Name, slug, tool.Description)
			}
		}
		for _, word := range []string{"final", "permanent", "never exist", "do not retry"} {
			if strings.Contains(strings.ToLower(tool.Description), word) {
				t.Errorf("%s claims finality (%q): %q", tool.Name, word, tool.Description)
			}
		}
	}
}
