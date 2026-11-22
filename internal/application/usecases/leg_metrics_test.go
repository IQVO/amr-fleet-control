package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/application/ports"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// armedSink fails every Append once armed, so a unit that ended a leg rolls back.
type armedSink struct {
	inner ports.EventSink
	armed bool
}

func (s *armedSink) Append(ctx context.Context, evs ...any) error {
	if s.armed {
		return errors.New("sink down")
	}
	return s.inner.Append(ctx, evs...)
}

// carry (Task 15 review): a failed leg counts once with its LegFailed reason and no leg or robot id,
// so amr_fleet_control.legs.ended keeps a low-cardinality reason label (ADR 0005 decision 3).
func TestAFailedLegCountsOnceWithItsReasonAndNoID(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	h.leg("C2", "ST1", "ASRS1-IN1")
	h.ack("R1")
	h.report("R1", func(r *robot.Report) {
		r.Errors = []robot.Error{{Type: robot.ErrTypeBatteryEmpty, Level: robot.LevelFatal}}
	})
	if got := h.metrics.Ended(); !reflect.DeepEqual(got, []string{"failed/ROBOT_FAULT"}) {
		t.Fatalf("metrics = %v, want [failed/ROBOT_FAULT]", got)
	}
}

// carry (Task 15 review): the counter records in the after-commit hook of the unit that ended the
// leg, so an ending its own unit or the outer unit rolls back counts nothing (ADR 0005 decision 3).
func TestALegEndingThatRollsBackCountsNothing(t *testing.T) {
	var sink *armedSink
	h := newHarnessWith(t, floor(t), nil, func(d *usecases.Deps) {
		sink = &armedSink{inner: d.Sink}
		d.Sink = sink
	})
	h.online("R1", "P1", 90)
	h.leg("C2", "ST1", "ASRS1-IN1")
	h.ack("R1")
	rep := h.seen["R1"]
	rep.Errors = []robot.Error{{Type: robot.ErrTypeBatteryEmpty, Level: robot.LevelFatal}}
	rep.At = rep.At.Add(1)
	sink.armed = true
	if err := h.svc.HandleState(ctx, vda.State{Robot: rid("R1"), Report: rep}); err == nil {
		t.Fatal("HandleState with the sink down = nil, want the append error")
	}
	if got := h.metrics.Ended(); len(got) != 0 {
		t.Fatalf("a rolled-back unit counted %v", got)
	}
	// The outer unit (the idempotency middleware's, P3-R15) fails after the use case flushed:
	// the leg ending rolls back with it, so it must not count.
	sink.armed = false
	boom := errors.New("outer unit fails after the use case")
	err := h.svc.UnitOfWork().Do(ctx, func(ctx context.Context) error {
		if err := h.svc.HandleState(ctx, vda.State{Robot: rid("R1"), Report: rep}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("outer Do = %v, want boom", err)
	}
	if got := h.metrics.Ended(); len(got) != 0 {
		t.Fatalf("a leg ending rolled back by the outer unit counted %v", got)
	}
}
