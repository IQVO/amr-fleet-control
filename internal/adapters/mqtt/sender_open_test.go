package mqtt

import (
	"context"
	"sync"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
)

// carry (Task 11 review): ruling P3-R10 across a reconnect. A packet handed to paho while the
// connection is down is stored and resumed later, possibly after commands queued behind it; so
// the sender keeps every command in its own FIFO until the connection is open again, and the
// order and the headerIds on the wire stay the enqueue order.
func TestTheSenderNeverPublishesWhileTheConnectionIsClosed(t *testing.T) {
	var mu sync.Mutex
	checks, openNow := 0, false
	l := quietLink()
	l.openPoll = time.Millisecond
	l.open = func() bool {
		mu.Lock()
		defer mu.Unlock()
		checks++
		openNow = checks > 5
		return openNow
	}
	w := &wire{}
	record := w.pub(func(string, float64) time.Duration { return 0 })
	l.pub = func(topic string, b []byte) error {
		mu.Lock()
		closed := !openNow
		mu.Unlock()
		if closed {
			t.Errorf("published %s while the connection was closed", topic)
		}
		return record(topic, b)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Start(ctx)
	_ = l.SendOrder(ctx, vda.Order{Robot: r1, OrderID: "leg-1"})
	_ = l.SendInstantActions(ctx, vda.InstantActions{Robot: r1, Actions: []vda.Action{{Type: "cancelOrder", ID: "c1", Blocking: "HARD"}}})
	_ = l.SendOrder(ctx, vda.Order{Robot: r1, OrderID: "leg-2"})
	got := w.await(t, 3)
	want := []wired{{"vda5050/v3/EMU/R1/order", 0}, {"vda5050/v3/EMU/R1/instantActions", 0}, {"vda5050/v3/EMU/R1/order", 1}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("wire = %v, want %v", got, want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if checks < 6 {
		t.Fatalf("the sender checked the connection %d times, want it to wait until the 6th check", checks)
	}
}

// carry (Task 11 review): publishes paho stored at the moment of a loss resume in publish order.
func TestTheClientResumesStoredPublishesInOrder(t *testing.T) {
	o := clientOptions("tcp://127.0.0.1:1883", "amr-fleet-control", func(pahomqtt.Client) {})
	if _, ok := o.Store.(*pahomqtt.OrderedMemoryStore); !ok {
		t.Fatalf("store = %T, want *OrderedMemoryStore (the default MemoryStore resumes in map order)", o.Store)
	}
}

// carry (Task 11 review): commands still queued when the sender stops are counted in the log
// (the tick resends orders, cancels and stopCharging after a restart).
func TestTheSenderLogsTheCommandsItDropsAtExit(t *testing.T) {
	sink := &logSink{}
	l := newFleetLink(&recUplink{}, sink.logger())
	l.open, l.openPoll = func() bool { return false }, time.Millisecond
	l.pub = func(string, []byte) error { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	for i := 0; i < 3; i++ {
		_ = l.SendOrder(ctx, vda.Order{Robot: r1, OrderID: "leg-1"})
	}
	l.Start(ctx)
	for deadline := time.Now().Add(2 * time.Second); len(sink.records("robot commands wait")) == 0 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for len(sink.records("mqtt sender stopped")) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := sink.records("mqtt sender stopped"); len(got) != 1 || got[0]["level"] != "WARN" || got[0]["dropped"] != 3.0 {
		t.Fatalf("records = %v", got)
	}
}
