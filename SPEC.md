# SPEC.md — amr-fleet-control

The agent entry point is `CLAUDE.md` (`AGENTS.md` is a symlink to it). This file is the longer description of what
amr-fleet-control is and does, as built on the feature branch. The wire contracts are `apis/openapi.yaml` (REST) and
`apis/asyncapi.yaml` (Kafka events); the robot contract is VDA 5050 v3.0.0 as IQVO/equipment-emulator `v0.1.0`
implements it (`docs/vda5050-subset.md`); the decisions are `docs/adr/0001`-`0006`. When this file and a contract or
an ADR disagree, the contract or ADR wins and this file is fixed. The documentation site (`docs/`, published to
https://iqvo.github.io/amr-fleet-control/ from `main`) has the long form of every section.

## Role

amr-fleet-control is the VDA 5050 master control bounded context of the warehouse-systems WCS tier (warehouse-docs
`docs/wcs/amr-fleet-control.md`; ADR 0001). Classification **Generic**: fleet managers ship with the robots, so this
one is built to the standard and behind the **subsystem-controller interface** a vendor fleet manager could implement
instead. CloudEvents segment `wes`. It is step 3 of the WCS build order, after the equipment emulator and asrs-control.

It **owns** the leg queue, the assignment of legs to robots, the node-and-edge map (one versioned file per process),
the reservation of every released node and edge, deadlock detection and resolution, charging and parking, and the
registry of robot connection, PackML state, errors and factsheet.

It **does not own** which container moves and when (material-flow-control, from the WES), container contents
(inventory-storage), the AS/RS (asrs-control) or the building's travel graph (facility-layout; the ACL is a later
plan, decision D1). No node, edge, order, base or horizon reaches the WES.

## Guarantees (responsibilities 1-6 of the context page)

1. A robot executes one order at a time.
2. A node or edge in a released base is reserved by exactly one robot.
3. `orderUpdateId` only increases within one order.
4. A robot below the charge threshold is never assigned a leg.
5. Every mission is pinned to one map version.
6. Every accepted leg ends in exactly one of `LegCompleted` or `LegFailed`, with a reason.

## Processes

| Binary | Port | What |
| --- | --- | --- |
| `cmd/api` | `:8080` | REST (relationship 4), the ONE MQTT fleet link (one worker for every robot message and the 1 s tick, one ordered sender), the transactional-outbox relay, the hourly idempotency purge, `/healthz` and `/readyz` (Postgres, Kafka, MQTT) |
| `cmd/mcp` | `:8090` | read-only MCP (Streamable HTTP, stateless): tools `get_fleet`, `list_open_legs`, `get_leg`, `get_robot`, resource `warehouse://amr-fleet-control/fleets/{fleetId}`, prompt `review_leg` |
| `cmd/a2a` | `:8093` | read-only A2A agent (JSON-RPC): the same four skills, the card at `/.well-known/agent-card.json` |

All three run on real infrastructure only (no in-memory runtime, `runtime_no_memory_fitness_test.go`). `cmd/api`
needs `DATABASE_URL`, `KAFKA_BROKERS`, `MQTT_URL` and `AMR_MAP_FILE`; `cmd/mcp` and `cmd/a2a` need `DATABASE_URL`
and `AMR_MAP_FILE` (and `A2A_AGENT_URL`) and read through a pool whose every transaction is `BEGIN READ ONLY`. A
missing or invalid variable is a boot error: one JSON line naming every bad variable, exit 1. The full tables are on
the site's Configuration page. `cmd/api` runs exactly ONE replica (the single traffic controller of its fleet).

## Write path

Every write (a REST use case, a robot message, a tick, the boot-time `Recover`) runs in one fleet unit of work: lock
the fleet row, load reservations, robots and open missions, change them, save them and the events (outbox rows) in
one transaction. The outermost unit is serialized in-process, so units never interleave and robot commands, sent only
from the after-commit hook, leave in commit order; a rolled-back write sends nothing.

## Interfaces

- REST: `POST /fleets/{fleetId}/legs` (`202` or an immediate refusal), `GET .../legs/{legId}`,
  `POST .../legs/{legId}/cancellation` (`200` cancelled, `202` robot asked, `409`), `GET /fleets/{fleetId}`,
  `GET /fleets/{fleetId}/robots/{manufacturer}/{serialNumber}`. Every POST takes `Idempotency-Key`; every refusal has
  a replay class in `internal/adapters/inbound/http/errors.go` (FINAL stored and replayed for 24 h, RETRYABLE releases
  the key; map-decided refusals are RETRYABLE, ruling P3-R13). No auth.
- Events (CloudEvents 1.0 via the outbox, both `warehouse.amr-fleet-control.events` and `.analytics`):
  `mission.LegAssigned`, `mission.ContainerPickedUp`, `mission.LegCompleted`, `mission.LegFailed`,
  `robot.RobotStateChanged`, `fleet.FleetCapacityChanged`, `fleetmap.DeadlockDetected`. Nothing consumed.
- Robots: VDA 5050 v3.0.0 over MQTT, `vda5050/v3/<manufacturer>/<serialNumber>/<topic>`; publishes `order` and
  `instantActions`, subscribes `connection`, `factsheet` and `state`.

## Map file

`AMR_MAP_FILE`: `fleetId`, `mapId`, `version`, `nodes` (`id`, `x`, `y`, `kind` WAYPOINT/PARKING/CHARGER/PORT/STATION,
optional `handoffPoint`) and undirected `edges` (`id`, `a`, `b`, optional `maxSpeed`). AS/RS port node ids equal the
robot world's port `nodeId`s (`config/map.example.yaml` is the emulator's example floor). A new `version` retires
every open mission of the old one at boot (`MAP_VERSION_RETIRED`).

## Quality gates

`make check-fast`, `make check` (pre-push), `make check-all`, `make coverage` (90 % over domain and application),
`make bdd`, `make arch-test`, `make integration` (Docker; the e2e test needs the emulator image
`EQUIPMENT_EMULATOR_IMAGE`, default `equipment-emulator:e2e`, built from equipment-emulator `v0.1.0`), `make mutation`.
The CI job list and the test pyramid are on the site's Testing page.
