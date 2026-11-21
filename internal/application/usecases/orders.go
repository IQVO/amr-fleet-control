package usecases

import (
	"context"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/dispatch"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// order queues the mission's current order message and records it as sent.
func (u *unit) order(ms *mission.Mission) {
	o := buildOrder(u.s.m, ms)
	ms.MarkSent(u.now)
	u.touchM(ms)
	u.sends = append(u.sends, func(ctx context.Context) error { return u.s.d.Downlink.SendOrder(ctx, o) })
}

// resend queues the current order message again with the same orderUpdateId.
//
//nolint:unused // the Tick (Task 9) resends unacknowledged orders; Task 9 drops this directive.
func (u *unit) resend(ms *mission.Mission) {
	o := buildOrder(u.s.m, ms)
	ms.Resent(u.now)
	u.touchM(ms)
	u.sends = append(u.sends, func(ctx context.Context) error { return u.s.d.Downlink.SendOrder(ctx, o) })
}

func (u *unit) instant(id robot.ID, actions ...vda.Action) {
	msg := vda.InstantActions{Robot: id, Actions: actions}
	u.sends = append(u.sends, func(ctx context.Context) error { return u.s.d.Downlink.SendInstantActions(ctx, msg) })
}

// cancelOrder sends cancelOrder and keeps the robot out of assignment until it is answered.
func (u *unit) cancelOrder(r *robot.Robot, actionID string) {
	u.instant(r.ID(), vda.Action{Type: vda.CancelOrder, ID: actionID, Blocking: vda.BlockingHard})
	r.AwaitAction(actionID)
	u.touchR(r)
}

// buildOrder is the order message from the mission's OrderStart: released up to the base end,
// horizon after it; node i has sequenceId 2i, the edge into it 2i-1 (decision D8).
func buildOrder(m *fleetmap.Map, ms *mission.Mission) vda.Order {
	route := ms.Route()
	o := vda.Order{Robot: ms.Robot(), OrderID: ms.ID(), UpdateID: ms.UpdateID(), Nodes: []vda.Node{}, Edges: []vda.Edge{}}
	for i := ms.OrderStart(); i < len(route); i++ {
		st := route[i]
		n, _ := m.Node(st.Node)
		node := vda.Node{ID: st.Node, Seq: vda.NodeSeq(i), Released: i <= ms.BaseEnd(), X: n.X, Y: n.Y, MapID: m.MapID(), Actions: []vda.Action{}}
		if st.Action != "" {
			node.Actions = append(node.Actions, actionFor(ms, st.Action))
		}
		o.Nodes = append(o.Nodes, node)
		if i > ms.OrderStart() {
			e, _ := m.Edge(st.Edge)
			o.Edges = append(o.Edges, vda.Edge{ID: st.Edge, Seq: vda.EdgeSeq(i), Released: i <= ms.BaseEnd(),
				Start: route[i-1].Node, End: st.Node, MaxSpeed: e.MaxSpeed})
		}
	}
	return o
}

func actionFor(ms *mission.Mission, action string) vda.Action {
	a := vda.Action{Type: action, ID: ms.ActionID(action), Blocking: vda.BlockingHard}
	if action == mission.ActionPick {
		a.Params = map[string]string{vda.ParamLoadID: ms.ContainerID()}
	}
	return a
}

// held lists what a robot still needs of its base: nodes reached..baseEnd and the edges between them.
func held(route []mission.Step, reached, baseEnd int) (nodes, edges []string) {
	for i := reached; i <= baseEnd; i++ {
		nodes = append(nodes, route[i].Node)
		if i > reached {
			edges = append(edges, route[i].Edge)
		}
	}
	return nodes, edges
}

// stretch lists a new part of a base: nodes from..to and the edge into each of them (from >= 1).
func stretch(route []mission.Step, from, to int) (nodes, edges []string) {
	for i := from; i <= to; i++ {
		nodes = append(nodes, route[i].Node)
		edges = append(edges, route[i].Edge)
	}
	return nodes, edges
}

func (u *unit) window(route []mission.Step, baseEnd, reached int, key string) (int, string, string) {
	return dispatch.Window(route, baseEnd, reached, u.s.cfg.ReleaseAhead, key, u.fm.NodeHolder, u.fm.EdgeHolder)
}
