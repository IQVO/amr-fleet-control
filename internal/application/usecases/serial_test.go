package usecases_test

import (
	"context"
	"os"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/memory"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
)

// carry (Task 11 review): ruling P3-R10 (amended) serializes at the OUTERMOST unit of work. Task
// 14's Idempotency middleware opens that unit around every POST and the use case joins it; with
// the lock inside the use case the lock order inverted (tx -> mutex vs mutex -> tx) and this
// pair deadlocked on the memory unit of work.
func TestAnOuterUnitOfWorkAndTheTickDoNotDeadlock(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	uow := h.svc.UnitOfWork()
	h.online("R1", "P1", 90)
	done := make(chan error, 2)
	go func() {
		done <- uow.Do(context.Background(), func(ctx context.Context) error {
			time.Sleep(50 * time.Millisecond) // middleware work before the handler
			_, err := h.svc.RequestLeg(ctx, usecases.LegRequest{FleetID: "AMR1", ContainerID: "C1", From: "ST1", To: "ASRS1-IN1"})
			return err
		})
	}()
	time.Sleep(10 * time.Millisecond)
	go func() { done <- h.svc.Tick(context.Background()) }()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
			t.Fatalf("DEADLOCK: %d of 2 units finished (outer unit of work + RequestLeg vs Tick)", i)
		}
	}
}

// carry (Task 11 review): ruling P3-R10 through the HTTP path: T0 commits a leg inside an outer
// unit of work whose hooks run late; T1 cancels it inside another outer unit of work. The
// commands reach the Downlink in commit order: order, then cancelOrder.
func TestCommandsReachTheDownlinkInCommitOrderThroughAnOuterUnitOfWork(t *testing.T) {
	late := &lateHooks{hold: 300 * time.Millisecond, first: make(chan struct{}), second: make(chan struct{})}
	seq := &seqDownlink{}
	h := newHarnessWith(t, floor(t), nil, func(d *usecases.Deps) {
		late.inner, d.UoW = d.UoW, late
		seq.Downlink, d.Downlink = d.Downlink, seq
	})
	svc, uow := h.svc, h.svc.UnitOfWork()
	h.online("R1", "P1", 90)
	late.arm()
	t0 := make(chan error, 1)
	go func() {
		t0 <- uow.Do(ctx, func(ctx context.Context) error {
			_, err := svc.RequestLeg(ctx, usecases.LegRequest{FleetID: "AMR1", ContainerID: "C1", From: "ST1", To: "ASRS1-IN1"})
			return err
		})
	}()
	<-late.first
	open, err := h.missions.Open(ctx)
	if err != nil || len(open) != 1 {
		t.Fatalf("open legs after T0 committed = %d, %v", len(open), err)
	}
	legID := open[0].ID()
	if err := uow.Do(ctx, func(ctx context.Context) error {
		_, _, err := svc.CancelLeg(ctx, "AMR1", legID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-t0; err != nil {
		t.Fatal(err)
	}
	seq.mu.Lock()
	defer seq.mu.Unlock()
	if len(seq.got) != 2 || seq.got[0] != "order "+legID || seq.got[1] != "cancelOrder "+legID+"-cancel" {
		t.Fatalf("downlink order = %v, want the order of %s then its cancelOrder", seq.got, legID)
	}
}

// carry (Task 11 review): a unit joins only the OPEN frame of its own decorator; a ctx kept from
// a finished unit (captured inside its fn; no hook is involved) takes the lock again, and a hook
// registered in a fresh unit still runs.
func TestASerialUnitOfWorkJoinsOnlyItsOwnOpenFrame(t *testing.T) {
	s := usecases.NewSerialUnitOfWork(memory.NewUnitOfWork(memory.NewStore()))
	var kept context.Context
	if err := s.Do(ctx, func(c context.Context) error {
		kept = c
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	holding, release := make(chan struct{}), make(chan struct{})
	go func() {
		_ = s.Do(ctx, func(context.Context) error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding
	entered := make(chan struct{})
	go func() {
		_ = s.Do(kept, func(context.Context) error {
			close(entered)
			return nil
		})
	}()
	select {
	case <-entered:
		t.Fatal("a ctx of a finished unit joined the running unit instead of waiting for its lock")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the waiting unit never ran after the lock was released")
	}
	var hooked bool
	if err := s.Do(ctx, func(c context.Context) error {
		s.AfterCommit(c, func() { hooked = true })
		return nil
	}); err != nil || !hooked {
		t.Fatalf("err = %v, hook ran = %v (AfterCommit passes through to the inner unit of work)", err, hooked)
	}
}
