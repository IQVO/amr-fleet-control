package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// params reads and checks the named path parameters.
func params(r *http.Request, names ...string) (map[string]string, error) {
	out := map[string]string{}
	for _, n := range names {
		v := chi.URLParam(r, n)
		if err := rejectNUL(n, v); err != nil {
			return nil, err
		}
		out[n] = v
	}
	return out, nil
}

func (s *Server) handleGetFleet(w http.ResponseWriter, r *http.Request) {
	p, err := params(r, "fleetId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.Service.Fleet(r.Context(), p["fleetId"])
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toFleet(v))
}

func (s *Server) handleGetLeg(w http.ResponseWriter, r *http.Request) {
	p, err := params(r, "fleetId", "legId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.Service.Leg(r.Context(), p["fleetId"], p["legId"])
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toLeg(v))
}

func (s *Server) handleGetRobot(w http.ResponseWriter, r *http.Request) {
	p, err := params(r, "fleetId", "manufacturer", "serialNumber")
	if err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.Service.Robot(r.Context(), p["fleetId"], robot.ID{Manufacturer: p["manufacturer"], SerialNumber: p["serialNumber"]})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toRobot(v, true))
}

func (s *Server) handleRequestLeg(w http.ResponseWriter, r *http.Request) {
	p, err := params(r, "fleetId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var body legRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := body.validate(); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.Service.RequestLeg(r.Context(), usecases.LegRequest{FleetID: p["fleetId"], ContainerID: body.ContainerID,
		From: body.From, To: body.To, Reference: body.Reference})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/fleets/"+v.FleetID+"/legs/"+v.LegID)
	writeJSON(w, http.StatusAccepted, toLeg(v))
}

func (s *Server) handleCancelLeg(w http.ResponseWriter, r *http.Request) {
	p, err := params(r, "fleetId", "legId")
	if err != nil {
		writeError(w, r, err)
		return
	}
	v, now, err := s.Service.CancelLeg(r.Context(), p["fleetId"], p["legId"])
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := http.StatusAccepted
	if now {
		status = http.StatusOK
	}
	writeJSON(w, status, toLeg(v))
}
