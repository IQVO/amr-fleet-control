package a2a

import (
	"errors"
	"fmt"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
)

type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

func problemMalformed(detail string) *problem {
	return &problem{Type: "malformed-skill-request", Title: "Malformed skill request", Detail: detail, Status: 400}
}

func problemUnknownSkill(s string) *problem {
	return &problem{Type: "unknown-skill", Title: "Unknown skill", Status: 400,
		Detail: fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", s)}
}

func problemInvalidArgument(detail string) *problem {
	return &problem{Type: "invalid-argument", Title: "Invalid skill argument", Detail: detail, Status: 400}
}

func problemFromUseCase(err error) *problem {
	for _, nf := range []error{usecases.ErrFleetNotFound, usecases.ErrLegNotFound, usecases.ErrRobotNotFound} {
		if errors.Is(err, nf) {
			return &problem{Type: "not-found", Title: "Not found", Detail: err.Error(), Status: 404}
		}
	}
	return &problem{Type: "internal-error", Title: "Internal error", Detail: "an unexpected internal error occurred", Status: 500}
}

// envelope is the data part of every answer.
type envelope struct {
	Skill   string         `json:"skill"`
	Data    map[string]any `json:"data,omitempty"`
	Problem *problem       `json:"problem,omitempty"`
}
