// Package mission is this context's execution of one leg by one robot (kind LEG), or an internal
// CHARGE or PARK run: the route, the released base, the VDA 5050 orderUpdateId (responsibility 3),
// the map version it is pinned to (responsibility 5) and its one outcome (responsibility 6).
package mission

import (
	"errors"
	"fmt"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

var (
	ErrInvalidMission    = errors.New("invalid mission")
	ErrInvalidTransition = errors.New("invalid mission transition")
	ErrNotCancellable    = errors.New("the container is already on the robot")
	ErrLegEnded          = errors.New("the leg has already ended")
)

type Kind string

const (
	Leg    Kind = "LEG"
	Charge Kind = "CHARGE"
	Park   Kind = "PARK"
)

type Status string

const (
	Queued          Status = "QUEUED"
	Assigned        Status = "ASSIGNED"
	PickedUp        Status = "PICKED_UP"
	CancelRequested Status = "CANCEL_REQUESTED"
	Done            Status = "DONE"
	Failed          Status = "FAILED"
	Cancelled       Status = "CANCELLED"
)

type Reason string

const (
	ReasonCancelled         Reason = "CANCELLED"
	ReasonRobotBlocked      Reason = "ROBOT_BLOCKED"
	ReasonRobotFault        Reason = "ROBOT_FAULT"
	ReasonRobotOffline      Reason = "ROBOT_OFFLINE"
	ReasonOrderRejected     Reason = "ORDER_REJECTED"
	ReasonPickFailed        Reason = "PICK_FAILED"
	ReasonDropFailed        Reason = "DROP_FAILED"
	ReasonMapVersionRetired Reason = "MAP_VERSION_RETIRED"
)

// The VDA 5050 node actions a route carries.
const (
	ActionPick   = "pick"
	ActionDrop   = "drop"
	ActionCharge = "startCharging"
)

// Step is one node of a route; Edge joins the previous step's node ("" on step 0).
type Step struct{ Node, Edge, Action string }

// LegSpec describes an accepted leg.
type LegSpec struct {
	ID, MapVersion, ContainerID, From, To, PickupNode, DropNode, Reference string
	At                                                                     time.Time
}

// State is the persisted form. SeenUpdate is the highest orderUpdateId a robot state showed (-1
// none); OrderStart is the route index of the first node of the latest order message.
type State struct {
	ID                                                                 string
	Kind                                                               Kind
	Status                                                             Status
	MapVersion, ContainerID, From, To, PickupNode, DropNode, Reference string
	Robot                                                              robot.ID
	Route                                                              []Step
	BaseEnd, Reached, OrderStart                                       int
	UpdateID                                                           uint32
	Sent                                                               bool
	Resends                                                            int
	SeenUpdate                                                         int64
	SentAt                                                             time.Time
	Reason                                                             Reason
	RobotError                                                         string
	Seq                                                                int64
	AcceptedAt, AssignedAt, PickedUpAt, EndedAt                        time.Time
}

// Mission is the aggregate.
type Mission struct{ s State }

// NewLeg accepts a leg: QUEUED, no robot yet.
func NewLeg(sp LegSpec) (*Mission, error) {
	if sp.ID == "" || sp.MapVersion == "" || sp.ContainerID == "" || sp.From == "" || sp.To == "" ||
		sp.PickupNode == "" || sp.DropNode == "" || sp.At.IsZero() {
		return nil, fmt.Errorf("%w: id, map version, container, both handoff points, both nodes and a time are required", ErrInvalidMission)
	}
	if sp.From == sp.To || sp.PickupNode == sp.DropNode {
		return nil, fmt.Errorf("%w: a leg must move the container to another handoff point", ErrInvalidMission)
	}
	return &Mission{s: State{ID: sp.ID, Kind: Leg, Status: Queued, MapVersion: sp.MapVersion, ContainerID: sp.ContainerID,
		From: sp.From, To: sp.To, PickupNode: sp.PickupNode, DropNode: sp.DropNode, Reference: sp.Reference,
		SeenUpdate: -1, AcceptedAt: sp.At}}, nil
}

// NewInternal starts a CHARGE or PARK mission, ASSIGNED to r at once.
func NewInternal(kind Kind, id, mapVersion string, r robot.ID, route []Step, baseEnd int, at time.Time) (*Mission, error) {
	if kind != Charge && kind != Park {
		return nil, fmt.Errorf("%w: kind %s is not internal", ErrInvalidMission, kind)
	}
	if id == "" || mapVersion == "" || r.IsZero() || at.IsZero() {
		return nil, fmt.Errorf("%w: id, map version, robot and time are required", ErrInvalidMission)
	}
	m := &Mission{s: State{ID: id, Kind: kind, Status: Assigned, MapVersion: mapVersion, Robot: r, SeenUpdate: -1, AcceptedAt: at, AssignedAt: at}}
	if err := m.setRoute(route, baseEnd); err != nil {
		return nil, err
	}
	return m, nil
}

// Rehydrate rebuilds a mission.
func Rehydrate(s State) *Mission {
	s.Route = append([]Step(nil), s.Route...)
	return &Mission{s: s}
}

// State returns a copy of the persisted form.
func (m *Mission) State() State {
	s := m.s
	s.Route = append([]Step(nil), m.s.Route...)
	return s
}

func (m *Mission) setRoute(route []Step, baseEnd int) error {
	if err := validRoute(m.s.Kind, route, m.s.PickupNode, m.s.DropNode); err != nil {
		return err
	}
	if baseEnd < 0 || baseEnd >= len(route) {
		return fmt.Errorf("%w: base end %d outside a route of %d steps", ErrInvalidMission, baseEnd, len(route))
	}
	m.s.Route = append([]Step(nil), route...)
	m.s.BaseEnd, m.s.Reached, m.s.OrderStart, m.s.UpdateID, m.s.Sent = baseEnd, 0, 0, 0, false
	return nil
}

func validRoute(kind Kind, route []Step, pickup, drop string) error {
	minSteps := 2
	if kind == Charge {
		minSteps = 1
	}
	if len(route) < minSteps {
		return fmt.Errorf("%w: a %s route needs %d steps", ErrInvalidMission, kind, minSteps)
	}
	at, err := actions(route)
	if err != nil {
		return err
	}
	if !fits(kind, route, at, pickup, drop) {
		return fmt.Errorf("%w: the %s route does not carry its actions", ErrInvalidMission, kind)
	}
	return nil
}

// actions returns the step index of each action (-1 when absent) after checking the steps chain.
func actions(route []Step) (map[string]int, error) {
	at := map[string]int{ActionPick: -1, ActionDrop: -1, ActionCharge: -1}
	for i, st := range route {
		if st.Node == "" || (i == 0) != (st.Edge == "") || (i > 0 && st.Node == route[i-1].Node) {
			return nil, fmt.Errorf("%w: step %d does not chain", ErrInvalidMission, i)
		}
		if st.Action == "" {
			continue
		}
		prev, known := at[st.Action]
		if !known || prev >= 0 {
			return nil, fmt.Errorf("%w: step %d action %q is unknown or repeated", ErrInvalidMission, i, st.Action)
		}
		at[st.Action] = i
	}
	return at, nil
}

func fits(kind Kind, route []Step, at map[string]int, pickup, drop string) bool {
	pick, dr, ch := at[ActionPick], at[ActionDrop], at[ActionCharge]
	switch kind {
	case Leg:
		return pick >= 0 && dr > pick && ch < 0 && route[pick].Node == pickup && route[dr].Node == drop
	case Charge:
		return ch == len(route)-1 && pick < 0 && dr < 0
	default:
		return pick < 0 && dr < 0 && ch < 0
	}
}

func (m *Mission) ID() string            { return m.s.ID }
func (m *Mission) Kind() Kind            { return m.s.Kind }
func (m *Mission) Status() Status        { return m.s.Status }
func (m *Mission) MapVersion() string    { return m.s.MapVersion }
func (m *Mission) ContainerID() string   { return m.s.ContainerID }
func (m *Mission) From() string          { return m.s.From }
func (m *Mission) To() string            { return m.s.To }
func (m *Mission) PickupNode() string    { return m.s.PickupNode }
func (m *Mission) DropNode() string      { return m.s.DropNode }
func (m *Mission) Reference() string     { return m.s.Reference }
func (m *Mission) Robot() robot.ID       { return m.s.Robot }
func (m *Mission) Route() []Step         { return append([]Step(nil), m.s.Route...) }
func (m *Mission) BaseEnd() int          { return m.s.BaseEnd }
func (m *Mission) Reached() int          { return m.s.Reached }
func (m *Mission) OrderStart() int       { return m.s.OrderStart }
func (m *Mission) UpdateID() uint32      { return m.s.UpdateID }
func (m *Mission) Sent() bool            { return m.s.Sent }
func (m *Mission) Resends() int          { return m.s.Resends }
func (m *Mission) SentAt() time.Time     { return m.s.SentAt }
func (m *Mission) Reason() Reason        { return m.s.Reason }
func (m *Mission) RobotError() string    { return m.s.RobotError }
func (m *Mission) Seq() int64            { return m.s.Seq }
func (m *Mission) AcceptedAt() time.Time { return m.s.AcceptedAt }
func (m *Mission) AssignedAt() time.Time { return m.s.AssignedAt }
func (m *Mission) PickedUpAt() time.Time { return m.s.PickedUpAt }
func (m *Mission) EndedAt() time.Time    { return m.s.EndedAt }

// AssignSeq records the acceptance order (set by the repository).
func (m *Mission) AssignSeq(seq int64) { m.s.Seq = seq }

// External is true for legs (they publish events).
func (m *Mission) External() bool { return m.s.Kind == Leg }

// Open is every status before an outcome.
func (m *Mission) Open() bool {
	return m.s.Status == Queued || m.Active()
}

// Active is an open mission that holds a robot.
func (m *Mission) Active() bool {
	return m.s.Status == Assigned || m.s.Status == PickedUp || m.s.Status == CancelRequested
}

// Unacknowledged: the latest order message went out and no state has shown it yet.
func (m *Mission) Unacknowledged() bool {
	return m.Active() && m.s.Sent && m.s.SeenUpdate < int64(m.s.UpdateID)
}

// ActionID is the VDA 5050 actionId of one of this mission's node actions.
func (m *Mission) ActionID(action string) string { return m.s.ID + "-" + action }

// CancelActionID is the actionId of this mission's cancelOrder.
func (m *Mission) CancelActionID() string { return m.s.ID + "-cancel" }

// Targets are the steps past the base end that carry an action, plus the last step of a route
// whose end carries none (a park): what a re-route must still reach, in order.
func (m *Mission) Targets() []Step {
	var out []Step
	for i := m.s.BaseEnd + 1; i < len(m.s.Route); i++ {
		if st := m.s.Route[i]; st.Action != "" || i == len(m.s.Route)-1 {
			out = append(out, st)
		}
	}
	return out
}
