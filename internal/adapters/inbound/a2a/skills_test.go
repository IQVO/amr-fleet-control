package a2a_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	inbounda2a "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/a2a"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

type reads struct{}

func (reads) Fleet(_ context.Context, id string) (usecases.FleetView, error) {
	if id != "AMR1" {
		return usecases.FleetView{}, usecases.ErrFleetNotFound
	}
	return usecases.FleetView{FleetID: "AMR1", MapID: "S1", MapVersion: "v1", HandoffPoints: []string{"ST1"}}, nil
}

func (reads) Leg(_ context.Context, _, id string) (usecases.LegView, error) {
	if id != "leg-1" {
		return usecases.LegView{}, usecases.ErrLegNotFound
	}
	return usecases.LegView{LegID: "leg-1", FleetID: "AMR1", ContainerID: "C1", From: "ST1", To: "ST2", Status: "ASSIGNED",
		Robot: robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}, MapVersion: "v1", AcceptedAt: t0, AssignedAt: t0}, nil
}

func (reads) OpenLegs(context.Context, string) ([]usecases.LegView, error) {
	return nil, nil
}

func (reads) Robot(_ context.Context, _ string, id robot.ID) (usecases.RobotView, error) {
	return usecases.RobotView{}, usecases.ErrRobotNotFound
}

func ask(t *testing.T, payload string) map[string]any {
	t.Helper()
	exec := inbounda2a.NewExecutor(inbounda2a.Deps{Reads: reads{}})
	var raw any
	_ = json.Unmarshal([]byte(payload), &raw)
	msg := sdk.NewMessage(sdk.MessageRoleUser, sdk.NewDataPart(raw))
	ec := &a2asrv.ExecutorContext{Message: msg}
	var out map[string]any
	for ev, err := range exec.Execute(context.Background(), ec) {
		if err != nil {
			t.Fatal(err)
		}
		m := ev.(*sdk.Message)
		b, _ := json.Marshal(m.Parts[0].Content.(sdk.Data).Value)
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func TestSkillsAnswerInTheEnvelope(t *testing.T) {
	got := ask(t, `{"skill":"get_leg","arguments":{"fleetId":"AMR1","legId":"leg-1"}}`)
	leg := got["data"].(map[string]any)["leg"].(map[string]any)
	if got["skill"] != "get_leg" || leg["status"] != "ASSIGNED" || leg["robot"] != "EMU/R1" {
		t.Fatalf("get_leg = %v", got)
	}
	if f := ask(t, `{"skill":"get_fleet","arguments":{"fleetId":"AMR1"}}`); f["data"].(map[string]any)["fleet"].(map[string]any)["mapVersion"] != "v1" {
		t.Fatalf("get_fleet = %v", f)
	}
	for payload, want := range map[string]string{
		`{"skill":"get_leg","arguments":{"fleetId":"AMR1","legId":"nope"}}`:                             "not-found",
		`{"skill":"get_fleet","arguments":{"fleetId":"AMR9"}}`:                                          "not-found",
		`{"skill":"get_robot","arguments":{"fleetId":"AMR1","manufacturer":"EMU","serialNumber":"R9"}}`: "not-found",
		`{"skill":"get_robot","arguments":{"fleetId":"AMR1"}}`:                                          "invalid-argument",
		`{"skill":"get_leg","arguments":{"fleetId":"AMR1","legId":"leg-1","x":1}}`:                      "invalid-argument",
		`{"skill":"request_leg","arguments":{}}`:                                                        "unknown-skill",
		`{"arguments":{}}`:                                                                              "malformed-skill-request",
	} {
		p, ok := ask(t, payload)["problem"].(map[string]any)
		if !ok || p["type"] != want {
			t.Errorf("%s = %v, want %s", payload, p, want)
		}
	}
}

func TestTheCardIsReadOnlyAtL1(t *testing.T) {
	card := inbounda2a.Card(inbounda2a.Config{AgentURL: "http://amr-fleet-control-a2a:8093/a2a", Version: "0.1.0"})
	if card.Name != "amr-fleet-control" || card.Capabilities.Streaming || len(card.SupportedInterfaces) != 1 ||
		card.SupportedInterfaces[0].URL != "http://amr-fleet-control-a2a:8093/a2a" {
		t.Fatalf("card = %+v", card)
	}
	ids := map[string]bool{}
	for _, s := range card.Skills {
		ids[s.ID] = true
		if !strings.Contains(strings.Join(s.Tags, ","), "authority:L1") || len(s.Examples) == 0 {
			t.Errorf("skill %s = %+v", s.ID, s)
		}
	}
	if len(ids) != 4 || !ids["get_fleet"] || !ids["list_open_legs"] || !ids["get_leg"] || !ids["get_robot"] {
		t.Fatalf("skills = %v", ids)
	}
}
