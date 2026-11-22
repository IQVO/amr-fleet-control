---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
description: "Symptom, cause and check for fleet-unavailable, legs that stay QUEUED, ORDER_REJECTED, PICK_FAILED LOAD_NOT_AVAILABLE, repeating deadlocks and /readyz 503 on mqtt."
---

# Troubleshooting

Start every diagnosis with the fleet view, `GET /fleets/{fleetId}` (or the MCP tool `get_fleet`): each robot's
`connection`, `state`, `battery_percent`, `available`, `node_id`, `mission_id`, `error_types` and the capacity counts.

| Symptom | Cause | Check |
| --- | --- | --- |
| `POST .../legs` answers `422 fleet-unavailable` | No robot is ONLINE. A robot counts as online only when its last `connection` is `ONLINE`, it has sent at least one `state`, and it is not stale (`ROBOT_STALE_AFTER_SECONDS`). | `capacity.robots_online` is 0. Check `MQTT_URL` and the broker, that the robots publish `vda5050/v3/<manufacturer>/<serialNumber>/state`, and `/readyz` (`mqtt`). The refusal is RETRYABLE: retry with the same key once a robot is back. |
| Legs stay `QUEUED` | No robot is assignable. Assignable = `IDLE` (not `EXECUTE`, `SUSPENDED` charging, `HELD` or `STOPPED`), no `cancelOrder` awaiting an answer, standing on a map node, battery >= `CHARGE_LOW_PERCENT`. | `capacity.robots_available` is 0. A robot with no `node_id` reports another `mapId` than the map file, or stands farther than `NODE_TOLERANCE_METRES` from every node. A robot below the threshold is sent to charge first and stays out until `CHARGE_RESUME_PERCENT`. |
| `LegFailed` `ORDER_REJECTED` | The robot refused the order: its `state` listed a new `VALIDATION_FAILURE` or `INVALID_ORDER` error (`robot_error`), or it never acknowledged the order after `ORDER_RESEND_LIMIT` resends. | `robot_error` names the robot's `errorType`. `INVALID_ORDER` from the emulator has two causes. (1) The order's first node is not the robot's `lastNodeId` (or within 0.5 m of it): the map's node ids or coordinates disagree with the robot's world (port node ids must equal the world's port `nodeId`s). (2) The robot's current order is not finished: it still runs an earlier order (a previous process's, or a cancelled leg's unfinished one); fleet control cancels an order it does not own with a `recover-` `cancelOrder` (see [Runbook, What a restart does](/docs/operations/runbook#what-a-restart-does)). A rising `ORDER_REJECTED` share on `amr_fleet_control.legs.ended` points at the same mismatch. |
| `LegFailed` `PICK_FAILED`, `robot_error` `LOAD_NOT_AVAILABLE` | The robot's `pick` action FAILED: nothing to pick at the pickup handoff point. | At an AS/RS output port the container must have been presented there first (asrs-control's retrieve; with the emulator: seed it and send a `RETRIEVE` telegram, see [Quickstart](/docs/overview/quickstart)). The leg is not retried here; material-flow-control decides. `DROP_FAILED` is the same at the drop. |
| `DeadlockDetected` keeps coming | Each NEW wait-for cycle publishes one event; a repeating event means cycles keep forming. The youngest mission of a cycle is re-routed around nodes others hold; when no route exists the cycle lasts until `DEADLOCK_TIMEOUT_SECONDS` and fails that leg `ROBOT_BLOCKED`. | `node_ids` and `robots` on the events show where. Typical causes: a single corridor between busy handoff points with no passing edge, or idle robots left on waypoints (they park only when a free `PARKING` node exists). Add an edge or parking node to the map (a new map `version`). |
| `/readyz` `503` naming `mqtt` | The fleet link is not connected. paho reconnects every 2 s; on every reconnect the link subscribes again and the retained `connection` and `factsheet` messages are replayed. | The broker address in `MQTT_URL`, the broker's logs, `WARN` lines from the link. At boot the first subscription round must succeed within 35 s, else `cmd/api` exits `1`. |
| `/readyz` `503` naming `postgres` or `kafka` | A ping of the pool, or a TCP dial of the first broker in `KAFKA_BROKERS`, failed. | The DSN and the broker list; the outbox keeps every event until Kafka is back (see [Runbook](/docs/operations/runbook)). |
