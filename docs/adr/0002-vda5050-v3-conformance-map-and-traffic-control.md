# 2. VDA 5050 v3 conformance, the map file and reservation-based traffic control

Status: Accepted (2026-11-20). Resolves the handoff's open question "VDA 5050 version — amr-fleet-control ADR
0001" (recorded here, next to the conformance rules it shapes).

## Decisions

1. **VDA 5050 v3.0.0 only, Conformist.** The design cites v3.0.0, relationship 6 is "Conformist to VDA 5050 v3",
   and the only robot side that exists (IQVO/equipment-emulator) implements a v3 subset. Supporting v2 robots is a
   scope choice the design leaves open; it is declined until a v2 robot exists, and would be a second codec behind
   the same `ports.Downlink`. amr-fleet-control's `internal/adapters/vda5050/messages.go` is the emulator's file,
   byte for byte; the translation (`codec.go`) is this repo's.
2. **One link, one worker.** One paho client subscribes `vda5050/v3/+/+/state`, `/connection` and `/factsheet`
   with `OrderMatters(true)`; callbacks only enqueue; one worker applies messages and the 1 s fleet tick in order;
   every write locks the fleet row. No inbox: a `state` is a full snapshot; an order is accepted when a `state`
   shows its `orderId`/`orderUpdateId`; it is rejected when the number of `VALIDATION_FAILURE`/`INVALID_ORDER`
   entries rises between two states while it is unacknowledged (the emulator appends one per rejection and clears
   the list on acceptance), or after `ORDER_RESEND_LIMIT` (3) resends without acknowledgement; it is resent with
   the same `orderUpdateId` after `ORDER_RESEND_AFTER` (5 s). A robot reporting a pending order no mission owns
   (after a restart, or a leg failed while it was offline) gets `cancelOrder`; a robot with a cancel in flight is
   never offered an order, and no order is resent to it. An unanswered `cancelOrder` is re-sent with the same
   action id after `ORDER_RESEND_AFTER`; after `ORDER_RESEND_LIMIT` re-sends its leg ends `CANCELLED` with the
   load the robot last reported, and a robot still reporting the pending order gets a fresh `cancelOrder`. A
   `CANCEL_REQUESTED` leg whose robot goes offline or stays blocked, or whose map version is retired, also ends
   `CANCELLED` with the load the robot last reported, never with another failure reason.
3. **Reservations.** A robot always holds the node it stands on. A mission's base reaches at most
   `RELEASE_AHEAD_NODES` nodes past the robot's last node; a node or edge enters the base only once reserved for that
   robot; passed elements are released on the next `state`. Edges are undirected corridors. `orderUpdateId` rises by
   one per extension or re-route.
4. **Deadlocks** are cycles of waits: each new cycle publishes one `DeadlockDetected` and re-routes its youngest
   mission around nodes held by others; a robot waiting longer than `REROUTE_AFTER` (10 s) for a node another
   robot holds is re-routed the same way; a cycle older than `DEADLOCK_TIMEOUT` (60 s) fails its youngest leg
   `ROBOT_BLOCKED` and cancels its order, which frees its base once the robot no longer reports it pending. A
   robot without a mission that a cancel stopped mid-edge keeps the edge it stands on and that edge's far node
   until it reports standing on a node again; a robot without a mission that still reports a pending order while
   its `cancelOrder` is unanswered keeps everything it holds.
5. **Charging and parking** are internal missions (publish nothing): below 20 % an idle robot drives to the nearest
   free charger and charges to 80 %; an idle robot off a parking or charger node drives to the nearest free parking.
6. **The map is a file.** `AMR_MAP_FILE` holds `fleetId`, `mapId`, `version`, nodes with x/y and kind, undirected
   edges, and the handoff point names of port and station nodes. facility-layout's travel graph is a per-zone REST
   read whose nodes carry no coordinates and that no event publishes, while VDA 5050 needs a `nodePosition` on every
   node: the ACL of relationship 3 is a later plan, after facility-layout registers the AS/RS ports, stations and
   coordinates (plan 05a). Port node ids equal the emulator world's port `nodeId`s.
7. **One map version per process.** Missions record it; at boot open missions of another version fail
   `MAP_VERSION_RETIRED` and the old version's reservations are dropped.

## Known limits

- The emulator has no collision model: reservations are the only thing keeping robots apart, and the e2e test can
  only prove the reservation bookkeeping, not physics.
- Order rejection is inferred from the `state` (no reply topic) by a rising count of rejection entries. The emulator
  keeps only the newest 50 errors, so a rejection reported on a full list does not raise the count; such an order
  stays unacknowledged until the resend limit fails it `ORDER_REJECTED` (about 20 s later).
- A `CANCEL_REQUESTED` leg's `cancelOrder` is re-sent up to `ORDER_RESEND_LIMIT` times. Every other `cancelOrder`
  (one sent as a mission ends failed, or one for an order no mission owns, `recover-` action ids) has no open
  mission behind it; the robot records when it was sent. Until the robot answers it, the robot is never offered an
  order. If it is lost, the tick forgets it once the robot's latest `state` neither shows a pending order nor lists
  that action: nothing is left to cancel, and anything sent later reaches the robot after the cancel. A lost cancel
  of a robot still reporting a pending order is re-sent, with the same action id, every `ORDER_RESEND_AFTER` until
  answered; until then the robot stays unassignable and keeps its whole base.
- A robot that a cancel stopped mid-edge and that then stands idle is assignable at its `lastNodeId`. The first
  move of its next order leaves the reserved corridor: the emulator drives straight to the order's node 1, not
  back along the edge it holds.
- A retained `ONLINE` connection replayed after a restart counts as a sign of life: a silent robot looks online for
  at most one more `ROBOT_STALE_AFTER` (boot recovery asks every robot for a `state` at once).
- Robots are discovered, not registered: any robot on the broker's `vda5050/v3` topics joins the fleet.

## Amendment (2026-11-22, plan 06)

1. **The example floor carries the pick face, the forward area and the clarification point.** `PK1` (pick face,
   `2,8`), `FWD1` (forward area, `4,8`) and `CLR1` (clarification point, `8,6`) are `STATION` nodes with
   `handoffPoint`s like `ST1`/`ST2`, joined by the undirected edges `F-PK1`, `G-FWD1`, `H-CLR1`. The map `version`
   is `2026-11-22.1`; no PORT node, robot or AS/RS path changed. material-flow-control's example layout consumes
   exactly these three handoff points (`WH1-PCK-PK1`, `WH1-FWD-FWD1`, `WH1-CLR-CLR1`).
2. **A disconnected map is refused at load.** `fleetmap.New` (so `config.LoadMap` at boot) now validates that the
   undirected graph is connected — every node reachable from every node — and fails with `ErrInvalidMap`, naming one
   unreachable pair. Decision 6's failure class (a handoff point the map cannot route to, surfacing only at runtime)
   becomes a boot error. The runtime refusal `handoff-unreachable` (`ErrHandoffUnreachable`) stays in the published
   REST catalogue as documented defence-in-depth: the catalogue row is a published contract, and a map that boots
   connected can still strand a target behind reservation state — the boot check narrows, not replaces, it.
3. **The map↔world contract is pinned.** `contracts/equipment-emulator.world.yaml` is a byte copy of
   equipment-emulator's `config/world.example.yaml` (the producer path — `/etc/emulator/world.yaml` is only the
   chart's in-container mount of it) at `e241d57`, registered in `contracts/pinned.json` and enforced by the
   `pinned-contracts` CI job. The world holds devices only — robots and the AS/RS with port `nodeId`s, no floor
   graph — so the pin is the mirror: map PORT node ids equal the world's port `nodeId`s, and every robot `mapId`
   equals the map's `mapId` (`internal/config/floor_contract_test.go`). The emulator world itself is unchanged.
4. **The file map is the contract.** The 18-node in-code floor in `internal/domain/fleetmap`'s tests is a
   deliberately small TEST fixture, not the floor of record: `config/map.example.yaml` (byte-equal to the chart's
   `files/map.yaml`, kept equal by the render test) is what `floor_contract_test.go` pins against the world.
