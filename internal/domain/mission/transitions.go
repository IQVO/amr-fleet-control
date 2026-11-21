package mission

import (
	"fmt"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

func (m *Mission) refuse(op string) error {
	return fmt.Errorf("%w: %s on a %s %s mission", ErrInvalidTransition, op, m.s.Status, m.s.Kind)
}

// Assign gives a queued leg its robot and route; baseEnd is the last released step.
func (m *Mission) Assign(r robot.ID, route []Step, baseEnd int, at time.Time) ([]any, error) {
	if m.s.Kind != Leg || m.s.Status != Queued {
		return nil, m.refuse("Assign")
	}
	if r.IsZero() {
		return nil, fmt.Errorf("%w: no robot", ErrInvalidMission)
	}
	if err := m.setRoute(route, baseEnd); err != nil {
		return nil, err
	}
	m.s.Robot, m.s.Status, m.s.AssignedAt = r, Assigned, at
	return []any{LegAssigned{LegID: m.s.ID, ContainerID: m.s.ContainerID, From: m.s.From, To: m.s.To,
		Reference: m.s.Reference, Robot: r, MapVersion: m.s.MapVersion, At: at}}, nil
}

// MarkSent records that the current order message went out.
func (m *Mission) MarkSent(at time.Time) { m.s.Sent, m.s.SentAt = true, at }

// Resent records one more resend of the current order message with the same orderUpdateId.
func (m *Mission) Resent(at time.Time) {
	m.s.Resends++
	m.s.SentAt = at
}

// Seen records an orderUpdateId a robot state showed; only the highest counts.
func (m *Mission) Seen(updateID uint32) {
	if int64(updateID) > m.s.SeenUpdate {
		m.s.SeenUpdate = int64(updateID)
	}
}

// Extend releases the route up to newEnd: the next order update (responsibility 3).
func (m *Mission) Extend(newEnd int) error {
	// carry (Task 5 review): like Reroute, Extend refuses a CANCEL_REQUESTED leg, so no base is
	// released to a robot that was asked to cancel (usecases.extendAll also skips it).
	if m.s.Status != Assigned && m.s.Status != PickedUp {
		return m.refuse("Extend")
	}
	if newEnd <= m.s.BaseEnd || newEnd >= len(m.s.Route) {
		return fmt.Errorf("%w: base end %d -> %d of %d steps", ErrInvalidMission, m.s.BaseEnd, newEnd, len(m.s.Route))
	}
	m.s.OrderStart, m.s.BaseEnd = m.s.BaseEnd, newEnd
	m.s.UpdateID++
	m.s.Sent, m.s.Resends = false, 0
	return nil
}

// Reroute replaces the horizon with tail, which starts at the base end's node; the released base
// never changes.
func (m *Mission) Reroute(tail []Step) error {
	if m.s.Status != Assigned && m.s.Status != PickedUp {
		return m.refuse("Reroute")
	}
	if len(tail) == 0 || tail[0].Node != m.s.Route[m.s.BaseEnd].Node || tail[0].Edge != "" {
		return fmt.Errorf("%w: a re-route starts at the base end %s", ErrInvalidMission, m.s.Route[m.s.BaseEnd].Node)
	}
	route := append(append([]Step{}, m.s.Route[:m.s.BaseEnd]...), tail...)
	route[m.s.BaseEnd] = m.s.Route[m.s.BaseEnd]
	if err := validRoute(m.s.Kind, route, m.s.PickupNode, m.s.DropNode); err != nil {
		return err
	}
	m.s.Route, m.s.OrderStart = route, m.s.BaseEnd
	m.s.UpdateID++
	m.s.Sent, m.s.Resends = false, 0
	return nil
}

// Progress records the robot reaching route index (from lastNodeSequenceId).
func (m *Mission) Progress(index int) error {
	if !m.Active() {
		return m.refuse("Progress")
	}
	if index < 0 || index > m.s.BaseEnd {
		return fmt.Errorf("%w: index %d outside the base 0..%d", ErrInvalidMission, index, m.s.BaseEnd)
	}
	if index > m.s.Reached {
		m.s.Reached = index
	}
	return nil
}

// PickUp records the leg's pick action FINISHED.
func (m *Mission) PickUp(at, robotAt time.Time) ([]any, error) {
	if m.s.Kind != Leg || m.s.Status != Assigned {
		return nil, m.refuse("PickUp")
	}
	m.s.Status, m.s.PickedUpAt = PickedUp, at
	return []any{ContainerPickedUp{LegID: m.s.ID, ContainerID: m.s.ContainerID, From: m.s.From, Reference: m.s.Reference,
		Robot: m.s.Robot, At: at, RobotTime: robotAt}}, nil
}

// Complete ends a picked-up leg (drop FINISHED) or an internal mission.
func (m *Mission) Complete(at, robotAt time.Time) ([]any, error) {
	switch {
	case m.s.Kind == Leg && m.s.Status == PickedUp:
		m.end(Done, at)
		return []any{LegCompleted{LegID: m.s.ID, ContainerID: m.s.ContainerID, To: m.s.To, Reference: m.s.Reference,
			Robot: m.s.Robot, At: at, RobotTime: robotAt}}, nil
	case m.s.Kind != Leg && m.s.Status == Assigned:
		m.end(Done, at)
		return nil, nil
	}
	return nil, m.refuse("Complete")
}

// Fail ends any open mission with reason; a leg raises LegFailed.
func (m *Mission) Fail(reason Reason, robotError string, at time.Time) ([]any, error) {
	if !m.Open() {
		return nil, fmt.Errorf("%w: %s is %s", ErrLegEnded, m.s.ID, m.s.Status)
	}
	if reason == "" {
		return nil, fmt.Errorf("%w: a failure needs a reason", ErrInvalidMission)
	}
	onRobot := m.s.Status == PickedUp
	m.s.Reason, m.s.RobotError = reason, robotError
	m.end(Failed, at)
	if m.s.Kind != Leg {
		return nil, nil
	}
	return []any{m.failed(onRobot, at)}, nil
}

// RequestCancel cancels a queued leg at once, asks the robot for an assigned one, and refuses a
// picked-up or ended one.
func (m *Mission) RequestCancel(at time.Time) (bool, []any, error) {
	if m.s.Kind != Leg {
		return false, nil, m.refuse("RequestCancel")
	}
	switch m.s.Status {
	case Queued:
		m.s.Reason = ReasonCancelled
		m.end(Cancelled, at)
		return true, []any{m.failed(false, at)}, nil
	case Assigned:
		// carry (Task 8 review): from here SentAt and Resends count the cancelOrder messages (the
		// order is no longer resent), so an unanswered cancel is re-sent and bounded like an order.
		m.s.Status = CancelRequested
		m.s.SentAt, m.s.Resends = at, 0
		return false, nil, nil
	case CancelRequested:
		return false, nil, nil
	case PickedUp:
		return false, nil, fmt.Errorf("%w: %s", ErrNotCancellable, m.s.ID)
	default:
		return false, nil, fmt.Errorf("%w: %s is %s", ErrLegEnded, m.s.ID, m.s.Status)
	}
}

// ConfirmCancel ends a cancel-requested leg once the robot answered the cancelOrder; onRobot says
// whether the robot reports the container as its load.
func (m *Mission) ConfirmCancel(onRobot bool, at time.Time) ([]any, error) {
	if m.s.Status != CancelRequested {
		return nil, m.refuse("ConfirmCancel")
	}
	m.s.Reason = ReasonCancelled
	m.end(Cancelled, at)
	return []any{m.failed(onRobot, at)}, nil
}

func (m *Mission) end(s Status, at time.Time) { m.s.Status, m.s.EndedAt = s, at }

func (m *Mission) failed(onRobot bool, at time.Time) LegFailed {
	return LegFailed{LegID: m.s.ID, ContainerID: m.s.ContainerID, From: m.s.From, To: m.s.To, Reference: m.s.Reference,
		Robot: m.s.Robot, Reason: m.s.Reason, RobotError: m.s.RobotError, ContainerOnRobot: onRobot, At: at}
}
