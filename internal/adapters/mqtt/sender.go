package mqtt

import (
	"context"
	"fmt"
	"time"

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
// unacknowledged order or cancel is resent by the tick.
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
			return
		case c := <-l.out:
			l.send(c)
		}
	}
}

// send publishes one command; a failure or a PUBACK timeout is logged, never retried here.
func (l *FleetLink) send(c command) {
	b, err := c.encode(l.headers, l.now())
	if err == nil {
		err = l.pub(c.topic, b)
	}
	if err != nil {
		l.log.Warn("robot command not sent; an unacknowledged order is resent by the tick", "topic", c.topic, "error", err)
	}
}
