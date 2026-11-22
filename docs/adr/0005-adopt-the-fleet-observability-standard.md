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
4. Chart: `otel.{enabled,endpoint,serviceName}` and `serviceVersion` (Task 21). No behaviour depends on
   `ENVIRONMENT`; `telemetry.Setup` reads it only to label telemetry as the resource attribute
   `deployment.environment.name` (default `local`), as in outbound-shipping and material-flow-control.

## Consequences

Only the facts written in a request's own unit of work carry that request's `traceparent` (appended after the
CloudEvents `content-type` header and stored with the outbox row). The tick and robot-message units run without a
span and store none, so a consumer's span for those facts is a root. The outbox relay forwards the stored headers
as they are and starts no publish span of its own. MQTT carries no W3C trace context, so robot messages start no
trace.
