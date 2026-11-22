package mission

import (
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// LegAssigned: a robot was chosen for the leg and its order went out.
type LegAssigned struct {
	LegID, ContainerID, From, To, Reference string
	Robot                                   robot.ID
	MapVersion                              string
	At                                      time.Time
}

// ContainerPickedUp: the robot's pick action FINISHED at the leg's from handoff point.
type ContainerPickedUp struct {
	LegID, ContainerID, From, Reference string
	Robot                               robot.ID
	At, RobotTime                       time.Time
}

// LegCompleted: the robot's drop action FINISHED at the leg's to handoff point.
type LegCompleted struct {
	LegID, ContainerID, To, Reference string
	Robot                             robot.ID
	At, RobotTime                     time.Time
}

// LegFailed: the leg's one failure outcome, with its reason.
type LegFailed struct {
	LegID, ContainerID, From, To, Reference string
	Robot                                   robot.ID
	Reason                                  Reason
	RobotError                              string
	ContainerOnRobot                        bool
	At                                      time.Time
}
