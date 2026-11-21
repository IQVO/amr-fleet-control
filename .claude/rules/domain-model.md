# Domain model: ubiquitous language, aggregates, events, use cases

## Ubiquitous Language (use these exact names)

- **Robot** — one AMR, identified by VDA 5050 `manufacturer` + `serialNumber` (`robot.ID`, key `<m>/<s>`).
- **Leg** — one transport move requested by material-flow-control: a container from one handoff point to another.
- **Mission** — this context's execution of one leg by one robot (kind LEG), or an internal CHARGE or PARK run.
- **Order** — the VDA 5050 order of one mission (`orderId` = mission id, `orderUpdateId` only increases). Inside
  this context only.
- **Node**, **Edge** — map elements; edges are undirected corridors. **HandoffPoint** — a node's public name
  (an AS/RS port or a station), the only map vocabulary material-flow-control uses.
- **Base**, **Horizon** — the released and the not yet released part of an order (VDA 5050).
- **Reservation** — a node or edge held by exactly one robot. **Wait** — a robot stopped at the end of its base
  because the next element is held by another robot. **Deadlock** — a cycle of waits.
- **ChargingStation** — a CHARGER node. **Parking** — a PARKING node where idle robots stand.
- **Factsheet** — a robot's published capabilities (subset: series name, max speed, max load mass).

## Aggregates

- **FleetMap** (`internal/domain/fleetmap`): one map version (topology, routing) and its reservations — a node or
  edge is reserved by exactly one robot (inv. 2); waits and deadlock cycles.
- **Robot** (`internal/domain/robot`): one mission at a time (inv. 1); PackML state STOPPED/HELD/SUSPENDED/
  EXECUTE/IDLE; never assignable below the charge threshold (inv. 4).
- **Mission** (`internal/domain/mission`): QUEUED → ASSIGNED → PICKED_UP → DONE; any open → FAILED; QUEUED →
  CANCELLED; ASSIGNED → CANCEL_REQUESTED → CANCELLED. `orderUpdateId` only increases (inv. 3), pinned to one map
  version (inv. 5), exactly one outcome (inv. 6).
- Pure policies: `capacity` (snapshot), `dispatch` (robot choice, route plan, release window, nearest free node).

## Domain events

- `LegAssigned`, `ContainerPickedUp`, `LegCompleted`, `LegFailed` (mission), `RobotStateChanged` (robot),
  `FleetCapacityChanged` (fleet), `DeadlockDetected` (fleetmap). Internal missions publish nothing.

## Key use cases (internal/application/usecases, one `Service`)

- `RequestLeg`, `CancelLeg` — REST commands; each runs in one fleet unit of work and ends with the scheduler.
- `HandleState`, `HandleConnection`, `HandleFactsheet`, `Tick` — the MQTT link's ordered worker; `Recover` — boot.
- `Leg`, `Fleet`, `Robot` — queries (REST, MCP, A2A).
