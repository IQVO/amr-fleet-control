//go:build integration

package mqtt

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/IQVO/amr-fleet-control/internal/testing/mqtttest"
)

// carry (Task 11 review): states published WHILE NewFleetLink subscribes never overtake the
// robot's retained connection and factsheet.
func TestTheRetainedReplayPrecedesLiveStateDuringSubscription(t *testing.T) {
	broker := mqtttest.Broker(t)
	robotSide, err := connect(broker, "emu-order-it", func(pahomqtt.Client) {})
	if err != nil {
		t.Fatal(err)
	}
	defer robotSide.Disconnect(0)
	robotSide.Publish("vda5050/v3/EMU/R1/connection", 1, true, []byte(`{`+hdr+`,"connectionState":"ONLINE"}`)).Wait()
	robotSide.Publish("vda5050/v3/EMU/R1/factsheet", 1, true, []byte(`{`+hdr+`}`)).Wait()
	stop, sent := make(chan struct{}), make(chan int)
	go func() {
		n := 0
		defer func() { sent <- n }()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = publish(robotSide, "vda5050/v3/EMU/R1/state", []byte(`{`+hdr+`,"lastNodeId":"N`+strconv.Itoa(n)+`"}`))
			n++
		}
	}()
	up := &recUplink{}
	link, err := NewFleetLink(broker, "amr-fleet-control-order-it", up, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	link.Start(ctx)
	time.Sleep(300 * time.Millisecond)
	close(stop)
	n := <-sent
	deadline := time.Now().Add(10 * time.Second)
	for countPrefix(up.all(), "state ") == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	got := up.all()
	if len(got) < 3 || got[0] != "connection EMU/R1 ONLINE" || got[1] != "factsheet EMU/R1" {
		t.Fatalf("published %d states; handled %d, first three %v: the retained replay must come first", n, len(got), got[:min(3, len(got))])
	}
}
