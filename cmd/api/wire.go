package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
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
	// lesson (plan 04 Task 19 review): run's boot steps in order (connect the link, Recover), the
	// loops it runs until the shutdown (the link's worker/tick/sender, the relay, the purger) and
	// what close shuts after every loop returned, in order (link, relay sink, pool).
	starts    []func(context.Context) error
	loops     []func(context.Context)
	closers   []func()
	closeOnce sync.Once
	banner    []any // the listening line's fleet and map version
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
	a.server = newHTTPServer(cfg, &inboundhttp.Server{Service: a.svc, Idempotency: idem.Middleware, Readiness: a.readiness}, log)
	a.sequence()
	a.banner = []any{"fleet", m.FleetID(), "map_version", m.Version()}
	return a, nil
}

// sequence fills run's boot steps, loops and closers (boot order: link connect, Recover, then the
// link's worker, the relay, the purger and HTTP; shutdown: loops, then link, sink, pool).
func (a *app) sequence() {
	a.starts = []func(context.Context) error{func(context.Context) error { return a.connect() }, a.svc.Recover}
	a.loops = []func(context.Context){a.runLink, func(ctx context.Context) { _ = a.relay.Run(ctx) }, a.purge}
	a.closers = []func(){func() {
		if a.down.link != nil {
			a.down.link.Close()
		}
	}, func() { _ = a.sink.Close() }, a.pool.Close}
}

// runLink runs the fleet link's worker, tick and sender until ctx ends and waits for all three.
func (a *app) runLink(ctx context.Context) {
	a.down.link.Start(ctx)
	a.down.link.Wait()
}

// newHTTPServer builds the REST server over s; the router's spans and metric series are named
// after OTEL_SERVICE_NAME. carry (Task 15 review): the name comes from the config, pinned by
// TestTheRouterIsNamedAfterTheConfiguredServiceName.
func newHTTPServer(cfg config, s *inboundhttp.Server, log *slog.Logger) *http.Server {
	s.ServiceName = cfg.ServiceName
	return &http.Server{Handler: inboundhttp.NewRouter(s, log), ReadHeaderTimeout: 5 * time.Second}
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

// close runs the closers once, in order (run calls it after every loop returned; main's defer and a
// test cleanup calling it again do nothing).
func (a *app) close() {
	a.closeOnce.Do(func() {
		for _, f := range a.closers {
			f()
		}
	})
}
