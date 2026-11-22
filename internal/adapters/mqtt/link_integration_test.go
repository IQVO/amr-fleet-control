//go:build integration

package mqtt

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
	"github.com/IQVO/amr-fleet-control/internal/testing/mqtttest"
)

func TestTheFleetLinkSpeaksVDA5050ThroughABroker(t *testing.T) {
	broker := mqtttest.Broker(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	robotSide, err := connect(broker, "emu-it", func(pahomqtt.Client) {})
	if err != nil {
		t.Fatal(err)
	}
	defer robotSide.Disconnect(0)
	// a retained connection published BEFORE the link subscribes is replayed to it (decision D13)
	tok := robotSide.Publish("vda5050/v3/EMU/R1/connection", 1, true, []byte(`{`+hdr+`,"connectionState":"ONLINE"}`))
	tok.Wait()
	var mu sync.Mutex
	var orders [][]byte
	if err := subscribe(robotSide, "vda5050/v3/EMU/R1/order", func(_ pahomqtt.Client, m pahomqtt.Message) {
		mu.Lock()
		defer mu.Unlock()
		orders = append(orders, m.Payload())
	}); err != nil {
		t.Fatal(err)
	}
	up := &recUplink{}
	link, err := NewFleetLink(broker, "amr-fleet-control-it", up, log)
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	link.Start(ctx)
	for i := 0; i < 20; i++ {
		_ = publish(robotSide, "vda5050/v3/EMU/R1/state", []byte(`{`+hdr+`,"lastNodeId":"N`+string(rune('a'+i))+`"}`))
	}
	if err := link.SendOrder(ctx, vda.Order{Robot: robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}, OrderID: "leg-1"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(orders)
		mu.Unlock()
		if n == 1 && countPrefix(up.all(), "state ") == 20 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	got := up.all()
	if got[0] != "connection EMU/R1 ONLINE" {
		t.Fatalf("the retained connection must come first: %v", got)
	}
	k := 0
	for _, s := range got {
		if len(s) > 6 && s[:6] == "state " {
			if want := "state EMU/R1 N" + string(rune('a'+k)); s != want {
				t.Fatalf("state %d = %q, want %q (arrival order)", k, s, want)
			}
			k++
		}
	}
	if k != 20 || len(orders) != 1 || !link.Connected() {
		t.Fatalf("states %d orders %d connected %v", k, len(orders), link.Connected())
	}
}

func countPrefix(ss []string, p string) int {
	n := 0
	for _, s := range ss {
		if len(s) >= len(p) && s[:len(p)] == p {
			n++
		}
	}
	return n
}
