# 0005. Adopt the fleet observability standard from the first release

Date: 2026-11-20

## Status

Accepted

## Context

The WCS fleet-standard plan (2026-11-20) requires every WCS service to ship the fleet standard from the start;
outbound-shipping ADR 0008 is the reference adoption (Tier 1 identical everywhere, one Tier 2 counter).

## Decision

1. Tier 1, copied from outbound-shipping `09dce81`: `telemetry.Setup` (OTLP/gRPC traces and metrics, W3C trace
   context and baggage, Go runtime metrics), `WithTraceContext` on every logger, `requestlog.RequestLogger`; an
   EMPTY `OTEL_EXPORTER_OTLP_ENDPOINT` means telemetry off. Middleware order: RequestID, otelchi,
   request duration, active requests, request log, recoverPanic.
2. One `telemetry.go` per long-running binary (`cmd/api`, `cmd/mcp`, `cmd/a2a`); `service.name` defaults
   `amr-fleet-control`, `amr-fleet-control-mcp`, `amr-fleet-control-a2a`.
3. Tier 2: `amr_fleet_control.legs.ended` (`{leg}`), attributes `outcome` and `reason`, recorded through
   `ports.Metrics` in the after-commit hook of the unit of work that ended the leg.
4. Chart: `otel.{enabled,endpoint,serviceName}` and `serviceVersion` (Task 21); no binary reads an `ENVIRONMENT`
   variable, so the chart renders none.

## Consequences

The outbox rows carry `traceparent` whenever a request span is active, so a leg request's trace continues into
Kafka. Robot messages start no trace (MQTT carries no W3C context).
