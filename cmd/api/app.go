package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	inboundhttp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/http"
)

const shutdownTimeout = 10 * time.Second

// run connects the link, recovers the fleet, starts the worker, the relay, the purger and HTTP
// on ln until ctx ends, then drains.
func (a *app) run(ctx context.Context, ln net.Listener) error {
	if err := a.connect(); err != nil {
		return err
	}
	if err := a.svc.Recover(ctx); err != nil {
		return err
	}
	a.down.link.Start(ctx)
	go func() { _ = a.relay.Run(ctx) }()
	go a.purge(ctx)
	go func() {
		<-ctx.Done()
		a.readiness.SetNotReady()
	}()
	errCh := make(chan error, 1)
	go func() {
		if err := a.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	a.log.Info("amr-fleet-control listening", "addr", ln.Addr().String(), "fleet", a.svc.Map().FleetID(), "map_version", a.svc.Map().Version())
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	drain, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return a.server.Shutdown(drain)
}

// purge enforces the 24-hour idempotency window, hourly.
func (a *app) purge(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		before := time.Now().UTC().Add(-inboundhttp.IdempotencyWindow)
		if _, err := a.idem.Purge(ctx, before); err != nil && ctx.Err() == nil {
			a.log.Warn("idempotency purge failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
