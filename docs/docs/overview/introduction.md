---
id: introduction
title: Introduction
sidebar_label: Introduction
description: "What amr-fleet-control is, what it owns and what it leaves to other contexts."
---

# amr-fleet-control

`amr-fleet-control` is the Generic VDA 5050 master control of the warehouse-systems WCS tier. It runs a fleet of
autonomous mobile robots for one customer, `material-flow-control`, which asks it to move one container from one
handoff point (an AS/RS port or a station) to another: a **leg**.

## What it does

| Capability | How |
| --- | --- |
| Mission assignment | Each queued leg, in acceptance order, goes to the assignable robot with the shortest route to the pickup, then the higher battery, then the lower robot key; a robot below `CHARGE_LOW_PERCENT` is never assigned. |
| Path and traffic control | The route is released as VDA 5050 *base* (at most `RELEASE_AHEAD_NODES` past the robot) and *horizon*; every released node and edge is reserved for exactly one robot. |
| Deadlock handling | Robots waiting on each other in a cycle raise one `DeadlockDetected`; the youngest mission is re-routed, and fails `ROBOT_BLOCKED` after `DEADLOCK_TIMEOUT_SECONDS`. |
| Charging | An idle robot below the threshold drives to the nearest free charger and stays out of assignment until `CHARGE_RESUME_PERCENT`. |
| Robot registry | Connection, PackML state, errors and factsheet of every robot, learned from the robots' own VDA 5050 messages. |
| Map | One versioned node-and-edge map file per process; every mission is pinned to its version. |

Every accepted leg ends in exactly one of `LegCompleted` or `LegFailed` with a reason.

## What it does not own

Which container moves when (material-flow-control, from the WES), the container's contents (inventory-storage), the
AS/RS (asrs-control) and the building's travel graph (facility-layout; the ACL that translates it is a later plan).
Nodes, edges, orders, base and horizon never reach the WES.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company,
and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.
