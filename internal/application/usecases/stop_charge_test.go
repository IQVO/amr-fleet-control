package usecases_test

import (
	"errors"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// carry (Task 11 review): a lost stopCharging (refused by a full downlink, lost with the
// connection, dropped at shutdown) is sent again after ORDER_RESEND_AFTER while the robot keeps
// charging, so a charged robot is never stranded SUSPENDED.
func TestALostStopChargingIsResentAfterTheResendDelay(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 15)
	o := h.lastOrder("R1")
	h.drive("R1", 0, 5)
	h.report("R1", func(r *robot.Report) {
		r.Pending, r.Charging, r.Battery = false, true, 50
		setAction(r, robot.ActionState{ID: o.OrderID + "-startCharging", Status: robot.ActionFinished}, false)
	})
	h.down.Fail(errors.New("mqtt downlink full (1024 commands)"))
	h.report("R1", func(r *robot.Report) { r.Battery = 80 })
	h.down.Fail(nil)
	h.must(h.svc.Tick(ctx))
	if ia := h.instants("R1"); len(ia) != 0 {
		t.Fatalf("stopCharging resent before ORDER_RESEND_AFTER: %+v", ia)
	}
	h.clock.Advance(usecases.DefaultConfig().ResendAfter)
	h.must(h.svc.Tick(ctx))
	ia := h.instants("R1")
	if len(ia) != 1 || ia[0].Type != vda.StopCharging {
		t.Fatalf("after ORDER_RESEND_AFTER the lost stopCharging is sent again: %+v", ia)
	}
	h.must(h.svc.Tick(ctx))
	if n := len(h.instants("R1")); n != 1 {
		t.Fatalf("stopCharging resent %d times within one resend delay", n)
	}
	h.report("R1", func(r *robot.Report) { r.Charging = false })
	h.clock.Advance(usecases.DefaultConfig().ResendAfter)
	h.must(h.svc.Tick(ctx))
	if n := len(h.instants("R1")); n != 1 {
		t.Fatalf("a robot that stopped charging got stopCharging again (%d)", n)
	}
}
