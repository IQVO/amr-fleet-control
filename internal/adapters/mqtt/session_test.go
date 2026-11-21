package mqtt

import (
	"errors"
	"testing"
)

// carry (Task 11 review): a broker outage is visible in the logs: the lost connection at WARN
// with its error, every (re)connection at INFO with a counter; the session stays clean.
func TestTheLinkLogsConnectionLossAndCountsReconnects(t *testing.T) {
	sink := &logSink{}
	l := newFleetLink(&recUplink{}, sink.logger())
	o := clientOptions("tcp://127.0.0.1:1883", "amr-fleet-control", l.onConnect)
	o.SetCleanSession(false)
	l.session(o)
	if !o.CleanSession || o.OnConnectionLost == nil {
		t.Fatalf("CleanSession = %v, lost handler set = %v", o.CleanSession, o.OnConnectionLost != nil)
	}
	o.OnConnectionLost(nil, errors.New("read tcp: connection reset by peer"))
	lost := sink.records("mqtt connection lost")
	if len(lost) != 1 || lost[0]["level"] != "WARN" || lost[0]["error"] != "read tcp: connection reset by peer" {
		t.Fatalf("lost records = %v", lost)
	}
	ok := func(string) error { return nil }
	l.connected(ok, func() bool { return true })
	l.connected(ok, func() bool { return true })
	up := sink.records("mqtt connected")
	if len(up) != 2 || up[0]["level"] != "INFO" || up[0]["reconnects"] != 0.0 || up[1]["reconnects"] != 1.0 || up[1]["connects"] != 2.0 {
		t.Fatalf("connected records = %v", up)
	}
}
