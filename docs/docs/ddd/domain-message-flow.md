---
id: domain-message-flow
title: Domain Message Flow
sidebar_label: Domain Message Flow
description: "A leg end to end, from the REST request to LegCompleted, and the failure path."
---

# Domain Message Flow

## A leg end to end

```mermaid
sequenceDiagram
  autonumber
  participant MFC as material-flow-control
  participant API as amr-fleet-control (cmd/api)
  participant DB as Postgres (outbox)
  participant K as Kafka
  participant R as robot EMU/R1
  MFC->>API: POST /fleets/AMR1/legs (Idempotency-Key) C-0001 ST1 to ASRS1-IN1
  API->>DB: one unit: leg QUEUED, schedule assigns R1, reserve base, LegAssigned row
  API-->>MFC: 202 Leg (ASSIGNED), Location
  API->>R: order leg-... (base + horizon), after commit
  DB-->>K: relay: LegAssigned
  K-->>MFC: LegAssigned
  R->>API: state (orderId, orderUpdateId shown: acknowledged)
  API->>R: order update (base extended, orderUpdateId + 1)
  R->>API: state (pick FINISHED at ST1)
  API->>DB: leg PICKED_UP, ContainerPickedUp row
  DB-->>K: ContainerPickedUp
  K-->>MFC: ContainerPickedUp
  R->>API: state (drop FINISHED at ASRS1-IN1)
  API->>DB: leg DONE, release, LegCompleted row (+ FleetCapacityChanged on a count change)
  DB-->>K: LegCompleted
  K-->>MFC: LegCompleted
  API->>R: order PARK mission to the nearest free parking node (the robot stands on a port)
```

Each arrow into the API is one fleet unit of work; each arrow to the robot is sent from the after-commit hook of the
unit that queued it, so a rolled-back write sends nothing.

## The failure path

| Failure | Detected | Outcome |
| --- | --- | --- |
| Robot refuses the order | a rise of `VALIDATION_FAILURE` / `INVALID_ORDER` in its `state` while unacknowledged, or `ORDER_RESEND_LIMIT` resends without acknowledgement | `LegFailed` `ORDER_REJECTED`, `robot_error` = the rejection type |
| Nothing to pick / drop | the `pick` / `drop` action FAILED | `LegFailed` `PICK_FAILED` / `DROP_FAILED`, `robot_error` (e.g. `LOAD_NOT_AVAILABLE`); `cancelOrder` |
| Fatal robot error | a `FATAL` error in its `state` | `LegFailed` `ROBOT_FAULT`; `cancelOrder` |
| Blocked robot | `HELD` longer than `ROBOT_BLOCKED_TIMEOUT_SECONDS` (tick) | `LegFailed` `ROBOT_BLOCKED`, `robot_error` = the blocking error; `cancelOrder` |
| Deadlock | a wait-for cycle older than `DEADLOCK_TIMEOUT_SECONDS` (after `DeadlockDetected` and a re-route attempt) | the youngest leg `LegFailed` `ROBOT_BLOCKED`; `cancelOrder` frees its base |
| Robot gone | `STOPPED` longer than `ROBOT_OFFLINE_TIMEOUT_SECONDS` (tick) | `LegFailed` `ROBOT_OFFLINE` |
| Map retired | at boot, the leg's map version is not the loaded one | `LegFailed` `MAP_VERSION_RETIRED` |

`container_on_robot` tells material-flow-control whether the container was already on the robot (it failed after
`PICKED_UP`). amr-fleet-control never retries a failed leg with another robot (decision D4): material-flow-control
decides whether to request a new leg.
