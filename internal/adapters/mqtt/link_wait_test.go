package mqtt

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
)

// lesson (plan 04 Task 19 review, shutdown order): the composition root waits for the link's worker,
// tick and sender before it disconnects the link and closes the pool, so no robot message is applied
// on a closed pool. Wait blocks while the link runs, and after the cancel it returns only once the
// task the worker was running and the publish the sender was making have finished; each case slows
// exactly one of them, so dropping either from Wait fails one case.
func TestWaitReturnsOnceTheWorkerTheTickAndTheSenderStopped(t *testing.T) {
	for _, c := range []struct {
		name                   string
		slowWorker, slowSender bool
	}{{"slow worker", true, false}, {"slow sender", false, true}} {
		t.Run(c.name, func(t *testing.T) { checkWait(t, c.slowWorker, c.slowSender) })
	}
}

func lag(slow bool) {
	if slow {
		time.Sleep(200 * time.Millisecond) // finishing after the cancel
	}
}

func checkWait(t *testing.T, slowWorker, slowSender bool) {
	sink := &logSink{}
	l := newFleetLink(&recUplink{}, sink.logger())
	publishing := make(chan struct{})
	var published atomic.Bool
	l.pub = func(string, []byte) error { // a publish still waiting for its PUBACK at the cancel
		close(publishing)
		lag(slowSender)
		published.Store(true)
		return nil
	}
	_ = l.SendOrder(context.Background(), vda.Order{Robot: r1, OrderID: "leg-1"})
	var finished atomic.Bool
	running := make(chan struct{})
	l.inbox.offer(func(ctx context.Context) {
		close(running)
		<-ctx.Done()
		lag(slowWorker)
		finished.Store(true)
	})
	ctx, cancel := context.WithCancel(context.Background())
	l.Start(ctx)
	<-running
	<-publishing
	waited := make(chan struct{})
	go func() { l.Wait(); close(waited) }()
	select {
	case <-waited:
		t.Fatal("Wait returned while the link was running")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after the cancel")
	}
	if !finished.Load() {
		t.Fatal("Wait returned before the worker's running task finished")
	}
	if !published.Load() || len(sink.records("mqtt sender stopped")) != 1 {
		t.Fatalf("Wait returned before the sender stopped: published %v, records %v", published.Load(), sink.records("mqtt sender stopped"))
	}
}

// Wait on a link that never started returns at once (a boot that failed before Start).
func TestWaitOnALinkThatNeverStartedReturns(t *testing.T) {
	l := newFleetLink(&recUplink{}, (&logSink{}).logger())
	done := make(chan struct{})
	go func() { l.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait blocked on a link that never started")
	}
}

// lesson (plan 04 Task 19 review): Close logs the disconnect with the commands still unsent, which
// includes any a draining HTTP request enqueued after the sender stopped.
func TestCloseLogsTheUnsentCommands(t *testing.T) {
	sink := &logSink{}
	l := newFleetLink(&recUplink{}, sink.logger())
	_ = l.SendOrder(context.Background(), vda.Order{Robot: r1, OrderID: "leg-1"})
	_ = l.SendOrder(context.Background(), vda.Order{Robot: r1, OrderID: "leg-1", UpdateID: 1})
	l.Close()
	if got := sink.records("mqtt link disconnected"); len(got) != 1 || got[0]["unsent"] != 2.0 || got[0]["level"] != "WARN" {
		t.Fatalf("records = %v, want one WARN with unsent 2", got)
	}
}
