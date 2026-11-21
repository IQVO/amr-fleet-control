---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
description: "amr-fleet-control is Generic: why it is built anyway, the three rules that keep it replaceable, and how a vendor fleet manager would replace it."
---

# Subdomain classification

**Generic.** Robot fleet control is the textbook "buy, don't build" part of a warehouse: VDA 5050 standardises it,
and fleet managers are bought from AMR vendors. fulfillment-execution's ADR-0015 files WCS equipment control as
Generic with low differentiation and high complexity, and the WCS design keeps that verdict.

## Building a Generic subdomain on purpose

The WCS design (warehouse-docs `docs/wcs/subdomains.md`, "Building a Generic subdomain on purpose") builds the
Generic parts as part of the study, under three rules that keep them replaceable:

> 1. **Build to the standard.** `amr-fleet-control` speaks VDA 5050 to its robots, so any VDA 5050 fleet controller
>    could replace it. `asrs-control` exposes the same subsystem-controller interface `material-flow-control` uses
>    for the AMR fleet (store or move a container, report state), so a vendor AS/RS controller behind an adapter
>    could replace it.
> 2. **Keep the Supporting part thin and fleet-owned.** Only `material-flow-control` holds fleet-specific rules, and
>    only it faces the WES.
> 3. **No equipment vocabulary crosses the seam.** The WES sees containers, stations, locations and transport
>    outcomes, never a robot, node, shuttle or rack position. That is ADR-0015's rule, enforced from the WCS side.

## Why it is built here

The study has no vendor fleet manager to buy, and the capacity loop (`FleetCapacityChanged` -> material-flow-control
-> the WES releasing work against automation utilisation) needs a fleet controller that reports real state. The robot
side exists (the equipment emulator speaks the robot side of VDA 5050 v3), so the master control is built against it
and proved end to end against it.

## How a vendor would replace it

A vendor fleet manager speaks VDA 5050 to the same robots; what it must offer material-flow-control is the shape of
relationship 4, unchanged:

- the REST interface: request a leg (`POST /fleets/{fleetId}/legs`, `202` or an immediate `422`), read it, cancel it
  (`.../cancellation`, `200`/`202`/`409`), read the fleet and a robot, with `Idempotency-Key` and the replay classes;
- the seven facts with their `data` (at least the five material-flow-control consumes: `LegAssigned`,
  `ContainerPickedUp`, `LegCompleted`, `LegFailed`, `FleetCapacityChanged`).

An adapter that translates the vendor's API into that shape replaces this service without a change in
material-flow-control, because no node, edge, order or base ever crossed the seam.
