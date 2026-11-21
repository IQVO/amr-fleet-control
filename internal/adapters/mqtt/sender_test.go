package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// wire records what the fake publisher put on the wire, in order.
type wire struct {
	mu   sync.Mutex
	msgs []wired
}

type wired struct {
	topic    string
	headerID float64
}

func (w *wire) pub(delay func(topic string, headerID float64) time.Duration) func(string, []byte) error {
	return func(topic string, b []byte) error {
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		id, _ := m["headerId"].(float64)
		time.Sleep(delay(topic, id))
		w.mu.Lock()
		defer w.mu.Unlock()
		w.msgs = append(w.msgs, wired{topic: topic, headerID: id})
		return nil
	}
}

func (w *wire) await(t *testing.T, n int) []wired {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		got := append([]wired(nil), w.msgs...)
		w.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	t.Fatalf("wire holds %d of %d messages: %v", len(w.msgs), n, w.msgs)
	return nil
}

var r1 = robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}

func started(t *testing.T, pub func(string, []byte) error) *FleetLink {
	t.Helper()
	l := quietLink()
	l.pub = pub
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	l.Start(ctx)
	return l
}

// ruling P3-R10: SendOrder only enqueues; the publish and its PUBACK wait happen in the sender.
func TestSendEnqueuesWithoutWaitingForThePublish(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	l := started(t, func(string, []byte) error {
		<-gate
		return nil
	})
	done := make(chan struct{})
	go func() {
		_ = l.SendOrder(context.Background(), vda.Order{Robot: r1, OrderID: "leg-1"})
		_ = l.SendInstantActions(context.Background(), vda.InstantActions{Robot: r1})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SendOrder/SendInstantActions blocked on a publish that has not completed")
	}
}

// ruling P3-R10: T0 enqueues an order, T1 then a cancel; a slow first publish does not let the
// cancel overtake the order on the wire.
func TestTheWireOrderIsTheEnqueueOrder(t *testing.T) {
	w := &wire{}
	l := started(t, w.pub(func(topic string, _ float64) time.Duration {
		if topic == "vda5050/v3/EMU/R1/order" {
			return 50 * time.Millisecond
		}
		return 0
	}))
	t0 := make(chan struct{})
	go func() {
		_ = l.SendOrder(context.Background(), vda.Order{Robot: r1, OrderID: "leg-1"})
		close(t0)
	}()
	<-t0
	cancelOrder := vda.InstantActions{Robot: r1, Actions: []vda.Action{{Type: "cancelOrder", ID: "c1", Blocking: "HARD"}}}
	if err := l.SendInstantActions(context.Background(), cancelOrder); err != nil {
		t.Fatal(err)
	}
	got := w.await(t, 2)
	if got[0].topic != "vda5050/v3/EMU/R1/order" || got[1].topic != "vda5050/v3/EMU/R1/instantActions" {
		t.Fatalf("wire order = %v, want order then instantActions", got)
	}
}

// ruling P3-R10: headers are taken in the sender, so concurrent callers still put headerIds on
// the wire in increasing order (earlier ids publish slower here).
func TestHeaderIdsAreMonotonicOnTheWireUnderConcurrency(t *testing.T) {
	const n = 20
	w := &wire{}
	l := started(t, w.pub(func(_ string, id float64) time.Duration {
		return time.Duration(n-int(id)) * time.Millisecond
	}))
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = l.SendOrder(context.Background(), vda.Order{Robot: r1, OrderID: "leg-1"})
		}()
	}
	wg.Wait()
	got := w.await(t, n)
	for i, m := range got {
		if m.headerID != float64(i) {
			t.Fatalf("message %d on the wire has headerId %v: %v", i, m.headerID, got)
		}
	}
}

// ruling P3-R10: a full downlink refuses at once (the use case logs it and the tick resends),
// and a failed publish is logged by the sender.
func TestAFullDownlinkRefusesAndAFailedPublishIsLogged(t *testing.T) {
	l := quietLink()
	l.pub = func(string, []byte) error { return nil }
	var err error
	for i := 0; i <= DownlinkSize && err == nil; i++ {
		err = l.SendOrder(context.Background(), vda.Order{Robot: r1, OrderID: "leg-1"})
	}
	if err == nil {
		t.Fatalf("%d commands queued without a sender and none refused", DownlinkSize+1)
	}
	sink := &logSink{}
	f := newFleetLink(&recUplink{}, sink.logger())
	f.pub = func(string, []byte) error { return errors.New("mqtt publish vda5050/v3/EMU/R1/order: timeout") }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.Start(ctx)
	_ = f.SendOrder(ctx, vda.Order{Robot: r1, OrderID: "leg-1"})
	deadline := time.Now().Add(2 * time.Second)
	for len(sink.records("robot command not sent")) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := sink.records("robot command not sent"); len(got) != 1 || got[0]["level"] != "WARN" ||
		got[0]["topic"] != "vda5050/v3/EMU/R1/order" || got[0]["error"] != "mqtt publish vda5050/v3/EMU/R1/order: timeout" {
		t.Fatalf("records = %v", got)
	}
}
