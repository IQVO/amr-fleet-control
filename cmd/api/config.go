package main

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
)

const defaultServiceName = "amr-fleet-control"

type config struct {
	DatabaseURL, MigrationsURL                     string
	Brokers                                        []string
	MQTTURL, MQTTClientID, MapFile, Addr, LogLevel string
	ServiceName, ServiceVersion, OTLPEndpoint      string
	OutboxInterval                                 time.Duration
	OutboxBatch, OutboxMaxAttempts                 int
	Policy                                         usecases.Config
}

type reader struct{ getenv func(string) string }

func (r reader) or(k, def string) string {
	if v := r.getenv(k); v != "" {
		return v
	}
	return def
}

func (r reader) atLeast(k string, def, min int) int {
	if n, err := strconv.Atoi(r.getenv(k)); err == nil && n >= min {
		return n
	}
	return def
}

func (r reader) seconds(k string, def int) time.Duration {
	return time.Duration(r.atLeast(k, def, 1)) * time.Second
}

func (r reader) positive(k string, def float64) float64 {
	if f, err := strconv.ParseFloat(r.getenv(k), 64); err == nil && f > 0 {
		return f
	}
	return def
}

func loadConfig(getenv func(string) string) (config, error) {
	r := reader{getenv: getenv}
	d := usecases.DefaultConfig()
	c := config{DatabaseURL: getenv("DATABASE_URL"), MigrationsURL: postgres.MigrationsURL(getenv), MQTTURL: getenv("MQTT_URL"),
		MapFile: getenv("AMR_MAP_FILE"), MQTTClientID: r.or("MQTT_CLIENT_ID", "amr-fleet-control"), Addr: r.or("ADDR", ":8080"),
		LogLevel: r.or("LOG_LEVEL", "info"), ServiceName: r.or("OTEL_SERVICE_NAME", defaultServiceName),
		ServiceVersion: getenv("SERVICE_VERSION"), OTLPEndpoint: getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		OutboxInterval: time.Duration(r.atLeast("OUTBOX_INTERVAL_MS", 250, 1)) * time.Millisecond,
		OutboxBatch:    r.atLeast("OUTBOX_BATCH", 100, 1), OutboxMaxAttempts: r.atLeast("OUTBOX_MAX_ATTEMPTS", 20, 0),
		Policy: usecases.Config{ChargeLow: r.positive("CHARGE_LOW_PERCENT", d.ChargeLow), ChargeResume: r.positive("CHARGE_RESUME_PERCENT", d.ChargeResume),
			ReleaseAhead: r.atLeast("RELEASE_AHEAD_NODES", d.ReleaseAhead, 1), NodeTolerance: r.positive("NODE_TOLERANCE_METRES", d.NodeTolerance),
			StaleAfter: r.seconds("ROBOT_STALE_AFTER_SECONDS", 90), ResendAfter: r.seconds("ORDER_RESEND_AFTER_SECONDS", 5),
			ResendLimit: r.atLeast("ORDER_RESEND_LIMIT", d.ResendLimit, 1), RerouteAfter: r.seconds("REROUTE_AFTER_SECONDS", 10),
			DeadlockTimeout: r.seconds("DEADLOCK_TIMEOUT_SECONDS", 60), BlockedTimeout: r.seconds("ROBOT_BLOCKED_TIMEOUT_SECONDS", 60),
			OfflineTimeout: r.seconds("ROBOT_OFFLINE_TIMEOUT_SECONDS", 60)}}
	for _, b := range strings.Split(getenv("KAFKA_BROKERS"), ",") {
		if b = strings.TrimSpace(b); b != "" {
			c.Brokers = append(c.Brokers, b)
		}
	}
	var problems []error
	for _, req := range []struct{ name, value string }{{"DATABASE_URL", c.DatabaseURL}, {"KAFKA_BROKERS", strings.Join(c.Brokers, ",")},
		{"MQTT_URL", c.MQTTURL}, {"AMR_MAP_FILE", c.MapFile}} {
		if req.value == "" {
			problems = append(problems, errors.New(req.name+" is required: amr-fleet-control runs on real Postgres, Kafka and MQTT with a map file only"))
		}
	}
	if err := c.Policy.Validate(); err != nil {
		problems = append(problems, err)
	}
	return c, errors.Join(problems...)
}

// kafkaCheck is the /readyz dependency for the brokers: a TCP dial of the first one.
func kafkaCheck(brokers []string) func(context.Context) error {
	return func(ctx context.Context) error {
		d := net.Dialer{Timeout: 2 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", brokers[0])
		if err != nil {
			return err
		}
		return conn.Close()
	}
}
