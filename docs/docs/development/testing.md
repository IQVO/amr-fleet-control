---
id: testing
title: Testing
sidebar_label: Testing
description: "The test pyramid with the real file names, the gates, how to run each layer and the CI job list."
---

# Testing

Tests assert exact values (nodes, indexes, counts, statuses, reasons), never just "no error". Every test that needs a
container carries `//go:build integration`, starts its own container with testcontainers, never calls `t.Skip` and
never reads a database URL from the environment (`TestPostgresIntegrationTestsUseTestcontainers`,
`TestKafkaIntegrationTestsUseTestcontainers`).

## The pyramid

| Layer | Files | Run with |
| --- | --- | --- |
| Domain unit tests | `internal/domain/fleetmap/{map,route,fleetmap,deadlock}_test.go`, `internal/domain/robot/{robot,report,await_carry,stop_charge}_test.go`, `internal/domain/mission/{mission,transitions,cancel_carry}_test.go`, `internal/domain/capacity/capacity_test.go`, `internal/domain/dispatch/dispatch_test.go` | `make test` |
| Use-case tests on the memory doubles | `internal/application/usecases/*_test.go` (`legs`, `uplink`, `traffic`, `tick`, `queries`, `serial`, `commit_order`, `hold_underway`, `lost_cancel`, `stop_charge`, `leg_metrics` and the review carries), over `internal/adapters/outbound/memory` (store, unit of work, repositories, clock, ids, downlink) via `harness_test.go` | `make test` |
| Adapter unit tests | `internal/adapters/inbound/http` (handlers, problem catalogue, replay classes, idempotency guard and gaps, middleware order, serial router), `internal/adapters/inbound/mcp`, `internal/adapters/inbound/a2a`, `internal/adapters/vda5050`, `internal/adapters/mqtt` (dispatch, sender, subscribe order, session), `internal/adapters/outbound/events` (golden CloudEvents, headers, tracing, contract `minLength`), `internal/adapters/outbound/telemetry`, `cmd/*` (boot, config, DSN split, drain) | `make test` |
| Fitness tests | `internal/architecture/*_test.go`: arch-go layering, the MCP/A2A dependency rules, the read-only agent sensors, the postgres pool-bypass sensor, Kafka writer settings, CloudEvents only, the outbox attempt cap, no memory doubles in `cmd/`, no auth middleware | `make arch-test` |
| BDD acceptance | `features/amr_fleet_control.feature` (8 scenarios: nearest robot and one ending; charge threshold; waiting on a held node; deadlock re-route; deadlock timeout `ROBOT_BLOCKED`; cancel queued vs picked-up; rejected order; robot offline) with `features/{features,steps,robots}_test.go` over the use cases and memory doubles | `make bdd` |
| Integration: Postgres | `internal/adapters/outbound/postgres/{repos,outbox,readonly,timeouts,pool_path_timeouts,uow_carry}_integration_test.go` | `make integration` |
| Integration: MQTT | `internal/adapters/mqtt/{link,reconnect,subscribe_order}_integration_test.go` against Mosquitto 2.0.20 | `make integration` |
| Integration: Kafka | `internal/adapters/outbound/kafka/relay_sink_latency_integration_test.go` against `confluent-local` 7.6.1 | `make integration` |
| Integration: binaries | `cmd/api/{boot,boot_order}_integration_test.go`, `cmd/mcp/{main,dsn}_integration_test.go`, `cmd/a2a/{main,dsn}_integration_test.go` | `make integration` |
| End to end | `cmd/api/e2e_emulator_integration_test.go`: Postgres, Kafka, Mosquitto and the REAL emulator robots (`EQUIPMENT_EMULATOR_IMAGE`, default `equipment-emulator:e2e`, built from equipment-emulator `v0.1.0` / `b58201e`): a station-to-port leg, a `PICK_FAILED` `LOAD_NOT_AVAILABLE` from an empty output port, a pick from an output port after seed + `RETRIEVE`, a blocked robot failing `ROBOT_BLOCKED`, charging below the threshold, a disconnected robot leaving the online count | `make integration` |
| Chart | `charts/amr-fleet-control/tests/render_test.sh` (the api env equals what `cmd/api` and the telemetry adapter read; the mcp/a2a env exact, both DSNs from the Secret, no Kafka or MQTT; one replica, `Recreate`, grace >= 30 s; probes; the default map is `config/map.example.yaml`; a render over a byte copy of warehouse-infra's `_platform.yaml`; every refusal), `test_service_selectors.py` (each Service selects one component), `test_kong_config.py` (routes, rewrites, plugins, no auth plugin, CORS headers and origin) | `bash charts/amr-fleet-control/tests/render_test.sh`, `python3 charts/amr-fleet-control/tests/test_*.py` (CI `chart-tests`) |

## Mutation testing

`.gremlins.yaml` gates at efficacy 99 and mutant coverage 99, written as plain numbers (gremlins v0.6.0 reads a
quoted threshold as 0, so it would never gate). gremlins fails when a measured value is `<=` its threshold, so every
package must measure above 99.00%. The `mutation-fast` CI job runs gremlins once per domain package under
`ulimit -v 4000000`; `make mutation` runs `./internal/domain/mission`, `make mutation-full` and the weekly `mutation`
job the aggregate `./internal/domain`. Measured 2026-11-21 (gremlins v0.6.0, workers 1, timeout coefficient 30, cold
test cache):

| Package | Killed | Lived | Not covered | Efficacy | Mutant coverage |
| --- | --- | --- | --- | --- | --- |
| `fleetmap` | 56 | 0 | 0 | 100.00% | 100.00% |
| `robot` | 48 | 0 | 0 | 100.00% | 100.00% |
| `mission` | 115 | 1 | 0 | 99.14% | 100.00% |
| `capacity` | 8 | 0 | 0 | 100.00% | 100.00% |
| `dispatch` | 39 | 0 | 0 | 100.00% | 100.00% |
| `./internal/domain` (aggregate) | 265 | 1 | 0 | 99.62% | 100.00% |

The one living mutant is equivalent (`mission.go` `fits`, `dr > pick` to `dr >= pick`: a pick and a drop never
share a step). The aggregate also timed one runaway-loop mutant out locally (`route.go:35`), which CI's memory cap
kills. `MUTATION.md` at the repository root lists both, with the proof, and the behaviour-preserving rewrites that
removed the other equivalent shapes.

## Gates

| Command | What it runs |
| --- | --- |
| `make check-fast` | fmt-check, vet, arch-test |
| `make check` | fmt-check, vet, build, lint (`golangci-lint`: funlen 80/50, cyclop 15, gocognit 20), `go test ./... -race`; the pre-push hook |
| `make check-all` | check, coverage, arch-test, bdd |
| `make coverage` | the 90 % gate over `internal/domain/...` and `internal/application/...` |
| `make integration` | `go test -tags=integration ./... -race -count=1` (Docker; build the emulator image first, see [Quickstart](/docs/overview/quickstart)) |
| `make mutation` | gremlins on `./internal/domain/mission` (`make mutation-full`: the aggregate `./internal/domain`; CI's `mutation-fast` runs every package) |
| `go vet -tags integration ./...` | compiles every integration test without Docker |

## CI jobs (`.github/workflows/ci.yml`)

`lint`, `guide-lint`, `complexity`, `file-length`, `test` (with `make coverage`), `bdd`, `integration` (builds the
emulator at the pinned commit), `mutation-fast`, `api-lint` (Spectral), `vuln` (govulncheck), `docs-api-drift`,
`helm-lint`, `chart-tests`, `arch-test`, `spec-breaking` and `trivy-scan` on pull requests; `mutation` and `drift` on
the weekly schedule; `docker-publish` and `release` on `main`. `docs.yml` builds this site on every pull request that
touches `docs/` or `apis/` and publishes it from `main`.
