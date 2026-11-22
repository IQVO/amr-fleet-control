---
id: sequence-diagrams
title: Sequence diagrams
sidebar_label: Sequence diagrams
description: "Cancellation of a QUEUED, ASSIGNED and PICKED_UP leg, a deadlock re-route, charging, and recovery after a restart."
---

# Sequence diagrams

Every message into amr-fleet-control is one fleet unit of work; robot commands leave after its commit.

## Cancellation

```mermaid
sequenceDiagram
  participant MFC as material-flow-control
  participant AFC as amr-fleet-control
  participant R as robot
  Note over MFC,AFC: QUEUED leg
  MFC->>AFC: POST .../legs/leg-1/cancellation
  AFC-->>MFC: 200 Leg CANCELLED
  AFC-->>MFC: LegFailed CANCELLED, container_on_robot false
  Note over MFC,R: ASSIGNED leg
  MFC->>AFC: POST .../legs/leg-2/cancellation
  AFC-->>MFC: 202 Leg CANCEL_REQUESTED
  AFC->>R: instantActions cancelOrder (actionId leg-2-cancel)
  alt the robot answers
    R->>AFC: state: leg-2-cancel FINISHED (or FAILED: no order left)
  else no answer
    AFC->>R: cancelOrder again every ORDER_RESEND_AFTER, at most ORDER_RESEND_LIMIT times
  end
  AFC-->>MFC: LegFailed CANCELLED, container_on_robot = robot reports the container as its load
  Note over MFC,AFC: PICKED_UP leg
  MFC->>AFC: POST .../legs/leg-3/cancellation
  AFC-->>MFC: 409 leg-not-cancellable (RETRYABLE)
```

## A deadlock and its re-route

```mermaid
sequenceDiagram
  participant R1 as robot R1
  participant AFC as amr-fleet-control
  participant R2 as robot R2
  participant K as Kafka
  R1->>AFC: state (at the base end, next node held by R2)
  AFC->>AFC: wait R1 for node held by R2
  R2->>AFC: state (next node held by R1)
  AFC->>AFC: wait-for cycle R1, R2 (new)
  AFC-->>K: DeadlockDetected robots [R1, R2], node_ids, deadlock_id
  AFC->>R2: order update: horizon re-planned from the base end around held nodes (youngest mission)
  alt the re-route frees the cycle
    R2->>AFC: state (moving on)
  else still a cycle after DEADLOCK_TIMEOUT_SECONDS
    AFC-->>K: LegFailed ROBOT_BLOCKED (youngest leg)
    AFC->>R2: cancelOrder (frees its base)
  end
```

## Charging

```mermaid
sequenceDiagram
  participant AFC as amr-fleet-control
  participant R as robot
  R->>AFC: state (IDLE, battery below CHARGE_LOW_PERCENT)
  AFC->>R: order ms-... to the nearest free CHARGER node, startCharging at its end
  R->>AFC: state (charging: SUSPENDED, out of assignment)
  Note over AFC: RobotStateChanged, FleetCapacityChanged (robots_charging + 1)
  R->>AFC: state (battery >= CHARGE_RESUME_PERCENT, mission done)
  AFC->>R: instantActions stopCharging (stop-uuid), resent after ORDER_RESEND_AFTER while unanswered
  R->>AFC: state (charging false: IDLE, assignable again)
```

## Recovery after a restart

```mermaid
sequenceDiagram
  participant AFC as cmd/api (new process)
  participant DB as Postgres
  participant B as MQTT broker
  participant R as robot
  AFC->>DB: migrations, pool
  AFC->>B: connect, subscribe connection, factsheet, state
  B-->>AFC: retained connection and factsheet of every robot
  AFC->>DB: Recover: missions of another map version end MAP_VERSION_RETIRED
  AFC->>R: stateRequest (every known robot), cancelOrder for each retired active leg
  R->>AFC: state
  alt the robot reports a pending order fleet control does not own
    AFC->>R: cancelOrder recover-uuid, no order until it is answered
  else the order is an open leg of this map version
    AFC->>AFC: track it as before the restart (resend if unacknowledged)
  end
```
