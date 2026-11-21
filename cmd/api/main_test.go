package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
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
