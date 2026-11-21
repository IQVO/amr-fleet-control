package robot

import "time"

// VDA 5050 error levels and the error types fleet control acts on.
const (
	LevelWarning  = "WARNING"
	LevelUrgent   = "URGENT"
	LevelCritical = "CRITICAL"
	LevelFatal    = "FATAL"

	ErrTypeValidation   = "VALIDATION_FAILURE"
	ErrTypeInvalidOrder = "INVALID_ORDER"
	ErrTypeBlocked      = "ROBOT_BLOCKED"
	ErrTypeBatteryEmpty = "BATTERY_EMPTY"
)

// VDA 5050 action statuses.
const (
	ActionWaiting  = "WAITING"
	ActionRunning  = "RUNNING"
	ActionFinished = "FINISHED"
	ActionFailed   = "FAILED"
)

// Error is one active robot error.
type Error struct{ Type, Level string }

// ActionState is the status of one node or instant action.
type ActionState struct{ ID, Type, Status string }

// Report is one VDA 5050 `state` message, translated (At is the header timestamp, the robot's
// clock). Pending is true while nodes or edges of the current order remain.
type Report struct {
	At             time.Time
	OrderID        string
	OrderUpdateID  uint32
	LastNodeID     string
	LastNodeSeq    uint32
	X, Y           float64
	MapID          string
	Driving        bool
	Pending        bool
	Battery        float64
	Charging       bool
	LoadIDs        []string
	Errors         []Error
	Actions        []ActionState
	InstantActions []ActionState
}

// Factsheet is the subset of the VDA 5050 factsheet fleet control keeps.
type Factsheet struct {
	SeriesName            string
	MaxSpeed, MaxLoadMass float64
}

// Action finds a node action or an instant action by id.
func (r Report) Action(id string) (ActionState, bool) {
	for _, list := range [][]ActionState{r.Actions, r.InstantActions} {
		for _, a := range list {
			if a.ID == id {
				return a, true
			}
		}
	}
	return ActionState{}, false
}

// ErrorTypes lists the active error types once each, in report order (never nil).
func (r Report) ErrorTypes() []string {
	out := []string{}
	seen := map[string]bool{}
	for _, e := range r.Errors {
		if !seen[e.Type] {
			seen[e.Type] = true
			out = append(out, e.Type)
		}
	}
	return out
}

// Blocking returns the first CRITICAL or FATAL error: the robot cannot run its order.
func (r Report) Blocking() (string, bool) {
	return r.first(func(e Error) bool { return e.Level == LevelCritical || e.Level == LevelFatal })
}

// Fatal returns the first FATAL error.
func (r Report) Fatal() (string, bool) {
	return r.first(func(e Error) bool { return e.Level == LevelFatal })
}

// Rejected returns the first order-rejection error (the emulator reports them as WARNINGs).
func (r Report) Rejected() (string, bool) {
	return r.first(func(e Error) bool { return e.Type == ErrTypeValidation || e.Type == ErrTypeInvalidOrder })
}

// Rejections counts the order-rejection entries. The emulator appends one entry per rejected
// order and clears the list when it accepts an order, so a rise between two reports is a new
// rejection (decision D19).
func (r Report) Rejections() int {
	n := 0
	for _, e := range r.Errors {
		if e.Type == ErrTypeValidation || e.Type == ErrTypeInvalidOrder {
			n++
		}
	}
	return n
}

func (r Report) first(match func(Error) bool) (string, bool) {
	for _, e := range r.Errors {
		if match(e) {
			return e.Type, true
		}
	}
	return "", false
}

func copyReport(r Report) Report {
	r.LoadIDs = append([]string(nil), r.LoadIDs...)
	r.Errors = append([]Error(nil), r.Errors...)
	r.Actions = append([]ActionState(nil), r.Actions...)
	r.InstantActions = append([]ActionState(nil), r.InstantActions...)
	return r
}
