//go:build integration

package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/testing/kafkatest"
	"github.com/IQVO/amr-fleet-control/internal/testing/mqtttest"
	"github.com/IQVO/amr-fleet-control/internal/testing/pgtest"
)

// carry (Task 16 review): run's boot steps are exactly connect then Recover (decisions D14, D19:
// Recover before the worker and before HTTP serves), and Recover is not a loop; kills "Recover
// before connect" and "Recover as a loop". The closers of the real wiring run link, sink, pool in
// that order (the pool outlives the link's last unit); kills "closers reversed".
func TestBootStepsAreConnectThenRecover(t *testing.T) {
	vars := map[string]string{"DATABASE_URL": pgtest.URL(t), "KAFKA_BROKERS": strings.Join(kafkatest.Brokers(t), ","),
		"MQTT_URL": mqtttest.Broker(t), "AMR_MAP_FILE": "../../config/map.example.yaml", "MQTT_CLIENT_ID": "amr-fleet-control-order"}
	cfg, err := loadConfig(env(vars))
	if err != nil {
		t.Fatal(err)
	}
	a, err := wire(context.Background(), cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.close)
	if len(a.starts) != 2 || len(a.loops) != 3 || len(a.closers) != 3 {
		t.Fatalf("starts=%d loops=%d closers=%d, want 2 boot steps (connect, Recover), 3 loops and 3 closers",
			len(a.starts), len(a.loops), len(a.closers))
	}
	if err := a.starts[0](context.Background()); err != nil || a.down.link == nil {
		t.Fatalf("the first boot step did not connect the fleet link: %v", err)
	}
	if err := a.starts[1](context.Background()); err != nil {
		t.Fatalf("the second boot step (Recover) = %v", err)
	}
	a.closeOnce.Do(func() {}) // the closers run by hand below; the cleanup's close is then a no-op
	a.closers[0]()
	if a.down.link.Connected() {
		t.Fatal("the first closer did not disconnect the fleet link")
	}
	if err := a.pool.Ping(context.Background()); err != nil {
		t.Fatalf("the pool closed before the link (first closer): %v", err)
	}
	a.closers[1]()
	if err := a.pool.Ping(context.Background()); err != nil {
		t.Fatalf("the pool closed with the relay sink (second closer): %v", err)
	}
	a.closers[2]()
	if err := a.pool.Ping(context.Background()); err == nil {
		t.Fatal("the last closer did not close the pool")
	}
}
