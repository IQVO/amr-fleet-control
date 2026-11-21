# 0004. The subsystem-controller REST interface (relationship 4) and its replay classes

Date: 2026-11-20

## Status

Accepted

## Context

The WCS context map: "Relationships 4 and 5 use the same shape so that the three WCS contexts follow one pattern"
and "an unknown container, a busy container or an unreachable destination must fail the request at once".
asrs-control built relationship 5 as `POST /asrs/{asrsId}/storage-jobs` (202 or 422), a `/cancellation`
sub-resource (200 or 202, 409 when the state forbids it) and every POST idempotent.

## Decision

1. Routes: `POST /fleets/{fleetId}/legs` (202 + Location, or 422), `GET /fleets/{fleetId}/legs/{legId}`,
   `POST /fleets/{fleetId}/legs/{legId}/cancellation` (200 cancelled at once, 202 robot asked, 409
   leg-not-cancellable / leg-ended), `GET /fleets/{fleetId}`, `GET /fleets/{fleetId}/robots/{manufacturer}/{serialNumber}`.
2. Replay classes (harness template docs/idempotency-replay-classes.md): FINAL = bad-request, invalid-parameter,
   fleet-not-found, leg-not-found, robot-not-found, unknown-handoff-point, idempotency-key-required,
   idempotency-key-reused; RETRYABLE = handoff-unreachable (a new map version may join them), container-busy,
   fleet-unavailable, leg-not-cancellable, leg-ended, internal-error, dependency-unavailable. No refusal commits
   writes, so no OUTCOME class exists.
3. The Idempotency-Key middleware is asrs-control's (key row in the request's unit of work, 24-hour window, age
   check on replay, hourly purge).

## Consequences

material-flow-control (plan 04) treats asrs-control and amr-fleet-control alike; a new refusal must declare its
class or `TestEveryProblemTypeDeclaresAReplayClass` fails.
