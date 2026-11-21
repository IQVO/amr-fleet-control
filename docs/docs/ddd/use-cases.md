---
id: use-cases
title: Use cases
sidebar_label: Use cases
description: "Every use case of usecases.Service: actor, preconditions, outcome, events and errors."
---

# Use cases

One application `Service` (`internal/application/usecases`). Every write runs in ONE fleet unit of work
(`Service.inFleet`: lock the fleet row, load reservations, robots and open missions, change, save, write the events to
the outbox, commit, then send the robot commands). Every write ends with `schedule` (below) unless noted. The reads
build no unit of work and work on `usecases.NewReader` too, which answers `ErrReadOnly` to every write.

## RequestLeg

| | |
| --- | --- |
| Actor | material-flow-control, `POST /fleets/{fleetId}/legs` |
| Preconditions | `fleetId` is the map's fleet; `container_id`, `from` and `to` set and different; both are handoff points of the map; a route joins them; at least one robot ONLINE; no open leg moves the container |
| Outcome | A `QUEUED` leg `leg-<uuid>` pinned to the map version; `schedule` may assign it in the same unit (`202` either way) |
| Events | `LegAssigned` when it was assigned at once; `FleetCapacityChanged` when a count changed |
| Errors | `ErrFleetNotFound`, `ErrInvalidRequest`, `ErrUnknownHandoff`, `ErrHandoffUnreachable` (checked before the unit), then `ErrFleetUnavailable`, `ErrContainerBusy` (the partial unique index `uq_missions_open_leg_container`) |

## CancelLeg

| | |
| --- | --- |
| Actor | material-flow-control, `POST /fleets/{fleetId}/legs/{legId}/cancellation` |
| Preconditions | The fleet exists; the leg exists and is a leg (not an internal mission) |
| Outcome | `QUEUED` -> `CANCELLED` at once (`200`); `ASSIGNED` -> `CANCEL_REQUESTED` and a `cancelOrder` `<leg>-cancel` to the robot (`202`); `CANCEL_REQUESTED` -> unchanged, nothing sent again (`202`) |
| Events | `LegFailed` `CANCELLED` (`container_on_robot` false) for a queued leg; later `LegFailed` `CANCELLED` when the robot answers |
| Errors | `ErrFleetNotFound`, `ErrLegNotFound`, `ErrNotCancellable` (`PICKED_UP`: the container is on the robot), `ErrLegEnded` |

## HandleConnection

| | |
| --- | --- |
| Actor | a robot, `connection` (retained) via the fleet link's worker |
| Outcome | The robot joins the registry if new; its connection is recorded (`ONLINE` refreshes `LastSeen`); what it holds is re-evaluated |
| Events | `RobotStateChanged` when the state or the connection changed; `FleetCapacityChanged` on a count change |

## HandleFactsheet

| | |
| --- | --- |
| Actor | a robot, `factsheet` (retained) |
| Outcome | The robot joins the registry if new; `seriesName`, `maxSpeed`, `maxLoadMass` are stored. No schedule. |

## HandleState

| | |
| --- | --- |
| Actor | a robot, `state` |
| Outcome | The robot joins the registry if new; the report is applied (node from `lastNodeId` or the nearest node within `NODE_TOLERANCE_METRES`, battery, errors, loads, action states); its mission is tracked: acknowledgement and progress, a rising rejection count (`ORDER_REJECTED`), a `FATAL` error (`ROBOT_FAULT`), `pick` FINISHED / FAILED, `drop` FINISHED / FAILED, the charge action, a park's arrival, the answer to a `cancelOrder`; a pending order fleet control does not own gets `cancelOrder` `recover-<uuid>`; passed nodes and edges are released |
| Events | `ContainerPickedUp`, `LegCompleted`, `LegFailed` (`ORDER_REJECTED`, `ROBOT_FAULT`, `PICK_FAILED`, `DROP_FAILED`, `CANCELLED`), `RobotStateChanged`, `FleetCapacityChanged`, `DeadlockDetected` (from `schedule`) |

## Tick

| | |
| --- | --- |
| Actor | the fleet link's worker, every 1 s, in line with the robot messages |
| Outcome, in order | stale robots -> `STOPPED`; unowned `cancelOrder`s forgotten or re-sent; legs of robots `STOPPED` past `ROBOT_OFFLINE_TIMEOUT` fail `ROBOT_OFFLINE`, of robots `HELD` past `ROBOT_BLOCKED_TIMEOUT` fail `ROBOT_BLOCKED`; unacknowledged orders resent (or failed `ORDER_REJECTED` after `ORDER_RESEND_LIMIT`); unanswered `cancelOrder`s re-sent (or the leg ended `CANCELLED`); robots waiting past `REROUTE_AFTER` re-routed; cycles older than `DEADLOCK_TIMEOUT` fail their youngest leg `ROBOT_BLOCKED`; `schedule` |
| Events | `LegFailed`, `RobotStateChanged`, `FleetCapacityChanged`, `DeadlockDetected`, `LegAssigned` |

A `CANCEL_REQUESTED` leg reached by a timeout ends `CANCELLED` with the load the robot last reported, never with the
timeout's reason.

## Recover

| | |
| --- | --- |
| Actor | `cmd/api` at boot, once, after the link connected and before its worker starts |
| Outcome | Open missions of another map version end (`MAP_VERSION_RETIRED`, or `CANCELLED` for a `CANCEL_REQUESTED` leg; an active one gets `cancelOrder`); a `stateRequest` to every known robot; `schedule` |
| Events | `LegFailed` `MAP_VERSION_RETIRED` / `CANCELLED`, `FleetCapacityChanged` |

## schedule (the policies, after every change)

In this order: extend every acknowledged base up to `RELEASE_AHEAD_NODES` past the robot when the next stretch can be
reserved, else record the wait; publish each new wait-for cycle once and re-route its youngest mission; send every
idle robot below `CHARGE_LOW_PERCENT` to the nearest free charger (CHARGE mission ending in `startCharging`); send
`stopCharging` to a charging robot at `CHARGE_RESUME_PERCENT`; assign queued legs in acceptance order to the nearest
assignable robot (route length to the pickup, then higher battery, then lower key); park every idle robot standing on
a port, station or waypoint at the nearest free parking node (PARK mission).

## The reads

| Read | Answers | Errors |
| --- | --- | --- |
| `Leg(fleetId, legId)` | one leg | `ErrFleetNotFound`, `ErrLegNotFound` (also for an internal mission id) |
| `OpenLegs(fleetId)` | the open legs in acceptance order (never an internal mission) | `ErrFleetNotFound` |
| `Fleet(fleetId)` | map id and version, sorted handoff points, every robot, live capacity counts | `ErrFleetNotFound` |
| `Robot(fleetId, id)` | one robot with its factsheet | `ErrFleetNotFound`, `ErrRobotNotFound` |
