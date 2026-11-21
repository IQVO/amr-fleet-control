package http

import (
	"fmt"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
)

type legRequest struct {
	ContainerID string `json:"container_id"`
	From        string `json:"from_handoff_point_id"`
	To          string `json:"to_handoff_point_id"`
	Reference   string `json:"reference,omitempty"`
}

func (b legRequest) validate() error {
	for _, f := range []struct{ name, v string }{{"container_id", b.ContainerID}, {"from_handoff_point_id", b.From}, {"to_handoff_point_id", b.To}} {
		if err := rejectNUL(f.name, f.v); err != nil {
			return err
		}
	}
	if b.Reference != "" {
		if err := rejectNUL("reference", b.Reference); err != nil {
			return err
		}
	}
	if b.From == b.To {
		return fmt.Errorf("%w: from_handoff_point_id and to_handoff_point_id must differ", errInvalidParameter)
	}
	return nil
}

type robotRef struct {
	Manufacturer string `json:"manufacturer"`
	SerialNumber string `json:"serial_number"`
}

type legResponse struct {
	LegID       string     `json:"leg_id"`
	FleetID     string     `json:"fleet_id"`
	ContainerID string     `json:"container_id"`
	From        string     `json:"from_handoff_point_id"`
	To          string     `json:"to_handoff_point_id"`
	Reference   string     `json:"reference,omitempty"`
	Status      string     `json:"status"`
	Robot       *robotRef  `json:"robot,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	RobotError  string     `json:"robot_error,omitempty"`
	MapVersion  string     `json:"map_version"`
	AcceptedAt  time.Time  `json:"accepted_at"`
	AssignedAt  *time.Time `json:"assigned_at,omitempty"`
	PickedUpAt  *time.Time `json:"picked_up_at,omitempty"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
}

func opt(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func toLeg(v usecases.LegView) legResponse {
	out := legResponse{LegID: v.LegID, FleetID: v.FleetID, ContainerID: v.ContainerID, From: v.From, To: v.To, Reference: v.Reference,
		Status: v.Status, Reason: v.Reason, RobotError: v.RobotError, MapVersion: v.MapVersion, AcceptedAt: v.AcceptedAt.UTC(),
		AssignedAt: opt(v.AssignedAt), PickedUpAt: opt(v.PickedUpAt), EndedAt: opt(v.EndedAt)}
	if !v.Robot.IsZero() {
		out.Robot = &robotRef{Manufacturer: v.Robot.Manufacturer, SerialNumber: v.Robot.SerialNumber}
	}
	return out
}

type factsheetResponse struct {
	SeriesName  string  `json:"series_name"`
	MaxSpeed    float64 `json:"max_speed"`
	MaxLoadMass float64 `json:"max_load_mass"`
}

type robotResponse struct {
	Manufacturer string             `json:"manufacturer"`
	SerialNumber string             `json:"serial_number"`
	Connection   string             `json:"connection"`
	State        string             `json:"state"`
	Battery      float64            `json:"battery_percent"`
	Charging     bool               `json:"charging"`
	Available    bool               `json:"available"`
	NodeID       string             `json:"node_id,omitempty"`
	MissionID    string             `json:"mission_id,omitempty"`
	ErrorTypes   []string           `json:"error_types"`
	LastSeenAt   time.Time          `json:"last_seen_at"`
	Factsheet    *factsheetResponse `json:"factsheet,omitempty"`
}

func toRobot(v usecases.RobotView, withFactsheet bool) robotResponse {
	out := robotResponse{Manufacturer: v.ID.Manufacturer, SerialNumber: v.ID.SerialNumber, Connection: v.Connection, State: v.State,
		Battery: v.Battery, Charging: v.Charging, Available: v.Available, NodeID: v.Node, MissionID: v.MissionID,
		ErrorTypes: v.ErrorTypes, LastSeenAt: v.LastSeenAt.UTC()}
	if out.ErrorTypes == nil {
		out.ErrorTypes = []string{}
	}
	if withFactsheet && v.HasFactsheet {
		out.Factsheet = &factsheetResponse{SeriesName: v.Factsheet.SeriesName, MaxSpeed: v.Factsheet.MaxSpeed, MaxLoadMass: v.Factsheet.MaxLoadMass}
	}
	return out
}

type capacityResponse struct {
	RobotsTotal     int `json:"robots_total"`
	RobotsOnline    int `json:"robots_online"`
	RobotsAvailable int `json:"robots_available"`
	RobotsCharging  int `json:"robots_charging"`
	RobotsHeld      int `json:"robots_held"`
	LegsQueued      int `json:"legs_queued"`
	LegsActive      int `json:"legs_active"`
}

type fleetResponse struct {
	FleetID       string           `json:"fleet_id"`
	MapID         string           `json:"map_id"`
	MapVersion    string           `json:"map_version"`
	HandoffPoints []string         `json:"handoff_points"`
	Robots        []robotResponse  `json:"robots"`
	Capacity      capacityResponse `json:"capacity"`
}

func toFleet(v usecases.FleetView) fleetResponse {
	c := v.Capacity
	out := fleetResponse{FleetID: v.FleetID, MapID: v.MapID, MapVersion: v.MapVersion, HandoffPoints: v.HandoffPoints, Robots: []robotResponse{},
		Capacity: capacityResponse{c.RobotsTotal, c.RobotsOnline, c.RobotsAvailable, c.RobotsCharging, c.RobotsHeld, c.LegsQueued, c.LegsActive}}
	for _, r := range v.Robots {
		out.Robots = append(out.Robots, toRobot(r, false))
	}
	return out
}
