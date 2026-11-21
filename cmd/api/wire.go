package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/http"
	"github.com/IQVO/amr-fleet-control/internal/adapters/mqtt"
	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/events"
	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/kafka"
	outboxrelay "github.com/IQVO/amr-fleet-control/internal/adapters/outbound/outbox"
	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/system"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	mapfile "github.com/IQVO/amr-fleet-control/internal/config"
)

// linkHolder is the service's Downlink until the fleet link exists (the link needs the service as
// its uplink); a command before then is an error the tick's resend repairs.
type linkHolder struct{ link *mqtt.FleetLink }

var errNoLink = errors.New("mqtt fleet link not connected yet")

func (h *linkHolder) SendOrder(ctx context.Context, o vda.Order) error {
	if h.link == nil {
		return errNoLink
	}
	return h.link.SendOrder(ctx, o)
}

func (h *linkHolder) SendInstantActions(ctx context.Context, a vda.InstantActions) error {
	if h.link == nil {
		return errNoLink
	}
	return h.link.SendInstantActions(ctx, a)
}

type app struct {
	log       *slog.Logger
	cfg       config
	pool      *pgxpool.Pool
	svc       *usecases.Service
	down      *linkHolder
	relay     *outboxrelay.Relay
	sink      *kafka.RelaySink
	idem      *postgres.IdempotencyStore
	readiness *inboundhttp.Readiness
	server    *http.Server
}

func wire(ctx context.Context, cfg config, log *slog.Logger) (*app, error) {
	m, err := mapfile.LoadMap(cfg.MapFile)
	if err != nil {
		return nil, err
	}
	if err := postgres.RunMigrations(cfg.MigrationsURL); err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	a := &app{log: log, cfg: cfg, pool: pool, down: &linkHolder{}, idem: postgres.NewIdempotencyStore(pool)}
	uow, outboxRepo := postgres.NewUnitOfWork(pool), postgres.NewOutboxRepo(pool)
	a.svc, err = usecases.NewService(m, cfg.Policy, usecases.Deps{UoW: uow, Fleets: postgres.NewFleets(pool), Robots: postgres.NewRobots(pool),
		Missions: postgres.NewMissions(pool), Sink: events.NewSink(outboxRepo, system.IDs{}.NewID, m.FleetID()), Downlink: a.down,
		Clock: system.Clock{}, IDs: system.IDs{}, Metrics: legMetrics(log), Log: log})
	if err != nil {
		pool.Close()
		return nil, err
	}
	a.sink = kafka.NewRelaySink(cfg.Brokers)
	a.relay = outboxrelay.NewOutboxRelay(outboxRepo, a.sink, log, outboxrelay.WithInterval(cfg.OutboxInterval),
		outboxrelay.WithBatchSize(cfg.OutboxBatch), outboxrelay.WithMaxAttempts(cfg.OutboxMaxAttempts))
	a.readiness = inboundhttp.NewReadiness(0, 0,
		inboundhttp.Dependency{Name: "postgres", Check: pool.Ping},
		inboundhttp.Dependency{Name: "kafka", Check: kafkaCheck(cfg.Brokers)},
		inboundhttp.Dependency{Name: "mqtt", Check: a.mqttCheck})
	idem := inboundhttp.NewIdempotency(a.idem, a.svc, system.Clock{}) // rulings P3-R10, P3-R15: the Service, so its serialized UoW
	router := inboundhttp.NewRouter(&inboundhttp.Server{Service: a.svc, Idempotency: idem.Middleware, Readiness: a.readiness,
		ServiceName: cfg.ServiceName}, log)
	a.server = &http.Server{Handler: router, ReadHeaderTimeout: 5 * time.Second}
	return a, nil
}

func (a *app) mqttCheck(context.Context) error {
	if a.down.link == nil || !a.down.link.Connected() {
		return errNoLink
	}
	return nil
}

// connect opens the ONE fleet link (decision D18); its subscriptions replay the retained
// connection and factsheet of every robot into the inbox.
func (a *app) connect() error {
	link, err := mqtt.NewFleetLink(a.cfg.MQTTURL, a.cfg.MQTTClientID, a.svc, a.log)
	if err != nil {
		return err
	}
	a.down.link = link
	return nil
}

func (a *app) close() {
	if a.down.link != nil {
		a.down.link.Close()
	}
	if a.sink != nil {
		_ = a.sink.Close()
	}
	a.pool.Close()
}
