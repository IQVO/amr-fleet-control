package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/telemetry"
	"github.com/IQVO/amr-fleet-control/internal/application/ports"
)

// telemetryFlushTimeout bounds the final export attempt. Without a deadline
// the exporter would retry against an unreachable Collector and stretch a
// shutdown out well past what an orchestrator will wait for.
const telemetryFlushTimeout = 5 * time.Second

// startTelemetry brings OpenTelemetry up and returns the flush func to
// defer. Export is non-blocking: an unreachable Collector costs telemetry,
// never availability. With no OTLP endpoint configured nothing is installed
// and the flush does nothing. A failed flush is logged, never returned:
// turning that into a non-zero exit would make every clean shutdown look
// like a crash.
func startTelemetry(logger *slog.Logger, cfg config) (func(), error) {
	shutdownTelemetry, err := telemetry.Setup(context.Background(), cfg.ServiceName, cfg.ServiceVersion, cfg.OTLPEndpoint)
	if err != nil {
		return nil, err
	}
	if cfg.OTLPEndpoint == "" {
		logger.Info("telemetry disabled: OTEL_EXPORTER_OTLP_ENDPOINT is not set", "service_name", cfg.ServiceName)
	} else {
		logger.Info("telemetry configured", "service_name", cfg.ServiceName, "otlp_endpoint", cfg.OTLPEndpoint)
	}
	return func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), telemetryFlushTimeout)
		defer cancel()
		if err := shutdownTelemetry(flushCtx); err != nil {
			logger.Warn("telemetry flush failed on shutdown", "error", err)
		}
	}, nil
}

// legMetrics builds the Tier 2 counter; a registration error (a programming error) leaves the
// service running with a no-op port, never without its legs.
func legMetrics(logger *slog.Logger) ports.Metrics {
	m, err := telemetry.NewLegMetrics()
	if err != nil {
		logger.Warn("leg metrics unavailable; amr_fleet_control.legs.ended is off", "error", err)
		return (*telemetry.LegMetrics)(nil)
	}
	return m
}
