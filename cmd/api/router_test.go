package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	inboundhttp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/http"
)

// carry (Task 15 review): OTEL_SERVICE_NAME reaches the router. The otelchi server span is named
// after cfg.ServiceName (attribute net.host.name), so a router that always says
// "amr-fleet-control" (mutant H) or a wiring that drops the configured name fails here.
func TestTheRouterIsNamedAfterTheConfiguredServiceName(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	srv := newHTTPServer(config{ServiceName: "afc"}, &inboundhttp.Server{}, slog.New(slog.DiscardHandler))
	if srv.ReadHeaderTimeout <= 0 {
		t.Fatalf("ReadHeaderTimeout = %v, want a bound", srv.ReadHeaderTimeout)
	}
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz = %d", rec.Code)
	}
	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want the one server span", len(spans))
	}
	var got []string
	for _, kv := range spans[0].Attributes() {
		got = append(got, string(kv.Key)+"="+kv.Value.String())
		if kv.Key == "net.host.name" && kv.Value.AsString() == "afc" {
			return
		}
	}
	t.Fatalf("server span attributes = %v, want net.host.name=afc (OTEL_SERVICE_NAME)", got)
}
