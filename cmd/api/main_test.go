package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"testing"
)

// lesson (plan 04 Task 19 review, ruling P4-R13): the JSON logger exists before the configuration
// is read, so a boot error reaches the fleet log pipeline as ONE JSON line naming every missing or
// invalid variable, and the process exits 1.
func TestABootErrorIsOneJSONLineNamingEveryBadVariable(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	var out bytes.Buffer
	if code := boot(&out, env(map[string]string{"OUTBOX_INTERVAL_MS": "0", "REROUTE_AFTER_SECONDS": "soon"})); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("boot wrote %d lines, want one JSON line: %q", len(lines), out.String())
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("boot error is not JSON: %v: %q", err, lines[0])
	}
	msg, _ := rec["error"].(string)
	if rec["level"] != "ERROR" || msg == "" {
		t.Fatalf("record = %v, want level ERROR with an error field", rec)
	}
	for _, name := range []string{"DATABASE_URL", "KAFKA_BROKERS", "MQTT_URL", "AMR_MAP_FILE", "OUTBOX_INTERVAL_MS", "REROUTE_AFTER_SECONDS"} {
		if !strings.Contains(msg, name) {
			t.Errorf("boot error %q does not name %s", msg, name)
		}
	}
}

func full() map[string]string {
	return map[string]string{"DATABASE_URL": "postgres://127.0.0.1:1/x", "KAFKA_BROKERS": "127.0.0.1:1", "MQTT_URL": "tcp://127.0.0.1:1",
		"AMR_MAP_FILE": "/nonexistent/map.yaml"}
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// lesson (plan 04 Task 19 review, finding 7): the listener is taken before wire builds anything, so
// a busy ADDR fails the boot before migrations ran or a pool opened. The map file does not exist: a
// wire that ran first would fail on it instead.
func TestABusyAddressFailsTheBootBeforeAnythingIsWired(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	busy := listen(t)
	t.Cleanup(func() { _ = busy.Close() })
	m := full()
	m["ADDR"] = busy.Addr().String()
	var out bytes.Buffer
	if code := boot(&out, env(m)); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if got := out.String(); !strings.Contains(got, "listen") || strings.Contains(got, "/nonexistent/map.yaml") {
		t.Fatalf("boot log = %q, want the listen error and no map error (Listen must precede wire)", got)
	}
}

// lesson (plan 04 Task 19 review, finding 7): a wire that fails releases the listener it was handed.
func TestAFailedWireReleasesTheListener(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	ln := listen(t)
	addr := ln.Addr().String()
	_ = ln.Close()
	m := full()
	m["ADDR"] = addr
	var out bytes.Buffer
	if code := boot(&out, env(m)); code != 1 || !strings.Contains(out.String(), "/nonexistent/map.yaml") {
		t.Fatalf("exit code = %d, log %q; want 1 and the map error", code, out.String())
	}
	again, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the failed boot kept %s: %v", addr, err)
	}
	_ = again.Close()
}
