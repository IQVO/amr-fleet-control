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
   shows its `orderId`/`orderUpdateId`, rejected when the next `state` carries `VALIDATION_FAILURE` or
   `INVALID_ORDER`, and resent with the same `orderUpdateId` after 5 s without either.
3. **Reservations.** A robot always holds the node it stands on. A mission's base reaches at most
   `RELEASE_AHEAD_NODES` nodes past the robot's last node; a node or edge enters the base only once reserved for that
   robot; passed elements are released on the next `state`. Edges are undirected corridors. `orderUpdateId` rises by
   one per extension or re-route.
4. **Deadlocks** are cycles of waits: each new cycle publishes one `DeadlockDetected` and re-routes its youngest
   mission around nodes held by others; a cycle older than 60 s fails that leg `ROBOT_BLOCKED` and cancels its order.
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
- Order rejection is inferred from the next `state` (no reply topic): a stale order error the robot still lists
  from an earlier order could fail a new mission `ORDER_REJECTED`; errors are cleared by the robot on every new order.
- Robots are discovered, not registered: any robot on the broker's `vda5050/v3` topics joins the fleet.
