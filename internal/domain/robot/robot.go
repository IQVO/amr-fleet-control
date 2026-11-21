// Package robot is one AMR of the fleet as fleet control sees it: the registry entry (connection,
// last state, errors, factsheet), its PackML state and its one mission (responsibilities 1 and 4).
package robot

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalidRobot = errors.New("invalid robot")
	ErrBusy         = errors.New("the robot already executes a mission")
)

// ID is a robot's VDA 5050 identity.
type ID struct{ Manufacturer, SerialNumber string }

// Key is "<manufacturer>/<serialNumber>", the CloudEvents subject and the reservation holder.
func (id ID) Key() string { return id.Manufacturer + "/" + id.SerialNumber }

// IsZero reports the empty id.
func (id ID) IsZero() bool { return id.Manufacturer == "" && id.SerialNumber == "" }

// ParseKey reverses Key.
func ParseKey(k string) (ID, error) {
	m, s, ok := strings.Cut(k, "/")
	if !ok || m == "" || s == "" || strings.Contains(s, "/") {
		return ID{}, fmt.Errorf("%w: key %q is not <manufacturer>/<serialNumber>", ErrInvalidRobot, k)
	}
	return ID{Manufacturer: m, SerialNumber: s}, nil
}

// Connection is the VDA 5050 connectionState.
type Connection string

const (
	ConnectionOnline  Connection = "ONLINE"
	ConnectionOffline Connection = "OFFLINE"
	ConnectionBroken  Connection = "CONNECTION_BROKEN"
)

// ParseConnection accepts the three VDA 5050 values.
func ParseConnection(s string) (Connection, error) {
	switch c := Connection(s); c {
	case ConnectionOnline, ConnectionOffline, ConnectionBroken:
		return c, nil
	}
	return "", fmt.Errorf("%w: connectionState %q", ErrInvalidRobot, s)
}

// State is the PackML state of a robot (decision D12).
type State string

const (
	Idle      State = "IDLE"
	Execute   State = "EXECUTE"
	Suspended State = "SUSPENDED"
	Held      State = "HELD"
	Stopped   State = "STOPPED"
)

// RobotStateChanged is raised when the state or the connection changes.
type RobotStateChanged struct {
	Robot         ID
	State         State
	Previous      State
	Connection    Connection
	Battery       float64
	Charging      bool
	ErrorTypes    []string
	At, RobotTime time.Time
}

// Snapshot is the persisted form of a robot.
type Snapshot struct {
	ID                                ID
	Connection                        Connection
	State                             State
	Report                            Report
	HasReport                         bool
	Node, MissionID                   string
	Factsheet                         Factsheet
	HasFactsheet, Stale               bool
	LastSeen, HeldSince, OfflineSince time.Time
	AwaitingAction                    string
	AwaitingSince                     time.Time // carry (Task 10 review): when the awaited action was last sent
	StopChargeSent                    bool
	StopChargeSentAt                  time.Time // carry (Task 11 review): when stopCharging was last sent
}

// Robot is the aggregate.
type Robot struct{ s Snapshot }

// New registers a robot seen for the first time at at: STOPPED until it reports.
func New(id ID, at time.Time) (*Robot, error) {
	if id.Manufacturer == "" || id.SerialNumber == "" || strings.Contains(id.Manufacturer+id.SerialNumber, "/") {
		return nil, fmt.Errorf("%w: %q / %q", ErrInvalidRobot, id.Manufacturer, id.SerialNumber)
	}
	return &Robot{s: Snapshot{ID: id, Connection: ConnectionOffline, State: Stopped, LastSeen: at, OfflineSince: at}}, nil
}

// Rehydrate rebuilds a robot.
func Rehydrate(s Snapshot) *Robot { return &Robot{s: copySnapshot(s)} }

// Snapshot returns a copy of the persisted form.
func (r *Robot) Snapshot() Snapshot { return copySnapshot(r.s) }

func copySnapshot(s Snapshot) Snapshot {
	s.Report = copyReport(s.Report)
	return s
}

func (r *Robot) ID() ID                  { return r.s.ID }
func (r *Robot) State() State            { return r.s.State }
func (r *Robot) Connection() Connection  { return r.s.Connection }
func (r *Robot) Node() string            { return r.s.Node }
func (r *Robot) MissionID() string       { return r.s.MissionID }
func (r *Robot) AwaitingAction() string  { return r.s.AwaitingAction }
func (r *Robot) Battery() float64        { return r.s.Report.Battery }
func (r *Robot) Charging() bool          { return r.s.Report.Charging }
func (r *Robot) HasFactsheet() bool      { return r.s.HasFactsheet }
func (r *Robot) Report() Report          { return copyReport(r.s.Report) }
func (r *Robot) Factsheet() Factsheet    { return r.s.Factsheet }
func (r *Robot) LastSeen() time.Time     { return r.s.LastSeen }
func (r *Robot) HeldSince() time.Time    { return r.s.HeldSince }
func (r *Robot) OfflineSince() time.Time { return r.s.OfflineSince }

// AwaitingSince is when the awaited instant action was last sent (zero when none is awaited).
func (r *Robot) AwaitingSince() time.Time { return r.s.AwaitingSince }

// Online is true for a connected, fresh robot that has reported at least once.
func (r *Robot) Online() bool {
	return r.s.Connection == ConnectionOnline && !r.s.Stale && r.s.HasReport
}

// ApplyConnection records a connection message.
func (r *Robot) ApplyConnection(c Connection, at, robotAt time.Time) []any {
	prev := r.mark()
	r.s.Connection = c
	if c == ConnectionOnline {
		r.s.Stale, r.s.LastSeen = false, at
	}
	return r.settle(prev, at, robotAt)
}

// ApplyReport records a state message; node is the map node the robot stands on ("" off the
// map). A finished or failed awaited instant action clears the wait.
func (r *Robot) ApplyReport(rep Report, node string, at time.Time) []any {
	prev := r.mark()
	r.s.Report, r.s.HasReport, r.s.Node = copyReport(rep), true, node
	r.s.Connection, r.s.LastSeen, r.s.Stale = ConnectionOnline, at, false
	if !rep.Charging {
		r.s.StopChargeSent, r.s.StopChargeSentAt = false, time.Time{}
	}
	if r.s.AwaitingAction != "" {
		if st, ok := rep.Action(r.s.AwaitingAction); ok && (st.Status == ActionFinished || st.Status == ActionFailed) {
			r.s.AwaitingAction, r.s.AwaitingSince = "", time.Time{}
		}
	}
	return r.settle(prev, at, rep.At)
}

// ApplyFactsheet records the robot's factsheet.
func (r *Robot) ApplyFactsheet(f Factsheet) {
	r.s.Factsheet, r.s.HasFactsheet = f, true
}

// MarkStale stops a robot that stopped reporting.
func (r *Robot) MarkStale(at time.Time) []any {
	if r.s.Stale {
		return nil
	}
	prev := r.mark()
	r.s.Stale = true
	return r.settle(prev, at, time.Time{})
}

// Assign gives the robot its one mission (responsibility 1).
func (r *Robot) Assign(missionID string, at time.Time) ([]any, error) {
	if missionID == "" {
		return nil, fmt.Errorf("%w: empty mission id", ErrInvalidRobot)
	}
	if r.s.MissionID != "" {
		return nil, fmt.Errorf("%w: %s holds %s", ErrBusy, r.s.ID.Key(), r.s.MissionID)
	}
	prev := r.mark()
	r.s.MissionID = missionID
	return r.settle(prev, at, time.Time{}), nil
}

// Release ends missionID on this robot; another id changes nothing.
func (r *Robot) Release(missionID string, at time.Time) []any {
	if r.s.MissionID != missionID {
		return nil
	}
	prev := r.mark()
	r.s.MissionID = ""
	return r.settle(prev, at, time.Time{})
}

// AwaitAction marks an instant action (a cancelOrder) whose outcome must be seen first, sent at at;
// an empty id stops awaiting.
//
// carry (Task 10 review): the send time lets the tick re-send a cancel no mission owns.
func (r *Robot) AwaitAction(id string, at time.Time) {
	r.s.AwaitingAction, r.s.AwaitingSince = id, at
	if id == "" {
		r.s.AwaitingSince = time.Time{}
	}
}

// MarkStopChargeSent records that stopCharging went out for the current charge, at no known
// time. carry (Task 11 review): for tests only; production calls MarkStopChargeSentAt with the
// send time. The zero time makes StopChargeOverdue hold at once while the robot still charges.
func (r *Robot) MarkStopChargeSent() { r.MarkStopChargeSentAt(time.Time{}) }

// MarkStopChargeSentAt is MarkStopChargeSent at a send time. carry (Task 11 review): the time
// lets the tick send a lost stopCharging again (StopChargeOverdue).
func (r *Robot) MarkStopChargeSentAt(at time.Time) {
	r.s.StopChargeSent, r.s.StopChargeSentAt = true, at
}

// Assignable: idle, on the map, nothing in flight, battery at or above low (responsibility 4).
func (r *Robot) Assignable(low float64) bool { return r.idleOnMap() && r.s.Report.Battery >= low }

// NeedsCharge: idle, on the map, nothing in flight, battery below low.
func (r *Robot) NeedsCharge(low float64) bool { return r.idleOnMap() && r.s.Report.Battery < low }

func (r *Robot) idleOnMap() bool {
	return r.s.State == Idle && r.s.AwaitingAction == "" && r.s.Node != ""
}

// Charged: charging without a mission, at or above resume, stopCharging not yet sent.
func (r *Robot) Charged(resume float64) bool { return r.charging(resume) && !r.s.StopChargeSent }

// StopChargeOverdue: still charging at or above resume, its stopCharging sent at least after
// ago and unanswered. carry (Task 11 review): a lost one would strand the robot SUSPENDED.
func (r *Robot) StopChargeOverdue(resume float64, at time.Time, after time.Duration) bool {
	return r.charging(resume) && r.s.StopChargeSent && at.Sub(r.s.StopChargeSentAt) >= after
}

func (r *Robot) charging(resume float64) bool {
	return r.s.State == Suspended && r.s.Report.Charging && r.s.MissionID == "" && r.s.Report.Battery >= resume
}

type mark struct {
	state State
	conn  Connection
}

func (r *Robot) mark() mark { return mark{state: r.s.State, conn: r.s.Connection} }

func (r *Robot) derive() State {
	switch _, blocking := r.s.Report.Blocking(); {
	case r.s.Connection != ConnectionOnline || r.s.Stale || !r.s.HasReport:
		return Stopped
	case blocking:
		return Held
	case r.s.Report.Charging:
		return Suspended
	case r.s.MissionID != "":
		return Execute
	default:
		return Idle
	}
}

// settle derives the state, keeps HeldSince/OfflineSince and raises RobotStateChanged when the
// state or the connection changed.
func (r *Robot) settle(prev mark, at, robotAt time.Time) []any {
	next := r.derive()
	r.s.State = next
	r.s.HeldSince = since(next == Held, prev.state == Held, r.s.HeldSince, at)
	r.s.OfflineSince = since(next == Stopped, prev.state == Stopped, r.s.OfflineSince, at)
	if next == prev.state && r.s.Connection == prev.conn {
		return nil
	}
	return []any{RobotStateChanged{Robot: r.s.ID, State: next, Previous: prev.state, Connection: r.s.Connection,
		Battery: r.s.Report.Battery, Charging: r.s.Report.Charging, ErrorTypes: r.s.Report.ErrorTypes(), At: at, RobotTime: robotAt}}
}

func since(now, before bool, kept, at time.Time) time.Time {
	switch {
	case !now:
		return time.Time{}
	case before && !kept.IsZero():
		return kept
	default:
		return at
	}
}
