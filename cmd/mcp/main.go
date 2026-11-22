// Command mcp is amr-fleet-control's read-only MCP server (Streamable HTTP, :8090). It reads
// Postgres through the same queries as the REST API and never dials Kafka or MQTT.
//
// READ-ONLY (ADR 0006): only usecases.NewReader over the Postgres robots and missions is wired - no
// unit of work, fleet lock, outbox, idempotency store, Kafka writer, MQTT link or relay - so this
// binary has no seam to write through (internal/architecture/agent_readonly_fitness_test.go). There
// is NO auth of any kind (fleet rule): access control is the in-cluster ClusterIP boundary.
//
// Environment:
//
//	MCP_ADDR                     listen address (default :8090)
//	DATABASE_URL                 REQUIRED. the Postgres DSN cmd/api owns
//	MIGRATIONS_DATABASE_URL      direct DSN for the boot migrations (default DATABASE_URL)
//	AMR_MAP_FILE                 REQUIRED. the fleet map YAML, the same file cmd/api reads
//	LOG_LEVEL                    debug | info | warn | error, any case (default info; another value is a boot error)
//	OTEL_SERVICE_NAME            service.name (default amr-fleet-control-mcp)
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

	inboundmcp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/mcp"
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
		slog.Error("mcp server exited with error", "error", err)
		return 1
	}
	return 0
}

// bootConfig is the environment of cmd/mcp.
type bootConfig struct {
	databaseURL, mapFile, addr                                         string
	migrationsURL, logLevel, serviceName, serviceVersion, otlpEndpoint string
}

func loadBootConfig(getenv func(string) string) (bootConfig, error) {
	c := bootConfig{databaseURL: getenv("DATABASE_URL"), mapFile: getenv("AMR_MAP_FILE"), addr: getenv("MCP_ADDR"),
		migrationsURL: postgres.MigrationsURL(getenv), logLevel: getenv("LOG_LEVEL")}
	c.serviceName, c.serviceVersion, c.otlpEndpoint = telemetryEnv(getenv)
	if c.addr == "" {
		c.addr = ":8090"
	}
	var problems []error
	if c.databaseURL == "" {
		problems = append(problems, errors.New("DATABASE_URL is required: the MCP tools read the real Postgres"))
	}
	if c.mapFile == "" {
		problems = append(problems, errors.New("AMR_MAP_FILE is required: the fleet view names the map's handoff points"))
	}
	if c.logLevel == "" {
		c.logLevel = "info"
	} else if _, err := telemetry.ParseLogLevel(c.logLevel); err != nil {
		problems = append(problems, err) // a set-but-unknown LOG_LEVEL is a boot error, never a silent info
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
		Handler: newRouter(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{Reads: rs.reads, FleetID: rs.fleetID})), logger, cfg.serviceName)}
	logger.Info("mcp server listening (Streamable HTTP)", "addr", ln.Addr().String())
	return serve(ctx, srv, ln)
}

// readSide is the read-only wiring of the MCP surface.
type readSide struct {
	reads   inboundmcp.Reads
	fleetID string
	close   func()
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
	return readSide{reads: reads, fleetID: m.FleetID(), close: pool.Close}, nil
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
