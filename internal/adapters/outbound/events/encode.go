package events

import (
	"fmt"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// Payloads (apis/asyncapi.yaml). *_at is amr-fleet-control's clock; robot_timestamp is the VDA
// 5050 header timestamp of the robot state the fact came from (the emulator's simulated time).

type robotRef struct {
	Manufacturer string `json:"manufacturer"`
	SerialNumber string `json:"serial_number"`
}

type legAssigned struct {
	LegID       string    `json:"leg_id"`
	FleetID     string    `json:"fleet_id"`
	ContainerID string    `json:"container_id"`
	From        string    `json:"from_handoff_point_id"`
	To          string    `json:"to_handoff_point_id"`
	Reference   string    `json:"reference,omitempty"`
	Robot       robotRef  `json:"robot"`
	MapVersion  string    `json:"map_version"`
	AssignedAt  time.Time `json:"assigned_at"`
}

type containerPickedUp struct {
	LegID          string     `json:"leg_id"`
	FleetID        string     `json:"fleet_id"`
	ContainerID    string     `json:"container_id"`
	From           string     `json:"from_handoff_point_id"`
	Reference      string     `json:"reference,omitempty"`
	Robot          robotRef   `json:"robot"`
	PickedUpAt     time.Time  `json:"picked_up_at"`
	RobotTimestamp *time.Time `json:"robot_timestamp,omitempty"`
}

type legCompleted struct {
	LegID          string     `json:"leg_id"`
	FleetID        string     `json:"fleet_id"`
	ContainerID    string     `json:"container_id"`
	To             string     `json:"to_handoff_point_id"`
	Reference      string     `json:"reference,omitempty"`
	Robot          robotRef   `json:"robot"`
	CompletedAt    time.Time  `json:"completed_at"`
	RobotTimestamp *time.Time `json:"robot_timestamp,omitempty"`
}

type legFailed struct {
	LegID            string    `json:"leg_id"`
	FleetID          string    `json:"fleet_id"`
	ContainerID      string    `json:"container_id"`
	From             string    `json:"from_handoff_point_id"`
	To               string    `json:"to_handoff_point_id"`
	Reference        string    `json:"reference,omitempty"`
	Robot            *robotRef `json:"robot,omitempty"`
	Reason           string    `json:"reason"`
	RobotError       string    `json:"robot_error,omitempty"`
	ContainerOnRobot bool      `json:"container_on_robot"`
	FailedAt         time.Time `json:"failed_at"`
}

type robotStateChanged struct {
	FleetID        string     `json:"fleet_id"`
	Robot          robotRef   `json:"robot"`
	State          string     `json:"state"`
	PreviousState  string     `json:"previous_state"`
	Connection     string     `json:"connection"`
	BatteryPercent float64    `json:"battery_percent"`
	Charging       bool       `json:"charging"`
	ErrorTypes     []string   `json:"error_types"`
	ChangedAt      time.Time  `json:"changed_at"`
	RobotTimestamp *time.Time `json:"robot_timestamp,omitempty"`
}

type fleetCapacityChanged struct {
	FleetID         string    `json:"fleet_id"`
	RobotsTotal     int       `json:"robots_total"`
	RobotsOnline    int       `json:"robots_online"`
	RobotsAvailable int       `json:"robots_available"`
	RobotsCharging  int       `json:"robots_charging"`
	RobotsHeld      int       `json:"robots_held"`
	LegsQueued      int       `json:"legs_queued"`
	LegsActive      int       `json:"legs_active"`
	ChangedAt       time.Time `json:"changed_at"`
}

type deadlockDetected struct {
	FleetID    string     `json:"fleet_id"`
	DeadlockID string     `json:"deadlock_id"`
	Robots     []robotRef `json:"robots"`
	NodeIDs    []string   `json:"node_ids"`
	DetectedAt time.Time  `json:"detected_at"`
}

func ref(id robot.ID) robotRef {
	return robotRef{Manufacturer: id.Manufacturer, SerialNumber: id.SerialNumber}
}

func optRef(id robot.ID) *robotRef {
	if id.IsZero() {
		return nil
	}
	r := ref(id)
	return &r
}

func robotTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func describe(fleetID string, e any) (occurrence, error) {
	switch ev := e.(type) {
	case mission.LegAssigned:
		return occurrence{"mission", "LegAssigned", ev.LegID, ev.At, legAssigned{ev.LegID, fleetID, ev.ContainerID, ev.From, ev.To,
			ev.Reference, ref(ev.Robot), ev.MapVersion, ev.At.UTC()}}, nil
	case mission.ContainerPickedUp:
		return occurrence{"mission", "ContainerPickedUp", ev.LegID, ev.At, containerPickedUp{ev.LegID, fleetID, ev.ContainerID, ev.From,
			ev.Reference, ref(ev.Robot), ev.At.UTC(), robotTime(ev.RobotTime)}}, nil
	case mission.LegCompleted:
		return occurrence{"mission", "LegCompleted", ev.LegID, ev.At, legCompleted{ev.LegID, fleetID, ev.ContainerID, ev.To,
			ev.Reference, ref(ev.Robot), ev.At.UTC(), robotTime(ev.RobotTime)}}, nil
	case mission.LegFailed:
		return occurrence{"mission", "LegFailed", ev.LegID, ev.At, legFailed{ev.LegID, fleetID, ev.ContainerID, ev.From, ev.To,
			ev.Reference, optRef(ev.Robot), string(ev.Reason), ev.RobotError, ev.ContainerOnRobot, ev.At.UTC()}}, nil
	case robot.RobotStateChanged:
		return occurrence{"robot", "RobotStateChanged", ev.Robot.Key(), ev.At, robotStateChanged{fleetID, ref(ev.Robot), string(ev.State),
			string(ev.Previous), string(ev.Connection), ev.Battery, ev.Charging, nonNil(ev.ErrorTypes), ev.At.UTC(), robotTime(ev.RobotTime)}}, nil
	case capacity.FleetCapacityChanged:
		s := ev.Snapshot
		return occurrence{"fleet", "FleetCapacityChanged", fleetID, s.At, fleetCapacityChanged{fleetID, s.RobotsTotal, s.RobotsOnline,
			s.RobotsAvailable, s.RobotsCharging, s.RobotsHeld, s.LegsQueued, s.LegsActive, s.At.UTC()}}, nil
	case fleetmap.DeadlockDetected:
		return describeDeadlock(fleetID, ev)
	default:
		return occurrence{}, fmt.Errorf("events: no CloudEvents encoder for %T", e)
	}
}

func describeDeadlock(fleetID string, ev fleetmap.DeadlockDetected) (occurrence, error) {
	robots := make([]robotRef, 0, len(ev.Robots))
	for _, k := range ev.Robots {
		id, err := robot.ParseKey(k)
		if err != nil {
			return occurrence{}, err
		}
		robots = append(robots, ref(id))
	}
	return occurrence{"fleetmap", "DeadlockDetected", fleetID, ev.At, deadlockDetected{fleetID, ev.DeadlockID, robots,
		append([]string{}, ev.Nodes...), ev.At.UTC()}}, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
