---
id: integration
title: Ecosystem integration
sidebar_label: Integration
description: "Who calls amr-fleet-control, who it talks to, what it publishes and who reads it: relationships 3, 4, 6 and 8 and the read-only consumers."
---

# Ecosystem integration

amr-fleet-control's relationships are numbered as on the WCS context map (warehouse-docs `docs/wcs/context-map.md`).

| # | With | Pattern | Channel | Built |
| --- | --- | --- | --- | --- |
| 4 | material-flow-control (customer) | Customer/Supplier; amr-fleet-control offers the subsystem-controller interface | REST in, Kafka events out | yes |
| 6 | the robots | Conformist to VDA 5050 v3.0.0 | MQTT, JSON | yes (against the equipment emulator) |
| 3 | facility-layout | Conformist through an ACL | facility-layout's events and REST reads | no: a map file instead (decision D1) |
| 8 | exception-management | Conformist (it conforms to every producer) | Kafka events | producer side only (`DeadlockDetected`, `LegFailed` `ROBOT_BLOCKED`) |
| - | warehouse-ops-agent, warehouse-console | read-only consumers | MCP, A2A | yes (`cmd/mcp`, `cmd/a2a`) |

## Relationship 4: inbound REST from material-flow-control

Base path: the Service `amr-fleet-control` on `:8080`, through Kong at `/api/amr-fleet-control` (the chart's HTTPRoute,
prefix stripped; live once warehouse-infra registers the release, Task 25). `{fleetId}` is the
map file's `fleetId` (example `AMR1`); a handoff point id is a map node's `handoffPoint` (example `ASRS1-OUT1`, `ST1`).
Every POST requires `Idempotency-Key` (1-128 characters); a replayed answer carries `Idempotency-Replayed: true`.
Bodies snake_case; errors RFC 9457 `application/problem+json` with `code`.

| Method and path | Use case | Body | Success | Refusals (code -> status, replay class) |
| --- | --- | --- | --- | --- |
| `POST /fleets/{fleetId}/legs` | `RequestLeg` | `{"container_id", "from_handoff_point_id", "to_handoff_point_id", "reference"?}` | `202` `Leg` + `Location: /fleets/{fleetId}/legs/{legId}` | `bad-request` 400 F (also `reference` present but `""`), `invalid-parameter` 400 F (also from = to), `fleet-not-found` 404 R, `unknown-handoff-point` 422 R, `handoff-unreachable` 422 R, `container-busy` 422 R, `fleet-unavailable` 422 R (no robot ONLINE) |
| `GET /fleets/{fleetId}/legs/{legId}` | `Leg` | - | `200` `Leg` | `invalid-parameter` 400, `fleet-not-found` 404, `leg-not-found` 404 |
| `POST /fleets/{fleetId}/legs/{legId}/cancellation` | `CancelLeg` | none (empty body) | `200` `Leg` status `CANCELLED`; `202` `Leg` status `CANCEL_REQUESTED` | `invalid-parameter` 400 F, `fleet-not-found` 404 R, `leg-not-found` 404 F, `leg-not-cancellable` 409 R (PICKED_UP), `leg-ended` 409 R |
| `GET /fleets/{fleetId}` | `Fleet` | - | `200` `Fleet` | `invalid-parameter` 400, `fleet-not-found` 404 |
| `GET /fleets/{fleetId}/robots/{manufacturer}/{serialNumber}` | `Robot` | - | `200` `Robot` | `invalid-parameter` 400, `fleet-not-found` 404, `robot-not-found` 404 |
| `GET /healthz`, `GET /readyz` | probes | - | `200` | `503` while draining or a dependency (Postgres, Kafka, MQTT) is down |

Common: `idempotency-key-required` 400 F, `idempotency-key-reused` 409 F, `internal-error` 500 R (never stored),
`dependency-unavailable` 503 R. F = FINAL (stored, replayed), R = RETRYABLE (key released, a retry executes).

This is the plan's relationship-4 table as built: ruling P3-R13 made every refusal decided by configuration that can
change inside the 24-hour replay window (`fleet-not-found`, `unknown-handoff-point`, `handoff-unreachable`) and
`robot-not-found` RETRYABLE; `leg-not-found` stays FINAL (legs are minted here and never deleted). Ruling P3-R14:
`reference`, when present, must be 1 to 128 characters.

## Relationship 6: the robots (VDA 5050 v3.0.0)

ONE MQTT client for the fleet publishes `order` and `instantActions` and subscribes `connection`, `factsheet` and
`state` on `vda5050/v3/<manufacturer>/<serialNumber>/<topic>`. A robot joins the registry with its first message.
The subset and the rules are on [VDA 5050](/docs/api-reference/vda5050); the conformance target is the equipment
emulator (`IQVO/equipment-emulator` `v0.1.0`).

## Relationship 3: facility-layout (deferred)

The ACL that would translate facility-layout's travel graph is a later plan (decision D1): facility-layout publishes no
travel-graph event, and its graph has no coordinates and no AMR node kinds. Until then the deployment owns the map
file (`AMR_MAP_FILE`), versioned and pinned per mission.

## Outbound: the seven events

Published through the outbox to `warehouse.amr-fleet-control.events` and, under the same `id`, to
`warehouse.amr-fleet-control.analytics` ([events](/docs/api-reference/events)). amr-fleet-control consumes no Kafka
topic.

| Event | Read by |
| --- | --- |
| `LegAssigned`, `ContainerPickedUp`, `LegCompleted`, `LegFailed` | material-flow-control (WCS plan 04, v0.1.0): transport progress, container tracking, the port pickup it confirms to asrs-control |
| `FleetCapacityChanged` | material-flow-control: its subsystem capacity view |
| `LegFailed` with `ROBOT_BLOCKED`, `DeadlockDetected` | exception-management (WCS plan 05, relationship 8): designed for, not consumed yet |
| `RobotStateChanged` | no consumer yet (material-flow-control skips it) |

material-flow-control's own asyncapi declares the five types it reads, so they carry no `x-no-consumer-reason`;
`RobotStateChanged` and `DeadlockDetected` keep one until a consumer declares them.

## Read-only consumers

warehouse-ops-agent and warehouse-console read through `cmd/mcp` (Kong `/api/mcp/amr-fleet-control`, rewritten to
`/mcp`) and `cmd/a2a` (Kong `/api/a2a/amr-fleet-control`, rewritten to `/`), authority L1 ([MCP and A2A](/docs/mcp/tools)). Neither can request or cancel a
leg: that is material-flow-control's REST call.
