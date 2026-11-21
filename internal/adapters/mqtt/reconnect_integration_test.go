//go:build integration

package mqtt

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/testing/mqtttest"
)

// cutProxy forwards TCP to the broker; cut drops every live connection and refuses new ones
// until heal. A restarted broker container gets a NEW mapped port (docker stop/start moved
// 59168 to 59169), so the link would never find it again; cutting its TCP connection drives
// paho's same connection-lost -> reconnect path while the broker keeps running.
type cutProxy struct {
	ln     net.Listener
	target string
	mu     sync.Mutex
	down   bool
	conns  []net.Conn
}

func newCutProxy(t *testing.T, brokerURL string) *cutProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &cutProxy{ln: ln, target: strings.TrimPrefix(brokerURL, "tcp://")}
	t.Cleanup(func() { _ = ln.Close(); p.cut() })
	go p.serve()
	return p
}

func (p *cutProxy) url() string { return "tcp://" + p.ln.Addr().String() }

func (p *cutProxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		down := p.down
		p.mu.Unlock()
		if down {
			_ = c.Close()
			continue
		}
		b, err := net.DialTimeout("tcp", p.target, 5*time.Second)
		if err != nil {
			_ = c.Close()
			continue
		}
		p.mu.Lock()
		p.conns = append(p.conns, c, b)
		p.mu.Unlock()
		go func() { _, _ = io.Copy(b, c); _ = b.Close() }()
		go func() { _, _ = io.Copy(c, b); _ = c.Close() }()
	}
}

func (p *cutProxy) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = true
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

func (p *cutProxy) heal() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = false
}

// robotInbox records what a robot receives on its order and instantActions topics, in order.
type robotInbox struct {
	mu  sync.Mutex
	got []string
}

func (r *robotInbox) handle(_ pahomqtt.Client, m pahomqtt.Message) {
	var b struct {
		HeaderID float64 `json:"headerId"`
		OrderID  string  `json:"orderId"`
	}
	_ = json.Unmarshal(m.Payload(), &b)
	name := m.Topic()[strings.LastIndex(m.Topic(), "/")+1:]
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, name+" "+b.OrderID+" "+string(rune('0'+int(b.HeaderID))))
}

func (r *robotInbox) await(t *testing.T, n int, wait time.Duration) []string {
	t.Helper()
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if got := r.all(); len(got) >= n {
			return got
		}
	}
	_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
	t.Fatalf("the robot received %d of %d commands: %v", len(r.all()), n, r.all())
	return nil
}

func (r *robotInbox) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

// carry (Task 11 review): ruling P3-R10 across a broker blip: commands enqueued while the
// link's connection is down reach the robot after the reconnect, in enqueue order (order, then
// its cancel, then the next order), with increasing headerIds; none is lost.
func TestCommandsQueuedDuringAConnectionLossReachTheRobotInOrder(t *testing.T) {
	broker := mqtttest.Broker(t)
	robotSide, err := connect(broker, "emu-blip", func(pahomqtt.Client) {})
	if err != nil {
		t.Fatal(err)
	}
	defer robotSide.Disconnect(0)
	in := &robotInbox{}
	for _, topic := range []string{"vda5050/v3/EMU/R1/order", "vda5050/v3/EMU/R1/instantActions"} {
		if err := subscribe(robotSide, topic, in.handle); err != nil {
			t.Fatal(err)
		}
	}
	proxy := newCutProxy(t, broker)
	link, err := NewFleetLink(proxy.url(), "amr-fleet-control-blip", &recUplink{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	link.Start(ctx)
	_ = link.SendOrder(ctx, vda.Order{Robot: r1, OrderID: "leg-0"})
	in.await(t, 1, 10*time.Second)
	proxy.cut()
	awaitLinkDown(t, link)
	_ = link.SendOrder(ctx, vda.Order{Robot: r1, OrderID: "leg-1"})
	_ = link.SendInstantActions(ctx, vda.InstantActions{Robot: r1, Actions: []vda.Action{{Type: "cancelOrder", ID: "leg-1-cancel", Blocking: "HARD"}}})
	_ = link.SendOrder(ctx, vda.Order{Robot: r1, OrderID: "leg-2"})
	time.Sleep(300 * time.Millisecond)
	if got := in.all(); len(got) != 1 {
		t.Fatalf("commands reached the robot through a cut connection: %v", got)
	}
	proxy.heal()
	got := in.await(t, 4, 20*time.Second)
	want := []string{"order leg-0 0", "order leg-1 1", "instantActions  0", "order leg-2 2"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("the robot received %v, want %v", got, want)
	}
}

func awaitLinkDown(t *testing.T, link *FleetLink) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if !link.Connected() {
			return
		}
	}
	t.Fatal("the link still reports an open connection 5 s after the proxy cut it")
}
