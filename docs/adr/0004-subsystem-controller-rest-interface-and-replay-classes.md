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
   leg-not-found, idempotency-key-required, idempotency-key-reused; RETRYABLE = fleet-not-found,
   unknown-handoff-point, handoff-unreachable, robot-not-found, container-busy, fleet-unavailable,
   leg-not-cancellable, leg-ended, internal-error, dependency-unavailable. A refusal decided by configuration that
   can change inside the 24-hour replay window without the client changing its request (the map file:
   fleet-not-found, unknown-handoff-point, handoff-unreachable) is RETRYABLE, never FINAL, so a map-fix deploy
   never leaves a deterministic-key retry pinned to the old refusal; robot-not-found is RETRYABLE too (robot facts
   create robots). leg-not-found stays FINAL (legs are minted here and never deleted). No refusal commits writes, so
   no OUTCOME class exists.
3. The Idempotency-Key middleware follows asrs-control's (key row in the request's unit of work, 24-hour window,
   age check on replay, hourly purge), with three differences:
   - its unit of work is the OUTERMOST serialized unit of a POST: the Service's SerialUnitOfWork, so the order is
     always serial lock -> transaction -> fleet row and the after-commit robot commands go out inside the lock (in
     commit order on the wire). `NewIdempotency` takes the Service, never a unit of work, and panics at boot when
     what the Service hands out lacks the `Serialized` marker: a raw unit of work or a second serial decorator (its
     own mutex, never joined by the Service's) would bring back the ABBA deadlock against the link worker's Tick;
   - it re-reads the key under the serial lock before running the handler: a concurrent duplicate that passed the
     first lookup while the original held the lock replays the original's committed answer (another body: 409
     idempotency-key-reused) instead of re-running the use case into a RETRYABLE refusal (container-busy, leg-ended);
   - the replay classes of decision 2 (ruling P3-R13): a refusal the map file or the robot registry decides is
     RETRYABLE, so its key is released and a retry is evaluated again.

## Consequences

material-flow-control (plan 04) treats asrs-control and amr-fleet-control alike; a new refusal must declare its
class or `TestEveryProblemTypeDeclaresAReplayClass` fails.
