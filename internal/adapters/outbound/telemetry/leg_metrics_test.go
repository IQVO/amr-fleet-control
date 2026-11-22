package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestLegEndedCountsByOutcomeAndReason(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	m, err := NewLegMetrics()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m.LegEnded(ctx, "completed", "")
	m.LegEnded(ctx, "completed", "")
	m.LegEnded(ctx, "failed", "ROBOT_BLOCKED")
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, mt := range sm.Metrics {
			if mt.Name != "amr_fleet_control.legs.ended" || mt.Unit != "{leg}" {
				continue
			}
			for _, dp := range mt.Data.(metricdata.Sum[int64]).DataPoints {
				o, _ := dp.Attributes.Value(attribute.Key("outcome"))
				r, hasReason := dp.Attributes.Value(attribute.Key("reason"))
				k := o.AsString()
				if hasReason {
					k += "/" + r.AsString()
				}
				got[k] = dp.Value
			}
		}
	}
	if got["completed"] != 2 || got["failed/ROBOT_BLOCKED"] != 1 || len(got) != 2 {
		t.Fatalf("data points = %v", got)
	}
	var nilMetrics *LegMetrics
	nilMetrics.LegEnded(ctx, "failed", "X") // a nil port is a no-op
}
