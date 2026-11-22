---
id: overview
title: API overview
slug: /api-reference
sidebar_position: 1
description: "The interfaces of amr-fleet-control (REST, Kafka events, VDA 5050 over MQTT, MCP and A2A), the endpoint table, idempotency and the problem catalogue with its replay classes."
---

# API overview

:::info Implemented by cmd/api
`cmd/api` serves the REST routes and the probes (`internal/adapters/inbound/http`) on `ADDR` (default `:8080`), runs the
ONE MQTT fleet link and the outbox relay. The contracts are `apis/openapi.yaml` (OpenAPI 3.1.0, version 1.0.0, 7
operations) and `apis/asyncapi.yaml` (AsyncAPI 2.6.0, version 1.0.0, 2 Kafka channels).
:::

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST | `apis/openapi.yaml` | The pages under *REST* are generated from the spec with `npm run gen-api-docs`; do not edit them by hand (the `docs-api-drift` CI job fails on a difference). Caller: material-flow-control (relationship 4). |
| Events | `apis/asyncapi.yaml` | Seven published CloudEvents types, none consumed: [events](/docs/api-reference/events); every field on [domain events](/docs/ddd/domain-events). |
| Robots | IQVO/equipment-emulator `docs/vda5050-subset.md` | VDA 5050 v3.0.0 over MQTT, fleet control's side: [VDA 5050](/docs/api-reference/vda5050). |
| MCP, A2A | `internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`, the Agent Card | Read-only, authority L1: [MCP and A2A](/docs/mcp/tools). |

## Endpoints

| Tag | Endpoint | Operation id | Success |
| --- | --- | --- | --- |
| legs | `POST /fleets/{fleetId}/legs` | [`requestLeg`](/docs/api-reference/rest/request-leg) | `202`, the leg (`QUEUED`, or already `ASSIGNED` when a robot was free) and `Location` |
| legs | `GET /fleets/{fleetId}/legs/{legId}` | [`getLeg`](/docs/api-reference/rest/get-leg) | `200`, the leg |
| legs | `POST /fleets/{fleetId}/legs/{legId}/cancellation` | [`cancelLeg`](/docs/api-reference/rest/cancel-leg) | `200` cancelled at once (`CANCELLED`), `202` the robot was asked (`CANCEL_REQUESTED`) |
| fleet | `GET /fleets/{fleetId}` | [`getFleet`](/docs/api-reference/rest/get-fleet) | `200`, map id and version, handoff points, robots, capacity |
| fleet | `GET /fleets/{fleetId}/robots/{manufacturer}/{serialNumber}` | [`getRobot`](/docs/api-reference/rest/get-robot) | `200`, one robot with its factsheet |
| health | `GET /healthz` | [`getLiveness`](/docs/api-reference/rest/get-liveness) | `200` |
| health | `GET /readyz` | [`getReadiness`](/docs/api-reference/rest/get-readiness) | `200` ready; `503` draining or Postgres, Kafka or the MQTT link down |

A leg is accepted or refused at once; its progress is reported by events (and readable with `GET`). No node, edge,
order, base or horizon appears in any body: the WES-facing vocabulary is handoff points, containers, legs and robots.
The full relationship-4 table, with every refusal per route, is on [ecosystem integration](/docs/ecosystem/integration).

## Idempotency

Every POST requires `Idempotency-Key` (1-128 characters; missing or longer is `400 idempotency-key-required`). A retry
with the same key and body gets the stored answer with `Idempotency-Replayed: true`; the same key with another body is
`409 idempotency-key-reused`. Only 2xx and FINAL refusals are stored, for 24 hours (age check on replay, hourly
purge); RETRYABLE refusals release the key, 5xx answers and panics are never stored. A duplicate that arrives while
the first copy runs waits on the serial lock and replays the first copy's answer (the key is read again under the
lock).

## Errors

RFC 9457 `application/problem+json` with a stable `code`, type `https://errors.amr-fleet-control.warehouse-systems.dev/<code>`
(`internal/adapters/inbound/http/errors.go`).

| Status | Code | Class |
| --- | --- | --- |
| 400 | `bad-request`, `invalid-parameter`, `idempotency-key-required` | FINAL |
| 404 | `leg-not-found` | FINAL |
| 409 | `idempotency-key-reused` | FINAL |
| 404 | `fleet-not-found`, `robot-not-found` | RETRYABLE (configuration, robot facts) |
| 422 | `unknown-handoff-point`, `handoff-unreachable` | RETRYABLE (configuration: the map file) |
| 422 | `container-busy`, `fleet-unavailable` | RETRYABLE (state) |
| 409 | `leg-not-cancellable`, `leg-ended` | RETRYABLE |
| 500, 503 | `internal-error`, `dependency-unavailable` | RETRYABLE |

A refusal decided by the map file is RETRYABLE because a map deploy can change it inside the 24-hour window without
the client changing its request (ruling P3-R13, [ADR 0004](/docs/adr/0004-subsystem-controller-rest-interface-and-replay-classes)).
No refusal commits a write, so no OUTCOME class exists.

No authentication, by fleet decision; `TestNoAuthMiddlewareReintroduced` keeps it so.
