---
id: tools
title: MCP tools and A2A skills
sidebar_label: MCP and A2A
description: "The read-only MCP server (four tools, one resource, one prompt) and the A2A agent (four skills, the Agent Card), authority L1, with example calls."
---

# MCP tools and A2A skills

Both surfaces are READ-ONLY, every tool and skill at authority **L1**
([ADR 0006](/docs/adr/0006-read-only-mcp-and-a2a-surfaces)). They read Postgres through `usecases.NewReader` on a
read-only pool, never dial Kafka or MQTT, and carry no authentication (fleet rule; access control is the in-cluster
boundary). Neither can request or cancel a leg: that is material-flow-control's REST call.

| Surface | Binary, port | Paths | Kong path |
| --- | --- | --- | --- |
| MCP (Streamable HTTP, stateless) | `cmd/mcp`, `:8090` | `/mcp` and `/`, `GET /healthz` | `/api/mcp/amr-fleet-control` |
| A2A (JSON-RPC) | `cmd/a2a`, `:8093` | `/a2a` and `/`, the card at `/.well-known/agent-card.json`, `GET /healthz` | `/api/a2a/amr-fleet-control` |

:::note
The Kong paths come with the chart's HTTPRoutes (Task 21) and the warehouse-infra registration (Task 25); in-cluster
the Services `amr-fleet-control-mcp:8090` and `amr-fleet-control-a2a:8093` are the endpoints.
:::

## MCP tools

Every tool is `readOnlyHint: true`, `idempotentHint: true`, `openWorldHint: false`, and declares
`_meta["com.warehouse/authority"] = "L1"` (`internal/adapters/inbound/mcp/testdata/tool_registry.golden.json` is the
registry the warehouse-docs agent catalogue reads). Inputs are camelCase; unknown input keys are refused.

| Tool | Arguments | Answer |
| --- | --- | --- |
| `get_fleet` | `fleetId` | `fleetId`, `mapId`, `mapVersion`, `handoffPoints`, `robots[]` (each as `get_robot`, factsheet included when known; the REST fleet view omits it), `capacity` (`robotsTotal`, `robotsOnline`, `robotsAvailable`, `robotsCharging`, `robotsHeld`, `legsQueued`, `legsActive`) |
| `list_open_legs` | `fleetId` | `legs[]`: the QUEUED, ASSIGNED, PICKED_UP and CANCEL_REQUESTED legs in acceptance order, each as `get_leg` |
| `get_leg` | `fleetId`, `legId` | `legId`, `fleetId`, `containerId`, `from`, `to`, `status`, `mapVersion`, `acceptedAt`; when set: `reference`, `robot` (`<manufacturer>/<serialNumber>`), `reason`, `robotError`, `assignedAt`, `pickedUpAt`, `endedAt` |
| `get_robot` | `fleetId`, `manufacturer`, `serialNumber` | `robot`, `connection`, `state`, `batteryPercent`, `charging`, `available`, `errorTypes`, `lastSeenAt`; when set: `nodeId`, `missionId`, `factsheet` (`seriesName`, `maxSpeed`, `maxLoadMass`) |

A failed call is a tool error whose text starts with the slug: `fleet-not-found`, `leg-not-found`, `robot-not-found`
(a robot appears with its first report, so a later read may find it), `validation-failed` (an empty `manufacturer` or
`serialNumber`), `internal-error` (no cause, DSN or SQL reaches the caller).

Example call (JSON-RPC over Streamable HTTP, `POST /mcp`):

```json
{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
 "params": {"name": "get_leg", "arguments": {"fleetId": "AMR1", "legId": "leg-0f3a91"}}}
```

The structured answer has the `get_leg` shape above, for example (illustrative values):

```json
{"legId": "leg-0f3a91", "fleetId": "AMR1", "containerId": "C-0001", "from": "ST1", "to": "ASRS1-IN1",
 "status": "ASSIGNED", "robot": "EMU/R1", "mapVersion": "2026-11-20.1",
 "acceptedAt": "2026-11-21T10:00:00Z", "assignedAt": "2026-11-21T10:00:00Z"}
```

### Resource and prompt

| Kind | Name | What |
| --- | --- | --- |
| Resource template | `warehouse://amr-fleet-control/fleets/{fleetId}` (`fleet`, `application/json`) | the `get_fleet` answer; an unknown or malformed URI is the protocol's resource-not-found naming the URI |
| Prompt | `review_leg` (argument `legId`, required) | the standard operating procedure for reviewing one leg: call `get_leg`, then `get_robot` for its robot, then `get_fleet`; how to read QUEUED, `ROBOT_BLOCKED`, `ROBOT_OFFLINE`, `ORDER_REJECTED`; when to escalate to the human fleet operator. A `legId` with a control character is refused. |

## A2A skills

The Agent Card (`GET /.well-known/agent-card.json`): name `amr-fleet-control`, `version` = `SERVICE_VERSION` (or
`dev`), one JSON-RPC interface at `A2A_AGENT_URL`, `streaming: false`, no security schemes and no security
requirements, provider `IQVO`. Every skill is tagged `authority:L1`.

| Skill | Arguments | `data` of the answer |
| --- | --- | --- |
| `get_fleet` | `fleetId` | `{"fleet": ...}` (the MCP `get_fleet` keys; each robot always carries `nodeId` and `missionId`, never a factsheet) |
| `list_open_legs` | `fleetId` | `{"legs": [...]}` |
| `get_leg` | `fleetId`, `legId` | `{"leg": ...}` (the MCP `get_leg` keys; `reason` and `robotError` always present) |
| `get_robot` | `fleetId`, `manufacturer`, `serialNumber` | `{"robot": ...}` (the MCP `get_robot` keys without the factsheet) |

A request is a `message/send` whose FIRST part is a data part (or a text part holding the same JSON):
`{"skill": "<id>", "arguments": {...}}`, decoded strictly (unknown keys, a trailing document and case-folded keys
are refused). The answer is one agent message bound to the caller's `contextId` (no task id): a data part with the
envelope `{"skill", "data"}` or `{"skill", "problem"}` and a text part with a one-line summary.

Example request part:

```json
{"skill": "get_leg", "arguments": {"fleetId": "AMR1", "legId": "leg-0f3a91"}}
```

A domain problem travels inside the envelope, never as a JSON-RPC error:

| `problem.code` | `type` | `status` | When |
| --- | --- | --- | --- |
| `fleet-not-found`, `leg-not-found`, `robot-not-found` | `not-found` | 404 | the read's typed error (the REST catalogue's slugs) |
| `unknown-skill` | `unknown-skill` | 400 | a skill the card does not offer (checked before the arguments) |
| `invalid-argument` | `invalid-argument` | 400 | bad or missing arguments |
| `malformed-skill-request` | `malformed-skill-request` | 400 | no parts, a first part that is neither data nor text, invalid JSON, no `skill` |
| `internal-error` | `internal-error` | 500 | anything else (the cause is logged, never returned) |
