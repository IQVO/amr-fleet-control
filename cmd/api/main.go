// Command api is amr-fleet-control: the subsystem-controller REST interface, the one MQTT fleet
// link (VDA 5050 v3) and the outbox relay.
package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/telemetry"
)

func main() { os.Exit(boot(os.Stdout, os.Getenv)) }

// boot runs the process and returns its exit code. lesson (plan 04 Task 19 review, ruling P4-R13):
// the JSON logger (default level) exists before the configuration is read, so a boot error reaches
// the fleet log pipeline as one JSON line naming every missing or invalid variable, and exit 1.
func boot(w io.Writer, getenv func(string) string) int {
	slog.SetDefault(newLoggerTo(w, "info"))
	if err := runMain(w, getenv); err != nil {
		slog.Error("amr-fleet-control exited with error", "error", err)
		return 1
	}
	return 0
}

func runMain(w io.Writer, getenv func(string) string) error {
	cfg, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	log := newLoggerTo(w, cfg.LogLevel)
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

// newLoggerTo is the JSON logger on w at level, stamping trace_id/span_id on every record written
// under a span.
func newLoggerTo(w io.Writer, level string) *slog.Logger {
	lv := slog.LevelInfo
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	}
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lv})))
}
