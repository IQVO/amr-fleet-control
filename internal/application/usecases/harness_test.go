package usecases_test

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/memory"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
)

// floor is config/map.example.yaml plus two unreachable nodes: ISLE (PARKING) and ST9 (STATION).
func floor(t *testing.T) *fleetmap.Map {
	t.Helper()
	n := func(id string, x, y float64, k fleetmap.NodeKind) fleetmap.Node {
		h := ""
		if k == fleetmap.Port || k == fleetmap.Station {
			h = id
		}
		return fleetmap.Node{ID: id, X: x, Y: y, Kind: k, HandoffPoint: h}
	}
	e := func(a, b string) fleetmap.Edge { return fleetmap.Edge{ID: a + "-" + b, A: a, B: b} }
	m, err := fleetmap.New(fleetmap.Spec{FleetID: "AMR1", MapID: "S1", Version: "v1",
		Nodes: []fleetmap.Node{n("P1", 0, 0, fleetmap.Parking), n("P2", 2, 0, fleetmap.Parking), n("P3", 4, 0, fleetmap.Parking),
			n("CH1", 6, 0, fleetmap.Charger), n("A", 0, 2, fleetmap.Waypoint), n("B", 2, 2, fleetmap.Waypoint),
			n("C", 4, 2, fleetmap.Waypoint), n("D", 6, 2, fleetmap.Waypoint), n("ASRS1-OUT1", 0, 4, fleetmap.Port),
			n("ASRS1-IN1", 2, 4, fleetmap.Port), n("ASRS1-OUT2", 4, 4, fleetmap.Port), n("ASRS1-IN2", 6, 4, fleetmap.Port),
			n("E", 0, 6, fleetmap.Waypoint), n("F", 2, 6, fleetmap.Waypoint), n("G", 4, 6, fleetmap.Waypoint),
			n("H", 6, 6, fleetmap.Waypoint), n("ST1", 0, 8, fleetmap.Station), n("ST2", 6, 8, fleetmap.Station),
			n("ISLE", 20, 20, fleetmap.Parking), n("ST9", 30, 30, fleetmap.Station)},
		Edges: []fleetmap.Edge{e("P1", "A"), e("P2", "B"), e("P3", "C"), e("CH1", "D"), e("A", "B"), e("B", "C"), e("C", "D"),
			e("A", "ASRS1-OUT1"), e("ASRS1-OUT1", "E"), e("B", "ASRS1-IN1"), e("ASRS1-IN1", "F"), e("C", "ASRS1-OUT2"),
			e("ASRS1-OUT2", "G"), e("D", "ASRS1-IN2"), e("ASRS1-IN2", "H"), e("E", "F"), e("F", "G"), e("G", "H"),
			e("E", "ST1"), e("H", "ST2"), e("ISLE", "ST9")}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type harness struct {
	t        *testing.T
	m        *fleetmap.Map
	svc      *usecases.Service
	clock    *memory.Clock
	down     *memory.Downlink
	events   *memory.Events
	metrics  *memory.Metrics
	fleets   *memory.Fleets
	robots   *memory.Robots
	missions *memory.Missions
	seen     map[string]robot.Report
}

func newHarness(t *testing.T, m *fleetmap.Map, tune func(*usecases.Config)) *harness {
	t.Helper()
	s := memory.NewStore()
	h := &harness{t: t, m: m, clock: memory.NewClock(t0), down: memory.NewDownlink(), events: memory.NewEvents(s),
		metrics: memory.NewMetrics(), fleets: memory.NewFleets(s), robots: memory.NewRobots(s), missions: memory.NewMissions(s),
		seen: map[string]robot.Report{}}
	cfg := usecases.DefaultConfig()
	if tune != nil {
		tune(&cfg)
	}
	svc, err := usecases.NewService(m, cfg, usecases.Deps{UoW: memory.NewUnitOfWork(s), Fleets: h.fleets, Robots: h.robots,
		Missions: h.missions, Sink: h.events, Downlink: h.down, Clock: h.clock, IDs: memory.NewIDs("u"), Metrics: h.metrics,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	h.svc = svc
	return h
}

func rid(serial string) robot.ID { return robot.ID{Manufacturer: "EMU", SerialNumber: serial} }

func (h *harness) must(err error) {
	h.t.Helper()
	if err != nil {
		h.t.Fatal(err)
	}
}

// online connects a robot standing on node with battery percent.
func (h *harness) online(serial, node string, battery float64) {
	h.t.Helper()
	h.must(h.svc.HandleConnection(ctx, vda.Connection{Robot: rid(serial), State: robot.ConnectionOnline, At: h.clock.Now()}))
	h.report(serial, func(r *robot.Report) {
		r.LastNodeID, r.Battery = node, battery
	})
}

// report publishes the robot's previous state changed by edit, at the robot's node position.
func (h *harness) report(serial string, edit func(*robot.Report)) {
	h.t.Helper()
	rep := h.seen[serial]
	rep.Errors = slices.Clone(rep.Errors)
	rep.Actions = slices.Clone(rep.Actions)
	rep.InstantActions = slices.Clone(rep.InstantActions)
	edit(&rep)
	if n, ok := h.m.Node(rep.LastNodeID); ok {
		rep.X, rep.Y = n.X, n.Y
	}
	rep.MapID, rep.At = h.m.MapID(), h.clock.Now().Add(time.Hour)
	h.seen[serial] = rep
	h.must(h.svc.HandleState(ctx, vda.State{Robot: rid(serial), Report: rep}))
}

// ack reports the robot's latest order as accepted and pending.
func (h *harness) ack(serial string) vda.Order {
	h.t.Helper()
	o := h.lastOrder(serial)
	h.report(serial, func(r *robot.Report) {
		if r.OrderID != o.OrderID {
			r.Actions, r.InstantActions, r.Errors = nil, nil, nil
		}
		r.OrderID, r.OrderUpdateID, r.Pending = o.OrderID, o.UpdateID, true
		for _, n := range o.Nodes {
			for _, a := range n.Actions {
				setAction(r, robot.ActionState{ID: a.ID, Type: a.Type, Status: robot.ActionWaiting}, false)
			}
		}
	})
	return o
}

// reach reports the robot standing on route index i of its order.
func (h *harness) reach(serial string, i int) {
	h.t.Helper()
	node := h.nodeAt(serial, i)
	h.report(serial, func(r *robot.Report) {
		r.LastNodeID, r.LastNodeSeq = node, vda.NodeSeq(i)
	})
}

// drive acknowledges and advances the robot one node at a time up to route index to.
func (h *harness) drive(serial string, from, to int) {
	h.t.Helper()
	for i := from; i <= to; i++ {
		h.ack(serial)
		h.reach(serial, i)
	}
}

// action reports one node action's status (an instant action when instant).
func (h *harness) action(serial, id, status string, instant bool) {
	h.t.Helper()
	h.report(serial, func(r *robot.Report) {
		setAction(r, robot.ActionState{ID: id, Status: status}, instant)
	})
}

func setAction(r *robot.Report, a robot.ActionState, instant bool) {
	list := &r.Actions
	if instant {
		list = &r.InstantActions
	}
	for i := range *list {
		if (*list)[i].ID == a.ID {
			(*list)[i].Status = a.Status
			return
		}
	}
	*list = append(*list, a)
}

func (h *harness) orders(serial string) []vda.Order {
	var out []vda.Order
	for _, o := range h.down.Orders() {
		if o.Robot == rid(serial) {
			out = append(out, o)
		}
	}
	return out
}

func (h *harness) lastOrder(serial string) vda.Order {
	h.t.Helper()
	os := h.orders(serial)
	if len(os) == 0 {
		h.t.Fatalf("no order sent to %s", serial)
	}
	return os[len(os)-1]
}

func (h *harness) nodeAt(serial string, i int) string {
	h.t.Helper()
	node := ""
	for _, o := range h.orders(serial) {
		for _, n := range o.Nodes {
			if n.Seq == vda.NodeSeq(i) {
				node = n.ID
			}
		}
	}
	if node == "" {
		h.t.Fatalf("no order to %s has route index %d", serial, i)
	}
	return node
}

func (h *harness) instants(serial string) []vda.Action {
	var out []vda.Action
	for _, ia := range h.down.Instant() {
		if ia.Robot == rid(serial) {
			out = append(out, ia.Actions...)
		}
	}
	return out
}

func ofType[T any](evs []any) []T {
	var out []T
	for _, e := range evs {
		if v, ok := e.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func nodeIDs(o vda.Order) []string {
	var out []string
	for _, n := range o.Nodes {
		out = append(out, n.ID)
	}
	return out
}

func released(o vda.Order) (nodes, edges int) {
	for _, n := range o.Nodes {
		if n.Released {
			nodes++
		}
	}
	for _, e := range o.Edges {
		if e.Released {
			edges++
		}
	}
	return nodes, edges
}

func (h *harness) leg(container, from, to string) usecases.LegView {
	h.t.Helper()
	v, err := h.svc.RequestLeg(ctx, usecases.LegRequest{FleetID: "AMR1", ContainerID: container, From: from, To: to, Reference: "tl-" + container})
	h.must(err)
	return v
}
