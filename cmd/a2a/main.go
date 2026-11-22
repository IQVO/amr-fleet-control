// Command a2a is amr-fleet-control's read-only A2A agent (JSON-RPC, :8093). It reads
// Postgres through the same queries as the REST API and the MCP server and never dials Kafka or MQTT.
//
// READ-ONLY (ADR 0006): only usecases.NewReader over the Postgres robots and missions is wired - no
// unit of work, fleet lock, outbox, idempotency store, Kafka writer, MQTT link or relay - so this
// binary has no seam to write through (internal/architecture/agent_readonly_fitness_test.go). There
// is NO auth of any kind (fleet rule): access control is the in-cluster ClusterIP boundary.
//
// Environment:
//
//	A2A_ADDR                     listen address (default :8093)
//	A2A_AGENT_URL                REQUIRED. the absolute URL the Agent Card advertises (e.g. http://amr-fleet-control-a2a:8093/a2a)
//	DATABASE_URL                 REQUIRED. the Postgres DSN cmd/api owns
//	MIGRATIONS_DATABASE_URL      direct DSN for the boot migrations (default DATABASE_URL)
//	AMR_MAP_FILE                 REQUIRED. the fleet map YAML, the same file cmd/api reads
//	LOG_LEVEL                    debug | info | warn | error, any case (default info; another value is a boot error)
//	OTEL_SERVICE_NAME            service.name (default amr-fleet-control-a2a)
//	OTEL_EXPORTER_OTLP_ENDPOINT  Collector OTLP/gRPC endpoint; empty = telemetry off (ADR 0005)
//	SERVICE_VERSION              service.version (default dev)
//	ENVIRONMENT                  deployment.environment.name (read by the telemetry adapter)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	inbounda2a "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/a2a"
	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/system"
	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/telemetry"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	mapfile "github.com/IQVO/amr-fleet-control/internal/config"
)

const shutdownTimeout = 10 * time.Second

// carry (plan 04 Task 20/21 review lessons; SUPERSEDES the brief's main/run/serve/newLogger): the
// JSON logger exists before the configuration is read, so a boot error is one JSON line naming every
// bad variable with exit code 1; LOG_LEVEL is validated; the listener is bound before any read is
// wired; serve drains HTTP before the pool closes and the telemetry flushes.
func main() { os.Exit(boot(os.Stdout, os.Getenv)) }

// boot runs the process until SIGINT/SIGTERM and returns its exit code.
func boot(w io.Writer, getenv func(string) string) int {
	slog.SetDefault(newLogger(w, "info"))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, w, getenv); err != nil {
		slog.Error("a2a agent exited with error", "error", err)
		return 1
	}
	return 0
}

// bootConfig is the environment of cmd/a2a.
type bootConfig struct {
	databaseURL, mapFile, addr, agentURL                               string
	migrationsURL, logLevel, serviceName, serviceVersion, otlpEndpoint string
}

func loadBootConfig(getenv func(string) string) (bootConfig, error) {
	c := bootConfig{databaseURL: getenv("DATABASE_URL"), mapFile: getenv("AMR_MAP_FILE"), addr: getenv("A2A_ADDR"),
		agentURL:      getenv("A2A_AGENT_URL"),
		migrationsURL: postgres.MigrationsURL(getenv), logLevel: getenv("LOG_LEVEL")}
	c.serviceName, c.serviceVersion, c.otlpEndpoint = telemetryEnv(getenv)
	if c.addr == "" {
		c.addr = ":8093"
	}
	var problems []error
	if c.databaseURL == "" {
		problems = append(problems, errors.New("DATABASE_URL is required: the A2A skills read the real Postgres"))
	}
	if c.mapFile == "" {
		problems = append(problems, errors.New("AMR_MAP_FILE is required: the fleet view names the map's handoff points"))
	}
	if c.logLevel == "" {
		c.logLevel = "info"
	} else if _, err := telemetry.ParseLogLevel(c.logLevel); err != nil {
		problems = append(problems, err) // a set-but-unknown LOG_LEVEL is a boot error, never a silent info
	}
	if c.agentURL == "" {
		problems = append(problems, errors.New("A2A_AGENT_URL is required: the Agent Card advertises it"))
	}
	return c, errors.Join(problems...)
}

func run(ctx context.Context, w io.Writer, getenv func(string) string) error {
	cfg, err := loadBootConfig(getenv)
	if err != nil {
		return err
	}
	logger := newLogger(w, cfg.logLevel)
	slog.SetDefault(logger)
	flush, err := startTelemetry(logger, cfg.serviceName, cfg.serviceVersion, cfg.otlpEndpoint)
	if err != nil {
		return err
	}
	defer flush()
	// Bind first, so a busy address is the boot error before any migration or pool is set up; a
	// failed buildReads closes the listener again.
	ln, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		return err
	}
	rs, err := buildReads(ctx, cfg)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer rs.close() // after serve's drain, before the telemetry flush
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second,
		Handler: newAgent(inbounda2a.Deps{Reads: rs.reads}, cfg.agentURL, cardVersion(cfg.serviceVersion), logger, cfg.serviceName)}
	logger.Info("a2a agent listening (JSON-RPC)", "addr", ln.Addr().String())
	return serve(ctx, srv, ln)
}

// readSide is the read-only wiring of the A2A surface.
type readSide struct {
	reads inbounda2a.Reads
	close func()
}

// buildReads loads the map, runs the idempotent migrations (this binary may boot before cmd/api) and wires
// usecases.NewReader over the Postgres robots and missions. READ-ONLY wiring: nothing that writes is built.
func buildReads(ctx context.Context, cfg bootConfig) (readSide, error) {
	m, err := mapfile.LoadMap(cfg.mapFile)
	if err != nil {
		return readSide{}, err
	}
	if err := postgres.RunMigrations(cfg.migrationsURL); err != nil {
		return readSide{}, fmt.Errorf("migrations: %w", err)
	}
	pool, err := postgres.NewReadOnlyPool(ctx, cfg.databaseURL) // carry (P4-R17 parity): every transaction READ ONLY
	if err != nil {
		return readSide{}, fmt.Errorf("postgres: %w", err)
	}
	reads, err := usecases.NewReader(m, usecases.DefaultConfig(), postgres.NewRobots(pool), postgres.NewMissions(pool), system.Clock{})
	if err != nil {
		pool.Close()
		return readSide{}, err
	}
	return readSide{reads: reads, close: pool.Close}, nil
}

// serve runs srv on ln until ctx ends, then drains it.
func serve(ctx context.Context, srv *http.Server, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	drain, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(drain)
}

// newLogger is the JSON logger on w at level, stamping trace_id/span_id under a span.
func newLogger(w io.Writer, level string) *slog.Logger {
	lvl, _ := telemetry.ParseLogLevel(level) // validated by loadBootConfig; the boot logger passes "info"
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl})))
}

// cardVersion is the Agent Card's version: SERVICE_VERSION, or "dev" when it is empty (telemetry.Setup applies the
// same fallback to service.version). carry (Task 19 review): extracted so the fallback is pinned by a unit test.
func cardVersion(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}
