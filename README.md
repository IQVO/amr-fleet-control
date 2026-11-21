# amr-fleet-control

`amr-fleet-control` is the VDA 5050 master control of the warehouse-systems WCS tier: it accepts transport legs from
material-flow-control over the subsystem-controller REST interface, assigns each leg to one robot by distance,
battery and availability, releases the route as VDA 5050 base and horizon while reserving every node and edge for
exactly one robot, detects and resolves deadlocks, sends robots to charge and to park, and keeps a registry of every
robot's connection, state, errors and factsheet. It does not own which container moves when (material-flow-control),
container contents (inventory-storage), the AS/RS (asrs-control) or the building's travel graph (facility-layout).
Classification: Generic, built behind the subsystem-controller interface so a vendor fleet manager could replace it.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

## Documentation

The site is https://iqvo.github.io/amr-fleet-control/ (built from `docs/`, published from `main`). The strategic
design is warehouse-docs `docs/wcs/` (WCS section).

| Page | What it holds |
| --- | --- |
| `docs/docs/overview/` | introduction, architecture, quickstart |
| `docs/docs/operations/` | configuration, runbook, observability, troubleshooting |
| `docs/docs/ddd/` | ubiquitous language, use cases, the ddd-crew pack |
| `docs/adr/` | ADRs 0001-0006 |
| `apis/openapi.yaml` | the REST subsystem-controller interface (relationship 4) |
| `apis/asyncapi.yaml` | the seven published events |
| `config/map.example.yaml` | the map file format |

## Architecture

Hexagonal, enforced by `internal/architecture/architecture_test.go` (arch-go):

    cmd/api, cmd/mcp, cmd/a2a       composition roots (REST + fleet link + relay; read-only MCP; read-only A2A)
    internal/domain/                fleetmap, robot, mission, capacity, dispatch
    internal/application/           ports, vda, usecases (one Service, every write in one fleet unit of work)
    internal/adapters/inbound/      http, mcp, a2a, requestlog
    internal/adapters/vda5050, mqtt VDA 5050 v3 codec and the one ordered fleet link
    internal/adapters/outbound/     postgres, events, outbox relay, kafka, telemetry, memory (test doubles), system

## Domain events / Integration

Publishes CloudEvents 1.0 (`com.warehouse.wes.amr-fleet-control.<entity>.<EventName>`) to
`warehouse.amr-fleet-control.events` and `warehouse.amr-fleet-control.analytics`, for example
`com.warehouse.wes.amr-fleet-control.mission.LegCompleted`. Seven types: LegAssigned, ContainerPickedUp,
LegCompleted, LegFailed (mission), RobotStateChanged (robot), FleetCapacityChanged (fleet), DeadlockDetected
(fleetmap). Consumes nothing from Kafka. Speaks VDA 5050 v3.0.0 to the robots on `vda5050/v3/<manufacturer>/<serialNumber>/<topic>`.

## Running it

`cmd/api` (HTTP on `:8080`): `DATABASE_URL`, `KAFKA_BROKERS`, `MQTT_URL` and `AMR_MAP_FILE` are required; every
other variable and its default is in `docs/docs/operations/configuration.md`. `cmd/mcp` (`:8090`) and `cmd/a2a`
(`:8093`) need `DATABASE_URL` and `AMR_MAP_FILE`; the agent also `A2A_AGENT_URL`.

## Observability

OpenTelemetry traces and metrics over OTLP/gRPC (`OTEL_EXPORTER_OTLP_ENDPOINT`; empty = off), `trace_id`/`span_id`
on every JSON log line, and the counter `amr_fleet_control.legs.ended` (ADR 0005).

## API

| Route | Purpose |
| --- | --- |
| `POST /fleets/{fleetId}/legs` | request a leg (202, or 422 at once) |
| `GET /fleets/{fleetId}/legs/{legId}` | read a leg |
| `POST /fleets/{fleetId}/legs/{legId}/cancellation` | cancel (200 cancelled, 202 robot asked) |
| `GET /fleets/{fleetId}` | the fleet: map version, handoff points, robots, capacity |
| `GET /fleets/{fleetId}/robots/{manufacturer}/{serialNumber}` | one robot |

Every POST takes `Idempotency-Key`; 2xx and FINAL refusals replay for 24 hours with `Idempotency-Replayed: true`,
RETRYABLE refusals release the key. Errors are `application/problem+json` with a `code`. No authentication (fleet
rule). MCP at `/mcp` (`:8090`), A2A at `/a2a` (`:8093`), both read-only.

## Helm chart

`charts/amr-fleet-control`: the api Deployment (exactly one replica, Recreate, grace 45 s), optional read-only mcp and
a2a Deployments, Services, a ConfigMap with the environment and `map.yaml` (default `config/map.example.yaml`), a
Secret for the database, default-off HTTPRoutes for `/api/amr-fleet-control`, `/api/mcp/amr-fleet-control` and
`/api/a2a/amr-fleet-control`, and the three default-off KongPlugins (`cors`, `prometheus`, `opentelemetry`). It reads
warehouse-infra's `_platform.yaml` keys and refuses to render without Postgres, Kafka and MQTT.

## Local development

`make check-fast`, `make check`, `make check-all`, `make integration` (Docker; the e2e test also needs the emulator
image named by `EQUIPMENT_EMULATOR_IMAGE`, default `equipment-emulator:e2e`:
`git -C <equipment-emulator checkout> archive v0.1.0 | docker build -t equipment-emulator:e2e -`, because GHCR's
`v0.1.0` image is amd64-only), `make mutation`, and `cd docs && npm ci && npm run build` for the site.

## Tests

Unit and BDD (`features/amr_fleet_control.feature`, `make bdd`), integration (`//go:build integration`: Postgres
repositories, the MQTT link against Mosquitto, the Kafka relay sink against Kafka, the end-to-end test against the
emulator robots), chart tests (`charts/amr-fleet-control/tests/`: `render_test.sh`, `test_service_selectors.py`,
`test_kong_config.py`), mutation (gremlins, thresholds in `.gremlins.yaml`, measured in Task 22).

## Architecture Decision Records

6 ADRs, indexed in `docs/adr/index.md`.

## License

See `LICENSE`.
