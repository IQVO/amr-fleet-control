---
id: events
title: Events
sidebar_label: Events
description: "The two topics, the CloudEvents envelope and the seven published types with one example each, from apis/asyncapi.yaml."
---

# Events

Source of truth: `apis/asyncapi.yaml` (AsyncAPI 2.6.0, version 1.0.0). amr-fleet-control publishes seven CloudEvents
types and consumes no Kafka topic.

## Topics

| Topic | What |
| --- | --- |
| `warehouse.amr-fleet-control.events` | The integration topic, written by the outbox relay. Kafka key = `subject`. |
| `warehouse.amr-fleet-control.analytics` | The analytics twin of every integration event: same `id`, `type` and `data`, `dataschema` `urn:warehouse:amr-fleet-control:analytics:<EventName>:v1`. Not an integration contract. |

## Envelope

CloudEvents 1.0 structured mode, header `content-type: application/cloudevents+json; charset=UTF-8`.
`specversion` `1.0`, `id` (a UUID, the same on both topics), `source` `/warehouse/amr-fleet-control`, `type`
`com.warehouse.wes.amr-fleet-control.<entity>.<EventName>`, `subject`, `time`, `datacontenttype` `application/json`,
`dataschema` `urn:warehouse:amr-fleet-control:events:<EventName>:v1`, `data` (snake_case). Consumers dispatch on the full
`type`, ignore unknown types and dedupe on `id`. A fact written in an HTTP request's own unit of work also carries
a `traceparent` Kafka header.

`time` and every `*_at` field are amr-fleet-control's clock. `robot_timestamp` is the VDA 5050 header timestamp of
the robot `state` the fact came from (the emulator's simulated time, ahead of wall time at `SIM_SPEED` > 1).

| Type | Entity | Subject |
| --- | --- | --- |
| `com.warehouse.wes.amr-fleet-control.mission.LegAssigned` | `mission` | the leg id |
| `com.warehouse.wes.amr-fleet-control.mission.ContainerPickedUp` | `mission` | the leg id |
| `com.warehouse.wes.amr-fleet-control.mission.LegCompleted` | `mission` | the leg id |
| `com.warehouse.wes.amr-fleet-control.mission.LegFailed` | `mission` | the leg id |
| `com.warehouse.wes.amr-fleet-control.robot.RobotStateChanged` | `robot` | `<manufacturer>/<serialNumber>` |
| `com.warehouse.wes.amr-fleet-control.fleet.FleetCapacityChanged` | `fleet` | the fleet id |
| `com.warehouse.wes.amr-fleet-control.fleetmap.DeadlockDetected` | `fleetmap` | the fleet id |

## LegAssigned

A robot was chosen for the leg and its order went out. `type` = `com.warehouse.wes.amr-fleet-control.mission.LegAssigned`; subject and key = the leg id.

Example (Leg leg-7f3a (container C-0001, ST1 to ASRS1-IN1) assigned to EMU/R1):

```json
{
  "specversion": "1.0",
  "id": "11111111-2222-4333-8444-555555555555",
  "source": "/warehouse/amr-fleet-control",
  "type": "com.warehouse.wes.amr-fleet-control.mission.LegAssigned",
  "subject": "leg-7f3a",
  "time": "2026-11-20T08:00:01Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:warehouse:amr-fleet-control:events:LegAssigned:v1",
  "data": {
    "leg_id": "leg-7f3a",
    "fleet_id": "AMR1",
    "container_id": "C-0001",
    "from_handoff_point_id": "ST1",
    "to_handoff_point_id": "ASRS1-IN1",
    "reference": "tl-9",
    "robot": {
      "manufacturer": "EMU",
      "serial_number": "R1"
    },
    "map_version": "v1",
    "assigned_at": "2026-11-20T08:00:01Z"
  }
}
```

`x-no-consumer-reason`: pending: material-flow-control (WCS plan 04) is the intended consumer; its asyncapi declares this type when it is built, and this reason is deleted in that wave.

## ContainerPickedUp

The robot's pick action finished at the leg's from handoff point; the container is on the robot. `type` = `com.warehouse.wes.amr-fleet-control.mission.ContainerPickedUp`; subject = the leg id. material-flow-control confirms an AS/RS port pickup to asrs-control with it (decision D15).

Example (C-0001 left station ST1 on EMU/R1):

```json
{
  "specversion": "1.0",
  "id": "21111111-2222-4333-8444-555555555555",
  "source": "/warehouse/amr-fleet-control",
  "type": "com.warehouse.wes.amr-fleet-control.mission.ContainerPickedUp",
  "subject": "leg-7f3a",
  "time": "2026-11-20T08:01:10Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:warehouse:amr-fleet-control:events:ContainerPickedUp:v1",
  "data": {
    "leg_id": "leg-7f3a",
    "fleet_id": "AMR1",
    "container_id": "C-0001",
    "from_handoff_point_id": "ST1",
    "reference": "tl-9",
    "robot": {
      "manufacturer": "EMU",
      "serial_number": "R1"
    },
    "picked_up_at": "2026-11-20T08:01:10Z",
    "robot_timestamp": "2026-11-20T08:11:40Z"
  }
}
```

`x-no-consumer-reason`: pending: material-flow-control (WCS plan 04) is the intended consumer; its asyncapi declares this type when it is built, and this reason is deleted in that wave.

## LegCompleted

The robot's drop action finished at the leg's to handoff point. `type` = `com.warehouse.wes.amr-fleet-control.mission.LegCompleted`; subject = the leg id. One of the two outcomes of every accepted leg (responsibility 6).

Example (C-0001 dropped at ASRS1-IN1):

```json
{
  "specversion": "1.0",
  "id": "31111111-2222-4333-8444-555555555555",
  "source": "/warehouse/amr-fleet-control",
  "type": "com.warehouse.wes.amr-fleet-control.mission.LegCompleted",
  "subject": "leg-7f3a",
  "time": "2026-11-20T08:02:30Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:warehouse:amr-fleet-control:events:LegCompleted:v1",
  "data": {
    "leg_id": "leg-7f3a",
    "fleet_id": "AMR1",
    "container_id": "C-0001",
    "to_handoff_point_id": "ASRS1-IN1",
    "reference": "tl-9",
    "robot": {
      "manufacturer": "EMU",
      "serial_number": "R1"
    },
    "completed_at": "2026-11-20T08:02:30Z",
    "robot_timestamp": "2026-11-20T08:25:00Z"
  }
}
```

`x-no-consumer-reason`: pending: material-flow-control (WCS plan 04) is the intended consumer; its asyncapi declares this type when it is built, and this reason is deleted in that wave.

## LegFailed

The leg ended without its drop, or was cancelled; the other outcome of every accepted leg. `type` = `com.warehouse.wes.amr-fleet-control.mission.LegFailed`; subject = the leg id. exception-management opens a case for reason ROBOT_BLOCKED. `container_on_robot` is true when the leg failed after the pickup.

Example (The container was not at ST1):

```json
{
  "specversion": "1.0",
  "id": "41111111-2222-4333-8444-555555555555",
  "source": "/warehouse/amr-fleet-control",
  "type": "com.warehouse.wes.amr-fleet-control.mission.LegFailed",
  "subject": "leg-8b21",
  "time": "2026-11-20T08:03:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:warehouse:amr-fleet-control:events:LegFailed:v1",
  "data": {
    "leg_id": "leg-8b21",
    "fleet_id": "AMR1",
    "container_id": "C-0002",
    "from_handoff_point_id": "ST1",
    "to_handoff_point_id": "ASRS1-IN1",
    "robot": {
      "manufacturer": "EMU",
      "serial_number": "R2"
    },
    "reason": "PICK_FAILED",
    "robot_error": "LOAD_NOT_AVAILABLE",
    "container_on_robot": false,
    "failed_at": "2026-11-20T08:03:00Z"
  }
}
```

`x-no-consumer-reason`: pending: material-flow-control (WCS plan 04) and exception-management (WCS plan 05, reason ROBOT_BLOCKED) are the intended consumers; deleted when the first of them declares this type.

## RobotStateChanged

A robot's PackML state or connection changed (never a battery tick). `type` = `com.warehouse.wes.amr-fleet-control.robot.RobotStateChanged`; subject = `<manufacturer>/<serialNumber>`.

Example (EMU/R1 is blocked by an obstacle):

```json
{
  "specversion": "1.0",
  "id": "51111111-2222-4333-8444-555555555555",
  "source": "/warehouse/amr-fleet-control",
  "type": "com.warehouse.wes.amr-fleet-control.robot.RobotStateChanged",
  "subject": "EMU/R1",
  "time": "2026-11-20T08:04:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:warehouse:amr-fleet-control:events:RobotStateChanged:v1",
  "data": {
    "fleet_id": "AMR1",
    "robot": {
      "manufacturer": "EMU",
      "serial_number": "R1"
    },
    "state": "HELD",
    "previous_state": "EXECUTE",
    "connection": "ONLINE",
    "battery_percent": 64.5,
    "charging": false,
    "error_types": [
      "ROBOT_BLOCKED"
    ],
    "changed_at": "2026-11-20T08:04:00Z",
    "robot_timestamp": "2026-11-20T08:40:00Z"
  }
}
```

`x-no-consumer-reason`: none yet: material-flow-control (WCS plan 04) skips this type and reads fleet capacity from FleetCapacityChanged; this reason is deleted when a consumer declares this type.

## FleetCapacityChanged

A count of the fleet's capacity snapshot changed (published on change, never periodically). `type` = `com.warehouse.wes.amr-fleet-control.fleet.FleetCapacityChanged`; subject = the fleet id. material-flow-control aggregates it into SubsystemCapacityChanged.

Example (One of three robots went offline):

```json
{
  "specversion": "1.0",
  "id": "61111111-2222-4333-8444-555555555555",
  "source": "/warehouse/amr-fleet-control",
  "type": "com.warehouse.wes.amr-fleet-control.fleet.FleetCapacityChanged",
  "subject": "AMR1",
  "time": "2026-11-20T08:05:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:warehouse:amr-fleet-control:events:FleetCapacityChanged:v1",
  "data": {
    "fleet_id": "AMR1",
    "robots_total": 3,
    "robots_online": 2,
    "robots_available": 1,
    "robots_charging": 0,
    "robots_held": 0,
    "legs_queued": 2,
    "legs_active": 1,
    "changed_at": "2026-11-20T08:05:00Z"
  }
}
```

`x-no-consumer-reason`: pending: material-flow-control (WCS plan 04) is the intended consumer; its asyncapi declares this type when it is built, and this reason is deleted in that wave.

## DeadlockDetected

Robots wait on each other in a cycle; published once per cycle. `type` = `com.warehouse.wes.amr-fleet-control.fleetmap.DeadlockDetected`; subject = the fleet id. For exception-management. `node_ids[i]` is the node `robots[i]` waits for.

Example (EMU/R1 and EMU/R2 face each other in one corridor):

```json
{
  "specversion": "1.0",
  "id": "71111111-2222-4333-8444-555555555555",
  "source": "/warehouse/amr-fleet-control",
  "type": "com.warehouse.wes.amr-fleet-control.fleetmap.DeadlockDetected",
  "subject": "AMR1",
  "time": "2026-11-20T08:06:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:warehouse:amr-fleet-control:events:DeadlockDetected:v1",
  "data": {
    "fleet_id": "AMR1",
    "deadlock_id": "dl-1c2d",
    "robots": [
      {
        "manufacturer": "EMU",
        "serial_number": "R1"
      },
      {
        "manufacturer": "EMU",
        "serial_number": "R2"
      }
    ],
    "node_ids": [
      "M2",
      "M1"
    ],
    "detected_at": "2026-11-20T08:06:00Z"
  }
}
```

`x-no-consumer-reason`: pending: exception-management (WCS plan 05) is the intended consumer; its asyncapi declares this type when that change lands, and this reason is deleted then.
