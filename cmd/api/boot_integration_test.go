//go:build integration

package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/testing/kafkatest"
	"github.com/IQVO/amr-fleet-control/internal/testing/mqtttest"
	"github.com/IQVO/amr-fleet-control/internal/testing/pgtest"
)

func getStatus(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// The composition root on real Postgres, Kafka and an MQTT broker (mqtttest): wire builds everything, run connects
// the one fleet link, recovers, serves (/readyz 200 with all three dependencies up, the map's fleet
// on GET), and a cancel shuts down in order: run returns nil, the link is disconnected and the pool
// is closed (lesson, plan 04 Task 19 review: the loops stop before the link, the sink and the pool).
func TestTheComposedServiceBootsServesAndShutsDown(t *testing.T) {
	vars := map[string]string{"DATABASE_URL": pgtest.URL(t), "KAFKA_BROKERS": strings.Join(kafkatest.Brokers(t), ","),
		"MQTT_URL": mqtttest.Broker(t), "AMR_MAP_FILE": "../../config/map.example.yaml", "MQTT_CLIENT_ID": "amr-fleet-control-boot"}
	cfg, err := loadConfig(env(vars))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := wire(ctx, cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ln := listen(t)
	done := runAsync(a, ctx, ln)
	base := "http://" + ln.Addr().String()
	code, body := 0, ""
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if code, body = getStatus(t, base+"/readyz"); code == http.StatusOK {
			break
		}
	}
	if code != http.StatusOK {
		t.Fatalf("/readyz = %d %s, want 200 with Postgres, Kafka and MQTT up", code, body)
	}
	if code, body = getStatus(t, base+"/fleets/AMR1"); code != http.StatusOK || !strings.Contains(body, `"AMR1"`) {
		t.Fatalf("GET /fleets/AMR1 = %d %s", code, body)
	}
	cancel()
	if err := waitRun(t, done); err != nil {
		t.Fatalf("run = %v, want a clean shutdown", err)
	}
	if a.down.link.Connected() {
		t.Fatal("the fleet link is still connected after run returned")
	}
	if err := a.pool.Ping(context.Background()); err == nil {
		t.Fatal("the pool still answers after run returned")
	}
	a.close() // main's defer: a no-op now
}
