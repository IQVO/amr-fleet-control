// Package vda holds the VDA 5050 v3 commands and messages as application value types: the use
// cases build them and the MQTT adapter encodes them (decision D2). No JSON here.
package vda

import (
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

const (
	BlockingHard = "HARD"
	BlockingNone = "NONE"

	CancelOrder      = "cancelOrder"
	StopCharging     = "stopCharging"
	StateRequest     = "stateRequest"
	FactsheetRequest = "factsheetRequest"

	ParamLoadID = "loadId"
)

// Action is a node action or an instant action.
type Action struct {
	Type, ID, Blocking string
	Params             map[string]string
}

// Node is one order node; X, Y and MapID are its nodePosition.
type Node struct {
	ID       string
	Seq      uint32
	Released bool
	X, Y     float64
	MapID    string
	Actions  []Action
}

// Edge is one order edge (never with actions).
type Edge struct {
	ID         string
	Seq        uint32
	Released   bool
	Start, End string
	MaxSpeed   float64
}

// Order is a new order (UpdateID 0) or an order update.
type Order struct {
	Robot    robot.ID
	OrderID  string
	UpdateID uint32
	Nodes    []Node
	Edges    []Edge
}

// InstantActions is one instantActions message.
type InstantActions struct {
	Robot   robot.ID
	Actions []Action
}

// State is one translated `state` message.
type State struct {
	Robot  robot.ID
	Report robot.Report
}

// Connection is one `connection` message; At is its header timestamp.
type Connection struct {
	Robot robot.ID
	State robot.Connection
	At    time.Time
}

// Factsheet is one `factsheet` message.
type Factsheet struct {
	Robot     robot.ID
	Factsheet robot.Factsheet
}

// NodeSeq is the sequenceId of the node at route index i (decision D8: nodes even).
func NodeSeq(i int) uint32 { return uint32(2 * i) } //nolint:gosec // route indexes are bounded by the map size

// EdgeSeq is the sequenceId of the edge into the node at route index i (i >= 1).
func EdgeSeq(i int) uint32 { return uint32(2*i - 1) } //nolint:gosec // route indexes are bounded by the map size

// IndexOfSeq maps a node sequenceId back to its route index.
func IndexOfSeq(seq uint32) (int, bool) {
	if seq%2 != 0 {
		return 0, false
	}
	return int(seq / 2), true
}
