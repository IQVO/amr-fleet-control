package mqtt

import (
	"errors"
	"fmt"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/IQVO/amr-fleet-control/internal/adapters/vda5050"
)

// uplinkTopics are the robot topics the link subscribes, in this order.
var uplinkTopics = []string{"state", "connection", "factsheet"}

// subscribeWait bounds how long NewFleetLink waits for the first subscription round (three
// subscriptions of at most 10 s each).
const subscribeWait = 35 * time.Second

// subscribeRetry is how often a later round retries a failed filter while connected.
const subscribeRetry = 2 * time.Second

// subAckRefused is the SUBACK return code of a refused filter (MQTT 3.1.1).
const subAckRefused = 0x80

// awaitSubscribe waits for one SUBACK. carry (Task 11 review): paho v1.5.1 completes a refused
// subscription without an error and only records 0x80 in SubscribeToken.Result, so the return
// code is checked here.
func awaitSubscribe(filter string, tok pahomqtt.Token, timeout time.Duration) error {
	if !tok.WaitTimeout(timeout) {
		return fmt.Errorf("mqtt subscribe %s: timeout", filter)
	}
	if err := tok.Error(); err != nil {
		return fmt.Errorf("mqtt subscribe %s: %w", filter, err)
	}
	if st, ok := tok.(interface{ Result() map[string]byte }); ok && st.Result()[filter] == subAckRefused {
		return fmt.Errorf("mqtt subscribe %s: refused by the broker (SUBACK 0x80)", filter)
	}
	return nil
}

// subscribeAll subscribes every uplink topic in order; it returns the filters that failed and
// their errors joined.
func subscribeAll(sub func(filter string) error) ([]string, error) {
	var failed []string
	var errs []error
	for _, name := range uplinkTopics {
		f := vda5050.Topic("+", "+", name)
		if err := sub(f); err != nil {
			failed, errs = append(failed, f), append(errs, err)
		}
	}
	return failed, errors.Join(errs...)
}

// connected runs after every (re)connection, on paho's own goroutine. carry (Task 11 review):
// the first round hands its errors to NewFleetLink, which then fails; a later round retries
// each failed filter every retryEvery while the connection stays open.
func (l *FleetLink) connected(sub func(filter string) error, open func() bool) {
	failed, err := subscribeAll(sub)
	first := false
	l.firstOnce.Do(func() {
		first = true
		l.firstRound <- err
	})
	if first || err == nil {
		return
	}
	l.log.Warn("mqtt subscribe failed; retrying while connected", "every", l.retryEvery, "error", err)
	l.retry(failed, sub, open)
}

// retry re-subscribes the failed filters until they all succeed or the connection closes.
func (l *FleetLink) retry(failed []string, sub func(filter string) error, open func() bool) {
	for len(failed) > 0 && open() {
		time.Sleep(l.retryEvery)
		if !open() {
			return
		}
		var still []string
		for _, f := range failed {
			if err := sub(f); err != nil {
				still = append(still, f)
				l.log.Warn("mqtt subscribe retry failed", "filter", f, "error", err)
				continue
			}
			l.log.Info("mqtt subscribed after a retry", "filter", f)
		}
		failed = still
	}
}

// awaitFirstRound returns the first round's result, or an error once wait has passed.
func awaitFirstRound(round <-chan error, wait time.Duration) error {
	select {
	case err := <-round:
		return err
	case <-time.After(wait):
		return fmt.Errorf("no subscription round within %s", wait)
	}
}

// awaitSubscribed waits for the first subscription round. carry (Task 11 review): a refused or
// failed first subscription fails NewFleetLink like the timeout does, and disconnects, so a link
// that cannot hear its robots never reports healthy.
func (l *FleetLink) awaitSubscribed(c interface{ Disconnect(quiesce uint) }, wait time.Duration) error {
	if err := awaitFirstRound(l.firstRound, wait); err != nil {
		c.Disconnect(0)
		return err
	}
	return nil
}
