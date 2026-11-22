package usecases_test

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/memory"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// carry (Task 8 review): a failed drop fails the picked-up leg DROP_FAILED with the robot's error,
// the container still on the robot, and cancels its order once.
func TestAFailedDropFailsTheLegWithTheContainerOnTheRobot(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 90)
	v := h.leg("C1", "ST1", "ASRS1-IN1")
	h.drive("R1", 0, 4)
	h.action("R1", v.LegID+"-pick", robot.ActionFinished, false)
	h.drive("R1", 5, 7)
	h.report("R1", func(r *robot.Report) {
		setAction(r, robot.ActionState{ID: v.LegID + "-drop", Status: robot.ActionFailed}, false)
		r.Errors = append(r.Errors, robot.Error{Type: "LOAD_NOT_AVAILABLE", Level: robot.LevelWarning})
	})
	lf := ofType[mission.LegFailed](h.events.All())
	if len(lf) != 1 || lf[0].Reason != mission.ReasonDropFailed || lf[0].RobotError != "LOAD_NOT_AVAILABLE" || !lf[0].ContainerOnRobot {
		t.Fatalf("LegFailed = %+v", lf)
	}
	if ia := h.instants("R1"); len(ia) != 1 || ia[0].Type != vda.CancelOrder || ia[0].ID != v.LegID+"-cancel" {
		t.Fatalf("instant = %+v", ia)
	}
}

// carry (Task 8 review): a failed startCharging ends the CHARGE mission (no leg event) and, once
// its cancelOrder is answered, the robot gets a new charge mission.
func TestAFailedChargeActionEndsTheChargeMissionAndChargesAgain(t *testing.T) {
	h := newHarness(t, floor(t), nil)
	h.online("R1", "P1", 15)
	o := h.lastOrder("R1")
	h.drive("R1", 0, 5)
	h.report("R1", func(r *robot.Report) {
		r.Pending = false
		setAction(r, robot.ActionState{ID: o.OrderID + "-startCharging", Status: robot.ActionFailed}, false)
	})
	if lf := ofType[mission.LegFailed](h.events.All()); len(lf) != 0 {
		t.Fatalf("an internal mission published LegFailed: %+v", lf)
	}
	if m, _ := h.missions.Get(ctx, o.OrderID); m.Status() != mission.Failed || m.Reason() != mission.ReasonRobotFault {
		t.Fatalf("charge mission = %s %s", m.Status(), m.Reason())
	}
	r, _ := h.robots.Get(ctx, rid("R1"))
	if r.MissionID() != "" || r.AwaitingAction() != o.OrderID+"-cancel" {
		t.Fatalf("robot = %+v", r.Snapshot())
	}
	h.action("R1", o.OrderID+"-cancel", robot.ActionFailed, true) // no order left to cancel
	r, _ = h.robots.Get(ctx, rid("R1"))
	if r.MissionID() == "" || r.MissionID() == o.OrderID {
		t.Fatalf("no new charge mission: %+v", r.Snapshot())
	}
	if m, _ := h.missions.Get(ctx, r.MissionID()); m.Kind() != mission.Charge {
		t.Fatalf("new mission = %s", m.Kind())
	}
}

// carry (Task 8 review): Config.Validate refuses every policy value the fleet cannot run with.
func TestConfigValidateRefusesUnusablePolicies(t *testing.T) {
	if err := usecases.DefaultConfig().Validate(); err != nil {
		t.Fatalf("defaults = %v", err)
	}
	for name, tune := range map[string]func(*usecases.Config){
		"ChargeLow 0":              func(c *usecases.Config) { c.ChargeLow = 0 },
		"ChargeLow = ChargeResume": func(c *usecases.Config) { c.ChargeLow = c.ChargeResume },
		"ChargeResume 101":         func(c *usecases.Config) { c.ChargeResume = 101 },
		"ReleaseAhead 0":           func(c *usecases.Config) { c.ReleaseAhead = 0 },
		"ResendLimit 0":            func(c *usecases.Config) { c.ResendLimit = 0 },
		"NodeTolerance 0":          func(c *usecases.Config) { c.NodeTolerance = 0 },
		"StaleAfter 0":             func(c *usecases.Config) { c.StaleAfter = 0 },
		"ResendAfter 0":            func(c *usecases.Config) { c.ResendAfter = 0 },
		"RerouteAfter 0":           func(c *usecases.Config) { c.RerouteAfter = 0 },
		"DeadlockTimeout 0":        func(c *usecases.Config) { c.DeadlockTimeout = 0 },
		"BlockedTimeout 0":         func(c *usecases.Config) { c.BlockedTimeout = 0 },
		"OfflineTimeout -1s":       func(c *usecases.Config) { c.OfflineTimeout = -time.Second },
	} {
		c := usecases.DefaultConfig()
		tune(&c)
		if err := c.Validate(); !errors.Is(err, usecases.ErrInvalidConfig) {
			t.Errorf("%s: Validate = %v", name, err)
		}
	}
	c := usecases.DefaultConfig()
	c.ChargeResume = 100
	if err := c.Validate(); err != nil {
		t.Fatalf("CHARGE_RESUME_PERCENT 100 = %v", err)
	}
}

// carry (Task 8 review): NewService refuses a missing map, an invalid config and every missing
// dependency.
func TestNewServiceRefusesAMissingMapOrDependency(t *testing.T) {
	s := memory.NewStore()
	full := func() usecases.Deps {
		return usecases.Deps{UoW: memory.NewUnitOfWork(s), Fleets: memory.NewFleets(s), Robots: memory.NewRobots(s),
			Missions: memory.NewMissions(s), Sink: memory.NewEvents(s), Downlink: memory.NewDownlink(), Clock: memory.NewClock(t0),
			IDs: memory.NewIDs("u"), Metrics: memory.NewMetrics(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	}
	m := floor(t)
	if _, err := usecases.NewService(m, usecases.DefaultConfig(), full()); err != nil {
		t.Fatalf("complete = %v", err)
	}
	if _, err := usecases.NewService(nil, usecases.DefaultConfig(), full()); !errors.Is(err, usecases.ErrInvalidConfig) {
		t.Fatalf("nil map = %v", err)
	}
	if _, err := usecases.NewService(m, usecases.Config{}, full()); !errors.Is(err, usecases.ErrInvalidConfig) {
		t.Fatalf("zero config = %v", err)
	}
	for name, drop := range map[string]func(*usecases.Deps){
		"UoW": func(d *usecases.Deps) { d.UoW = nil }, "Fleets": func(d *usecases.Deps) { d.Fleets = nil },
		"Robots": func(d *usecases.Deps) { d.Robots = nil }, "Missions": func(d *usecases.Deps) { d.Missions = nil },
		"Sink": func(d *usecases.Deps) { d.Sink = nil }, "Downlink": func(d *usecases.Deps) { d.Downlink = nil },
		"Clock": func(d *usecases.Deps) { d.Clock = nil }, "IDs": func(d *usecases.Deps) { d.IDs = nil },
		"Metrics": func(d *usecases.Deps) { d.Metrics = nil }, "Log": func(d *usecases.Deps) { d.Log = nil },
	} {
		d := full()
		drop(&d)
		if _, err := usecases.NewService(m, usecases.DefaultConfig(), d); !errors.Is(err, usecases.ErrInvalidConfig) {
			t.Errorf("without %s: NewService = %v", name, err)
		}
	}
}
