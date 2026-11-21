package robot_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

var (
	t0  = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	rid = robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}
)

func idleReport(battery float64) robot.Report {
	return robot.Report{At: t0.Add(time.Hour), LastNodeID: "P1", MapID: "S1", Battery: battery}
}

func online(t *testing.T) *robot.Robot {
	t.Helper()
	r, err := robot.New(rid, t0)
	if err != nil {
		t.Fatal(err)
	}
	r.ApplyConnection(robot.ConnectionOnline, t0, t0)
	r.ApplyReport(idleReport(90), "P1", t0)
	return r
}

func changed(t *testing.T, evs []any) robot.RobotStateChanged {
	t.Helper()
	if len(evs) != 1 {
		t.Fatalf("events = %+v, want one RobotStateChanged", evs)
	}
	e, ok := evs[0].(robot.RobotStateChanged)
	if !ok {
		t.Fatalf("event %T", evs[0])
	}
	return e
}

// (The body of the brief's test is split into checkConnectionThenReportEvents to stay under this
// repo's cyclop limit of 15; the assertions are the brief's, unchanged.)
func TestANewRobotIsStoppedUntilItIsOnlineAndReports(t *testing.T) {
	if _, err := robot.New(robot.ID{SerialNumber: "R1"}, t0); !errors.Is(err, robot.ErrInvalidRobot) {
		t.Fatalf("New without manufacturer = %v", err)
	}
	if _, err := robot.New(robot.ID{Manufacturer: "EMU"}, t0); !errors.Is(err, robot.ErrInvalidRobot) {
		t.Fatalf("New without serial = %v", err)
	}
	if _, err := robot.New(robot.ID{Manufacturer: "E/MU", SerialNumber: "R1"}, t0); !errors.Is(err, robot.ErrInvalidRobot) {
		t.Fatalf("New with a slash = %v", err)
	}
	r, _ := robot.New(rid, t0)
	if r.State() != robot.Stopped || r.Connection() != robot.ConnectionOffline || r.Online() || !r.OfflineSince().Equal(t0) {
		t.Fatalf("new robot = %+v", r.Snapshot())
	}
	checkConnectionThenReportEvents(t, r)
	if r.Node() != "P1" || !r.Online() || !r.LastSeen().Equal(t0.Add(time.Second)) || !r.OfflineSince().IsZero() {
		t.Fatalf("robot = %+v", r.Snapshot())
	}
}

func checkConnectionThenReportEvents(t *testing.T, r *robot.Robot) {
	t.Helper()
	e := changed(t, r.ApplyConnection(robot.ConnectionOnline, t0, t0.Add(time.Hour)))
	if e.State != robot.Stopped || e.Previous != robot.Stopped || e.Connection != robot.ConnectionOnline || !e.RobotTime.Equal(t0.Add(time.Hour)) {
		t.Fatalf("connection event = %+v", e)
	}
	e = changed(t, r.ApplyReport(idleReport(90), "P1", t0.Add(time.Second)))
	if e.State != robot.Idle || e.Previous != robot.Stopped || e.Battery != 90 || e.Robot != rid || !e.At.Equal(t0.Add(time.Second)) ||
		!e.RobotTime.Equal(t0.Add(time.Hour)) || e.ErrorTypes == nil {
		t.Fatalf("report event = %+v", e)
	}
}

func TestAStateMessageMeansTheRobotIsConnected(t *testing.T) {
	r, _ := robot.New(rid, t0)
	e := changed(t, r.ApplyReport(idleReport(50), "P1", t0))
	if e.State != robot.Idle || e.Connection != robot.ConnectionOnline {
		t.Fatalf("event = %+v", e)
	}
}

func TestABatteryTickChangesNothingPublished(t *testing.T) {
	r := online(t)
	if evs := r.ApplyReport(idleReport(89.5), "P1", t0.Add(time.Second)); len(evs) != 0 {
		t.Fatalf("a battery tick published %+v", evs)
	}
	if r.Battery() != 89.5 {
		t.Fatalf("battery = %v", r.Battery())
	}
}

func TestBlockingErrorsHoldTheRobotAndRecordWhen(t *testing.T) {
	r := online(t)
	rep := idleReport(90)
	rep.Errors = []robot.Error{{Type: robot.ErrTypeBlocked, Level: robot.LevelCritical}}
	e := changed(t, r.ApplyReport(rep, "P1", t0.Add(time.Minute)))
	if e.State != robot.Held || e.Previous != robot.Idle || !reflect.DeepEqual(e.ErrorTypes, []string{"ROBOT_BLOCKED"}) {
		t.Fatalf("event = %+v", e)
	}
	if !r.HeldSince().Equal(t0.Add(time.Minute)) {
		t.Fatalf("HeldSince = %v", r.HeldSince())
	}
	r.ApplyReport(rep, "P1", t0.Add(2*time.Minute))
	if !r.HeldSince().Equal(t0.Add(time.Minute)) {
		t.Fatal("a second HELD report moved HeldSince")
	}
	changed(t, r.ApplyReport(idleReport(90), "P1", t0.Add(3*time.Minute)))
	if r.State() != robot.Idle || !r.HeldSince().IsZero() {
		t.Fatalf("after the error cleared = %+v", r.Snapshot())
	}
	warn := idleReport(90)
	warn.Errors = []robot.Error{{Type: robot.ErrTypeInvalidOrder, Level: robot.LevelWarning}}
	if evs := r.ApplyReport(warn, "P1", t0.Add(4*time.Minute)); len(evs) != 0 || r.State() != robot.Idle {
		t.Fatalf("a WARNING changed the state: %+v", evs)
	}
}

func TestChargingSuspendsAndAMissionExecutes(t *testing.T) {
	r := online(t)
	evs, err := r.Assign("leg-1", t0)
	if err != nil || changed(t, evs).State != robot.Execute || r.MissionID() != "leg-1" {
		t.Fatalf("Assign = %+v %v", evs, err)
	}
	if _, err := r.Assign("leg-2", t0); !errors.Is(err, robot.ErrBusy) {
		t.Fatalf("a second mission = %v (responsibility 1)", err)
	}
	if _, err := r.Assign("", t0); !errors.Is(err, robot.ErrInvalidRobot) {
		t.Fatalf("an empty mission id = %v", err)
	}
	if evs := r.Release("leg-9", t0); len(evs) != 0 || r.MissionID() != "leg-1" {
		t.Fatal("releasing another mission released this one")
	}
	if changed(t, r.Release("leg-1", t0)).State != robot.Idle {
		t.Fatal("release did not idle the robot")
	}
	rep := idleReport(15)
	rep.Charging = true
	if e := changed(t, r.ApplyReport(rep, "CH1", t0)); e.State != robot.Suspended || !e.Charging {
		t.Fatalf("charging = %+v", e)
	}
}

// (The body of the brief's test is split into checkAwaitedActionOutcomes to stay under this
// repo's cyclop limit of 15; the assertions are the brief's, unchanged.)
func TestAssignabilityFollowsTheChargeThreshold(t *testing.T) {
	r := online(t)
	r.ApplyReport(idleReport(20), "P1", t0)
	if !r.Assignable(20) || r.NeedsCharge(20) {
		t.Fatal("a robot exactly at the threshold is assignable")
	}
	r.ApplyReport(idleReport(19.9), "P1", t0)
	if r.Assignable(20) || !r.NeedsCharge(20) {
		t.Fatal("a robot below the threshold is never assignable (responsibility 4)")
	}
	r.ApplyReport(idleReport(90), "", t0)
	if r.Assignable(20) || r.NeedsCharge(20) {
		t.Fatal("a robot off the map is neither assignable nor sent to charge")
	}
	r.ApplyReport(idleReport(90), "P1", t0)
	r.AwaitAction("leg-1-cancel")
	if r.Assignable(20) || r.NeedsCharge(20) || r.AwaitingAction() != "leg-1-cancel" {
		t.Fatal("a robot with a cancel in flight is not offered an order")
	}
	checkAwaitedActionOutcomes(t, r)
	_, _ = r.Assign("leg-2", t0)
	if r.Assignable(20) || r.NeedsCharge(20) {
		t.Fatal("a robot with a mission is not assignable")
	}
}

func checkAwaitedActionOutcomes(t *testing.T, r *robot.Robot) {
	t.Helper()
	rep := idleReport(90)
	rep.InstantActions = []robot.ActionState{{ID: "leg-1-cancel", Status: robot.ActionRunning}}
	r.ApplyReport(rep, "P1", t0)
	if r.AwaitingAction() == "" {
		t.Fatal("a RUNNING cancel cleared the wait")
	}
	rep.InstantActions[0].Status = robot.ActionFinished
	r.ApplyReport(rep, "P1", t0)
	if r.AwaitingAction() != "" || !r.Assignable(20) {
		t.Fatal("a FINISHED cancel must clear the wait")
	}
	r.AwaitAction("x")
	rep.InstantActions = []robot.ActionState{{ID: "x", Status: robot.ActionFailed}}
	r.ApplyReport(rep, "P1", t0)
	if r.AwaitingAction() != "" {
		t.Fatal("a FAILED instant action must clear the wait")
	}
}

func TestChargedMeansSuspendedAtTheResumeLevelOnce(t *testing.T) {
	r := online(t)
	rep := idleReport(79)
	rep.Charging = true
	r.ApplyReport(rep, "CH1", t0)
	if r.Charged(80) {
		t.Fatal("79 % is not charged")
	}
	rep.Battery = 80
	r.ApplyReport(rep, "CH1", t0)
	if !r.Charged(80) {
		t.Fatal("80 % while charging is charged")
	}
	r.MarkStopChargeSent()
	if r.Charged(80) {
		t.Fatal("stopCharging is sent once")
	}
	rep.Charging = false
	r.ApplyReport(rep, "CH1", t0)
	rep.Charging = true
	r.ApplyReport(rep, "CH1", t0)
	if !r.Charged(80) {
		t.Fatal("a new charge after the robot stopped charging re-arms stopCharging")
	}
}

func TestStaleAndBrokenRobotsAreStopped(t *testing.T) {
	r := online(t)
	e := changed(t, r.MarkStale(t0.Add(2*time.Minute)))
	if e.State != robot.Stopped || e.Previous != robot.Idle || r.Online() || !r.OfflineSince().Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("stale = %+v", e)
	}
	if evs := r.MarkStale(t0.Add(3 * time.Minute)); len(evs) != 0 {
		t.Fatal("a stale robot was marked stale twice")
	}
	changed(t, r.ApplyReport(idleReport(90), "P1", t0.Add(4*time.Minute)))
	if r.State() != robot.Idle {
		t.Fatal("a fresh report revives a stale robot")
	}
	e = changed(t, r.ApplyConnection(robot.ConnectionBroken, t0.Add(5*time.Minute), time.Time{}))
	if e.State != robot.Stopped || e.Connection != robot.ConnectionBroken {
		t.Fatalf("broken = %+v", e)
	}
}

func TestSnapshotRoundTripsAndIsACopy(t *testing.T) {
	r := online(t)
	rep := idleReport(70)
	rep.Errors = []robot.Error{{Type: "X", Level: robot.LevelWarning}}
	rep.LoadIDs = []string{"C1"}
	r.ApplyReport(rep, "P1", t0)
	r.ApplyFactsheet(robot.Factsheet{SeriesName: "EMU-AMR", MaxSpeed: 1.5, MaxLoadMass: 50})
	s := r.Snapshot()
	if !reflect.DeepEqual(robot.Rehydrate(s).Snapshot(), s) || !s.HasFactsheet || r.Factsheet().MaxSpeed != 1.5 || !r.HasFactsheet() {
		t.Fatalf("round trip = %+v", s)
	}
	s.Report.Errors[0].Type = "Y"
	s.Report.LoadIDs[0] = "C2"
	if r.Report().Errors[0].Type != "X" || r.Report().LoadIDs[0] != "C1" {
		t.Fatal("Snapshot shares the robot's slices")
	}
	if r.Charging() || r.ID() != rid {
		t.Fatal("getters")
	}
}
