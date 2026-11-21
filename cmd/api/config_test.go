package main

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string {
		return m[k]
	}
}

func TestEveryMissingMandatoryVariableIsNamed(t *testing.T) {
	_, err := loadConfig(env(nil))
	if err == nil {
		t.Fatal("an empty environment booted")
	}
	for _, name := range []string{"DATABASE_URL", "KAFKA_BROKERS", "MQTT_URL", "AMR_MAP_FILE"} {
		if !strings.Contains(err.Error(), name+" is required") {
			t.Errorf("the error does not name %s: %v", name, err)
		}
	}
}

// (The brief's TestDefaultsAndOverrides is split into checkDefaults, checkTimingDefaults,
// checkOverrides and checkRefusals to stay under this repo's cyclop limit of 15; the defaults condition is split
// into two ifs with the same message. Every comparison is the brief's, unchanged and in order.)
func TestDefaultsAndOverrides(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": " 127.0.0.1:9092 , ,127.0.0.1:9093", "MQTT_URL": "tcp://127.0.0.1:1883", "AMR_MAP_FILE": "/etc/amr/map.yaml"}
	c, err := loadConfig(env(base))
	if err != nil {
		t.Fatal(err)
	}
	checkDefaults(t, c)
	checkOverrides(t, base)
	checkRefusals(t, base)
}

func checkDefaults(t *testing.T, c config) {
	t.Helper()
	p := c.Policy
	if len(c.Brokers) != 2 || c.Brokers[1] != "127.0.0.1:9093" || c.Addr != ":8080" || c.MQTTClientID != "amr-fleet-control" ||
		c.ServiceName != "amr-fleet-control" || c.OTLPEndpoint != "" || c.OutboxInterval != 250*time.Millisecond ||
		p.ChargeLow != 20 || p.ChargeResume != 80 || p.ReleaseAhead != 2 || p.NodeTolerance != 0.25 || p.StaleAfter != 90*time.Second {
		t.Fatalf("defaults = %+v", c)
	}
	checkTimingDefaults(t, c)
}

func checkTimingDefaults(t *testing.T, c config) {
	t.Helper()
	p := c.Policy
	if p.ResendAfter != 5*time.Second || p.ResendLimit != 3 || p.RerouteAfter != 10*time.Second || p.DeadlockTimeout != time.Minute ||
		p.BlockedTimeout != time.Minute || p.OfflineTimeout != time.Minute {
		t.Fatalf("defaults = %+v", c)
	}
}

func checkOverrides(t *testing.T, base map[string]string) {
	t.Helper()
	over := map[string]string{"CHARGE_LOW_PERCENT": "30", "CHARGE_RESUME_PERCENT": "95.5", "RELEASE_AHEAD_NODES": "4", "NODE_TOLERANCE_METRES": "0.5",
		"ROBOT_STALE_AFTER_SECONDS": "30", "ORDER_RESEND_LIMIT": "5", "OTEL_SERVICE_NAME": "afc", "OTEL_EXPORTER_OTLP_ENDPOINT": "otel:4317"}
	for k, v := range base {
		over[k] = v
	}
	c, err := loadConfig(env(over))
	if err != nil || c.Policy.ChargeLow != 30 || c.Policy.ChargeResume != 95.5 || c.Policy.ReleaseAhead != 4 || c.Policy.NodeTolerance != 0.5 ||
		c.Policy.StaleAfter != 30*time.Second || c.Policy.ResendLimit != 5 || c.ServiceName != "afc" || c.OTLPEndpoint != "otel:4317" {
		t.Fatalf("overrides = %+v %v", c, err)
	}
}

func checkRefusals(t *testing.T, base map[string]string) {
	t.Helper()
	bad := map[string]string{"CHARGE_LOW_PERCENT": "90"}
	for k, v := range base {
		bad[k] = v
	}
	if _, err := loadConfig(env(bad)); err == nil || !strings.Contains(err.Error(), "CHARGE_LOW_PERCENT") {
		t.Fatalf("a low threshold above the resume level booted: %v", err)
	}
	garbage := map[string]string{"RELEASE_AHEAD_NODES": "many", "ROBOT_STALE_AFTER_SECONDS": "-1"}
	for k, v := range base {
		garbage[k] = v
	}
	// lesson (plan 04 Task 19 review, finding 1): an invalid value is a boot error naming it; the
	// brief's "unparsable values must fall back to the defaults" is superseded (deviation D2).
	_, err := loadConfig(env(garbage))
	for _, name := range []string{"RELEASE_AHEAD_NODES", "ROBOT_STALE_AFTER_SECONDS"} {
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("unparsable or negative %s booted: %v", name, err)
		}
	}
}

// lesson (plan 04 Task 19 review, finding 1, ruling P4-R14): every interval, timeout, batch, count
// and the outbox attempt cap must be at least 1 and every threshold a finite number above 0. A 0
// would panic a ticker, disable a cap or end every leg at once, so it is a boot error naming the
// variable, never a silent default.
func TestAZeroOrInvalidSettingIsABootErrorNamingIt(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "127.0.0.1:9092", "MQTT_URL": "tcp://127.0.0.1:1883", "AMR_MAP_FILE": "/m.yaml"}
	bad := map[string][]string{}
	for _, name := range []string{"OUTBOX_INTERVAL_MS", "OUTBOX_BATCH", "OUTBOX_MAX_ATTEMPTS", "RELEASE_AHEAD_NODES",
		"ROBOT_STALE_AFTER_SECONDS", "ORDER_RESEND_AFTER_SECONDS", "ORDER_RESEND_LIMIT", "REROUTE_AFTER_SECONDS",
		"DEADLOCK_TIMEOUT_SECONDS", "ROBOT_BLOCKED_TIMEOUT_SECONDS", "ROBOT_OFFLINE_TIMEOUT_SECONDS"} {
		bad[name] = []string{"0", "-3", "1.5", "soon"}
	}
	for _, name := range []string{"CHARGE_LOW_PERCENT", "CHARGE_RESUME_PERCENT", "NODE_TOLERANCE_METRES"} {
		bad[name] = []string{"0", "-1", "NaN", "+Inf", "high"}
	}
	for name, values := range bad {
		for _, v := range values {
			m := map[string]string{name: v}
			for k, bv := range base {
				m[k] = bv
			}
			if _, err := loadConfig(env(m)); err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("%s=%q: err = %v, want a boot error naming it", name, v, err)
			}
		}
	}
	m := map[string]string{"OUTBOX_INTERVAL_MS": "1", "OUTBOX_BATCH": "1", "OUTBOX_MAX_ATTEMPTS": "1", "ORDER_RESEND_LIMIT": "1"}
	for k, bv := range base {
		m[k] = bv
	}
	if c, err := loadConfig(env(m)); err != nil || c.OutboxInterval != time.Millisecond || c.OutboxBatch != 1 || c.OutboxMaxAttempts != 1 ||
		c.Policy.ResendLimit != 1 {
		t.Fatalf("the smallest valid values = %+v %v", c, err)
	}
}
