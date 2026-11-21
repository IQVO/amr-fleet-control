---
id: class-diagram
title: Class diagram
sidebar_label: Class diagram
description: "The domain types of fleetmap, robot, mission, capacity and dispatch, as built."
---

# Class diagram

The domain packages (`internal/domain/...`) import only the standard library and each other. Fields are the
persisted state structs (`fleetmap.State`, `robot.Snapshot`, `mission.State`); the aggregates keep them private.

```mermaid
classDiagram
  direction LR
  class Map {
    fleetID, mapID, version
    nodes map~Node~
    edges map~Edge~
    +New(Spec) Map
    +Route(from, to, avoid) Path
    +Nearest(x, y, tolerance) string
    +NodeOfHandoff(h) string
    +HandoffPoints() []string
  }
  class Node {
    ID
    X, Y float64
    Kind NodeKind
    HandoffPoint
  }
  class Edge {
    ID
    A, B
    MaxSpeed float64
  }
  class Path {
    Nodes []string
    Edges []string
    Length float64
  }
  class FleetMap {
    nodes, edges (element to robot key)
    waits (robot key to Wait)
    reported deadlocks
    +Reserve(robot, nodes, edges) error
    +Retain(robot, nodes, edges)
    +ReleaseRobot(robot)
    +SetWait(robot, node, blocker, at)
    +Cycles() [][]string
    +DetectDeadlocks(at, newID) []DeadlockDetected
  }
  class Wait {
    Node, Blocker
    Since time
  }
  class DeadlockDetected {
    DeadlockID
    Robots []string
    Nodes []string
    At time
  }
  class Robot {
    ID
    Connection
    State
    Report
    Node, MissionID
    Factsheet
    AwaitingAction, AwaitingSince
    LastSeen, HeldSince, OfflineSince
    StopChargeSent, StopChargeSentAt
    +ApplyConnection() []any
    +ApplyReport() []any
    +MarkStale() []any
    +Assign(missionID, at) error
    +Release(missionID, at)
    +Online() bool
    +Assignable(low) bool
    +NeedsCharge(low) bool
  }
  class Report {
    At, OrderID, OrderUpdateID
    LastNodeID, LastNodeSeq
    X, Y, MapID
    Driving, Pending
    Battery, Charging
    LoadIDs, Errors, Actions, InstantActions
  }
  class RobotStateChanged {
    Robot ID
    State, Previous
    Connection
    Battery, Charging, ErrorTypes
    At, RobotTime
  }
  class Mission {
    ID, Kind, Status, MapVersion
    ContainerID, From, To, PickupNode, DropNode, Reference
    Robot ID
    Route []Step
    BaseEnd, Reached, OrderStart
    UpdateID, Sent, Resends, SentAt
    Reason, RobotError
    AcceptedAt, AssignedAt, PickedUpAt, EndedAt
    +Assign() []any
    +Extend(newEnd) error
    +Reroute(tail) error
    +Progress(index) error
    +PickUp() []any
    +Complete() []any
    +Fail(reason, robotError, at) []any
    +RequestCancel(at) bool
    +ConfirmCancel(onRobot, at) []any
  }
  class Step {
    Node, Edge, Action
  }
  class LegEvents {
    LegAssigned
    ContainerPickedUp
    LegCompleted
    LegFailed
  }
  class CapacitySnapshot {
    RobotsTotal, RobotsOnline, RobotsAvailable
    RobotsCharging, RobotsHeld
    LegsQueued, LegsActive
    At time
    +Compute(robots, low, missions, at)$
    +Next(prev, hadPrev, next)$
  }
  class Dispatch {
    +Choose(map, pickup, candidates)$ Candidate
    +Plan(map, start, targets, avoid)$ []Step
    +Window(route, baseEnd, reached, ahead, self, holders)$
    +NearestFree(map, from, kind, free)$ string
  }
  Map "1" *-- "many" Node
  Map "1" *-- "many" Edge
  Map ..> Path : Route
  FleetMap --> Map : one version
  FleetMap "1" *-- "many" Wait
  FleetMap ..> DeadlockDetected : raises
  Robot *-- Report
  Robot ..> RobotStateChanged : raises
  Mission "1" *-- "many" Step
  Mission ..> LegEvents : raises (legs only)
  Mission --> Robot : robot.ID
  CapacitySnapshot ..> Robot
  CapacitySnapshot ..> Mission
  Dispatch ..> Map
```

| Enum | Values |
| --- | --- |
| `fleetmap.NodeKind` | `WAYPOINT`, `PARKING`, `CHARGER`, `PORT`, `STATION` |
| `robot.Connection` | `ONLINE`, `OFFLINE`, `CONNECTION_BROKEN` |
| `robot.State` | `IDLE`, `EXECUTE`, `SUSPENDED`, `HELD`, `STOPPED` |
| `mission.Kind` | `LEG`, `CHARGE`, `PARK` |
| `mission.Status` | `QUEUED`, `ASSIGNED`, `PICKED_UP`, `CANCEL_REQUESTED`, `DONE`, `FAILED`, `CANCELLED` |
| `mission.Reason` | `CANCELLED`, `ROBOT_BLOCKED`, `ROBOT_FAULT`, `ROBOT_OFFLINE`, `ORDER_REJECTED`, `PICK_FAILED`, `DROP_FAILED`, `MAP_VERSION_RETIRED` |

## The leg state machine

As `internal/domain/mission/transitions.go` and the use cases drive it:

```mermaid
stateDiagram-v2
  [*] --> QUEUED : RequestLeg (202)
  QUEUED --> ASSIGNED : Assign / LegAssigned
  QUEUED --> CANCELLED : RequestCancel (200) / LegFailed CANCELLED
  QUEUED --> FAILED : Recover, map retired / LegFailed MAP_VERSION_RETIRED
  ASSIGNED --> PICKED_UP : pick FINISHED / ContainerPickedUp
  ASSIGNED --> CANCEL_REQUESTED : RequestCancel (202), cancelOrder sent
  ASSIGNED --> FAILED : ORDER_REJECTED, ROBOT_FAULT, PICK_FAILED, ROBOT_OFFLINE, ROBOT_BLOCKED, MAP_VERSION_RETIRED
  PICKED_UP --> DONE : drop FINISHED / LegCompleted
  PICKED_UP --> FAILED : DROP_FAILED, ROBOT_FAULT, ORDER_REJECTED, ROBOT_OFFLINE, ROBOT_BLOCKED, MAP_VERSION_RETIRED
  CANCEL_REQUESTED --> CANCELLED : cancelOrder FINISHED or FAILED, cancel timeout, offline, blocked, deadlock or map retired / LegFailed CANCELLED
  DONE --> [*]
  FAILED --> [*]
  CANCELLED --> [*]
```

`PICKED_UP` refuses a cancel (`409 leg-not-cancellable`); an ended leg refuses it with `409 leg-ended`; a second cancel
of a `CANCEL_REQUESTED` leg changes nothing (`202`). `container_on_robot` on `LegFailed` is true when the leg failed
after `PICKED_UP`, or (for a cancel) when the robot's last report lists the container as its load. CHARGE and PARK
missions start `ASSIGNED` and end `DONE` or `FAILED` without events.

## The robot state

`robot.State` is DERIVED on every connection, report or staleness change, in this priority order, and
`RobotStateChanged` is raised when the state or the connection differs from before:

```mermaid
flowchart TD
  S{"connection ONLINE,<br/>not stale, has reported?"} -- no --> STOPPED
  S -- yes --> H{"an active CRITICAL<br/>or FATAL error?"}
  H -- yes --> HELD
  H -- no --> C{"charging?"}
  C -- yes --> SUSPENDED
  C -- no --> M{"holds a mission?"}
  M -- yes --> EXECUTE
  M -- no --> IDLE
```

A new robot starts `STOPPED` until it is `ONLINE` and has sent a `state`.
