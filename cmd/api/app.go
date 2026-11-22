package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	inboundhttp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/http"
)

// shutdownTimeout bounds the HTTP drain; a var so TestTheHTTPDrainIsBoundedByShutdownTimeout can
// shorten it. carry (Task 16 review).
var shutdownTimeout = 10 * time.Second

// run boots the fleet side (the starts: connect the link, Recover), runs the loops and HTTP on ln
// until ctx ends or Serve fails, then shuts down. lesson (plan 04 Task 19 review): readiness flips
// first; then the loops' own context ends (the link's worker, tick and sender, the relay and the
// purger stop) while HTTP drains (bounded by shutdownTimeout); once every loop has returned, close
// disconnects the link and closes the relay sink and the pool; main flushes telemetry last. Worst
// case ~10 s drain + a straggling unit's 5 s statement/lock timeouts + the 0.25 s disconnect + the
// 5 s flush, well inside a 30 s termination grace.
func (a *app) run(ctx context.Context, ln net.Listener) error {
	for _, start := range a.starts {
		if err := start(ctx); err != nil {
			_ = ln.Close()
			a.close()
			return err
		}
	}
	// The loops' context is cancelled by run itself, after the readiness flip, never directly by
	// the signal.
	loopCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	defer stop()
	var loops sync.WaitGroup
	for _, f := range a.loops {
		loops.Go(func() { f(loopCtx) })
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- a.server.Serve(ln) }()
	a.log.Info("amr-fleet-control listening", append([]any{"addr", ln.Addr().String()}, a.banner...)...)
	var err error
	select {
	case <-ctx.Done():
	case err = <-serveErr:
	}
	a.readiness.SetNotReady()
	stop()
	drain, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := a.server.Shutdown(drain)
	loops.Wait()
	a.close()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return shutdownErr
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
