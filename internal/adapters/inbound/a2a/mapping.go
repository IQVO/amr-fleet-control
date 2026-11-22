package a2a

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
)

// problem is a domain or request problem inside the envelope. carry (plan 04 Task 21 lesson, P3-R13): Code is the
// exact slug (the REST catalogue's for a shared outcome, else the type); no field states a replay class or finality.
type problem struct {
	Type   string `json:"type"`
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

func problemMalformed(detail string) *problem {
	return &problem{Type: "malformed-skill-request", Code: "malformed-skill-request", Title: "Malformed skill request", Detail: detail, Status: 400}
}

func problemUnknownSkill(s string) *problem {
	return &problem{Type: "unknown-skill", Code: "unknown-skill", Title: "Unknown skill", Status: 400,
		Detail: fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", s)}
}

func problemInvalidArgument(detail string) *problem {
	return &problem{Type: "invalid-argument", Code: "invalid-argument", Title: "Invalid skill argument", Detail: detail, Status: 400}
}

// useCaseProblems names each typed read error with the REST catalogue's slug (internal/adapters/inbound/http/errors.go)
// as its code, so an agent sees one error vocabulary on every surface. carry (plan 04 Task 21 lesson).
var useCaseProblems = []struct {
	target error
	p      problem
}{
	{usecases.ErrFleetNotFound, problem{Type: "not-found", Code: "fleet-not-found", Title: "Fleet not found", Status: 404}},
	{usecases.ErrLegNotFound, problem{Type: "not-found", Code: "leg-not-found", Title: "Leg not found", Status: 404}},
	{usecases.ErrRobotNotFound, problem{Type: "not-found", Code: "robot-not-found", Title: "Robot not found", Status: 404}},
}

// problemFromUseCase maps a read error: a typed error keeps its slug and message; anything else is logged and
// answered 500 with an adapter-owned detail (never the cause, so no DSN or SQL reaches an agent).
func problemFromUseCase(ctx context.Context, err error) *problem {
	for _, e := range useCaseProblems {
		if errors.Is(err, e.target) {
			p := e.p
			p.Detail = err.Error()
			return &p
		}
	}
	slog.ErrorContext(ctx, "a2a skill failed", "error", logReplacer.Replace(err.Error()))
	return &problem{Type: "internal-error", Code: "internal-error", Title: "Internal error", Detail: "an unexpected internal error occurred", Status: 500}
}

// envelope is the data part of every answer.
type envelope struct {
	Skill   string         `json:"skill"`
	Data    map[string]any `json:"data,omitempty"`
	Problem *problem       `json:"problem,omitempty"`
}
