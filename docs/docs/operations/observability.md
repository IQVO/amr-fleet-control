---
id: observability
title: Observability
sidebar_label: Observability
description: "Tier 1 OpenTelemetry in all three binaries, the amr_fleet_control.legs.ended counter, the log fields, the agent spans and the Kong metrics."
---

# Observability

amr-fleet-control adopts the fleet observability standard from its first release
([ADR 0005](/docs/adr/0005-adopt-the-fleet-observability-standard), outbound-shipping ADR 0008).

## Tier 1 (every binary)

| Signal | What |
| --- | --- |
| Export | OTLP/gRPC traces and metrics to `OTEL_EXPORTER_OTLP_ENDPOINT` (metrics pushed every 15 s). **Empty endpoint = telemetry off**: nothing is installed and the process logs `telemetry disabled: OTEL_EXPORTER_OTLP_ENDPOINT is not set`. An unreachable Collector costs telemetry, never availability; the final flush is bounded by 5 s. |
| Resource | `service.name` (`OTEL_SERVICE_NAME`; defaults `amr-fleet-control`, `amr-fleet-control-mcp`, `amr-fleet-control-a2a`), `service.version` (`SERVICE_VERSION`, default `dev`), `deployment.environment.name` (`ENVIRONMENT`, default `local`). |
| Propagation | W3C trace context and baggage. |
| Runtime | Go runtime metrics. |
| HTTP | otelchi server spans named after the chi route, `http.server.request.duration` and (cmd/api) `http.server.active_requests`. Middleware order in `cmd/api`: RequestID, otelchi, request duration, active requests, request log, recoverPanic. |
| Logs | JSON on stdout; every line written under a span carries `trace_id` and `span_id`. The access log line has `method`, `path`, `status`, `duration_ms`, `bytes`, `request_id`. |

## Tier 2: the leg counter

| Instrument | Unit | Attributes | Recorded |
| --- | --- | --- | --- |
| `amr_fleet_control.legs.ended` (Int64Counter) | `{leg}` | `outcome` = `completed` or `failed`; `reason` (only when failed) = `CANCELLED`, `ROBOT_BLOCKED`, `ROBOT_FAULT`, `ROBOT_OFFLINE`, `ORDER_REJECTED`, `PICK_FAILED`, `DROP_FAILED`, `MAP_VERSION_RETIRED` | once per accepted leg, in the after-commit hook of the unit of work that ended it (`ports.Metrics`, `internal/adapters/outbound/telemetry/leg_metrics.go`) |

A rolled-back unit records nothing. A cancelled leg counts `failed` / `CANCELLED`, like its `LegFailed` event.

## Traces across the outbox

A fact written in an HTTP request's own unit of work carries that request's `traceparent` header on its outbox row
(after the CloudEvents `content-type` header), and the relay forwards the stored headers as they are, so the
consumer's span joins the request's trace. Facts from the tick and from robot messages carry none: MQTT carries no
W3C context, so a consumer's span for those facts is a root. The relay starts no publish span of its own.

## The agent surfaces

| Surface | Span | Attributes |
| --- | --- | --- |
| `cmd/mcp` | `mcp.tool <name>` per tool call | `gen_ai.operation.name` = `execute_tool`, `gen_ai.tool.name`, `warehouse.authority` = `L1`; a failed call records the error's slug only and marks the span `Error` |
| `cmd/a2a` | `a2a.skill <id>` per run (`unknown` for a skill the card does not offer) | `a2a.skill`, `gen_ai.operation.name` = `execute_tool`, `gen_ai.tool.name`, `warehouse.authority` = `L1`; a problem answer records its `code` only and marks the span `Error` |

## Kong RED metrics

The chart's HTTPRoutes (`/api/amr-fleet-control`, `/api/mcp/amr-fleet-control`, `/api/a2a/amr-fleet-control`) carry
the chart's KongPlugins (`cors`, `prometheus`, `opentelemetry`; default off) through `konghq.com/plugins`. With the
`prometheus` plugin on, Kong gives the edge RED metrics (rate, errors, duration) per route; the `opentelemetry` plugin
sends Kong's spans to `kongPlugins.opentelemetry.tracesEndpoint` (warehouse-infra's `_platform.yaml` supplies it).

:::note
The routes and plugins go live when warehouse-infra registers the release and switches them on (Task 25).
:::
