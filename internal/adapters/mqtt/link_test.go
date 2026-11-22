package mqtt

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

type recUplink struct {
	mu  sync.Mutex
	got []string
}

func (u *recUplink) add(s string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.got = append(u.got, s)
	return nil
}

func (u *recUplink) HandleState(_ context.Context, s vda.State) error {
	return u.add("state " + s.Robot.Key() + " " + s.Report.LastNodeID)
}

func (u *recUplink) HandleConnection(_ context.Context, c vda.Connection) error {
	return u.add("connection " + c.Robot.Key() + " " + string(c.State))
}

func (u *recUplink) HandleFactsheet(_ context.Context, f vda.Factsheet) error {
	return u.add("factsheet " + f.Robot.Key())
}

func (u *recUplink) Tick(context.Context) error { return u.add("tick") }

func (u *recUplink) all() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.got...)
}

const hdr = `"headerId":0,"timestamp":"2026-11-20T09:00:00.000Z","version":"3.0.0","manufacturer":"EMU","serialNumber":"R1"`

func TestTheWorkerHandlesMessagesAndTicksInArrivalOrderAndSkipsGarbage(t *testing.T) {
	up := &recUplink{}
	l := newFleetLink(up, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := l.handle
	h("vda5050/v3/EMU/R1/connection", []byte(`{`+hdr+`,"connectionState":"ONLINE"}`))
	h("vda5050/v3/EMU/R1/state", []byte(`{`+hdr+`,"lastNodeId":"P1"}`))
	h("vda5050/v3/EMU/R1/state", []byte(`{`))
	h("vda5050/v3/EMU/R1/order", []byte(`{}`))
	h("elsewhere", []byte(`{}`))
	h("vda5050/v3/EMU/R1/factsheet", []byte(`{`+hdr+`}`))
	l.inbox.offer(func(ctx context.Context) { l.tick(ctx) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.inbox.drain(ctx)
	want := []string{"connection EMU/R1 ONLINE", "state EMU/R1 P1", "factsheet EMU/R1", "tick"}
	deadline := time.Now().Add(2 * time.Second)
	for len(up.all()) < len(want) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := up.all(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("handled = %v", got)
	}
}

// ruling P3-R10: the sends are asynchronous now, so this brief test starts the link's sender,
// guards the recording with a mutex and waits for the three publishes; its assertions are the
// brief's, unchanged.
func TestSendPublishesOnTheRobotsTopicWithCountingHeaders(t *testing.T) {
	l := newFleetLink(&recUplink{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var mu sync.Mutex
	var topics []string
	var bodies []map[string]any
	l.pub = func(topic string, b []byte) error {
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		mu.Lock()
		defer mu.Unlock()
		topics, bodies = append(topics, topic), append(bodies, m)
		return nil
	}
	r1 := robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.Start(ctx)
	if err := l.SendOrder(ctx, vda.Order{Robot: r1, OrderID: "leg-1"}); err != nil {
		t.Fatal(err)
	}
	_ = l.SendOrder(ctx, vda.Order{Robot: r1, OrderID: "leg-1", UpdateID: 1})
	_ = l.SendInstantActions(ctx, vda.InstantActions{Robot: r1, Actions: []vda.Action{{Type: "cancelOrder", ID: "x", Blocking: "HARD"}}})
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		mu.Lock()
		n := len(topics)
		mu.Unlock()
		if n == 3 {
			break
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(topics) != 3 {
		t.Fatalf("published %d of 3: %v", len(topics), topics)
	}
	if topics[0] != "vda5050/v3/EMU/R1/order" || topics[2] != "vda5050/v3/EMU/R1/instantActions" {
		t.Fatalf("topics = %v", topics)
	}
	if bodies[0]["headerId"] != 0.0 || bodies[1]["headerId"] != 1.0 || bodies[2]["headerId"] != 0.0 || bodies[1]["orderUpdateId"] != 1.0 {
		t.Fatalf("bodies = %v", bodies)
	}
}
