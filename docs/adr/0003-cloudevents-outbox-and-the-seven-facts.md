# 0003. CloudEvents through a transactional outbox: the seven facts

Date: 2026-11-20

## Status

Accepted

## Context

material-flow-control drives every leg from amr-fleet-control's facts (WCS context map relationship 4,
"progress on events"), exception-management opens cases from `DeadlockDetected` and `LegFailed` with reason
`ROBOT_BLOCKED` (context page, Outbound), and the fleet's Event Standard fixes the envelope.

## Decision

1. Seven facts, entity = the raising aggregate: `mission` (LegAssigned, ContainerPickedUp, LegCompleted,
   LegFailed; subject = leg id), `robot` (RobotStateChanged; subject `<manufacturer>/<serialNumber>`), `fleet`
   (FleetCapacityChanged; subject = fleet id), `fleetmap` (DeadlockDetected; subject = fleet id).
   `ContainerPickedUp` is added to the context page's outbound list (plan 03 Q3).
2. Every event is written in the use case's unit of work to `outbox_events`, twice: `warehouse.amr-fleet-control.events`
   and `.analytics`, one CloudEvents `id`. The relay (copied from asrs-control) sends with kafka-go `BatchTimeout`
   10 ms; the default 1 s was paid per row and stalled asrs-control's relay.
3. `time` and `*_at` are this context's clock; `robot_timestamp` is the robot's VDA 5050 header time.
4. No VDA 5050 vocabulary leaves the WCS except node ids in DeadlockDetected (subdomains rule 3).
5. amr-fleet-control consumes no Kafka topic.

### Published types

The type catalogue other contexts read (`apis/asyncapi.yaml` is the contract; the architecture sensor keeps the two equal):

| `type` | `subject` | Consumers |
| --- | --- | --- |
| `com.warehouse.wes.amr-fleet-control.mission.LegAssigned` | leg id | material-flow-control |
| `com.warehouse.wes.amr-fleet-control.mission.ContainerPickedUp` | leg id | material-flow-control |
| `com.warehouse.wes.amr-fleet-control.mission.LegCompleted` | leg id | material-flow-control |
| `com.warehouse.wes.amr-fleet-control.mission.LegFailed` | leg id | material-flow-control; exception-management (reason `ROBOT_BLOCKED`) |
| `com.warehouse.wes.amr-fleet-control.robot.RobotStateChanged` | `<manufacturer>/<serialNumber>` | none yet (material-flow-control, plan 04, skips it; designed for its capacity view) |
| `com.warehouse.wes.amr-fleet-control.fleet.FleetCapacityChanged` | fleet id | material-flow-control (capacity loop) |
| `com.warehouse.wes.amr-fleet-control.fleetmap.DeadlockDetected` | fleet id | exception-management |

## Consequences

Facts are never lost between a commit and Kafka; consumers dedupe on `id`. A new fact is a new AsyncAPI message
and golden case.
