// Package http is amr-fleet-control's REST adapter: the subsystem-controller interface
// material-flow-control calls (WCS context map relationship 4, the same shape as asrs-control's
// relationship 5), problem+json errors with replay classes, the Idempotency-Key middleware on
// every POST and the probes. No auth middleware (fleet decision 2026-09-11).
package http

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/IQVO/amr-fleet-control/internal/adapters/inbound/requestlog"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
)

// Server holds what the handlers need. Idempotency nil leaves the POSTs unwrapped (handler unit
// tests only; cmd/ always wires it). Readiness nil means always ready.
type Server struct {
	Service     *usecases.Service
	Idempotency func(http.Handler) http.Handler
	Readiness   *Readiness
	// ServiceName names the otelchi server spans and metric series (OTEL_SERVICE_NAME).
	ServiceName string
}

// NewRouter builds every route of apis/openapi.yaml.
func NewRouter(s *Server, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	r := chi.NewRouter()
	name := s.ServiceName
	if name == "" {
		name = "amr-fleet-control"
	}
	metricCfg := otelchimetric.NewBaseConfig(name)
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(name, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(metricCfg))
	r.Use(otelchimetric.NewServerActiveRequests(metricCfg))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(recoverPanic)
	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	r.Get("/fleets/{fleetId}", s.handleGetFleet)
	r.Get("/fleets/{fleetId}/legs/{legId}", s.handleGetLeg)
	r.Get("/fleets/{fleetId}/robots/{manufacturer}/{serialNumber}", s.handleGetRobot)
	r.Group(func(r chi.Router) {
		if s.Idempotency != nil {
			r.Use(s.Idempotency)
		}
		r.Post("/fleets/{fleetId}/legs", s.handleRequestLeg)
		r.Post("/fleets/{fleetId}/legs/{legId}/cancellation", s.handleCancelLeg)
	})
	logger.Debug("router built")
	return r
}
