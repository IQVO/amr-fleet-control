package a2a

import (
	"context"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// carry (plan 04 Task 21 review lesson): a message-only agent creates no Task, so the reply must not carry the
// taskId the SDK mints for every run (a client would GetTask an id no store holds); it keeps the caller's contextId.
func TestTheReplyKeepsTheCallersContextAndNamesNoTask(t *testing.T) {
	client := agent(t, fakeReads{})
	for name, part := range map[string]*a2a.Part{
		"an answer":         skill("get_fleet", map[string]any{"fleetId": "AMR1"}),
		"a problem":         skill("get_fleet", map[string]any{"fleetId": "AMR9"}),
		"a malformed reply": skill("", nil),
	} {
		msg := a2a.NewMessage(a2a.MessageRoleUser, part)
		msg.ContextID = "ctx-9"
		resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
		if err != nil {
			t.Fatalf("%s: SendMessage: %v", name, err)
		}
		reply, ok := resp.(*a2a.Message)
		if !ok {
			t.Fatalf("%s: reply = %T, want a message", name, resp)
		}
		if reply.TaskID != "" || reply.ContextID != "ctx-9" || reply.Role != a2a.MessageRoleAgent {
			t.Errorf("%s: reply taskId %q contextId %q role %v, want no task and the caller's ctx-9", name, reply.TaskID, reply.ContextID, reply.Role)
		}
	}
}
