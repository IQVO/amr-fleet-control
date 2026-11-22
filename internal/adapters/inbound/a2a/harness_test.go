package a2a

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// carry (plan 04 Task 21 lessons): the in-package harness drives the REAL JSON-RPC wire (card -> SDK client ->
// handler -> executor -> reads), like material-flow-control 622190f internal/adapters/inbound/a2a/skills_test.go.

var at0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

// fakeReads mirrors the brief's skills_test.go double (package a2a_test).
type fakeReads struct{}

func (fakeReads) Fleet(_ context.Context, id string) (usecases.FleetView, error) {
	if id != "AMR1" {
		return usecases.FleetView{}, usecases.ErrFleetNotFound
	}
	return usecases.FleetView{FleetID: "AMR1", MapID: "S1", MapVersion: "v1", HandoffPoints: []string{"ST1"}}, nil
}

func (fakeReads) Leg(_ context.Context, _, id string) (usecases.LegView, error) {
	if id != "leg-1" {
		return usecases.LegView{}, usecases.ErrLegNotFound
	}
	return usecases.LegView{LegID: "leg-1", FleetID: "AMR1", ContainerID: "C1", From: "ST1", To: "ST2", Status: "ASSIGNED",
		Robot: robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}, MapVersion: "v1", AcceptedAt: at0, AssignedAt: at0}, nil
}

func (fakeReads) OpenLegs(context.Context, string) ([]usecases.LegView, error) { return nil, nil }

func (fakeReads) Robot(context.Context, string, robot.ID) (usecases.RobotView, error) {
	return usecases.RobotView{}, usecases.ErrRobotNotFound
}

func agent(t *testing.T, reads Reads) *a2aclient.Client {
	t.Helper()
	srv := httptest.NewServer(Handler(NewExecutor(Deps{Reads: reads})))
	t.Cleanup(srv.Close)
	client, err := a2aclient.NewFromCard(context.Background(), Card(Config{AgentURL: srv.URL, Version: "test"}))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// send delivers part and returns the envelope JSON and the text summary of the one reply.
func send(t *testing.T, client *a2aclient.Client, part *a2a.Part) (map[string]any, string) {
	t.Helper()
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) != 2 {
		t.Fatalf("reply = %T %+v, want one message with a data and a text part", resp, resp)
	}
	raw, _ := json.Marshal(msg.Parts[0].Content.(a2a.Data).Value)
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	return env, string(msg.Parts[1].Content.(a2a.Text))
}

func skill(name string, args map[string]any) *a2a.Part {
	return a2a.NewDataPart(map[string]any{"skill": name, "arguments": args})
}

func problemType(env map[string]any) string {
	p, _ := env["problem"].(map[string]any)
	s, _ := p["type"].(string)
	return s
}
