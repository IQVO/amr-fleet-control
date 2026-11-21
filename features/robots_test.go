package features_test

import (
	"context"
	"fmt"
	"slices"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// bot is a scripted VDA 5050 robot: it reports full states built from what it was sent.
type bot struct {
	w      *world
	serial string
	rep    robot.Report
	route  map[int]vda.Node // route index -> node, from every order received
	acked  int              // number of orders acknowledged
}

func (b *bot) id() robot.ID { return robot.ID{Manufacturer: "EMU", SerialNumber: b.serial} }

func (b *bot) orders() []vda.Order {
	var out []vda.Order
	for _, o := range b.w.down.Orders() {
		if o.Robot == b.id() {
			out = append(out, o)
		}
	}
	return out
}

func (b *bot) send(edit func(*robot.Report)) error {
	edit(&b.rep)
	if n, ok := b.w.m.Node(b.rep.LastNodeID); ok {
		b.rep.X, b.rep.Y = n.X, n.Y
	}
	b.rep.MapID, b.rep.At = b.w.m.MapID(), b.w.clock.Now()
	rep := b.rep
	rep.Errors, rep.Actions = slices.Clone(rep.Errors), slices.Clone(rep.Actions)
	rep.InstantActions, rep.LoadIDs = slices.Clone(rep.InstantActions), slices.Clone(rep.LoadIDs)
	return b.w.svc.HandleState(context.Background(), vda.State{Robot: b.id(), Report: rep})
}

// ack accepts the newest order not yet acknowledged; false when there is none.
func (b *bot) ack() (bool, error) {
	os := b.orders()
	if len(os) == b.acked {
		return false, nil
	}
	o := os[len(os)-1]
	b.acked = len(os)
	for _, n := range o.Nodes {
		b.route[int(n.Seq/2)] = n
	}
	return true, b.send(func(r *robot.Report) {
		if r.OrderID != o.OrderID {
			r.Actions, r.InstantActions, r.Errors = nil, nil, nil
		}
		r.OrderID, r.OrderUpdateID, r.Pending = o.OrderID, o.UpdateID, true
		for _, n := range o.Nodes {
			for _, a := range n.Actions {
				if _, known := r.Action(a.ID); !known {
					r.Actions = append(r.Actions, robot.ActionState{ID: a.ID, Type: a.Type, Status: robot.ActionWaiting})
				}
			}
		}
	})
}

// reach stands on route index i, then finishes its node actions. Standing on the last node of
// the order already empties nodeStates (the emulator's Snapshot lists nodes after the current
// one), so that report is no longer pending.
func (b *bot) reach(i int) error {
	n, ok := b.route[i]
	if !ok {
		return fmt.Errorf("robot %s has no route index %d", b.serial, i)
	}
	_, more := b.route[i+1]
	if err := b.send(func(r *robot.Report) { r.LastNodeID, r.LastNodeSeq, r.Pending = n.ID, vda.NodeSeq(i), more }); err != nil {
		return err
	}
	for _, a := range n.Actions {
		if err := b.finish(a); err != nil {
			return err
		}
	}
	return nil
}

func (b *bot) finish(a vda.Action) error {
	return b.send(func(r *robot.Report) {
		switch a.Type {
		case "pick":
			r.LoadIDs = []string{a.Params[vda.ParamLoadID]}
		case "drop":
			r.LoadIDs = nil
		case "startCharging":
			r.Charging = true
		}
		for k := range r.Actions {
			if r.Actions[k].ID == a.ID {
				r.Actions[k].Status = robot.ActionFinished
			}
		}
	})
}

// released is the highest released route index the robot knows of.
func (b *bot) released() int {
	end := -1
	for i, n := range b.route {
		if n.Released && i > end {
			end = i
		}
	}
	return end
}

// drive acknowledges and walks released nodes until no new order arrives; with stop (an action
// type) it returns right after finishing that action.
func (b *bot) drive(stop string) error {
	at := int(b.rep.LastNodeSeq / 2)
	for guard := 0; guard < 200; guard++ {
		if _, err := b.ack(); err != nil {
			return err
		}
		if at >= b.released() {
			if len(b.orders()) == b.acked {
				return nil
			}
			continue
		}
		at++
		if err := b.reach(at); err != nil {
			return err
		}
		if acts := b.route[at].Actions; stop != "" && len(acts) > 0 && acts[0].Type == stop {
			return nil
		}
		if !b.rep.Pending { // the order ended; a park or charge order may follow from this node
			if len(b.orders()) == b.acked {
				return nil
			}
			at, b.route, b.rep.LastNodeSeq = 0, map[int]vda.Node{}, 0
		}
	}
	return fmt.Errorf("robot %s did not finish its orders", b.serial)
}
