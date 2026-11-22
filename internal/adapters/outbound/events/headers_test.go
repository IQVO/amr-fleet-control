package events

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/IQVO/amr-fleet-control/internal/adapters/kafka/cloudevents"
	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
)

// carry (Task 15 review): with the propagator on, every row keeps its CloudEvents content-type header
// and gains exactly the traceparent after it: the headers are [content-type, traceparent], never replaced.
func TestTheTraceHeadersKeepTheContentType(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })
	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))
	out := &rec{}
	if err := NewSink(out, func() string { return "id" }, "AMR1").Append(ctx, capacity.FleetCapacityChanged{Snapshot: capacity.Snapshot{RobotsTotal: 1, At: at}}); err != nil {
		t.Fatal(err)
	}
	for _, m := range out.msgs {
		if len(m.Headers) != 2 || m.Headers[0].Key != "content-type" || m.Headers[0].Value != cloudevents.MediaType || m.Headers[1].Key != "traceparent" {
			t.Fatalf("%s headers = %+v, want content-type then traceparent", m.Topic, m.Headers)
		}
	}
}
