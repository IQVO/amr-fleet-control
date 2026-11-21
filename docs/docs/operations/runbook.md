---
id: runbook
title: Runbook
sidebar_label: Runbook
description: "Boot and shutdown order, the probes, one replica, what a restart does, changing the map version, stuck robots and legs, the outbox lag."
---

# Runbook

## Boot

`cmd/api` boots in this order (`cmd/api/main.go`, `wire.go`, `app.go`):

1. The JSON logger at `info`, then the environment: a bad environment is one JSON error line and exit `1`.
2. OpenTelemetry (nothing installed when `OTEL_EXPORTER_OTLP_ENDPOINT` is empty).
3. `net.Listen` on `ADDR`: a busy address fails the boot before anything else is wired.
4. The map file, the embedded migrations (on `MIGRATIONS_DATABASE_URL`), the Postgres pool, the use-case `Service`,
   the Kafka relay sink, the outbox relay, the readiness checks and the HTTP server.
5. The fleet link connects and waits for its first subscription round (`connection`, `factsheet`, `state`, each
   SUBACK awaited, at most 35 s): the retained `connection` and `factsheet` of every robot arrive first.
6. `Recover` runs once (below).
7. The loops start (the link's worker with the 1 s tick and the ordered sender, the outbox relay, the hourly
   idempotency purge), then HTTP serves.

A failure in steps 5-6 closes the listener and everything opened, and exits `1`. `cmd/mcp` and `cmd/a2a` read the
environment, start telemetry, listen, then load the map, run the migrations and open the read-only pool.

## Shutdown

On `SIGTERM` or `SIGINT` (or when the HTTP server fails), `cmd/api`:

1. flips `/readyz` to `503` (`not_ready`),
2. cancels the loops' context (link worker, tick and sender, relay, purger) while HTTP drains (bounded by 10 s),
3. waits for the loops, then disconnects the link, closes the Kafka sink and the pool,
4. flushes telemetry (bounded by 5 s).

A command queued during the drain may stay unsent; after the restart the tick resends every unacknowledged order,
`cancelOrder` and `stopCharging`, and `Recover` asks every robot for a fresh state.

## Probes

| Probe | Answers |
| --- | --- |
| `GET /healthz` | `200 {"status":"ok"}` while the process runs. Liveness. |
| `GET /readyz` | `200 {"status":"ready"}`; `503 {"status":"not_ready"}` while draining; `503` problem `dependency-unavailable` with `detail` `not ready: <names>` when `postgres` (ping), `kafka` (TCP dial of the first broker) or `mqtt` (the fleet link is connected) fails. Checks are cached 3 s, 2 s timeout. Readiness. |

`cmd/mcp` and `cmd/a2a` serve `GET /healthz` only.

## One replica

`cmd/api` is the single traffic controller of its fleet: ONE MQTT link, ONE worker, one in-process serial lock around
every unit of work (decision D18, ruling P3-R10). Run exactly one replica, and replace it with `Recreate`, never a
rolling update: two processes would both send orders and reserve nodes.

The chart enforces it: it refuses to render a `replicaCount` other than 1 for the api component, the api Deployment
uses strategy `Recreate`, and `terminationGracePeriodSeconds` defaults to 45 (the stop sequence is bounded at about
20 s; the chart refuses less than 30). `cmd/mcp` and `cmd/a2a` are read-only and may scale (`mcp.replicaCount`,
`a2a.replicaCount`).

## What a restart does

`Recover` (`internal/application/usecases/tick.go`), in one fleet unit of work, before the link's worker starts:

- Every open mission of ANOTHER map version than the loaded file's ends (decision D14): a `QUEUED` or active leg fails
  `MAP_VERSION_RETIRED` (an active one also gets `cancelOrder`), a `CANCEL_REQUESTED` leg ends `CANCELLED` with the
  load the robot last reported.
- A `stateRequest` instant action goes to every known robot, so each one reports at once.
- The fleet is scheduled (base extension, deadlocks, charging, assignment, parking).

Then the robots' states arrive in order. A robot that reports a pending order fleet control does not own (an order
of the previous process, a retired leg) gets `cancelOrder` with action id `recover-<uuid>` and is offered nothing
until it answers (decision D19). Reservations of another map version are dropped when the fleet row is loaded.

## Changing the map version

1. Edit the map file and change its `version` (and nothing else at the same time as a robot fleet change you cannot
   roll back).
2. Roll out the new file and restart `cmd/api` (and `cmd/mcp`, `cmd/a2a`, which read the same file).
3. Expect `LegFailed` `MAP_VERSION_RETIRED` for every leg that was open; material-flow-control retries them on the
   new map. New legs record the new version (`map_version` on the leg and on `LegAssigned`).

A refusal that depends on the map (`fleet-not-found`, `unknown-handoff-point`, `handoff-unreachable`) is RETRYABLE
(ruling P3-R13): after the deploy, a retry with the same `Idempotency-Key` runs again on the new map.

## A robot stuck HELD

`HELD` means the robot reports an active `CRITICAL` or `FATAL` error (`GET /fleets/{fleetId}/robots/{manufacturer}/{serialNumber}`,
`error_types`). A `FATAL` error fails its leg `ROBOT_FAULT` at once; otherwise its leg fails `ROBOT_BLOCKED` after
`ROBOT_BLOCKED_TIMEOUT_SECONDS` and fleet control sends `cancelOrder`. The robot stays `HELD`, and out of assignment,
until it stops reporting the error: clear the cause on the robot (with the emulator, `POST /faults` with
`"active":false`). Fleet control never clears a robot error itself.

## A leg stuck ASSIGNED

1. Read the leg and its robot. A robot that has not acknowledged the order (its state does not show the leg id and
   `orderUpdateId`) gets it again every `ORDER_RESEND_AFTER_SECONDS`; after `ORDER_RESEND_LIMIT` resends the leg fails
   `ORDER_REJECTED` (with `robot_error` when the robot listed a rejection). A rising count of `VALIDATION_FAILURE` /
   `INVALID_ORDER` errors fails it at once.
2. A robot that acknowledged the order but does not move is waiting for a node another robot holds: after
   `REROUTE_AFTER_SECONDS` it is re-routed around the held nodes; a wait-for cycle publishes `DeadlockDetected` and
   fails its youngest leg `ROBOT_BLOCKED` after `DEADLOCK_TIMEOUT_SECONDS`.
3. A robot that went silent turns `STOPPED` after `ROBOT_STALE_AFTER_SECONDS`, and its leg fails `ROBOT_OFFLINE`
   after `ROBOT_OFFLINE_TIMEOUT_SECONDS` more.

`cmd/api` logs a robot command it could not hand to the sender at `WARN` (`robot command not sent; ...`).

## The outbox

Every event is a row of `outbox_events`, written in the same transaction as the change; the relay publishes it to both
topics. The lag:

```sql
select count(*) from outbox_events where published_at is null;
```

A row whose publish failed `OUTBOX_MAX_ATTEMPTS` times is excluded from later passes and logged once at `ERROR`
(`outbox relay: row reached the attempt cap ...`). After fixing the cause, re-arm it:
`update outbox_events set attempts = 0 where published_at is null and attempts >= 20;` (use the configured cap).

## The idempotency window

Stored answers live in `idempotency_keys`; a replay checks the 24-hour window, and `cmd/api` purges older rows every
hour.
