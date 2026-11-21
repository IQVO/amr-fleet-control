package mqtt

import (
	"context"
	"fmt"
	"strings"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/IQVO/amr-fleet-control/internal/adapters/vda5050"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
)

// DownlinkSize is how many robot commands the link buffers for its sender.
const DownlinkSize = 1024

// command is one robot command waiting for the sender. ruling P3-R10: encode takes the header
// in the sender, right before the publish, so headerId order is wire order.
type command struct {
	topic  string
	encode func(h *vda5050.Headers, at time.Time) ([]byte, error)
}

// SendOrder enqueues an order (ports.Downlink). ruling P3-R10: it never waits for the broker;
// the use case enqueues in commit order and the one sender keeps that order on the wire.
func (l *FleetLink) SendOrder(_ context.Context, o vda.Order) error {
	return l.enqueue(command{topic: vda5050.Topic(o.Robot.Manufacturer, o.Robot.SerialNumber, "order"),
		encode: func(h *vda5050.Headers, at time.Time) ([]byte, error) {
			return vda5050.EncodeOrder(o, h.Next("order", o.Robot, at))
		}})
}

// SendInstantActions enqueues instant actions (ports.Downlink), like SendOrder.
func (l *FleetLink) SendInstantActions(_ context.Context, a vda.InstantActions) error {
	return l.enqueue(command{topic: vda5050.Topic(a.Robot.Manufacturer, a.Robot.SerialNumber, "instantActions"),
		encode: func(h *vda5050.Headers, at time.Time) ([]byte, error) {
			return vda5050.EncodeInstantActions(a, h.Next("instantActions", a.Robot, at))
		}})
}

// enqueue never blocks: a full downlink refuses the command, which the use case logs; an
// unacknowledged order, cancel or stopCharging is resent by the tick.
func (l *FleetLink) enqueue(c command) error {
	select {
	case l.out <- c:
		return nil
	default:
		return fmt.Errorf("mqtt downlink full (%d commands): %s not sent", cap(l.out), c.topic)
	}
}

// sendAll publishes the queued commands one at a time until ctx ends; each publish waits for
// its PUBACK (bounded) before the next one leaves.
func (l *FleetLink) sendAll(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			l.stopped(0)
			return
		case c := <-l.out:
			if !l.send(ctx, c) {
				l.stopped(1)
				return
			}
		}
	}
}

// stopped logs how many commands the stopped sender leaves unsent (held: one it had taken).
// carry (Task 11 review): the tick resends orders, cancels and stopCharging after a restart.
func (l *FleetLink) stopped(held int) {
	if n := held + len(l.out); n > 0 {
		l.log.Warn("mqtt sender stopped; robot commands dropped", "dropped", n)
		return
	}
	l.log.Info("mqtt sender stopped", "dropped", 0)
}

// send publishes one command once the connection is open; a failure or a PUBACK timeout is
// logged, never retried here. false: ctx ended while the connection was closed.
func (l *FleetLink) send(ctx context.Context, c command) bool {
	if !l.awaitOpen(ctx) {
		return false
	}
	b, err := c.encode(l.headers, l.now())
	if err == nil {
		err = l.pub(c.topic, b)
	}
	if err != nil {
		l.log.Warn("robot command not sent; an unacknowledged order is resent by the tick (so are a cancelOrder and a stopCharging)", "topic", c.topic, "error", err)
	}
	return true
}

// awaitOpen waits (ctx-bounded) until the connection is open. carry (Task 11 review): ruling
// P3-R10 across a reconnect: paho stores a publish made while the connection is down and resumes
// it after the reconnect, possibly behind commands sent later, so the sender never hands it a
// packet while the connection is closed; the commands wait in this FIFO instead, in order.
func (l *FleetLink) awaitOpen(ctx context.Context) bool {
	if l.open() {
		return true
	}
	l.log.Warn("mqtt connection closed or a resumed publish still unacknowledged; robot commands wait in the downlink until it is open and drained", "queued", len(l.out)+1)
	t := time.NewTicker(l.openPoll)
	defer t.Stop()
	for !l.open() {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
	return true
}

// openAndDrained: the connection is open AND paho's store holds no outbound packet. carry (Task
// 11 review): paho marks the connection open before it resumes the publishes stored at a loss,
// so a fresh command could overtake a resumed one. paho v1.5.1 deletes the "o.<mid>" key on the
// PUBACK (persistInbound, net.go:197) before it completes the token (net.go:227), so after a
// normal publish the store is already empty; after a PUBACK timeout the sender holds the FIFO
// until the resumed packet is acknowledged. A stored SUBSCRIBE waits only for its SUBACK.
func openAndDrained(connected func() bool, store pahomqtt.Store) func() bool {
	return func() bool { return connected() && !pendingOutbound(store) }
}

// pendingOutbound: the store holds a packet paho sent (or will resume) and the broker has not
// acknowledged yet (paho keys them "o.<mid>").
func pendingOutbound(store pahomqtt.Store) bool {
	for _, k := range store.All() {
		if strings.HasPrefix(k, "o.") {
			return true
		}
	}
	return false
}
