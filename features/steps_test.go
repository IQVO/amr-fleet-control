package features_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/memory"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/config"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

type world struct {
	m        *fleetmap.Map
	svc      *usecases.Service
	clock    *memory.Clock
	down     *memory.Downlink
	events   *memory.Events
	fleets   *memory.Fleets
	missions *memory.Missions
	bots     map[string]*bot
	legs     map[string]string // container -> leg id
}

func (w *world) start(m *fleetmap.Map, ahead int) error {
	s := memory.NewStore()
	w.m, w.clock, w.down, w.events = m, memory.NewClock(t0), memory.NewDownlink(), memory.NewEvents(s)
	w.fleets, w.missions, w.bots, w.legs = memory.NewFleets(s), memory.NewMissions(s), map[string]*bot{}, map[string]string{}
	cfg := usecases.DefaultConfig()
	cfg.ReleaseAhead = ahead
	svc, err := usecases.NewService(m, cfg, usecases.Deps{UoW: memory.NewUnitOfWork(s), Fleets: w.fleets,
		Robots: memory.NewRobots(s), Missions: w.missions, Sink: w.events, Downlink: w.down, Clock: w.clock,
		IDs: memory.NewIDs("u"), Metrics: memory.NewMetrics(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	w.svc = svc
	return err
}

func (w *world) exampleFloor() error {
	m, err := config.LoadMap("../config/map.example.yaml")
	if err != nil {
		return err
	}
	return w.start(m, 2)
}

func (w *world) corridor(bypass string) error {
	nodes := []fleetmap.Node{{ID: "S1", Kind: fleetmap.Station, HandoffPoint: "S1"}, {ID: "M1", X: 2, Kind: fleetmap.Waypoint},
		{ID: "M2", X: 4, Kind: fleetmap.Waypoint}, {ID: "S2", X: 6, Kind: fleetmap.Station, HandoffPoint: "S2"}}
	edges := []fleetmap.Edge{{ID: "S1-M1", A: "S1", B: "M1"}, {ID: "M1-M2", A: "M1", B: "M2"}, {ID: "M2-S2", A: "M2", B: "S2"}}
	if bypass == "with" {
		nodes = append(nodes, fleetmap.Node{ID: "B1", X: 2, Y: 3, Kind: fleetmap.Waypoint}, fleetmap.Node{ID: "B2", X: 4, Y: 3, Kind: fleetmap.Waypoint})
		edges = append(edges, fleetmap.Edge{ID: "S1-B1", A: "S1", B: "B1"}, fleetmap.Edge{ID: "B1-B2", A: "B1", B: "B2"},
			fleetmap.Edge{ID: "B2-S2", A: "B2", B: "S2"}, fleetmap.Edge{ID: "M1-B1", A: "M1", B: "B1"}, fleetmap.Edge{ID: "M2-B2", A: "M2", B: "B2"})
	}
	m, err := fleetmap.New(fleetmap.Spec{FleetID: "AMR1", MapID: "S1", Version: "v1", Nodes: nodes, Edges: edges})
	if err != nil {
		return err
	}
	return w.start(m, 1)
}

func (w *world) online(serial, node string, battery int) error {
	b := &bot{w: w, serial: serial, route: map[int]vda.Node{}}
	w.bots[serial] = b
	if err := w.svc.HandleConnection(context.Background(), vda.Connection{Robot: b.id(), State: robot.ConnectionOnline, At: t0}); err != nil {
		return err
	}
	return b.send(func(r *robot.Report) { r.LastNodeID, r.Battery = node, float64(battery) })
}

func (w *world) request(container, from, to string) error {
	v, err := w.svc.RequestLeg(context.Background(), usecases.LegRequest{FleetID: "AMR1", ContainerID: container, From: from, To: to})
	w.legs[container] = v.LegID
	return err
}

func (w *world) leg(container string) (*mission.Mission, error) {
	return w.missions.Get(context.Background(), w.legs[container])
}

func (w *world) legIs(container, status string) error {
	m, err := w.leg(container)
	if err != nil {
		return err
	}
	if string(m.Status()) != status {
		return fmt.Errorf("leg of %s is %s, want %s", container, m.Status(), status)
	}
	return nil
}

func (w *world) assignedTo(container, serial string) error {
	m, err := w.leg(container)
	if err != nil {
		return err
	}
	if m.Robot().SerialNumber != serial || m.Status() != mission.Assigned {
		return fmt.Errorf("leg of %s: %s on %q", container, m.Status(), m.Robot().SerialNumber)
	}
	return nil
}

func (w *world) failedWith(container, reason string) error {
	m, err := w.leg(container)
	if err != nil {
		return err
	}
	if !m.Open() && string(m.Reason()) == reason {
		return nil
	}
	return fmt.Errorf("leg of %s: %s %s, want failed %s", container, m.Status(), m.Reason(), reason)
}

func (w *world) published(n int, name, container string) error {
	got := 0
	for _, e := range w.events.All() {
		var c string
		switch ev := e.(type) {
		case mission.LegAssigned:
			c = ev.ContainerID
		case mission.ContainerPickedUp:
			c = ev.ContainerID
		case mission.LegCompleted:
			c = ev.ContainerID
		case mission.LegFailed:
			c = ev.ContainerID
		}
		if c == container && fmt.Sprintf("%T", e) == "mission."+name {
			got++
		}
	}
	if got != n {
		return fmt.Errorf("%d %s for %s, want %d", got, name, container, n)
	}
	return nil
}

func (w *world) deadlocks(n int) error {
	got := 0
	for _, e := range w.events.All() {
		if _, ok := e.(fleetmap.DeadlockDetected); ok {
			got++
		}
	}
	if got != n {
		return fmt.Errorf("%d deadlocks, want %d", got, n)
	}
	return nil
}

func (w *world) sentTo(serial, node string) error {
	os := w.bots[serial].orders()
	if len(os) == 0 {
		return fmt.Errorf("no order to %s", serial)
	}
	last := os[len(os)-1].Nodes
	if got := last[len(last)-1].ID; got != node {
		return fmt.Errorf("%s was sent to %s, want %s", serial, got, node)
	}
	return nil
}

func (w *world) sentThrough(serial, node, via string) error {
	if err := w.sentTo(serial, node); err != nil {
		return err
	}
	os := w.bots[serial].orders()
	for _, n := range os[len(os)-1].Nodes {
		if n.ID == via {
			return nil
		}
	}
	return fmt.Errorf("%s's last order does not pass %s", serial, via)
}

func (w *world) stands(serial, node string) error {
	if got := w.bots[serial].rep.LastNodeID; got != node {
		return fmt.Errorf("%s stands on %s, want %s", serial, got, node)
	}
	return nil
}

func (w *world) waits(serial, node, blocker string) error {
	rec, _, _ := w.fleets.Get(context.Background(), "AMR1")
	wt, ok := rec.Reservations.Waits["EMU/"+serial]
	if !ok || wt.Node != node || wt.Blocker != "EMU/"+blocker {
		return fmt.Errorf("waits = %+v", rec.Reservations.Waits)
	}
	return nil
}

func (w *world) notWaiting(serial string) error {
	rec, _, _ := w.fleets.Get(context.Background(), "AMR1")
	if _, ok := rec.Reservations.Waits["EMU/"+serial]; ok {
		return fmt.Errorf("%s still waits: %+v", serial, rec.Reservations.Waits)
	}
	return nil
}

func (w *world) charge(serial string, to int) error {
	b := w.bots[serial]
	if err := b.send(func(r *robot.Report) { r.Battery = float64(to) }); err != nil {
		return err
	}
	return b.send(func(r *robot.Report) { r.Charging = false })
}

func (w *world) reject(serial, errType string) error {
	return w.bots[serial].send(func(r *robot.Report) {
		r.Errors = append(r.Errors, robot.Error{Type: errType, Level: robot.LevelWarning})
	})
}

func (w *world) pass(seconds int) error {
	w.clock.Advance(time.Duration(seconds) * time.Second)
	return w.svc.Tick(context.Background())
}

func (w *world) cancel(container string) error {
	_, _, err := w.svc.CancelLeg(context.Background(), "AMR1", w.legs[container])
	return err
}

func (w *world) cancelRefused(container, code string) error {
	err := w.cancel(container)
	codes := map[string]error{"leg-not-cancellable": usecases.ErrNotCancellable, "leg-ended": usecases.ErrLegEnded}
	if !errors.Is(err, codes[code]) {
		return fmt.Errorf("cancel = %v, want %s", err, code)
	}
	return nil
}

func initializeScenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Step(`^the example floor$`, w.exampleFloor)
	sc.Step(`^the corridor floor (with|without) a bypass$`, w.corridor)
	sc.Step(`^robot "(\w+)" is online at "([^"]+)" with (\d+)% battery$`, w.online)
	sc.Step(`^container "(\w+)" is requested from "([^"]+)" to "([^"]+)"$`, w.request)
	sc.Step(`^the leg of "(\w+)" is assigned to "(\w+)"$`, w.assignedTo)
	sc.Step(`^the leg of "(\w+)" is "([A-Z_]+)"$`, w.legIs)
	sc.Step(`^the leg of "(\w+)" failed with "([A-Z_]+)"$`, w.failedWith)
	sc.Step(`^the leg of "(\w+)" is cancelled$`, w.cancel)
	sc.Step(`^cancelling the leg of "(\w+)" is refused with "([a-z-]+)"$`, w.cancelRefused)
	sc.Step(`^(\d+) "(\w+)" events? (?:was|were) published for "(\w+)"$`, w.published)
	sc.Step(`^(\d+) deadlocks? (?:was|were) published$`, w.deadlocks)
	sc.Step(`^robot "(\w+)" was sent to "([^"]+)"$`, w.sentTo)
	sc.Step(`^robot "(\w+)" was sent to "([^"]+)" through "([^"]+)"$`, w.sentThrough)
	sc.Step(`^robot "(\w+)" stands on "([^"]+)"$`, w.stands)
	sc.Step(`^robot "(\w+)" waits for "([^"]+)" held by "(\w+)"$`, w.waits)
	sc.Step(`^robot "(\w+)" is no longer waiting$`, w.notWaiting)
	sc.Step(`^robot "(\w+)" acknowledges its order$`, func(s string) error { _, err := w.bots[s].ack(); return err })
	sc.Step(`^robot "(\w+)" reaches route index (\d+)$`, func(s string, i int) error { return w.bots[s].reach(i) })
	sc.Step(`^robot "(\w+)" drives its orders? to the end$`, func(s string) error { return w.bots[s].drive("") })
	sc.Step(`^robot "(\w+)" drives its order up to the pickup$`, func(s string) error { return w.bots[s].drive("pick") })
	sc.Step(`^robot "(\w+)" charges to (\d+)%$`, w.charge)
	sc.Step(`^robot "(\w+)" rejects its order with "([A-Z_]+)"$`, w.reject)
	sc.Step(`^(\d+) seconds pass$`, w.pass)
}
