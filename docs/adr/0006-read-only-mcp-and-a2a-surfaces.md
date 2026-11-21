# 0006. Read-only MCP (:8090) and A2A (:8093) surfaces at authority L1

Date: 2026-11-20

## Status

Accepted

## Context

The agentic standard v2 (warehouse-docs, AS-1..AS-9, authority L1-L4) gives every context an MCP server and an A2A
agent over its own use cases; the WCS context map notes that warehouse-ops-agent and warehouse-console "will read
equipment state". Writes to amr-fleet-control belong to material-flow-control (relationship 4) alone.

## Decision

1. `cmd/mcp` (Streamable HTTP, stateless, `/mcp` and `/`, `GET /healthz`): tools `get_fleet`, `list_open_legs`,
   `get_leg`, `get_robot`, all read-only, idempotent, closed world, `_meta["com.warehouse/authority"] = "L1"`;
   resource template `warehouse://amr-fleet-control/fleets/{fleetId}`; prompt `review_leg`.
2. `cmd/a2a` (JSON-RPC, `/a2a` and `/`, the card at `/.well-known/agent-card.json`, `GET /healthz`): skills of the
   same four names, each tagged `authority:L1`; domain problems travel inside the message envelope.
3. Both read Postgres through `usecases.NewReader` (every write answers ErrReadOnly), need `DATABASE_URL` and
   `AMR_MAP_FILE`, never dial Kafka or MQTT, never start the relay, carry no auth (fleet rule).
4. Neither may request or cancel a leg: an agent that must move a container asks material-flow-control.

## Consequences

warehouse-docs' agent catalogue picks the MCP tools up from `testdata/tool_registry.golden.json` and the A2A
skills from its card index, which `a2a-cards.py --write` refreshes from LIVE cards: the catalogue rows follow the
first deploy (Task 25 hands that over). A future L2 tool needs its own ADR.
