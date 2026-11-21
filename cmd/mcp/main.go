// Command mcp is amr-fleet-control's read-only MCP server (Streamable HTTP, :8090). It reads
// Postgres through the same queries as the REST API and never dials Kafka or MQTT.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
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

func main() {
	if err := run(); err != nil {
		slog.Error("mcp server exited with error", "error", err)
		os.Exit(1)
	}
}

// bootConfig is the environment of cmd/mcp.
type bootConfig struct{ databaseURL, mapFile, addr string }

func loadBootConfig(getenv func(string) string) (bootConfig, error) {
	c := bootConfig{databaseURL: getenv("DATABASE_URL"), mapFile: getenv("AMR_MAP_FILE"), addr: getenv("MCP_ADDR")}
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
	return c, errors.Join(problems...)
}

func run() error {
	logger := newLogger(os.Getenv("LOG_LEVEL"))
	slog.SetDefault(logger)
	cfg, err := loadBootConfig(os.Getenv)
	if err != nil {
		return err
	}
	serviceName, serviceVersion, endpoint := telemetryEnv(os.Getenv)
	flush, err := startTelemetry(logger, serviceName, serviceVersion, endpoint)
	if err != nil {
		return err
	}
	defer flush()
	m, err := mapfile.LoadMap(cfg.mapFile)
	if err != nil {
		return err
	}
	if err := postgres.RunMigrations(postgres.MigrationsURL(os.Getenv)); err != nil {
		return err
	}
	pool, err := postgres.NewPool(context.Background(), cfg.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	reads, err := usecases.NewReader(m, usecases.DefaultConfig(), postgres.NewRobots(pool), postgres.NewMissions(pool), system.Clock{})
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: cfg.addr, ReadHeaderTimeout: 5 * time.Second,
		Handler: newRouter(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{Reads: reads, FleetID: m.FleetID()})), logger, serviceName)}
	return serve(logger, srv)
}

func serve(logger *slog.Logger, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("mcp server listening (Streamable HTTP)", "addr", srv.Addr)
		errCh <- srv.ListenAndServe()
	}()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}
	drain, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(drain)
}

func newLogger(level string) *slog.Logger {
	lvl := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}
