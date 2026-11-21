# 1. amr-fleet-control is a bounded context of the warehouse fleet

Status: Accepted (2026-11-20). Implements the WCS strategic design (warehouse-docs `docs/wcs/amr-fleet-control.md`
on `main`, PR #165).

## Context

The WCS design splits the tier into `material-flow-control` (Supporting) over two subsystem controllers,
`asrs-control` (built, plan WCS-02) and `amr-fleet-control` (both Generic). amr-fleet-control is step 3 of the build
order: "VDA 5050 master control against the emulated robots, with the map from `facility-layout`".

## Decision

Create `amr-fleet-control`, Generic, CloudEvents segment `wes`, repository `IQVO/amr-fleet-control`.

- Owns: mission assignment, path and traffic control (base/horizon, reservations), deadlock handling, charging and
  parking, the robot registry, the versioned node-and-edge map. Responsibilities 1-6 of the context page are its
  guarantees.
- Does not own: which container moves, why and where it ends, station sequence and buffers (material-flow-control);
  container contents (inventory-storage); the building's structure and travel graph (facility-layout; this context
  keeps only its own map, ADR 0002); the AS/RS (asrs-control).

| Neighbour | Relationship | Edge |
| --- | --- | --- |
| material-flow-control | Customer/Supplier, amr-fleet-control supplies the subsystem-controller interface (relationship 4), the same shape as asrs-control's | REST: request a leg, cancel a leg, read a leg, the fleet and a robot. Kafka: the seven facts of ADR 0003 |
| robots (emulated by IQVO/equipment-emulator) | Conformist to VDA 5050 v3.0.0 (relationship 6) | MQTT `vda5050/v3/<manufacturer>/<serialNumber>/<topic>` (ADR 0002) |
| exception-management | Conformist on their side (relationship 8) | consumes `DeadlockDetected` and `LegFailed` reason `ROBOT_BLOCKED` |
| facility-layout | Conformist through an ACL (relationship 3) | deferred: no travel-graph event and no coordinates exist yet (ADR 0002 decision 6) |

No call to any other context; no Kafka consumer. The fleet standard is built in from the first release:
OpenTelemetry (ADR 0005), read-only MCP and A2A (ADR 0006), Kong config in the chart, a Docusaurus site.

## Consequences

- material-flow-control (plan 04) can be built against two real subsystem controllers with one interface shape.
- One more service and one more MQTT client of the broker. One replica of `cmd/api` (single traffic controller).
