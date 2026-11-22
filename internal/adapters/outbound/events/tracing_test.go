package events

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
)

// Ported from material-flow-control (plan 04, reviewed) internal/adapters/outbound/events/tracing_test.go,
// adapted to this sink's constructor (fleet id) and the FleetCapacityChanged fact.
func traceHeader(t *testing.T, ctx context.Context) []string {
	t.Helper()
	out := &rec{}
	if err := NewSink(out, func() string { return "id" }, "AMR1").Append(ctx, capacity.FleetCapacityChanged{Snapshot: capacity.Snapshot{RobotsTotal: 1, At: at}}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range out.msgs {
		for _, h := range m.Headers {
			if h.Key == "traceparent" {
				got = append(got, h.Value)
			}
		}
	}
	return got
}

// The outbox rows carry the W3C trace context of the unit of work that wrote them, so a leg
// request's trace continues into Kafka (ADR 0005 Consequences); a unit without a span (a robot
// message, a tick) stores none.
func TestTheOutboxRowsCarryTheTraceContextOfTheirUnitOfWork(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })
	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))
	got := traceHeader(t, ctx)
	want := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	if len(got) != 2 || got[0] != want || got[1] != want {
		t.Fatalf("traceparent = %v, want %s on both rows (events + analytics)", got, want)
	}
	if got := traceHeader(t, context.Background()); len(got) != 0 {
		t.Fatalf("a unit of work without a span stored traceparent %v", got)
	}
}
