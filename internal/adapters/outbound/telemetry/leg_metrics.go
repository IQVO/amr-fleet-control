package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/IQVO/amr-fleet-control/internal/application/ports"
)

// meterName scopes this service's own instruments.
const meterName = "github.com/IQVO/amr-fleet-control"

// legsEndedName is the Tier 2 business metric (ADR 0005): every accepted leg's one outcome.
const legsEndedName = "amr_fleet_control.legs.ended"

// LegMetrics implements ports.Metrics against the global MeterProvider (a no-op until Setup).
type LegMetrics struct {
	ended metric.Int64Counter
}

var _ ports.Metrics = (*LegMetrics)(nil)

// NewLegMetrics registers the counter; call it after Setup.
func NewLegMetrics() (*LegMetrics, error) {
	ended, err := otel.Meter(meterName).Int64Counter(legsEndedName,
		metric.WithDescription("Accepted legs by outcome: completed, or failed with reason (CANCELLED, ROBOT_BLOCKED, ROBOT_FAULT, "+
			"ROBOT_OFFLINE, ORDER_REJECTED, PICK_FAILED, DROP_FAILED, MAP_VERSION_RETIRED). A rising ROBOT_BLOCKED share means "+
			"traffic or deadlocks; a rising ORDER_REJECTED means the map and the robots' world disagree."),
		metric.WithUnit("{leg}"))
	if err != nil {
		return nil, err
	}
	return &LegMetrics{ended: ended}, nil
}

// LegEnded implements ports.Metrics.
func (m *LegMetrics) LegEnded(ctx context.Context, outcome, reason string) {
	if m == nil || m.ended == nil {
		return
	}
	attrs := []attribute.KeyValue{attribute.String("outcome", outcome)}
	if reason != "" {
		attrs = append(attrs, attribute.String("reason", reason))
	}
	m.ended.Add(ctx, 1, metric.WithAttributes(attrs...))
}
