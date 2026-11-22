package mcp_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/mcp"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

type fakeReads struct{}

func (fakeReads) Fleet(_ context.Context, fleetID string) (usecases.FleetView, error) {
	if fleetID != "AMR1" {
		return usecases.FleetView{}, usecases.ErrFleetNotFound
	}
	return usecases.FleetView{FleetID: "AMR1", MapID: "S1", MapVersion: "v1", HandoffPoints: []string{"ST1"},
		Robots:   []usecases.RobotView{{ID: robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}, State: "IDLE", Connection: "ONLINE", Battery: 90, Available: true, LastSeenAt: t0}},
		Capacity: capacity.Snapshot{RobotsTotal: 1, RobotsOnline: 1, RobotsAvailable: 1, At: t0}}, nil
}

func (fakeReads) Leg(_ context.Context, _, legID string) (usecases.LegView, error) {
	if legID != "leg-1" {
		return usecases.LegView{}, usecases.ErrLegNotFound
	}
	return usecases.LegView{LegID: "leg-1", FleetID: "AMR1", ContainerID: "C1", From: "ST1", To: "ASRS1-IN1", Status: "FAILED",
		Reason: "ROBOT_BLOCKED", RobotError: "ROBOT_BLOCKED", Robot: robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}, MapVersion: "v1", AcceptedAt: t0, EndedAt: t0}, nil
}

func (fakeReads) OpenLegs(context.Context, string) ([]usecases.LegView, error) {
	return []usecases.LegView{}, nil
}

func (fakeReads) Robot(_ context.Context, _ string, id robot.ID) (usecases.RobotView, error) {
	if id.SerialNumber != "R1" {
		return usecases.RobotView{}, usecases.ErrRobotNotFound
	}
	return usecases.RobotView{ID: id, State: "HELD", Connection: "ONLINE", ErrorTypes: []string{"ROBOT_BLOCKED"}, LastSeenAt: t0}, nil
}

func session(t *testing.T) *sdk.ClientSession {
	t.Helper()
	srv := httptest.NewServer(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{Reads: fakeReads{}, FleetID: "AMR1"})))
	t.Cleanup(srv.Close)
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
	s, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func call(t *testing.T, s *sdk.ClientSession, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	var out map[string]any
	_ = json.Unmarshal([]byte(text), &out)
	if res.IsError {
		return map[string]any{"error": text}, true
	}
	return out, false
}

// deviation (Task 18 D2, cyclop 17 > 15): the brief's single test is split into the reads and the
// refusals (a t.Helper function); every assertion line is byte-identical to the brief.
func TestEveryToolReadsTheUseCases(t *testing.T) {
	s := session(t)
	assertEveryToolReads(t, s)
	assertTheRefusalSlugs(t, s)
}

func assertEveryToolReads(t *testing.T, s *sdk.ClientSession) {
	t.Helper()
	f, isErr := call(t, s, "get_fleet", map[string]any{"fleetId": "AMR1"})
	if isErr || f["fleetId"] != "AMR1" || f["mapVersion"] != "v1" || f["capacity"].(map[string]any)["robotsAvailable"] != 1.0 {
		t.Fatalf("get_fleet = %v", f)
	}
	l, isErr := call(t, s, "get_leg", map[string]any{"fleetId": "AMR1", "legId": "leg-1"})
	if isErr || l["status"] != "FAILED" || l["reason"] != "ROBOT_BLOCKED" || l["robot"] != "EMU/R1" {
		t.Fatalf("get_leg = %v", l)
	}
	r, isErr := call(t, s, "get_robot", map[string]any{"fleetId": "AMR1", "manufacturer": "EMU", "serialNumber": "R1"})
	if isErr || r["state"] != "HELD" || r["errorTypes"].([]any)[0] != "ROBOT_BLOCKED" {
		t.Fatalf("get_robot = %v", r)
	}
	o, isErr := call(t, s, "list_open_legs", map[string]any{"fleetId": "AMR1"})
	if isErr || len(o["legs"].([]any)) != 0 {
		t.Fatalf("list_open_legs = %v", o)
	}
}

func assertTheRefusalSlugs(t *testing.T, s *sdk.ClientSession) {
	t.Helper()
	for _, c := range []struct {
		tool string
		args map[string]any
		slug string
	}{
		{"get_fleet", map[string]any{"fleetId": "AMR9"}, "fleet-not-found"},
		{"get_leg", map[string]any{"fleetId": "AMR1", "legId": "nope"}, "leg-not-found"},
		{"get_robot", map[string]any{"fleetId": "AMR1", "manufacturer": "EMU", "serialNumber": "R9"}, "robot-not-found"},
		{"get_robot", map[string]any{"fleetId": "AMR1", "manufacturer": "", "serialNumber": "R1"}, "validation-failed"},
	} {
		if e, isErr := call(t, s, c.tool, c.args); !isErr || !strings.HasPrefix(e["error"].(string), c.slug+":") {
			t.Fatalf("%s %v = %v", c.tool, c.args, e)
		}
	}
}
