package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/IQVO/amr-fleet-control/internal/application/ports"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
)

// LegRequest is relationship 4's "request a leg".
type LegRequest struct{ FleetID, ContainerID, From, To, Reference string }

// RequestLeg accepts a leg or refuses it at once (decision D5), then schedules the fleet.
func (s *Service) RequestLeg(ctx context.Context, req LegRequest) (LegView, error) {
	if req.FleetID != s.m.FleetID() {
		return LegView{}, fmt.Errorf("%w: %s", ErrFleetNotFound, req.FleetID)
	}
	if req.ContainerID == "" || req.From == "" || req.To == "" || req.From == req.To {
		return LegView{}, fmt.Errorf("%w: container_id and two different handoff points are required", ErrInvalidRequest)
	}
	pickup, ok := s.m.NodeOfHandoff(req.From)
	if !ok {
		return LegView{}, fmt.Errorf("%w: %s", ErrUnknownHandoff, req.From)
	}
	drop, ok := s.m.NodeOfHandoff(req.To)
	if !ok {
		return LegView{}, fmt.Errorf("%w: %s", ErrUnknownHandoff, req.To)
	}
	if _, ok := s.m.Route(pickup, drop, nil); !ok {
		return LegView{}, fmt.Errorf("%w: no route from %s to %s", ErrHandoffUnreachable, req.From, req.To)
	}
	var view LegView
	err := s.inFleet(ctx, func(u *unit) error {
		if !u.anyOnline() {
			return ErrFleetUnavailable
		}
		ms, err := mission.NewLeg(mission.LegSpec{ID: "leg-" + s.d.IDs.NewID(), MapVersion: s.m.Version(), ContainerID: req.ContainerID,
			From: req.From, To: req.To, PickupNode: pickup, DropNode: drop, Reference: req.Reference, At: u.now})
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
		if err := u.insert(ms); errors.Is(err, ports.ErrOpenLegExists) {
			return fmt.Errorf("%w: %s", ErrContainerBusy, req.ContainerID)
		} else if err != nil {
			return err
		}
		if err := u.schedule(); err != nil {
			return err
		}
		view = legView(s.m.FleetID(), ms)
		return nil
	})
	return view, err
}

// CancelLeg cancels a queued leg at once (true) or asks the robot (false) (decision D17).
func (s *Service) CancelLeg(ctx context.Context, fleetID, legID string) (LegView, bool, error) {
	if fleetID != s.m.FleetID() {
		return LegView{}, false, fmt.Errorf("%w: %s", ErrFleetNotFound, fleetID)
	}
	var view LegView
	var now bool
	err := s.inFleet(ctx, func(u *unit) error {
		ms, err := u.missionByID(legID)
		if err != nil {
			return err
		}
		if !ms.External() {
			return fmt.Errorf("%w: %s", ErrLegNotFound, legID)
		}
		wasRequested := ms.Status() == mission.CancelRequested
		immediate, evs, err := ms.RequestCancel(u.now)
		if err != nil {
			return err
		}
		u.raise(evs...)
		u.touchM(ms)
		if immediate {
			u.ended = append(u.ended, endedLeg{outcome: "failed", reason: string(mission.ReasonCancelled)})
		} else if r, ok := u.robots[ms.Robot().Key()]; ok && !wasRequested {
			u.cancelOrder(r, ms.CancelActionID())
		}
		if err := u.schedule(); err != nil {
			return err
		}
		view, now = legView(s.m.FleetID(), ms), immediate
		return nil
	})
	return view, now, err
}
