package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// errorCatalogue maps use-case errors to the slugs of the REST catalogue.
var errorCatalogue = []struct {
	target error
	slug   string
}{
	{usecases.ErrFleetNotFound, "fleet-not-found"},
	{usecases.ErrLegNotFound, "leg-not-found"},
	{usecases.ErrRobotNotFound, "robot-not-found"},
	{robot.ErrInvalidRobot, "validation-failed"},
}

// toolErr is a tool error the model reads as "<slug>: <detail>"; the detail may echo its input.
type toolErr struct{ slug, detail string }

func (e toolErr) Error() string { return fmt.Sprintf("%s: %s", e.slug, e.detail) }

func toolError(slug, detail string) error {
	return toolErr{slug: slug, detail: detail}
}

// slugOf names a tool error by its catalogue slug only. carry (Task 18 review M3): spans record the
// slug, never the detail, which for a not-found echoes the raw model input.
func slugOf(err error) string {
	var te toolErr
	if errors.As(err, &te) {
		return te.slug
	}
	return "internal-error"
}

func mapError(ctx context.Context, err error) error {
	for _, e := range errorCatalogue {
		if errors.Is(err, e.target) {
			return toolError(e.slug, err.Error())
		}
	}
	slog.ErrorContext(ctx, "mcp tool failed with an unexpected error", "error", err)
	return toolError("internal-error", "an unexpected internal error occurred")
}
