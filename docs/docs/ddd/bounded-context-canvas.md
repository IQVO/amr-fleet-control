---
id: bounded-context-canvas
title: Bounded Context Canvas
sidebar_label: Bounded Context Canvas
description: "The ddd-crew Bounded Context Canvas of amr-fleet-control: purpose, classification, roles, communication, language, business decisions D3-D20, assumptions and open questions."
---

# Bounded Context Canvas

## Name and purpose

**amr-fleet-control.** Execute transport legs with a fleet of autonomous mobile robots, safely and without
deadlocks: the "fleet control system" of VDA 5050 (assignment of orders, detection and resolution of blockages,
traffic control).

## Strategic classification

| Dimension | Value |
| --- | --- |
| Domain | Generic (built to the standard so a vendor fleet manager could replace it) |
| Business model | Compliance with the standard; enabler of the capacity loop |
| Evolution | Commodity (VDA 5050 fleet managers are products) |

## Domain roles

Execution context (it carries out legs another context decides) and gateway to a standard (VDA 5050 over MQTT).

## Inbound communication

| From | Message | Channel |
| --- | --- | --- |
| material-flow-control | request a leg, cancel a leg, read a leg / the fleet / a robot | REST (relationship 4) |
| the robots | `connection`, `factsheet`, `state` | MQTT, VDA 5050 v3.0.0 (relationship 6) |
| warehouse-ops-agent, warehouse-console | reads | MCP, A2A (read-only) |
| facility-layout | (deferred: a map file instead) | (relationship 3) |

## Outbound communication

| To | Message | Channel |
| --- | --- | --- |
| the robots | `order`, `instantActions` | MQTT, VDA 5050 v3.0.0 |
| material-flow-control | `LegAssigned`, `ContainerPickedUp`, `LegCompleted`, `LegFailed`, `FleetCapacityChanged` | Kafka |
| exception-management | `DeadlockDetected`, `LegFailed` `ROBOT_BLOCKED` | Kafka (relationship 8) |
| (no consumer yet) | `RobotStateChanged` | Kafka |

## Ubiquitous language

Robot, Mission, Order, Node, Edge, Base, Horizon, Action, Factsheet, ChargingStation, Leg, Handoff point, Map
version, Reservation, Wait, Wait-for graph, Deadlock, PackML state, Capacity: see
[ubiquitous language](/docs/ddd/ubiquitous-language).

## Business decisions (as built)

| # | Decision |
| --- | --- |
| D3 | A robot holds at most one mission; fleet control keeps the leg queue; a robot is offered a mission only when IDLE, with no cancel in flight and standing on a map node. |
| D4 | One leg = one mission = one VDA 5050 order; the leg id is the `orderId`. A failed leg is never moved to another robot: material-flow-control retries. CHARGE and PARK missions (`ms-<uuid>`) publish no leg event. |
| D5 | Relationship 4 mirrors asrs-control's relationship 5: `202` or `422` at once, cancellation as a sub-resource (`200` / `202` / `409`), every POST idempotent. |
| D6 | Assignment per queued leg in acceptance order: shortest route to the pickup, then higher battery, then lower robot key; never below `CHARGE_LOW_PERCENT`. |
| D7 | Traffic control by reservation: a robot holds its node; the base reaches at most `RELEASE_AHEAD_NODES` past it and every new stretch is reserved all or nothing; passed elements are released on the next `state`; edges are undirected corridors; a robot whose next node is held waits at its base end. |
| D8 | `orderUpdateId` from 0, +1 per base extension and re-route; a resend repeats it; an update starts at the previous base end. |
| D9 | Every new wait-for cycle publishes one `DeadlockDetected` and re-routes the youngest mission around held nodes; a robot waiting `REROUTE_AFTER` is re-routed; a cycle older than `DEADLOCK_TIMEOUT` fails its youngest leg `ROBOT_BLOCKED` and cancels that order. |
| D10 | An idle robot below the threshold gets a CHARGE mission to the nearest free charger ending in `startCharging`, stays `SUSPENDED` until `CHARGE_RESUME_PERCENT`, then gets `stopCharging` (resent while unanswered). |
| D11 | An idle robot on a port, station or waypoint gets a PARK mission to the nearest free parking node. |
| D12 | PackML robot states `STOPPED`, `HELD`, `SUSPENDED`, `EXECUTE`, `IDLE`; `RobotStateChanged` on a state or connection change, never for a battery tick; stale after `ROBOT_STALE_AFTER`. |
| D13 | The registry is learned from the robots' own `connection`, `state` and `factsheet`. |
| D14 | One map version per process; missions pinned to it; at boot every open mission of another version ends `MAP_VERSION_RETIRED` and the old reservations are dropped. |
| D15 | Event entities `mission`, `robot`, `fleet`, `fleetmap`; `ContainerPickedUp` added to the context page's outbound list. |
| D16 | Every accepted leg ends in exactly one `LegCompleted` or `LegFailed` with reason `CANCELLED`, `ROBOT_BLOCKED`, `ROBOT_FAULT`, `ROBOT_OFFLINE`, `ORDER_REJECTED`, `PICK_FAILED`, `DROP_FAILED` or `MAP_VERSION_RETIRED`, plus `robot_error` and `container_on_robot`. |
| D17 | Cancellation: QUEUED at once (`200`); ASSIGNED -> `cancelOrder` (`202`, `CANCEL_REQUESTED`) ending `CANCELLED` on the robot's answer (FINISHED or FAILED) or the cancel's own timeout; PICKED_UP `409 leg-not-cancellable`; ended `409 leg-ended`. A `CANCEL_REQUESTED` leg reached by a timeout or a map retirement ends `CANCELLED` with the robot's reported load. |
| D18 | One MQTT link, one ordered worker (messages and the 1 s tick), one replica, every write under the fleet-row lock and the serial unit of work. |
| D19 | No inbox: a `state` is a full snapshot; acceptance = the `orderId` and `orderUpdateId` show; rejection = the rejection count rises while unacknowledged, or `ORDER_RESEND_LIMIT` resends; resend after `ORDER_RESEND_AFTER`; an unowned pending order gets `cancelOrder`; a robot with a cancel in flight is offered nothing. |
| D20 | Capacity is a snapshot of seven counts, published as `FleetCapacityChanged` on every count change, never periodically. |

## Assumptions

- The robots conform to VDA 5050 v3.0.0 as the equipment emulator implements it (the e2e test proves it against
  `v0.1.0`).
- The map file and the robots' world name the same nodes (AS/RS port nodes = the world's port `nodeId`s).
- One `cmd/api` replica controls the fleet; MQTT delivers one robot's messages in order.

## Open questions

| # | Question | This context's answer | Still open for |
| --- | --- | --- | --- |
| Q1 | VDA 5050 version | v3.0.0 only (D2, ADR 0002) | reopen when a v2 robot exists |
| Q2 | Map ownership | a deployment-owned map file (D1); the facility-layout ACL is a later plan | facility-layout: travel-graph events with coordinates and AMR node kinds, or an AMR overlay kept here |
| Q3 | No pickup fact on the context page | `ContainerPickedUp` added (D15) | warehouse-docs: add it to the context page and message-flows flow 2 |
| Q4 | How capacity is measured | seven counts on every change (D20) | material-flow-control's `SubsystemCapacityChanged` aggregation |
