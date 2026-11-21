package http

import (
	"errors"
	"net/http"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
)

const problemBaseURI = "https://errors.amr-fleet-control.warehouse-systems.dev/"

// replayClass tells the Idempotency-Key middleware what to do with a refusal
// (warehouse-harness-template docs/idempotency-replay-classes.md): FINAL is stored and replayed,
// RETRYABLE releases the key. No refusal here commits writes, so no OUTCOME class exists.
type replayClass string

const (
	classFinal     replayClass = "FINAL"
	classRetryable replayClass = "RETRYABLE"
)

type problem struct {
	status int
	slug   string
	title  string
	class  replayClass
}

var (
	errMalformedRequest = errors.New("request body is not valid JSON for this operation")
	errInvalidParameter = errors.New("parameter is invalid")
)

var (
	internalProblem        = problem{http.StatusInternalServerError, "internal-error", "Internal server error", classRetryable}
	dependencyUnavailable  = problem{http.StatusServiceUnavailable, "dependency-unavailable", "A runtime dependency is unavailable", classRetryable}
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required", classFinal}
	idempotencyKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request", classFinal}
	invalidParameter       = problem{http.StatusBadRequest, "invalid-parameter", "Parameter is invalid", classFinal}
)

// problemCatalogue maps every refusal to its problem type (plan 03, relationship-4 table).
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, problem{http.StatusBadRequest, "bad-request", "Malformed request", classFinal}},
	{errInvalidParameter, invalidParameter},
	{usecases.ErrInvalidRequest, invalidParameter},
	{usecases.ErrFleetNotFound, problem{http.StatusNotFound, "fleet-not-found", "Fleet not found", classFinal}},
	{usecases.ErrLegNotFound, problem{http.StatusNotFound, "leg-not-found", "Leg not found", classFinal}},
	{usecases.ErrRobotNotFound, problem{http.StatusNotFound, "robot-not-found", "Robot not found", classFinal}},
	{usecases.ErrUnknownHandoff, problem{http.StatusUnprocessableEntity, "unknown-handoff-point", "The handoff point is not on this fleet's map", classFinal}},
	{usecases.ErrHandoffUnreachable, problem{http.StatusUnprocessableEntity, "handoff-unreachable", "No route joins the two handoff points on the current map", classRetryable}},
	{usecases.ErrContainerBusy, problem{http.StatusUnprocessableEntity, "container-busy", "An open leg already moves the container", classRetryable}},
	{usecases.ErrFleetUnavailable, problem{http.StatusUnprocessableEntity, "fleet-unavailable", "No robot of the fleet is online", classRetryable}},
	{usecases.ErrNotCancellable, problem{http.StatusConflict, "leg-not-cancellable", "The container is already on the robot", classRetryable}},
	{usecases.ErrLegEnded, problem{http.StatusConflict, "leg-ended", "The leg has already ended", classRetryable}},
}

func problemFor(err error) problem {
	for _, e := range problemCatalogue {
		if errors.Is(err, e.err) {
			return e.p
		}
	}
	return internalProblem
}
