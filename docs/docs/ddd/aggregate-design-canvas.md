---
id: aggregate-design-canvas
title: Aggregate Design Canvas
sidebar_label: Aggregate Design Canvas
description: "FleetMap, Robot and Mission: the six invariants, which aggregate enforces each, their commands and events, and the fleet-row lock as the consistency boundary."
---

# Aggregate Design Canvas

## The six invariants (context page "Responsibilities")

| # | Invariant | Enforced by |
| --- | --- | --- |
| 1 | A robot executes one order at a time. | `robot.Robot.Assign` refuses a second mission; `Assignable` requires `IDLE` and no cancel in flight. |
| 2 | A node or edge in a released base is reserved by exactly one robot. | `fleetmap.FleetMap.Reserve` (all or nothing; refuses an element another robot holds), `Retain`, `ReleaseRobot`. |
| 3 | `orderUpdateId` only increases within one order. | `mission.Mission.Extend` and `Reroute` (+1 each; `Resent` keeps it). |
| 4 | A robot below the charge threshold is never assigned a leg. | `robot.Robot.Assignable(low)`; the scheduler assigns legs only to `dispatch.Choose` candidates built from assignable robots. |
| 5 | Every mission is pinned to one map version. | `mission.NewLeg` / `NewInternal` require it; `fleetmap.Rehydrate` refuses reservations of another version; `Recover` ends missions of a retired version. |
| 6 | Every accepted leg ends in exactly one of `LegCompleted` or `LegFailed`, with a reason. | `mission.Mission` transitions: `Complete`, `Fail`, `RequestCancel` (queued), `ConfirmCancel` refuse an ended mission; `Fail` requires a reason. |

## FleetMap (`internal/domain/fleetmap`)

| | |
| --- | --- |
| Identity | the fleet (one per map file); its state lives in `fleets.reservations` |
| State | node and edge holders, waits (robot -> node, blocker, since), the deadlocks already published |
| Commands | `Reserve`, `Retain`, `ReleaseRobot`, `SetWait`, `ClearWait`, `DetectDeadlocks` |
| Queries | `NodeHolder`, `EdgeHolder`, `Held`, `WaitOf`, `Cycles`, `CycleSince`; the topology `Map` (`Route` = Dijkstra with an avoid set, `Nearest`, `NodeOfHandoff`) |
| Events | `DeadlockDetected` |

## Robot (`internal/domain/robot`)

| | |
| --- | --- |
| Identity | `robot.ID` (`manufacturer`, `serialNumber`); row `robots.robot_key` |
| State | connection, last report (battery, charging, errors, loads, action states, position, `lastNodeId`), node, mission id, awaited instant action and since when, stale flag, held/offline since, factsheet, stop-charging mark |
| Commands | `ApplyConnection`, `ApplyReport`, `ApplyFactsheet`, `MarkStale`, `Assign`, `Release`, `AwaitAction`, `MarkStopChargeSentAt` |
| Queries | `State` (derived PackML), `Online`, `Assignable`, `NeedsCharge`, `Charged`, `StopChargeOverdue` |
| Events | `RobotStateChanged` (state or connection changed) |

## Mission (`internal/domain/mission`)

| | |
| --- | --- |
| Identity | `leg-<uuid>` (a leg) or `ms-<uuid>` (CHARGE, PARK); row `missions.mission_id` |
| State | kind, status, map version, container, from/to handoff points and their nodes, reference, robot, route steps, base end, reached index, order start, `orderUpdateId`, sent / resends / sent-at, reason, robot error, timestamps |
| Commands | `Assign`, `MarkSent`, `Resent`, `Seen`, `Extend`, `Reroute`, `Progress`, `PickUp`, `Complete`, `Fail`, `RequestCancel`, `ConfirmCancel` |
| Events | `LegAssigned`, `ContainerPickedUp`, `LegCompleted`, `LegFailed` (legs only; internal missions raise none) |

The policies `capacity` (snapshot and `FleetCapacityChanged` on a count change) and `dispatch` (`Choose`, `Plan`,
`Window`, `NearestFree`) are pure functions over these aggregates.

## The consistency boundary: the fleet row

The three aggregates change TOGETHER (an assignment reserves nodes, gives the robot a mission and assigns the leg),
so the transactional boundary is the fleet, not one aggregate: every write locks `fleets` (`SELECT ... FOR UPDATE`),
loads the reservations, every robot and every open mission, and saves the changed ones in the same transaction as
the events. With one worker per process and the serial unit of work in front of it, writes are serial; the lock keeps
them serial even across processes. The fleet is small (tens of robots), so the whole-fleet load is cheap.
