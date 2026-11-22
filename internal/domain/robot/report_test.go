package robot_test

import (
	"reflect"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// (The body of the brief's test is split into checkMixedReportQueries to stay under this repo's
// gocognit limit of 20; the assertions are the brief's, unchanged.)
func TestReportQueries(t *testing.T) {
	rep := robot.Report{
		Errors: []robot.Error{
			{Type: robot.ErrTypeInvalidOrder, Level: robot.LevelWarning},
			{Type: robot.ErrTypeBlocked, Level: robot.LevelCritical},
			{Type: robot.ErrTypeInvalidOrder, Level: robot.LevelWarning},
			{Type: robot.ErrTypeBatteryEmpty, Level: robot.LevelFatal},
		},
		Actions:        []robot.ActionState{{ID: "leg-1-pick", Type: "pick", Status: robot.ActionFinished}},
		InstantActions: []robot.ActionState{{ID: "leg-1-cancel", Type: "cancelOrder", Status: robot.ActionFailed}},
	}
	checkMixedReportQueries(t, rep)
	clean := robot.Report{Errors: []robot.Error{{Type: "SAME_ORDER_UPDATE_ID", Level: robot.LevelWarning}, {Type: robot.ErrTypeValidation, Level: robot.LevelUrgent}}}
	if _, ok := clean.Blocking(); ok {
		t.Fatal("WARNING and URGENT errors do not block")
	}
	if _, ok := clean.Fatal(); ok {
		t.Fatal("no FATAL error, yet Fatal")
	}
	if e, ok := clean.Rejected(); !ok || e != robot.ErrTypeValidation {
		t.Fatalf("VALIDATION_FAILURE is a rejection: %q %v", e, ok)
	}
	if got := (robot.Report{}).ErrorTypes(); got == nil || len(got) != 0 {
		t.Fatalf("no errors must be an empty, non-nil list: %#v", got)
	}
	if _, ok := (robot.Report{}).Rejected(); ok {
		t.Fatal("an empty report rejected something")
	}
	if got := rep.Rejections(); got != 2 {
		t.Fatalf("Rejections = %d, want 2 (every INVALID_ORDER / VALIDATION_FAILURE entry counts)", got)
	}
	if got := clean.Rejections(); got != 1 {
		t.Fatalf("clean Rejections = %d", got)
	}
}

func checkMixedReportQueries(t *testing.T, rep robot.Report) {
	t.Helper()
	if got := rep.ErrorTypes(); !reflect.DeepEqual(got, []string{"INVALID_ORDER", "ROBOT_BLOCKED", "BATTERY_EMPTY"}) {
		t.Fatalf("ErrorTypes = %v", got)
	}
	if e, ok := rep.Blocking(); !ok || e != "ROBOT_BLOCKED" {
		t.Fatalf("Blocking = %q %v", e, ok)
	}
	if e, ok := rep.Fatal(); !ok || e != "BATTERY_EMPTY" {
		t.Fatalf("Fatal = %q %v", e, ok)
	}
	if e, ok := rep.Rejected(); !ok || e != "INVALID_ORDER" {
		t.Fatalf("Rejected = %q %v", e, ok)
	}
	if a, ok := rep.Action("leg-1-pick"); !ok || a.Status != robot.ActionFinished {
		t.Fatalf("Action(pick) = %+v %v", a, ok)
	}
	if a, ok := rep.Action("leg-1-cancel"); !ok || a.Status != robot.ActionFailed {
		t.Fatalf("an instant action must be found too: %+v %v", a, ok)
	}
	if _, ok := rep.Action("nope"); ok {
		t.Fatal("unknown action found")
	}
}

func TestParseKeyAndConnection(t *testing.T) {
	id, err := robot.ParseKey("EMU/R1")
	if err != nil || id != (robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}) || id.Key() != "EMU/R1" || id.IsZero() {
		t.Fatalf("ParseKey = %+v %v", id, err)
	}
	for _, bad := range []string{"", "EMU", "/R1", "EMU/", "EMU/R1/x"} {
		if _, err := robot.ParseKey(bad); err == nil {
			t.Fatalf("ParseKey(%q) succeeded", bad)
		}
	}
	if !(robot.ID{}).IsZero() {
		t.Fatal("the zero id is not zero")
	}
	for _, c := range []string{"ONLINE", "OFFLINE", "CONNECTION_BROKEN"} {
		if got, err := robot.ParseConnection(c); err != nil || string(got) != c {
			t.Fatalf("ParseConnection(%s) = %v %v", c, got, err)
		}
	}
	if _, err := robot.ParseConnection("SLEEPING"); err == nil {
		t.Fatal("an unknown connection state parsed")
	}
}
