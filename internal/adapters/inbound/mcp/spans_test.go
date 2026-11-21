package mcp_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	inboundmcp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/mcp"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
)

// recordSpans installs a span recorder as the global tracer provider for one test.
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

func spansNamed(sr *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if s.Name() == name {
			out = append(out, s)
		}
	}
	return out
}

// brokenReads fails the fleet read the way an infrastructure error does: its text holds a DSN.
type brokenReads struct{ fakeReads }

const leakyDSN = "postgres://amr:secret@db.internal:5432/amr"

func (brokenReads) Fleet(context.Context, string) (usecases.FleetView, error) {
	return usecases.FleetView{}, errors.New("dial " + leakyDSN + ": SELECT snapshot FROM robots: connection refused")
}

func sessionWith(t *testing.T, reads inboundmcp.Reads) *sdk.ClientSession {
	t.Helper()
	srv := httptest.NewServer(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{Reads: reads, FleetID: "AMR1"})))
	t.Cleanup(srv.Close)
	s, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).
		Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// carry (plan 04 Task 20/21 review lessons): every tool call is ONE "mcp.tool <name>" span carrying
// gen_ai.operation.name=execute_tool, the tool name and its declared authority; a failed call records
// the MAPPED tool error and marks the span Error.
func TestEveryToolCallIsOneSpanWithTheAuthority(t *testing.T) {
	sr := recordSpans(t)
	s := session(t)
	calls := map[string]map[string]any{
		"get_fleet":      {"fleetId": "AMR1"},
		"list_open_legs": {"fleetId": "AMR1"},
		"get_leg":        {"fleetId": "AMR1", "legId": "leg-1"},
		"get_robot":      {"fleetId": "AMR1", "manufacturer": "EMU", "serialNumber": "R1"},
	}
	for name, args := range calls {
		if _, isErr := call(t, s, name, args); isErr {
			t.Fatalf("%s failed", name)
		}
		spans := spansNamed(sr, "mcp.tool "+name)
		if len(spans) != 1 {
			t.Fatalf("%s: spans = %d, want 1", name, len(spans))
		}
		a := spanAttrs(spans[0])
		if a["gen_ai.operation.name"] != "execute_tool" || a["gen_ai.tool.name"] != name || a["warehouse.authority"] != "L1" {
			t.Errorf("%s: attributes = %v", name, a)
		}
		if spans[0].Status().Code != codes.Unset || len(spans[0].Events()) != 0 {
			t.Errorf("%s: the successful call = %+v %v", name, spans[0].Status(), spans[0].Events())
		}
	}
	call(t, s, "get_robot", map[string]any{"fleetId": "AMR1", "manufacturer": "EMU", "serialNumber": "R9"})
	robots := spansNamed(sr, "mcp.tool get_robot")
	if len(robots) != 2 {
		t.Fatalf("get_robot spans = %d, want 2", len(robots))
	}
	failed := robots[1]
	if failed.Status().Code != codes.Error || failed.Status().Description != "robot-not-found" ||
		len(failed.Events()) != 1 || failed.Events()[0].Name != "exception" {
		t.Errorf("the failed call = %+v %v", failed.Status(), failed.Events())
	}
}

// An unexpected error reaches neither the model nor the trace: the span holds the generic internal-error.
func TestAnInfrastructureErrorLeavesNoDSNInTheSpan(t *testing.T) {
	sr := recordSpans(t)
	e, isErr := call(t, sessionWith(t, brokenReads{}), "get_fleet", map[string]any{"fleetId": "AMR1"})
	if !isErr || e["error"] != "internal-error: an unexpected internal error occurred" {
		t.Fatalf("get_fleet = %v", e)
	}
	spans := spansNamed(sr, "mcp.tool get_fleet")
	if len(spans) != 1 || spans[0].Status().Description != "internal-error" {
		t.Fatalf("spans = %v", spans)
	}
	for _, ev := range spans[0].Events() {
		for _, kv := range ev.Attributes {
			if strings.Contains(kv.Value.String(), "postgres://") || strings.Contains(kv.Value.String(), "SELECT") {
				t.Errorf("the span event %s leaks %q", ev.Name, kv.Value.String())
			}
		}
	}
}

// echoingReads answers a not-found the way the real Reader does: the error text echoes the raw model input.
type echoingReads struct{ fakeReads }

func (echoingReads) Leg(_ context.Context, _, legID string) (usecases.LegView, error) {
	return usecases.LegView{}, fmt.Errorf("%w: %s", usecases.ErrLegNotFound, legID)
}

// carry (Task 18 review M3): the span carries the slug only. The raw model input a not-found echoes
// ("leg-not-found: leg not found: <legId>", unbounded) reaches the model's tool result, never the trace.
func TestAFailedCallSpanCarriesTheSlugNotTheRawId(t *testing.T) {
	sr := recordSpans(t)
	raw := "leg-" + strings.Repeat("x", 64)
	e, isErr := call(t, sessionWith(t, echoingReads{}), "get_leg", map[string]any{"fleetId": "AMR1", "legId": raw})
	if msg, _ := e["error"].(string); !isErr || !strings.HasPrefix(msg, "leg-not-found: ") || !strings.Contains(msg, raw) {
		t.Fatalf("get_leg = %v, want the leg-not-found tool error naming the id", e)
	}
	spans := spansNamed(sr, "mcp.tool get_leg")
	if len(spans) != 1 || spans[0].Status().Code != codes.Error || spans[0].Status().Description != "leg-not-found" {
		t.Fatalf("spans = %+v", spans)
	}
	for _, ev := range spans[0].Events() {
		for _, kv := range ev.Attributes {
			if strings.Contains(kv.Value.String(), raw) {
				t.Errorf("the span event %s carries the raw id: %s=%q", ev.Name, kv.Key, kv.Value.String())
			}
		}
	}
}
