package mqtt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/IQVO/amr-fleet-control/internal/adapters/vda5050"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
)

// TickEvery is the fleet tick period; ticks run on the link's worker, in line with the robot
// messages (decision D18).
const TickEvery = time.Second

// Uplink is the application side of the link.
type Uplink interface {
	HandleState(ctx context.Context, s vda.State) error
	HandleConnection(ctx context.Context, c vda.Connection) error
	HandleFactsheet(ctx context.Context, f vda.Factsheet) error
	Tick(ctx context.Context) error
}

// FleetLink is the ONE MQTT client of the fleet: wildcard subscriptions, one inbox, one worker.
type FleetLink struct {
	up      Uplink
	inbox   *inbox
	headers *vda5050.Headers
	now     func() time.Time
	log     *slog.Logger
	pub     func(topic string, payload []byte) error
	client  pahomqtt.Client
	start   sync.Once
}

var uplinkTopics = []string{"state", "connection", "factsheet"}

// subscribeWait bounds how long NewFleetLink waits for the first subscription round (three
// subscriptions of at most 10 s each).
const subscribeWait = 35 * time.Second

// NewFleetLink connects and subscribes on every (re)connection; messages that arrive before
// Start wait in the inbox. It returns once the first subscription round is done: paho runs
// onConnect on its own goroutine, so without the wait a robot message published right after
// NewFleetLink returned could reach the broker before the subscription and be lost.
func NewFleetLink(brokerURL, clientID string, up Uplink, log *slog.Logger) (*FleetLink, error) {
	l := newFleetLink(up, log)
	subscribed := make(chan struct{})
	var first sync.Once
	c, err := connect(brokerURL, clientID, func(c pahomqtt.Client) {
		for _, name := range uplinkTopics {
			if err := subscribe(c, vda5050.Topic("+", "+", name), l.handler()); err != nil {
				log.Warn("mqtt subscribe failed; retried on the next reconnect", "topic", name, "error", err)
			}
		}
		first.Do(func() { close(subscribed) })
	})
	if err != nil {
		return nil, err
	}
	select {
	case <-subscribed:
	case <-time.After(subscribeWait):
		c.Disconnect(0)
		return nil, fmt.Errorf("mqtt subscribe %s as %s: no subscription round within %s", brokerURL, clientID, subscribeWait)
	}
	l.client = c
	l.pub = func(topic string, b []byte) error {
		if !c.IsConnectionOpen() {
			return errors.New("mqtt client disconnected")
		}
		return publish(c, topic, b)
	}
	return l, nil
}

func newFleetLink(up Uplink, log *slog.Logger) *FleetLink {
	return &FleetLink{up: up, inbox: newInbox(InboxSize, "fleet", log), headers: vda5050.NewHeaders(), now: time.Now, log: log}
}

// handler is paho's message handler: it only enqueues.
func (l *FleetLink) handler() pahomqtt.MessageHandler {
	return func(_ pahomqtt.Client, m pahomqtt.Message) {
		l.handle(m.Topic(), m.Payload())
	}
}

func (l *FleetLink) handle(topic string, payload []byte) {
	l.inbox.offer(func(ctx context.Context) { l.dispatch(ctx, topic, payload) })
}

// Start runs the worker and the tick until ctx ends.
func (l *FleetLink) Start(ctx context.Context) {
	l.start.Do(func() {
		go l.inbox.drain(ctx)
		go l.runTicks(ctx)
	})
}

func (l *FleetLink) runTicks(ctx context.Context) {
	t := time.NewTicker(TickEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.inbox.put(ctx, l.tick)
		}
	}
}

func (l *FleetLink) tick(ctx context.Context) {
	if err := l.up.Tick(ctx); err != nil {
		l.log.Error("fleet tick failed", "error", err)
	}
}

func (l *FleetLink) dispatch(ctx context.Context, topic string, payload []byte) {
	_, name, ok := vda5050.ParseTopic(topic)
	if !ok {
		l.log.Warn("message on an unexpected topic ignored", "topic", topic)
		return
	}
	var err error
	switch name {
	case "state":
		var s vda.State
		if s, err = vda5050.DecodeState(payload); err == nil {
			err = l.up.HandleState(ctx, s)
		}
	case "connection":
		var c vda.Connection
		if c, err = vda5050.DecodeConnection(payload); err == nil {
			err = l.up.HandleConnection(ctx, c)
		}
	case "factsheet":
		var f vda.Factsheet
		if f, err = vda5050.DecodeFactsheet(payload); err == nil {
			err = l.up.HandleFactsheet(ctx, f)
		}
	default:
		return
	}
	if err != nil {
		l.log.Error("robot message not applied", "topic", topic, "error", err)
	}
}

// SendOrder publishes an order (ports.Downlink).
func (l *FleetLink) SendOrder(_ context.Context, o vda.Order) error {
	b, err := vda5050.EncodeOrder(o, l.headers.Next("order", o.Robot, l.now()))
	if err != nil {
		return err
	}
	return l.pub(vda5050.Topic(o.Robot.Manufacturer, o.Robot.SerialNumber, "order"), b)
}

// SendInstantActions publishes instant actions (ports.Downlink).
func (l *FleetLink) SendInstantActions(_ context.Context, a vda.InstantActions) error {
	b, err := vda5050.EncodeInstantActions(a, l.headers.Next("instantActions", a.Robot, l.now()))
	if err != nil {
		return err
	}
	return l.pub(vda5050.Topic(a.Robot.Manufacturer, a.Robot.SerialNumber, "instantActions"), b)
}

// Connected reports the broker connection (readiness).
func (l *FleetLink) Connected() bool { return l.client != nil && l.client.IsConnectionOpen() }

// Close disconnects.
func (l *FleetLink) Close() {
	if l.client != nil {
		l.client.Disconnect(250)
	}
}
