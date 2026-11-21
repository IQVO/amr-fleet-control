// Command api is amr-fleet-control: the subsystem-controller REST interface, the one MQTT fleet
// link (VDA 5050 v3) and the outbox relay.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/telemetry"
)

func main() {
	if err := runMain(); err != nil {
		slog.Error("amr-fleet-control exited with error", "error", err)
		os.Exit(1)
	}
}

func runMain() error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)
	flush, err := startTelemetry(log, cfg)
	if err != nil {
		return err
	}
	defer flush()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	a, err := wire(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer a.close()
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	return a.run(ctx, ln)
}

func newLogger(level string) *slog.Logger {
	lv := slog.LevelInfo
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	}
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv})))
}
