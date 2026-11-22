---
id: domain-events
title: Domain events
sidebar_label: Domain events
description: "The seven published events: type, entity, subject, every data field with its rule, and when each is raised."
---

# Domain events

The domain raises plain Go values (`internal/domain/mission/events.go`, `robot.RobotStateChanged`,
`capacity.FleetCapacityChanged`, `fleetmap.DeadlockDetected`); `internal/adapters/outbound/events` encodes each one
as a CloudEvent written to the outbox for both topics in the same transaction. The field rules below are
`apis/asyncapi.yaml`'s; every required string id has `minLength: 1` (no empty id is contract-valid).
Examples: [events](/docs/api-reference/events).

## LegAssigned

- **Type** `com.warehouse.wes.amr-fleet-control.mission.LegAssigned`; entity `mission`; subject the leg id.
- **Raised** A queued leg is given to a robot (`mission.Assign`, in the scheduler's `assignAll`); its first order goes out after the commit.

| Field | Type | Required | Rule |
| --- | --- | --- | --- |
| `leg_id` | string | yes | minLength 1 |
| `fleet_id` | string | yes | minLength 1 |
| `container_id` | string | yes | minLength 1 |
| `from_handoff_point_id` | string | yes | minLength 1 |
| `to_handoff_point_id` | string | yes | minLength 1 |
| `reference` | string | no | The caller's reference from the leg request, when given. |
| `robot` | object (`manufacturer`, `serial_number`, both minLength 1) | yes |  |
| `map_version` | string | yes | minLength 1; The map version the leg is pinned to (e.g. v1). |
| `assigned_at` | string | yes | date-time |

## ContainerPickedUp

- **Type** `com.warehouse.wes.amr-fleet-control.mission.ContainerPickedUp`; entity `mission`; subject the leg id.
- **Raised** The robot's `state` reports the leg's `pick` action FINISHED (`mission.PickUp`).

| Field | Type | Required | Rule |
| --- | --- | --- | --- |
| `leg_id` | string | yes | minLength 1 |
| `fleet_id` | string | yes | minLength 1 |
| `container_id` | string | yes | minLength 1 |
| `from_handoff_point_id` | string | yes | minLength 1 |
| `reference` | string | no | The caller's reference from the leg request, when given. |
| `robot` | object (`manufacturer`, `serial_number`, both minLength 1) | yes |  |
| `picked_up_at` | string | yes | date-time |
| `robot_timestamp` | string | no | date-time; The VDA 5050 header timestamp of the robot state the fact came from (the emulator's simulated time); absent when no robot state carried it. |

## LegCompleted

- **Type** `com.warehouse.wes.amr-fleet-control.mission.LegCompleted`; entity `mission`; subject the leg id.
- **Raised** The robot's `state` reports the leg's `drop` action FINISHED (`mission.Complete`).

| Field | Type | Required | Rule |
| --- | --- | --- | --- |
| `leg_id` | string | yes | minLength 1 |
| `fleet_id` | string | yes | minLength 1 |
| `container_id` | string | yes | minLength 1 |
| `to_handoff_point_id` | string | yes | minLength 1 |
| `reference` | string | no | The caller's reference from the leg request, when given. |
| `robot` | object (`manufacturer`, `serial_number`, both minLength 1) | yes |  |
| `completed_at` | string | yes | date-time |
| `robot_timestamp` | string | no | date-time; The VDA 5050 header timestamp of the robot state the fact came from (the emulator's simulated time); absent when no robot state carried it. |

## LegFailed

- **Type** `com.warehouse.wes.amr-fleet-control.mission.LegFailed`; entity `mission`; subject the leg id.
- **Raised** A leg ends without its drop: a failure (`mission.Fail`, every reason but `CANCELLED`) or a cancellation (`RequestCancel` on a QUEUED leg, `ConfirmCancel` once the robot answered the `cancelOrder` or the cancel timed out).

| Field | Type | Required | Rule |
| --- | --- | --- | --- |
| `leg_id` | string | yes | minLength 1 |
| `fleet_id` | string | yes | minLength 1 |
| `container_id` | string | yes | minLength 1 |
| `from_handoff_point_id` | string | yes | minLength 1 |
| `to_handoff_point_id` | string | yes | minLength 1 |
| `reference` | string | no | The caller's reference from the leg request, when given. |
| `robot` | object (`manufacturer`, `serial_number`, both minLength 1) | no |  |
| `reason` | string | yes | one of `CANCELLED`, `ROBOT_BLOCKED`, `ROBOT_FAULT`, `ROBOT_OFFLINE`, `ORDER_REJECTED`, `PICK_FAILED`, `DROP_FAILED`, `MAP_VERSION_RETIRED` |
| `robot_error` | string | no | The robot's VDA 5050 errorType (e.g. LOAD_NOT_AVAILABLE), when the failure came from the robot. |
| `container_on_robot` | boolean | yes | True when the leg failed after the pickup. |
| `failed_at` | string | yes | date-time |

## RobotStateChanged

- **Type** `com.warehouse.wes.amr-fleet-control.robot.RobotStateChanged`; entity `robot`; subject `<manufacturer>/<serialNumber>`.
- **Raised** A robot's derived PackML state or its connection changes (`robot.settle`); never for a battery change alone.

| Field | Type | Required | Rule |
| --- | --- | --- | --- |
| `fleet_id` | string | yes | minLength 1 |
| `robot` | object (`manufacturer`, `serial_number`, both minLength 1) | yes |  |
| `state` | string | yes | one of `IDLE`, `EXECUTE`, `SUSPENDED`, `HELD`, `STOPPED` |
| `previous_state` | string | yes | one of `IDLE`, `EXECUTE`, `SUSPENDED`, `HELD`, `STOPPED` |
| `connection` | string | yes | one of `ONLINE`, `OFFLINE`, `CONNECTION_BROKEN` |
| `battery_percent` | number | yes | minimum 0; maximum 100 |
| `charging` | boolean | yes |  |
| `error_types` | array of string | yes |  |
| `changed_at` | string | yes | date-time |
| `robot_timestamp` | string | no | date-time; The VDA 5050 header timestamp of the robot state the fact came from (the emulator's simulated time); absent when no robot state carried it. |

## FleetCapacityChanged

- **Type** `com.warehouse.wes.amr-fleet-control.fleet.FleetCapacityChanged`; entity `fleet`; subject the fleet id.
- **Raised** Any count of the capacity snapshot changes at the end of a fleet unit of work (`capacity.Next`); never periodically.

| Field | Type | Required | Rule |
| --- | --- | --- | --- |
| `fleet_id` | string | yes | minLength 1 |
| `robots_total` | integer | yes | minimum 0 |
| `robots_online` | integer | yes | minimum 0 |
| `robots_available` | integer | yes | minimum 0 |
| `robots_charging` | integer | yes | minimum 0 |
| `robots_held` | integer | yes | minimum 0 |
| `legs_queued` | integer | yes | minimum 0 |
| `legs_active` | integer | yes | minimum 0 |
| `changed_at` | string | yes | date-time |

## DeadlockDetected

- **Type** `com.warehouse.wes.amr-fleet-control.fleetmap.DeadlockDetected`; entity `fleetmap`; subject the fleet id.
- **Raised** A NEW wait-for cycle appears (`FleetMap.DetectDeadlocks`): one event per cycle until it dissolves.

| Field | Type | Required | Rule |
| --- | --- | --- | --- |
| `fleet_id` | string | yes | minLength 1 |
| `deadlock_id` | string | yes | minLength 1 |
| `robots` | array of robot objects | yes | minItems 2 |
| `node_ids` | array of string (minLength 1) | yes | minItems 2; node_ids[i] is the node robots[i] waits for. |
| `detected_at` | string | yes | date-time |
