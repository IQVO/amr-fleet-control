---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Routes (apis/openapi.yaml), each to one `usecases.Service` method — the subsystem-controller interface of WCS
relationship 4, the same shape as asrs-control's relationship 5:

- `GET /healthz` -> liveness; `GET /readyz` -> Postgres, Kafka and the MQTT link
- `GET /fleets/{fleetId}` -> `Fleet` (robots, handoff points, capacity)
- `POST /fleets/{fleetId}/legs` -> `RequestLeg`; 202
- `GET /fleets/{fleetId}/legs/{legId}` -> `Leg`
- `POST /fleets/{fleetId}/legs/{legId}/cancellation` -> `CancelLeg`; 200 cancelled at once, 202 robot asked
- `GET /fleets/{fleetId}/robots/{manufacturer}/{serialNumber}` -> `Robot`

## Conventions

- Errors: RFC 9457 `application/problem+json` with `code`; every problem type declares a replay class in
  `errors.go`. FINAL: 400s, 404 leg-not-found, 409 idempotency-key-reused. RETRYABLE: 404 fleet-not-found,
  robot-not-found; 422 unknown-handoff-point, handoff-unreachable, container-busy, fleet-unavailable; 409
  leg-not-cancellable, leg-ended; 500, 503 (ruling P3-R13: a refusal the map file decides is never FINAL).
- Every POST requires `Idempotency-Key` (1-128 chars); replays carry `Idempotency-Replayed: true`; 24 h window.
- Unauthenticated by fleet decision (2026-09-11); `TestNoAuthMiddlewareReintroduced` keeps it so.
- Bodies are snake_case. No node, edge or order appears in a leg body.
- Middleware order (ADR 0005): RequestID, otelchi, request duration, request log, recoverPanic.
