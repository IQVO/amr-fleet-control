package http_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	ahttp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/http"
)

// requestLines records every "http request" line with the trace id of the context it was logged
// under. A local handler keeps this inbound-adapter test off the outbound telemetry package.
type requestLines struct {
	mu    sync.Mutex
	lines []requestLine
}

type requestLine struct {
	level   slog.Level
	status  int64
	traceID trace.TraceID
}

func (h *requestLines) Enabled(context.Context, slog.Level) bool { return true }
func (h *requestLines) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *requestLines) WithGroup(string) slog.Handler            { return h }
func (h *requestLines) Handle(ctx context.Context, r slog.Record) error {
	if r.Message != "http request" {
		return nil
	}
	l := requestLine{level: r.Level, traceID: trace.SpanContextFromContext(ctx).TraceID()}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "status" {
			l.status = a.Value.Int64()
		}
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lines = append(h.lines, l)
	return nil
}

// carry (Task 15 review): the Tier 1 order (ADR 0005 decision 1). The request log runs inside the
// otelchi span, so its line carries the server span's trace id, and outside recoverPanic, so a
// panic is logged at ERROR with status 500.
func TestTheRequestLogRunsInsideTheSpanAndOutsideRecover(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	rec := &requestLines{}
	h := ahttp.NewRouter(&ahttp.Server{Service: nil}, slog.New(rec)) // nil service: the handler panics
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/fleets/AMR1", nil))
	spans := sr.Ended()
	if len(rec.lines) != 1 || len(spans) == 0 {
		t.Fatalf("request log lines %+v, spans %v, want one line and the server span", rec.lines, spans)
	}
	line, want := rec.lines[0], spans[len(spans)-1].SpanContext().TraceID()
	if !line.traceID.IsValid() || line.traceID != want {
		t.Fatalf("request log trace id = %s, want the server span's %s", line.traceID, want)
	}
	if line.level != slog.LevelError || line.status != http.StatusInternalServerError {
		t.Fatalf("panic request log = %+v, want level ERROR status 500", line)
	}
}
