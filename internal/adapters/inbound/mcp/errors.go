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

func toolError(slug, detail string) error {
	return fmt.Errorf("%s: %s", slug, detail)
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
