---
id: configuration
title: Configuration
sidebar_label: Configuration
description: "Every environment variable of cmd/api, cmd/mcp and cmd/a2a with its default and rule, and the map file format."
---

# Configuration

Configuration is the environment and one map file. A missing required variable or a set-but-invalid value is a boot
error: the process writes ONE JSON log line naming every bad variable and exits `1`.

## cmd/api

Source: `cmd/api/config.go` (`loadConfig`), `usecases.DefaultConfig`, `usecases.Config.Validate`.

| Variable | Default | Rule and meaning |
| --- | --- | --- |
| `DATABASE_URL` | required | The Postgres DSN of the pool (repositories, unit of work, outbox, idempotency store). |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | The DSN the embedded migrations run on at boot; set it to a direct (non-pooler) connection when `DATABASE_URL` goes through PgBouncer. |
| `KAFKA_BROKERS` | required | Comma-separated brokers of the outbox relay; `/readyz` dials the first one. |
| `MQTT_URL` | required | The broker of the ONE fleet link, e.g. `tcp://mosquitto:1883`. |
| `MQTT_CLIENT_ID` | `amr-fleet-control` | The link's MQTT client id. |
| `AMR_MAP_FILE` | required | The map YAML (below). |
| `ADDR` | `:8080` | The HTTP listen address; bound before anything else is wired, so a busy address is the boot error. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`/`warning` or `error`. |
| `OTEL_SERVICE_NAME` | `amr-fleet-control` | `service.name`; also names the otelchi spans and HTTP metric series. |
| `SERVICE_VERSION` | `dev` (when empty) | `service.version`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty = telemetry off | OTLP/gRPC endpoint URL of the Collector (ADR 0005). |
| `ENVIRONMENT` | `local` | Read by `telemetry.Setup` only, as the resource attribute `deployment.environment.name`; no behaviour depends on it. |
| `OUTBOX_INTERVAL_MS` | `250` | Relay poll interval; an integer >= 1. |
| `OUTBOX_BATCH` | `100` | Rows per relay pass; an integer >= 1. |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Publish attempts per row before the relay passes it by; an integer >= 1 (0 would disable the cap). |
| `CHARGE_LOW_PERCENT` | `20` | Below it a robot is never assigned a leg and an idle robot goes to charge; a finite number > 0. |
| `CHARGE_RESUME_PERCENT` | `80` | A charging robot gets `stopCharging` at or above it; a finite number > 0, and `0 < CHARGE_LOW_PERCENT < CHARGE_RESUME_PERCENT <= 100`. |
| `RELEASE_AHEAD_NODES` | `2` | How many nodes past the robot's last reached node the base reaches; an integer >= 1. |
| `NODE_TOLERANCE_METRES` | `0.25` | A robot with no `lastNodeId` stands on the nearest node within this distance; a finite number > 0. |
| `ROBOT_STALE_AFTER_SECONDS` | `90` | No `state` (or `connection`) for this long makes a robot stale, so `STOPPED`; an integer >= 1. |
| `ORDER_RESEND_AFTER_SECONDS` | `5` | An unacknowledged order (or an unanswered `cancelOrder`/`stopCharging`) is sent again after this; an integer >= 1. |
| `ORDER_RESEND_LIMIT` | `3` | Resends before an unacknowledged order fails its leg `ORDER_REJECTED` (a `cancelOrder` ends its leg `CANCELLED`); an integer >= 1. |
| `REROUTE_AFTER_SECONDS` | `10` | A robot waiting this long for a node another robot holds is re-routed; an integer >= 1. |
| `DEADLOCK_TIMEOUT_SECONDS` | `60` | A wait-for cycle older than this fails its youngest leg `ROBOT_BLOCKED`; an integer >= 1. |
| `ROBOT_BLOCKED_TIMEOUT_SECONDS` | `60` | A robot `HELD` longer than this fails its leg `ROBOT_BLOCKED`; an integer >= 1. |
| `ROBOT_OFFLINE_TIMEOUT_SECONDS` | `60` | A robot `STOPPED` longer than this fails its leg `ROBOT_OFFLINE`; an integer >= 1. |

:::note
`cmd/api` does not validate `LOG_LEVEL` yet: an unknown value (or a value in another case, such as `DEBUG`) silently
logs at `info`. `cmd/mcp` and `cmd/a2a` already refuse it at boot; `cmd/api` switches to the same
`telemetry.ParseLogLevel` rule in Task 22.
:::

## cmd/mcp

Source: `cmd/mcp/main.go` (`loadBootConfig`), `cmd/mcp/telemetry.go`.

| Variable | Default | Rule and meaning |
| --- | --- | --- |
| `MCP_ADDR` | `:8090` | Listen address. |
| `DATABASE_URL` | required | The Postgres DSN `cmd/api` owns; opened with `postgres.NewReadOnlyPool` (every transaction `BEGIN READ ONLY`). |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | The DSN of the boot migrations (this binary may boot before `cmd/api`). |
| `AMR_MAP_FILE` | required | The same map file `cmd/api` reads (the fleet view names its handoff points). |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`/`warning`, `error`, any case; any other value is a boot error. |
| `OTEL_SERVICE_NAME` | `amr-fleet-control-mcp` | `service.name`. |
| `SERVICE_VERSION` | `dev` (when empty) | `service.version`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty = telemetry off | As for `cmd/api`. |
| `ENVIRONMENT` | `local` | `deployment.environment.name` only. |

## cmd/a2a

Source: `cmd/a2a/main.go` (`loadBootConfig`), `cmd/a2a/telemetry.go`.

| Variable | Default | Rule and meaning |
| --- | --- | --- |
| `A2A_ADDR` | `:8093` | Listen address. |
| `A2A_AGENT_URL` | required | The absolute URL the Agent Card advertises, e.g. `http://amr-fleet-control-a2a:8093/a2a`. |
| `DATABASE_URL` | required | As for `cmd/mcp` (read-only pool). |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | As for `cmd/mcp`. |
| `AMR_MAP_FILE` | required | As for `cmd/mcp`. |
| `LOG_LEVEL` | `info` | As for `cmd/mcp` (validated). |
| `OTEL_SERVICE_NAME` | `amr-fleet-control-a2a` | `service.name`. |
| `SERVICE_VERSION` | `dev` (when empty) | `service.version` and the card's `version`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | empty = telemetry off | As for `cmd/api`. |
| `ENVIRONMENT` | `local` | `deployment.environment.name` only. |

## The map file

`AMR_MAP_FILE` names one YAML file (decision D1; loader `internal/config/fleetmap.go`, rules
`internal/domain/fleetmap/map.go`). Unknown keys are refused.

| Key | Meaning |
| --- | --- |
| `fleetId` | The fleet id of every REST path (`AMR1`). Required. |
| `mapId` | The VDA 5050 `mapId` the robots report; a robot reporting another map stands on no node. Required. |
| `version` | The map version every mission is pinned to. Required. |
| `nodes[]` | `id`, `x`, `y` (metres, finite), `kind` (`WAYPOINT`, `PARKING`, `CHARGER`, `PORT`, `STATION`), optional `handoffPoint` (only on a `PORT` or `STATION`, each handoff point on one node). At least one node. |
| `edges[]` | `id`, `a`, `b` (two different known nodes, no second edge between the same pair, non-zero length), optional `maxSpeed` (finite, not negative). Edges are undirected corridors. |

An AS/RS port node's id must be the emulator world's port `nodeId`. `config/map.example.yaml`, the floor of the
emulator's example world:

```yaml
fleetId: AMR1
mapId: S1
version: "2026-11-20.1"
nodes:
  - {id: P1, x: 0, y: 0, kind: PARKING}
  - {id: P2, x: 2, y: 0, kind: PARKING}
  - {id: P3, x: 4, y: 0, kind: PARKING}
  - {id: CH1, x: 6, y: 0, kind: CHARGER}
  - {id: A, x: 0, y: 2, kind: WAYPOINT}
  - {id: B, x: 2, y: 2, kind: WAYPOINT}
  - {id: C, x: 4, y: 2, kind: WAYPOINT}
  - {id: D, x: 6, y: 2, kind: WAYPOINT}
  - {id: ASRS1-OUT1, x: 0, y: 4, kind: PORT, handoffPoint: ASRS1-OUT1}
  - {id: ASRS1-IN1, x: 2, y: 4, kind: PORT, handoffPoint: ASRS1-IN1}
  - {id: ASRS1-OUT2, x: 4, y: 4, kind: PORT, handoffPoint: ASRS1-OUT2}
  - {id: ASRS1-IN2, x: 6, y: 4, kind: PORT, handoffPoint: ASRS1-IN2}
  - {id: E, x: 0, y: 6, kind: WAYPOINT}
  - {id: F, x: 2, y: 6, kind: WAYPOINT}
  - {id: G, x: 4, y: 6, kind: WAYPOINT}
  - {id: H, x: 6, y: 6, kind: WAYPOINT}
  - {id: ST1, x: 0, y: 8, kind: STATION, handoffPoint: ST1}
  - {id: ST2, x: 6, y: 8, kind: STATION, handoffPoint: ST2}
edges:
  - {id: P1-A, a: P1, b: A}
  - {id: P2-B, a: P2, b: B}
  - {id: P3-C, a: P3, b: C}
  - {id: CH1-D, a: CH1, b: D}
  - {id: A-B, a: A, b: B}
  - {id: B-C, a: B, b: C}
  - {id: C-D, a: C, b: D}
  - {id: A-ASRS1-OUT1, a: A, b: ASRS1-OUT1}
  - {id: ASRS1-OUT1-E, a: ASRS1-OUT1, b: E}
  - {id: B-ASRS1-IN1, a: B, b: ASRS1-IN1}
  - {id: ASRS1-IN1-F, a: ASRS1-IN1, b: F}
  - {id: C-ASRS1-OUT2, a: C, b: ASRS1-OUT2}
  - {id: ASRS1-OUT2-G, a: ASRS1-OUT2, b: G}
  - {id: D-ASRS1-IN2, a: D, b: ASRS1-IN2}
  - {id: ASRS1-IN2-H, a: ASRS1-IN2, b: H}
  - {id: E-F, a: E, b: F}
  - {id: F-G, a: F, b: G}
  - {id: G-H, a: G, b: H}
  - {id: E-ST1, a: E, b: ST1}
  - {id: H-ST2, a: H, b: ST2}
```

:::note
The Helm chart (`charts/amr-fleet-control`, Task 21) mounts this file from a ConfigMap and sets the environment from
its values; it is not built yet.
:::
