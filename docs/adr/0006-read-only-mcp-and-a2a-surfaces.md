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

Guards (Task 18): `internal/architecture/agent_readonly_fitness_test.go` is the read-only sensor of both
surfaces and their `cmd/` roots (arch-go does not constrain `cmd/`): an allowlist of read identifiers resolved
by import path, no write-verb method on any value, no raw driver, no Kafka, MQTT, VDA 5050, outbox or event
import. Every tool call is one `mcp.tool <name>` span (`gen_ai.operation.name=execute_tool`,
`warehouse.authority=L1`) holding the mapped error only. `fleet-not-found` and `robot-not-found` are
RETRYABLE (ADR 0004: a map deploy or a robot's first report clears them), so no tool description calls an
unknown id final; an unknown fleet on the resource is `mcp.ResourceNotFoundError(uri)`. A boot error is one
JSON line naming every bad variable (a set-but-unknown `LOG_LEVEL` included), and the listener is bound
before the migrations and the pool.

Guards (Task 19): the A2A agent mirrors them. The sensor checks both surfaces and both `cmd/` roots (a missing
directory fails; there is no "not built yet" branch) and `TestA2AAdapterDependencyRule` keeps the adapter additive.
A skill request is decoded strictly (nothing after the JSON document, keys matched byte for byte); every run is
one `a2a.skill <id>` span (`gen_ai.operation.name=execute_tool`, `warehouse.authority=L1`, the problem's slug as
the status), an id the card does not offer is `a2a.skill unknown` with no raw id; a problem carries the REST
catalogue slug as `code` and no replay class; the reply is one agent message bound to the caller's `contextId`
and names no task. Both agent roots read through `postgres.NewReadOnlyPool` (plan 04 ruling P4-R17 parity): every
transaction the adapter begins on it is `BEGIN READ ONLY`, out-of-unit reads included, so a write fails with
SQLSTATE 25006 even behind PgBouncer transaction pooling, where a `default_transaction_read_only` startup
parameter may be refused; the sensor refuses `postgres.NewPool` in agent sources, and `cmd/api` keeps it.
Migrations run on `MIGRATIONS_DATABASE_URL`, the read pool on `DATABASE_URL`.
