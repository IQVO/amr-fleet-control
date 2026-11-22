# Before the first release: amr-fleet-control

The first merge to `main` runs `docker-publish` and `release`. Two services built from
this template (pick-face-replenishment, task-interleaving) each needed a rework because
packaging was missing or wrong when that moment came. Work through this list on a feature
branch, **before** the first `develop` → `main` PR. Delete a row only when the service
really does not have that part, and say so in the PR.

`new-service.sh` already generated `Dockerfile` and `.dockerignore` for you (verbatim from
the fleet reference). The chart is NOT generated: it is shaped by what the service runs
(consumer groups, MCP, analytics, frontend), so copy the closest sibling's
`charts/<repo>/` and adapt it. References on `origin/develop`:
`IQVO/pick-face-replenishment` (Kafka consumers, MCP, analytics, frontend) and
`IQVO/task-interleaving`.

| # | Item | Done when | Guarded by | Status (plan 03) |
|---|------|-----------|------------|---|
| 1 | **Real runtime only.** `cmd/api` wires Postgres and Kafka; the in-memory adapters (`internal/adapters/outbound/memory`) are test doubles. No `*_MODE`/permissive fallback, no default that boots "healthy" without `DATABASE_URL` or `KAFKA_BROKERS`. | `cmd/**` has no import of the memory package; the binary exits at boot on missing `DATABASE_URL`, `KAFKA_BROKERS` or a consumer group id. | `TestRuntimeBinaryHasNoInMemoryAdapters` (`internal/architecture/runtime_no_memory_fitness_test.go`). The boot-error behaviour has no generic sensor: cover it in `cmd/api` tests and in the chart test (row 8). | Done: `cmd/api` wires Postgres, Kafka and MQTT, `cmd/mcp`/`cmd/a2a` Postgres read-only; a missing `DATABASE_URL`, `KAFKA_BROKERS`, `MQTT_URL` or `AMR_MAP_FILE` is a boot error (`cmd/api/config_test.go`, boot tests). No consumer group (row 9). |
| 2 | **No Go file at the repo root.** The godog runner and steps live in `features/` (`package features_test`, `Paths: "."`), binaries in `cmd/<name>/`, everything else in `internal/`. | `ls *.go` at the root prints nothing; `make bdd` still finds `TestFeatures`. | `TestNoGoFilesAtRepoRoot` (`internal/architecture/no_go_files_at_root_fitness_test.go`). | Done (`TestNoGoFilesAtRepoRoot`). |
| 3 | **`Dockerfile` builds every `cmd/*`.** Generated; confirm `ENTRYPOINT ["./api"]` matches your main binary and `EXPOSE` matches `HTTP_ADDR`. | `docker build .` succeeds locally; `docker run --rm <image> /app/api` starts (or exits with the intended config error). | `trivy-scan` job builds the image on every PR to `main`/`develop`; `docker-publish` on push to `main`. | Done: the image ships `api`, `mcp` and `a2a` (Task 21: arm64 build, uid 1000). |
| 4 | **`.dockerignore`** excludes docs, charts, harness, tests output. Generated. | Build context is small; `.git`, `.claude/`, `docs/`, `charts/` are not sent. | none: review once. | Done (generated, unchanged). |
| 5 | **Helm chart `charts/amr-fleet-control/`** with `Chart.yaml` `name: amr-fleet-control`. The chart directory name, the `Chart.yaml` name and the `helm package` file name in the `release` job must all be `amr-fleet-control`. | `helm lint charts/amr-fleet-control` and `helm template` with the required values both pass. | `helm-lint` (PRs) and the `release` job (packages `amr-fleet-control-<version>.tgz`). | Done: `charts/amr-fleet-control` (`helm-lint`, Task 21). |
| 6 | **`DATABASE_URL` comes from a Secret** (`secretKeyRef`, chart `secret.yaml` or `database.existingSecret`), never from the ConfigMap or a literal. | `helm template` shows `secretKeyRef` for `DATABASE_URL`; the ConfigMap has no DSN. | chart test (row 8). | Done: `DATABASE_URL` and `MIGRATIONS_DATABASE_URL` from the Secret (`render_test.sh`). |
| 7 | **Pod hardening and shutdown room.** `runAsNonRoot`, `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`; `terminationGracePeriodSeconds` greater than the sum of the binary's shutdown bounds (drain delay + HTTP + consumers + relay). | Values and Deployment carry all of it; the chart refuses a grace period below the sum. | chart test (row 8). | Done: hardening and grace period refused below the bound (`render_test.sh`). |
| 8 | **Chart tests in `charts/amr-fleet-control/tests/`** (`render_test.sh`, `test_service_selectors.py`): envFrom ConfigMap mounted, mandatory values refuse to render, every env var `cmd/api` reads is rendered, no `*_MODE`, Secret for the DSN, hardening, grace period, every Service selects exactly one Deployment. Copy and adapt the reference tests. | `bash charts/amr-fleet-control/tests/render_test.sh` prints `PASS`. | `chart-tests` job runs every `tests/*.sh` and `test_*.py`. **It only warns when the directory is empty**, so an absent test passes CI: the checklist is the guard. | Done: `render_test.sh`, `test_service_selectors.py`, `test_kong_config.py`, `test_agents_colocation.py`. |
| 9 | **Consumer group ids are chart values**, stable and service-prefixed, never derived from the pod name. | Each `*_CONSUMER_GROUP` is rendered from `config.*` and refuses to render empty. | chart test (row 8); `kafka-testing-and-consumers.md` rule. | Not applicable: amr-fleet-control consumes no Kafka topic. |
| 10 | **Optional CI jobs match the service.** `web` (needs `web/`), `docs-api-drift` (needs `docs/` and `apis/openapi.yaml`), `api-lint` (needs `apis/*.yaml` and the spectral rulesets): delete a job and its `docker-publish` `needs:` entry if the service has no such part, or add the part. | No job in `ci.yml` is permanently red or skipped for a missing directory. | first PR's CI run. | Done: `api-lint`, `docs-api-drift`, `mutation-fast` and `integration` run (restored in Task 20); no `web/`, so no `web` job. |
| 11 | **Registry access** images push to `ghcr.io/iqvo` with the Actions token (`packages: write` on `docker-publish`/`release`, already in `ci.yml`); no Docker Hub secrets are needed. | A dry-run of `docker-publish` on a throwaway branch, or ask the repo admin. | none. | Configured (`packages: write` on `docker-publish`/`release`); proven by the first push to `main`. |
| 12 | **`govulncheck` is clean** before the PR to `main` (CI `vuln` job installs `@latest`; `make vuln` runs the same command). | `make vuln` exits 0. | `vuln` job (a `docker-publish` prerequisite). | Done: `govulncheck ./...` clean at the Task 22 head. |

Plan 03 also delivered, ticked in the develop PR (Task 22):

- [ ] CI: every job of `ci.yml` green on the develop PR (lint, guide-lint, complexity, file-length, test with the
  90 % coverage gate, bdd, integration including the end-to-end test against the emulator, mutation-fast, api-lint,
  vuln, docs-api-drift, helm-lint, chart-tests, arch-test, spec-breaking, trivy-scan). CI runs on pull requests only,
  so the PR is its first run; the same gates passed locally at the Task 22 head.
- [x] Chart tests (`charts/amr-fleet-control/tests/`) and `check-chart-selectors`.
- [x] The image builds (`Dockerfile`, every `cmd/*`).
- [x] The docs site builds (`docs.yml`, `cd docs && npm ci && npm run build`).
- [x] The mutation gate is live: `.gremlins.yaml` at 99/99, plain numbers, measured per package (`MUTATION.md`).
- [x] ADRs 0001-0006 (`docs/adr/index.md`).
- [x] The README disclaimer.

Tick this list in the PR that adds `Dockerfile`/chart wiring, and keep the file: it is the
service's packaging record. Reference for the three failures this list prevents: a runtime that
can run without Postgres/Kafka (row 1), Go files at the root (row 2), a release that fails on
the first push to `main` because packaging was never built (rows 3-8).
