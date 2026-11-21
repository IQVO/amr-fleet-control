---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
description: "Every term of amr-fleet-control's language with its meaning and the code identifier that carries it."
---

# Ubiquitous language

The first eight terms are the context page's (warehouse-docs `docs/wcs/amr-fleet-control.md`, "Ubiquitous
language"); the rest are the terms the code added. VDA 5050 vocabulary (order, node, edge, base, horizon, action)
stays inside this context: the WES and material-flow-control see legs, handoff points, containers and robots.

| Term | Meaning | Code |
| --- | --- | --- |
| **Robot** | One AMR, identified by VDA 5050 `manufacturer` and `serialNumber`; its key is `<manufacturer>/<serialNumber>`. | `robot.Robot`, `robot.ID`, `ID.Key()` |
| **Mission** | This context's execution of one leg by one robot; also an internal CHARGE or PARK trip, which is not a leg. | `mission.Mission`, `mission.Kind` (`LEG`, `CHARGE`, `PARK`) |
| **Order** | A VDA 5050 order sent to a robot: a node and edge graph segment with `orderId` (the mission id) and `orderUpdateId`. Used only inside this context. | `vda.Order`, `usecases.buildOrder` |
| **Node**, **Edge** | The map elements: nodes with `x`, `y` in metres and a kind (`WAYPOINT`, `PARKING`, `CHARGER`, `PORT`, `STATION`); edges are undirected corridors. Today from the map file, later translated from facility-layout's travel graph. | `fleetmap.Node`, `fleetmap.Edge`, `fleetmap.NodeKind` |
| **Base**, **Horizon** | The released and the not yet released parts of an order. The base reaches at most `RELEASE_AHEAD_NODES` past the robot's last reached node. | `Mission.BaseEnd()`, `vda.Node.Released`, `dispatch.Window` |
| **Action** | A VDA 5050 action at a node (`pick` with `loadId`, `drop`, `startCharging`) or an instant action (`cancelOrder`, `stopCharging`, `stateRequest`). Never an edge action. | `vda.Action`, `mission.ActionPick`, `ActionDrop`, `ActionCharge` |
| **Factsheet** | A robot's published capabilities; this context keeps `seriesName`, `maxSpeed`, `maxLoadMass`. | `robot.Factsheet` |
| **ChargingStation** | A node where robots charge: a map node of kind `CHARGER`. | `fleetmap.Charger` |
| **Leg** | One move of one container from one handoff point to another, requested by material-flow-control. Id `leg-<uuid>`, which is also its VDA 5050 `orderId`. | `mission.NewLeg`, `mission.LegSpec`, `usecases.LegRequest` |
| **Handoff point** | Where a container changes hands: an AS/RS port or a station, named by a `PORT` or `STATION` node's `handoffPoint` (e.g. `ASRS1-OUT1`, `ST1`). The only location vocabulary the REST interface and the events use. | `Map.NodeOfHandoff`, `Map.HandoffPoints` |
| **Fleet** | The robots and the map of one map file, addressed by `fleetId` (`AMR1`). | `fleetmap.Map.FleetID()`, the `fleets` row |
| **Map version** | The `version` of the map file. Every mission is pinned to the version it was accepted under; a mission of another version ends `MAP_VERSION_RETIRED` at boot. | `Map.Version()`, `Mission.MapVersion()` |
| **Reservation** | A node or edge held by exactly one robot: the node it stands on, its released base, and (after a cancel mid-edge) the edge it is on. | `fleetmap.FleetMap.Reserve`, `Retain`, `NodeHolder`, `EdgeHolder` |
| **Wait** | A robot whose next node is held by another robot waits at the end of its base; the wait records the node, the blocker and since when. | `fleetmap.Wait`, `FleetMap.SetWait` |
| **Wait-for graph**, **Deadlock** | Robots waiting on robots; a cycle in it is a deadlock, published once as `DeadlockDetected` and resolved by re-routing the youngest mission or failing it `ROBOT_BLOCKED`. | `FleetMap.Cycles`, `FleetMap.DetectDeadlocks`, `fleetmap.DeadlockDetected` |
| **Re-route** | A new horizon from the base end around every node another robot holds; the released base never changes. | `Mission.Reroute`, `usecases.reroute` |
| **PackML state** | The robot's state: `STOPPED` (not ONLINE, stale or no state yet), `HELD` (an active `CRITICAL` or `FATAL` error), `SUSPENDED` (charging), `EXECUTE` (holds a mission), `IDLE`. Derived in that priority order. | `robot.State`, `Robot.derive` |
| **Connection** | The robot's last VDA 5050 `connection`: `ONLINE`, `OFFLINE`, `CONNECTION_BROKEN`. | `robot.Connection` |
| **Online** | Connected (`ONLINE`), not stale and has reported at least once; a fleet with no online robot refuses legs (`fleet-unavailable`). | `Robot.Online()` |
| **Stale** | No message for `ROBOT_STALE_AFTER_SECONDS`: the robot is `STOPPED`. | `Robot.MarkStale` |
| **Assignable**, **Available** | `IDLE`, no cancel awaiting an answer, standing on a map node, battery >= `CHARGE_LOW_PERCENT`. Counted as `robots_available`. | `Robot.Assignable` |
| **Charge threshold** | `CHARGE_LOW_PERCENT`: below it a robot is never assigned and an idle one goes to charge; `CHARGE_RESUME_PERCENT` ends the charge. | `Robot.NeedsCharge`, `Robot.Charged` |
| **Capacity** | The snapshot `robots_total`, `robots_online`, `robots_available`, `robots_charging`, `robots_held`, `legs_queued`, `legs_active`, published on every count change. | `capacity.Snapshot`, `capacity.Compute`, `capacity.Next` |
| **Leg status** | `QUEUED`, `ASSIGNED`, `PICKED_UP`, `CANCEL_REQUESTED`, `DONE`, `FAILED`, `CANCELLED`. | `mission.Status` |
| **Failure reason** | `CANCELLED`, `ROBOT_BLOCKED`, `ROBOT_FAULT`, `ROBOT_OFFLINE`, `ORDER_REJECTED`, `PICK_FAILED`, `DROP_FAILED`, `MAP_VERSION_RETIRED`. | `mission.Reason` |
| **Robot error** | The robot's own `errorType` that explains a failure (e.g. `LOAD_NOT_AVAILABLE`, `ROBOT_BLOCKED`, `INVALID_ORDER`). | `robot.Error`, `LegFailed.RobotError` |
