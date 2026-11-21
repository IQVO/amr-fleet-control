package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

// carry (Task 18 review; plan 04 Task 21 lessons): cmd/a2a's boot contract, ported from cmd/mcp/boot_test.go
// (JSON boot error, LOG_LEVEL validated, Listen before the reads, drain before the pool closes).

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// deadDB is a Postgres DSN nothing listens on: the boot migrations fail at once.
const deadDB = "postgres://amr@127.0.0.1:1/amr?sslmode=disable&connect_timeout=1"

const exampleMap = "../../config/map.example.yaml"

func bootEnv(addr string) func(string) string {
	return env(map[string]string{"A2A_ADDR": addr, "DATABASE_URL": deadDB, "AMR_MAP_FILE": exampleMap, "A2A_AGENT_URL": "http://a2a.test/a2a"})
}

// A boot error is ONE JSON line naming every bad variable, and the exit code is 1.
func TestABootErrorIsOneJSONLineNamingEveryBadVariable(t *testing.T) {
	var buf bytes.Buffer
	if code := boot(&buf, env(map[string]string{"LOG_LEVEL": "verbose"})); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var rec map[string]any
	if len(lines) != 1 || json.Unmarshal([]byte(lines[0]), &rec) != nil {
		t.Fatalf("boot output = %q, want one JSON line", buf.String())
	}
	msg, _ := rec["error"].(string)
	for _, v := range []string{"DATABASE_URL", "AMR_MAP_FILE", "A2A_AGENT_URL", "LOG_LEVEL"} {
		if rec["level"] != "ERROR" || !strings.Contains(msg, v) {
			t.Errorf("boot error record %v does not name %s", rec, v)
		}
	}
}

// LOG_LEVEL: unset is info; set, it is trimmed and case-insensitive; set-but-unknown is a boot error.
func TestLogLevelIsValidatedAtBoot(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://db", "AMR_MAP_FILE": "/m.yaml", "A2A_AGENT_URL": "http://a/a2a"}
	for level, ok := range map[string]bool{"": true, "debug": true, " DEBUG ": true, "Warn": true, "error": true,
		"verbose": false, " ": false, "0": false} {
		vars := map[string]string{"LOG_LEVEL": level}
		for k, v := range base {
			vars[k] = v
		}
		_, err := loadBootConfig(env(vars))
		if ok != (err == nil) || (!ok && !strings.Contains(err.Error(), "LOG_LEVEL")) {
			t.Errorf("LOG_LEVEL=%q: err = %v, want accepted=%v", level, err, ok)
		}
	}
	if l := newLogger(io.Discard, " DEBUG "); !l.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("an accepted upper-case level is not honoured")
	}
}

func TestTheAgentURLIsReadFromA2AAgentURL(t *testing.T) {
	c, err := loadBootConfig(env(map[string]string{"DATABASE_URL": "postgres://db", "AMR_MAP_FILE": "/m.yaml", "A2A_AGENT_URL": "http://a/a2a"}))
	if err != nil || c.agentURL != "http://a/a2a" {
		t.Fatalf("agentURL = %q (%v)", c.agentURL, err)
	}
}

func TestBootConfigDefaultsAndOverrides(t *testing.T) {
	c, err := loadBootConfig(env(map[string]string{"DATABASE_URL": "postgres://db", "AMR_MAP_FILE": "/m.yaml", "A2A_AGENT_URL": "http://a/a2a"}))
	if err != nil || c.addr != ":8093" || c.migrationsURL != "postgres://db" || c.serviceName != "amr-fleet-control-a2a" ||
		c.otlpEndpoint != "" || c.serviceVersion != "" || c.logLevel != "info" {
		t.Fatalf("defaults = %+v (%v)", c, err)
	}
	c, err = loadBootConfig(env(map[string]string{"DATABASE_URL": "postgres://db", "AMR_MAP_FILE": "/m.yaml", "A2A_ADDR": ":9", "A2A_AGENT_URL": "http://a/a2a",
		"MIGRATIONS_DATABASE_URL": "postgres://direct", "OTEL_SERVICE_NAME": "x", "SERVICE_VERSION": "v1",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317", "LOG_LEVEL": "debug"}))
	if err != nil || c.addr != ":9" || c.migrationsURL != "postgres://direct" || c.serviceName != "x" || c.serviceVersion != "v1" ||
		c.otlpEndpoint != "c:4317" || c.logLevel != "debug" {
		t.Fatalf("overrides = %+v (%v)", c, err)
	}
}

// The listener is bound BEFORE buildReads, so a busy address is the boot error (at once) rather than
// surfacing only after the migrations and the pool were set up.
func TestABusyAddressIsTheBootErrorBeforeAnyRead(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	err = run(context.Background(), io.Discard, bootEnv(busy.Addr().String()))
	if !errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "migrations") {
		t.Fatalf("boot on a busy address = %v, want the bind error before any read is wired", err)
	}
}

// When buildReads fails after the bind, the listener is closed (the port is free again).
func TestAFailedBuildReadsClosesTheListener(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()
	if err := run(context.Background(), io.Discard, bootEnv(addr)); err == nil || !strings.Contains(err.Error(), "migrations") {
		t.Fatalf("boot against a dead database = %v, want the migrations error", err)
	}
	again, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("the listener on %s was left open after the failed boot: %v", addr, err)
	}
	_ = again.Close()
}

// A missing map file fails the boot before any migration runs.
func TestBuildReadsRefusesAMissingMapFileBeforeTheMigrations(t *testing.T) {
	_, err := buildReads(context.Background(), bootConfig{mapFile: "/does/not/exist.yaml", databaseURL: deadDB, migrationsURL: deadDB})
	if err == nil || strings.Contains(err.Error(), "migrations") {
		t.Fatalf("buildReads with a missing map = %v, want the map error first", err)
	}
}

// Shutdown order: when the run context ends, serve drains the in-flight request BEFORE it returns, so
// run's deferred pool close (and then the telemetry flush) can never cut a read in half.
func TestServeDrainsTheInFlightRequestBeforeReturning(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "done")
	}), ReadHeaderTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, srv, ln) }()
	answered := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			answered <- "error: " + err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		answered <- string(b)
	}()
	<-started
	cancel()
	select {
	case err := <-served:
		t.Fatalf("serve returned (%v) while a request was in flight", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	if got := <-answered; got != "done" {
		t.Fatalf("the in-flight request = %q, want it drained to completion", got)
	}
	if err := <-served; err != nil {
		t.Fatalf("serve after the drain = %v", err)
	}
}

func TestServeReturnsAListenerFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	if err := serve(context.Background(), &http.Server{ReadHeaderTimeout: time.Second}, ln); err == nil {
		t.Fatal("serve on a closed listener returned nil")
	}
}

func TestStartTelemetryWithoutAnEndpointIsOff(t *testing.T) {
	var buf bytes.Buffer
	flush, err := startTelemetry(newLogger(&buf, "info"), "amr-fleet-control-a2a", "", "")
	if err != nil {
		t.Fatal(err)
	}
	flush()
	if !strings.Contains(buf.String(), "telemetry disabled") {
		t.Fatalf("boot log = %s", buf.String())
	}
}
