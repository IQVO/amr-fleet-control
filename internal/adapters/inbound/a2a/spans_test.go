package a2a

import (
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return sr
}

func spanAttrs(s sdktrace.ReadOnlySpan) map[attribute.Key]string {
	out := map[attribute.Key]string{}
	for _, kv := range s.Attributes() {
		out[kv.Key] = kv.Value.String()
	}
	return out
}

func skillSpans(sr *tracetest.SpanRecorder) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if strings.HasPrefix(s.Name(), "a2a.skill") {
			out = append(out, s)
		}
	}
	return out
}

// spanValues is every string a span records: status description, attributes and event attributes.
func spanValues(s sdktrace.ReadOnlySpan) string {
	seen := s.Status().Description
	for _, kv := range s.Attributes() {
		seen += " " + kv.Value.String()
	}
	for _, ev := range s.Events() {
		for _, kv := range ev.Attributes {
			seen += " " + kv.Value.String()
		}
	}
	return seen
}

// carry (plan 04 Task 20/21 review lessons, MCP parity): every skill run is one "a2a.skill <id>" span carrying
// gen_ai.operation.name=execute_tool, the tool name and the authority L1; a problem answer records the error and
// marks the span Error with the problem's SLUG only (the detail echoes the caller's id).
func TestEverySkillRunIsOneSpanWithTheAuthority(t *testing.T) {
	sr := recordSpans(t)
	client := agent(t, fakeReads{})
	send(t, client, skill("get_leg", map[string]any{"fleetId": "AMR1", "legId": "leg-1"}))
	send(t, client, skill("get_leg", map[string]any{"fleetId": "AMR1", "legId": "leg-raw-id-9"}))
	spans := skillSpans(sr)
	if len(spans) != 2 || spans[0].Name() != "a2a.skill get_leg" || spans[1].Name() != "a2a.skill get_leg" {
		t.Fatalf("skill spans = %v, want two a2a.skill get_leg", spanNames(spans))
	}
	assertSkillAttributes(t, spans, "get_leg")
	if spans[0].Status().Code != codes.Unset || len(spans[0].Events()) != 0 {
		t.Errorf("the answered run = %+v %v", spans[0].Status(), spans[0].Events())
	}
	if spans[1].Status().Code != codes.Error || spans[1].Status().Description != "leg-not-found" ||
		len(spans[1].Events()) != 1 || spans[1].Events()[0].Name != "exception" || strings.Contains(spanValues(spans[1]), "leg-raw-id-9") {
		t.Errorf("the problem run = %+v %v (values %q), want Error leg-not-found without the raw id", spans[1].Status(), spans[1].Events(), spanValues(spans[1]))
	}
}

// carry (plan 04 Task 21 review lesson): an id the card does not offer is ONE span named "a2a.skill unknown", and
// the caller-chosen id appears nowhere on it (name, attributes, status, events).
func TestAnUnknownSkillIsOneLowCardinalitySpan(t *testing.T) {
	sr := recordSpans(t)
	raw := strings.Repeat("x", 200) + "-attacker-chosen"
	if env, _ := send(t, agent(t, fakeReads{}), skill(raw, map[string]any{"fleetId": "AMR1"})); problemType(env) != "unknown-skill" {
		t.Fatalf("envelope = %v", env)
	}
	spans := skillSpans(sr)
	if len(spans) != 1 || spans[0].Name() != "a2a.skill unknown" {
		t.Fatalf("skill spans = %v, want exactly one named a2a.skill unknown", spanNames(spans))
	}
	s := spans[0]
	if a := spanAttrs(s); a["a2a.skill"] != "unknown" || a["gen_ai.tool.name"] != "unknown" || a["warehouse.authority"] != "L1" {
		t.Errorf("attributes = %v", a)
	}
	if seen := spanValues(s); strings.Contains(seen, "attacker-chosen") || s.Status().Code != codes.Error || s.Status().Description != "unknown-skill" {
		t.Errorf("the span records the raw id or no error: status %+v, values %q", s.Status(), seen)
	}
}

// A malformed request names no skill: it opens no skill span.
func TestAMalformedRequestOpensNoSkillSpan(t *testing.T) {
	sr := recordSpans(t)
	if env, _ := send(t, agent(t, fakeReads{}), skill("", nil)); problemType(env) != "malformed-skill-request" {
		t.Fatalf("envelope = %v", env)
	}
	if spans := skillSpans(sr); len(spans) != 0 {
		t.Fatalf("skill spans = %v, want none", spanNames(spans))
	}
}

func assertSkillAttributes(t *testing.T, spans []sdktrace.ReadOnlySpan, id string) {
	t.Helper()
	for _, s := range spans {
		a := spanAttrs(s)
		if a["gen_ai.operation.name"] != "execute_tool" || a["gen_ai.tool.name"] != id || a["a2a.skill"] != id ||
			a["warehouse.authority"] != "L1" {
			t.Errorf("attributes = %v", a)
		}
	}
}

func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	out := make([]string, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.Name())
	}
	return out
}
