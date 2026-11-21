package mqtt

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/IQVO/amr-fleet-control/internal/adapters/vda5050"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
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

// FleetLink is the ONE MQTT client of the fleet: wildcard subscriptions, one inbox, one worker,
// and one sender that publishes robot commands in the order they were enqueued.
type FleetLink struct {
	up      Uplink
	inbox   *inbox
	headers *vda5050.Headers
	now     func() time.Time
	log     *slog.Logger
	pub     func(topic string, payload []byte) error
	client  pahomqtt.Client
	start   sync.Once
	// carry (Task 11 review): the first subscription round reports to NewFleetLink.
	firstRound chan error
	firstOnce  sync.Once
	retryEvery time.Duration
	connects   atomic.Int64
	out        chan command // ruling P3-R10: the one FIFO of robot commands
	// carry (Task 11 review): the sender publishes only while open() holds, polling every openPoll.
	open     func() bool
	openPoll time.Duration
}

// NewFleetLink connects and subscribes on every (re)connection; messages that arrive before
// Start wait in the inbox. It returns once the first subscription round is done: paho runs
// onConnect on its own goroutine, so without the wait a robot message published right after
// NewFleetLink returned could reach the broker before the subscription and be lost.
func NewFleetLink(brokerURL, clientID string, up Uplink, log *slog.Logger) (*FleetLink, error) {
	l := newFleetLink(up, log)
	c, err := connect(brokerURL, clientID, l.onConnect, l.session)
	if err != nil {
		return nil, err
	}
	if err := l.awaitSubscribed(c, subscribeWait); err != nil {
		return nil, fmt.Errorf("mqtt subscribe %s as %s: %w", brokerURL, clientID, err)
	}
	l.client, l.open = c, c.IsConnectionOpen
	l.pub = func(topic string, b []byte) error { return publish(c, topic, b) }
	return l, nil
}

// onConnect subscribes on every (re)connection (paho runs it on its own goroutine).
func (l *FleetLink) onConnect(c pahomqtt.Client) {
	l.connected(func(filter string) error { return subscribe(c, filter, l.handler()) }, c.IsConnectionOpen)
}

// session tunes the link's client. carry (Task 11 review): a lost connection is logged at WARN
// with its error (connected logs every (re)connection at INFO with a counter), so a broker
// outage shows in the logs before robots go stale. CleanSession stays true: every connection
// subscribes again and the retained connection and factsheet are replayed.
func (l *FleetLink) session(o *pahomqtt.ClientOptions) {
	o.SetCleanSession(true).SetConnectionLostHandler(func(_ pahomqtt.Client, err error) {
		l.log.Warn("mqtt connection lost; reconnecting every 2 s", "error", err)
	})
}

// openPoll is how often a waiting sender checks whether the connection is open again.
const openPoll = 50 * time.Millisecond

func newFleetLink(up Uplink, log *slog.Logger) *FleetLink {
	return &FleetLink{up: up, inbox: newInbox(InboxSize, "fleet", log), headers: vda5050.NewHeaders(), now: time.Now, log: log,
		firstRound: make(chan error, 1), retryEvery: subscribeRetry, out: make(chan command, DownlinkSize),
		open: func() bool { return true }, openPoll: openPoll}
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
		go l.sendAll(ctx) // ruling P3-R10
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
	id, name, ok := vda5050.ParseTopic(topic)
	if !ok {
		l.log.Warn("message on an unexpected topic ignored", "topic", topic)
		return
	}
	// carry (Task 11 review): a zero-length payload is how a retained message is cleared.
	if len(payload) == 0 {
		l.log.Debug("empty payload ignored (a cleared retained message)", "topic", topic)
		return
	}
	if err := l.apply(ctx, id, name, payload); err != nil {
		l.log.Error("robot message not applied", "topic", topic, "error", err)
	}
}

// apply decodes one robot message and hands it to the application; other topics are ignored.
func (l *FleetLink) apply(ctx context.Context, id robot.ID, name string, payload []byte) error {
	switch name {
	case "state":
		s, err := vda5050.DecodeState(payload)
		if err == nil {
			err = sameRobot(id, s.Robot)
		}
		if err != nil {
			return err
		}
		return l.up.HandleState(ctx, s)
	case "connection":
		c, err := vda5050.DecodeConnection(payload)
		if err == nil {
			err = sameRobot(id, c.Robot)
		}
		if err != nil {
			return err
		}
		return l.up.HandleConnection(ctx, c)
	case "factsheet":
		f, err := vda5050.DecodeFactsheet(payload)
		if err == nil {
			err = sameRobot(id, f.Robot)
		}
		if err != nil {
			return err
		}
		return l.up.HandleFactsheet(ctx, f)
	}
	return nil
}

// sameRobot refuses a message whose header names another robot than its topic. carry (Task 11
// review): applying it would move the header's robot on another robot's report.
func sameRobot(topic, header robot.ID) error {
	if topic != header {
		return fmt.Errorf("%w: topic robot %s, header robot %s", vda5050.ErrInvalidMessage, topic.Key(), header.Key())
	}
	return nil
}

// Connected reports the broker connection (readiness).
func (l *FleetLink) Connected() bool { return l.client != nil && l.client.IsConnectionOpen() }

// Close disconnects.
func (l *FleetLink) Close() {
	if l.client != nil {
		l.client.Disconnect(250)
	}
}
