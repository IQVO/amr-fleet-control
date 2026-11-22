---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
description: "Run amr-fleet-control locally against Postgres, Kafka, Mosquitto and the equipment emulator's robots, request one leg, and inject faults."
---

# Quickstart

No `docker compose`: each dependency is one container, and the robots are the
[equipment emulator](https://github.com/IQVO/equipment-emulator) at its `v0.1.0` release (commit `b58201e`), the
version the end-to-end test runs against.

## 1. Infrastructure

```bash
docker run -d --name afc-postgres -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:16
docker run -d --name afc-kafka -p 9092:9092 confluentinc/confluent-local:7.6.1
docker run -d --name afc-mosquitto -p 1883:1883 eclipse-mosquitto:2.0.20 mosquitto -c /mosquitto-no-auth.conf
```

The relay's kafka-go writer creates `warehouse.amr-fleet-control.events` and `warehouse.amr-fleet-control.analytics`
on first write (`AllowAutoTopicCreation`); `confluent-local` allows it.

## 2. The robots: the emulator at v0.1.0

Build the image from the release tag (GHCR's `ghcr.io/iqvo/equipment-emulator:v0.1.0` has no arm64 manifest, so on
an Apple-silicon machine build it locally):

```bash
git -C ~/warehouse-systems/equipment-emulator fetch --tags
git -C ~/warehouse-systems/equipment-emulator archive v0.1.0 | docker build -t equipment-emulator:e2e -
```

Run it with its example world (baked into the image at `/etc/emulator/world.yaml`: robots `EMU/R1`, `EMU/R2`,
`EMU/R3` at (0,0), (2,0), (4,0) on map `S1`, AS/RS `ASRS1` with port nodes `ASRS1-IN1`, `ASRS1-OUT1`, `ASRS1-IN2`,
`ASRS1-OUT2`). Its admin API listens on `:8080` inside the container; publish it on `8081` so it does not collide
with `cmd/api`:

```bash
docker run -d --name afc-emulator -p 8081:8080 \
  -e MQTT_URL=tcp://host.docker.internal:1883 -e SIM_SPEED=10 equipment-emulator:e2e
```

`config/map.example.yaml` is fleet control's side of the same floor: the robots start on `P1`, `P2`, `P3` and every
port node id equals the emulator's port `nodeId`.

## 3. Run cmd/api

```bash
DATABASE_URL='postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable' \
KAFKA_BROKERS=localhost:9092 \
MQTT_URL=tcp://localhost:1883 \
AMR_MAP_FILE=config/map.example.yaml \
go run ./cmd/api
```

The boot runs the embedded migrations, connects the fleet link, runs `Recover` (a `stateRequest` to every known robot)
and logs `amr-fleet-control listening` with the fleet id `AMR1` and the map version `2026-11-20.1`. Every other
variable and its default is on [Configuration](/docs/operations/configuration). `GET /readyz` answers `200` once
Postgres, Kafka and the MQTT link are up.

## 4. Request one leg

```bash
curl -s -X POST localhost:8080/fleets/AMR1/legs \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: quickstart-1' \
  -d '{"container_id":"C-0001","from_handoff_point_id":"ST1","to_handoff_point_id":"ASRS1-IN1"}'
```

The answer is `202` with the leg and `Location: /fleets/AMR1/legs/<legId>`; follow it with
`curl -s localhost:8080/fleets/AMR1/legs/<legId>` through `ASSIGNED`, `PICKED_UP` and `DONE`, and watch
`curl -s localhost:8080/fleets/AMR1` for the robots and the capacity counts. Repeating the POST with the same key
and body replays the stored answer with `Idempotency-Replayed: true`.

## 5. The emulator's admin calls

These are the calls `cmd/api/e2e_emulator_integration_test.go` makes (emulator `apis/openapi.yaml` at `v0.1.0`):

| Call | What it does |
| --- | --- |
| `GET /asrs/ASRS1` | the AS/RS view, with `ports` (port id -> container id, `""` when empty) |
| `POST /faults` `{"kind":"ROBOT_BATTERY","target":"R3","active":true,"value":15}` | sets R3's battery to 15 %: it drives to `CH1` and charges (`SUSPENDED`) |
| `POST /faults` `{"kind":"ROBOT_BLOCKED","target":"R1","active":true}` | a `CRITICAL` `ROBOT_BLOCKED` error: the robot is `HELD`, its leg fails `ROBOT_BLOCKED` after `ROBOT_BLOCKED_TIMEOUT_SECONDS` |
| `POST /faults` `{"kind":"ROBOT_DISCONNECT","target":"R2","active":true}` | R2 goes silent: `STOPPED` once stale, out of `robots_online` |
| `POST /asrs/ASRS1/seeds` `{"containerId":"C-C","position":{"aisle":2,"level":1,"slot":1,"depth":1}}` | puts a container into a rack position |

Each answers `204`; send the same fault with `"active":false` to clear it (the admin calls are on `localhost:8081`
here).

**A container at an OUTPUT port.** The emulator accepts `POST /asrs/{asrsId}/ports/{portId}/container` on INPUT
ports only (`409 WRONG_PORT_KIND` on `OUT1`/`OUT2`). A container reaches an output port the way asrs-control brings
it there: seed it (`POST /asrs/ASRS1/seeds`), then publish a `RETRIEVE` telegram on `asrs/v1/ASRS1/down`:

```bash
docker exec afc-mosquitto mosquitto_pub -q 1 -t asrs/v1/ASRS1/down -m \
  '{"telegramId":"t-1","type":"RETRIEVE","timestamp":"2026-11-21T10:00:00.000Z","jobId":"t-1","containerId":"C-C","portId":"OUT2","from":{"aisle":2,"level":1,"slot":1,"depth":1}}'
```

Once `GET /asrs/ASRS1` shows `"OUT2":"C-C"`, a leg from `ASRS1-OUT2` picks it. A leg from an empty output port fails
`PICK_FAILED` with `robot_error` `LOAD_NOT_AVAILABLE`.

## 6. The integration tests

`go test -tags=integration ./...` (`make integration`) starts its own Postgres, Kafka and Mosquitto with
testcontainers and the emulator from the LOCAL image named by `EQUIPMENT_EMULATOR_IMAGE` (default
`equipment-emulator:e2e`), so build that image first (step 2). CI builds it from the pinned commit `b58201e` in the
`integration` job.

`make emulator-image` runs that `git archive v0.1.0 | docker build -t equipment-emulator:e2e -` recipe (override
`EMULATOR_DIR` if your equipment-emulator checkout is not a sibling).
