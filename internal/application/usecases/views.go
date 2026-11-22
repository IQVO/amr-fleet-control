package usecases

import (
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// LegView is the relationship-4 read model of a leg (zero values are absent).
type LegView struct {
	LegID, FleetID, ContainerID, From, To, Reference, Status string
	Robot                                                    robot.ID
	Reason, RobotError, MapVersion                           string
	AcceptedAt, AssignedAt, PickedUpAt, EndedAt              time.Time
}

func legView(fleetID string, m *mission.Mission) LegView {
	return LegView{LegID: m.ID(), FleetID: fleetID, ContainerID: m.ContainerID(), From: m.From(), To: m.To(),
		Reference: m.Reference(), Status: string(m.Status()), Robot: m.Robot(), Reason: string(m.Reason()),
		RobotError: m.RobotError(), MapVersion: m.MapVersion(), AcceptedAt: m.AcceptedAt(), AssignedAt: m.AssignedAt(),
		PickedUpAt: m.PickedUpAt(), EndedAt: m.EndedAt()}
}
