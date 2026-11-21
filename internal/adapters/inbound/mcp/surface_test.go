package mcp_test

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The agentic standard (AS-1..AS-9): read-only, authority L1 on every tool, one resource, one prompt.
//
// deviation (Task 18 D2, cyclop 22 > 15): the brief's single test is split into three t.Helper
// functions; every assertion line is byte-identical to the brief.
func TestTheSurfaceIsReadOnlyAtAuthorityL1(t *testing.T) {
	s := session(t)
	assertReadOnlyL1Tools(t, s)
	assertTheFleetResource(t, s)
	assertTheReviewLegPrompt(t, s)
}

func assertReadOnlyL1Tools(t *testing.T, s *sdk.ClientSession) {
	t.Helper()
	tools, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"get_fleet": true, "list_open_legs": true, "get_leg": true, "get_robot": true}
	if len(tools.Tools) != len(want) {
		t.Fatalf("tools = %d, want %d", len(tools.Tools), len(want))
	}
	for _, tool := range tools.Tools {
		a := tool.Annotations
		if !want[tool.Name] || a == nil || !a.ReadOnlyHint || !a.IdempotentHint || a.OpenWorldHint == nil || *a.OpenWorldHint ||
			tool.Meta["com.warehouse/authority"] != "L1" || len(tool.Description) < 80 {
			t.Errorf("tool %s = %+v meta %v", tool.Name, a, tool.Meta)
		}
	}
}

func assertTheFleetResource(t *testing.T, s *sdk.ClientSession) {
	t.Helper()
	rt, err := s.ListResourceTemplates(context.Background(), nil)
	if err != nil || len(rt.ResourceTemplates) != 1 || rt.ResourceTemplates[0].URITemplate != "warehouse://amr-fleet-control/fleets/{fleetId}" {
		t.Fatalf("resource templates = %+v %v", rt, err)
	}
	res, err := s.ReadResource(context.Background(), &sdk.ReadResourceParams{URI: "warehouse://amr-fleet-control/fleets/AMR1"})
	if err != nil || !strings.Contains(res.Contents[0].Text, `"fleetId":"AMR1"`) {
		t.Fatalf("read resource = %+v %v", res, err)
	}
}

func assertTheReviewLegPrompt(t *testing.T, s *sdk.ClientSession) {
	t.Helper()
	ps, err := s.ListPrompts(context.Background(), nil)
	if err != nil || len(ps.Prompts) != 1 || ps.Prompts[0].Name != "review_leg" {
		t.Fatalf("prompts = %+v %v", ps, err)
	}
	p, err := s.GetPrompt(context.Background(), &sdk.GetPromptParams{Name: "review_leg", Arguments: map[string]string{"legId": "leg-7"}})
	if err != nil || !strings.Contains(p.Messages[0].Content.(*sdk.TextContent).Text, "get_leg(fleetId = AMR1, legId = leg-7)") {
		t.Fatalf("prompt = %+v %v", p, err)
	}
}
