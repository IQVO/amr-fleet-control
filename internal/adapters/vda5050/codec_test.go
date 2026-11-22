package vda5050_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/adapters/vda5050"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

var (
	r1 = robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}
	at = time.Date(2026, 11, 20, 8, 0, 0, 123_000_000, time.UTC)
)

func asJSON(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestEncodeOrderIsTheVDA5050Wire(t *testing.T) {
	o := vda.Order{Robot: r1, OrderID: "leg-1", UpdateID: 1,
		Nodes: []vda.Node{{ID: "A", Seq: 2, Released: true, X: 0, Y: 2, MapID: "S1", Actions: []vda.Action{}},
			{ID: "ST1", Seq: 4, Released: false, X: 0, Y: 8, MapID: "S1", Actions: []vda.Action{{Type: "pick", ID: "leg-1-pick", Blocking: "HARD", Params: map[string]string{"loadId": "C1"}}}}},
		Edges: []vda.Edge{{ID: "A-ST1", Seq: 3, Released: false, Start: "A", End: "ST1", MaxSpeed: 1.2}}}
	h := vda5050.NewHeaders().Next("order", r1, at)
	b, err := vda5050.EncodeOrder(o, h)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"headerId":0,"timestamp":"2026-11-20T08:00:00.123Z","version":"3.0.0","manufacturer":"EMU","serialNumber":"R1",
	"orderId":"leg-1","orderUpdateId":1,
	"nodes":[{"nodeId":"A","sequenceId":2,"released":true,"nodePosition":{"x":0,"y":2,"mapId":"S1"},"actions":[]},
	         {"nodeId":"ST1","sequenceId":4,"released":false,"nodePosition":{"x":0,"y":8,"mapId":"S1"},
	          "actions":[{"actionType":"pick","actionId":"leg-1-pick","blockingType":"HARD","actionParameters":[{"key":"loadId","value":"C1"}]}]}],
	"edges":[{"edgeId":"A-ST1","sequenceId":3,"released":false,"startNodeId":"A","endNodeId":"ST1","maxSpeed":1.2,"actions":[]}]}`
	if got := asJSON(t, b); !reflect.DeepEqual(got, asJSON(t, []byte(want))) {
		t.Fatalf("order =\n%s", b)
	}
	o.Edges[0].MaxSpeed = 0
	b, _ = vda5050.EncodeOrder(o, h)
	if edge := asJSON(t, b).(map[string]any)["edges"].([]any)[0].(map[string]any); edge["maxSpeed"] != nil {
		t.Fatal("an edge without a speed limit must omit maxSpeed")
	}
}

func TestEncodeInstantActionsAndHeaderCounters(t *testing.T) {
	hs := vda5050.NewHeaders()
	_ = hs.Next("instantActions", r1, at)
	h := hs.Next("instantActions", r1, at)
	if h.HeaderID != 1 || hs.Next("order", r1, at).HeaderID != 0 || hs.Next("instantActions", robot.ID{Manufacturer: "EMU", SerialNumber: "R2"}, at).HeaderID != 0 {
		t.Fatal("headerId counts per topic and robot, from 0")
	}
	b, err := vda5050.EncodeInstantActions(vda.InstantActions{Robot: r1, Actions: []vda.Action{{Type: "cancelOrder", ID: "leg-1-cancel", Blocking: "HARD"}}}, h)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"headerId":1,"timestamp":"2026-11-20T08:00:00.123Z","version":"3.0.0","manufacturer":"EMU","serialNumber":"R1",
	"actions":[{"actionType":"cancelOrder","actionId":"leg-1-cancel","blockingType":"HARD"}]}`
	if !reflect.DeepEqual(asJSON(t, b), asJSON(t, []byte(want))) {
		t.Fatalf("instantActions =\n%s", b)
	}
}

func TestParseTopic(t *testing.T) {
	id, name, ok := vda5050.ParseTopic("vda5050/v3/EMU/R1/state")
	if !ok || id != r1 || name != "state" {
		t.Fatalf("ParseTopic = %+v %q %v", id, name, ok)
	}
	for _, bad := range []string{"vda5050/v2/EMU/R1/state", "x/v3/EMU/R1/state", "vda5050/v3/EMU/state", "vda5050/v3/EMU/R1/state/x", "vda5050/v3//R1/state"} {
		if _, _, ok := vda5050.ParseTopic(bad); ok {
			t.Fatalf("ParseTopic(%q) accepted", bad)
		}
	}
}

func TestDecodeStateTranslatesTheEmulatorState(t *testing.T) {
	b := []byte(`{"headerId":7,"timestamp":"2026-11-20T09:00:00.000Z","version":"3.0.0","manufacturer":"EMU","serialNumber":"R1",
	"orderId":"leg-1","orderUpdateId":2,"lastNodeId":"E","lastNodeSequenceId":6,
	"nodeStates":[{"nodeId":"ST1","sequenceId":8,"released":true}],"edgeStates":[{"edgeId":"E-ST1","sequenceId":7,"released":true}],
	"mobileRobotPosition":{"x":0.5,"y":6,"theta":1.57,"mapId":"S1","localized":true},"driving":true,"paused":false,
	"actionStates":[{"actionId":"leg-1-pick","actionType":"pick","actionStatus":"WAITING"}],
	"instantActionStates":[{"actionId":"stop-1","actionType":"stopCharging","actionStatus":"FINISHED"}],
	"powerSupply":{"stateOfCharge":71.5,"charging":false},"operatingMode":"AUTOMATIC",
	"errors":[{"errorType":"ROBOT_BLOCKED","errorLevel":"CRITICAL","errorDescription":"obstacle on the path"}],
	"information":[],"safetyState":{"activeEmergencyStop":"NONE","fieldViolation":false},"loads":[{"loadId":"C9"}]}`)
	st, err := vda5050.DecodeState(b)
	if err != nil {
		t.Fatal(err)
	}
	want := robot.Report{At: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC), OrderID: "leg-1", OrderUpdateID: 2, LastNodeID: "E",
		LastNodeSeq: 6, X: 0.5, Y: 6, MapID: "S1", Driving: true, Pending: true, Battery: 71.5, LoadIDs: []string{"C9"},
		Errors:         []robot.Error{{Type: "ROBOT_BLOCKED", Level: "CRITICAL"}},
		Actions:        []robot.ActionState{{ID: "leg-1-pick", Type: "pick", Status: "WAITING"}},
		InstantActions: []robot.ActionState{{ID: "stop-1", Type: "stopCharging", Status: "FINISHED"}}}
	if st.Robot != r1 || !reflect.DeepEqual(st.Report, want) {
		t.Fatalf("state = %+v\nwant  %+v", st, want)
	}
	idle, err := vda5050.DecodeState([]byte(`{"headerId":0,"timestamp":"2026-11-20T09:00:00.000Z","version":"3.0.0","manufacturer":"EMU","serialNumber":"R1",
	"orderId":"","orderUpdateId":0,"lastNodeId":"","lastNodeSequenceId":0,"nodeStates":[],"edgeStates":[],"driving":false,"paused":false,
	"actionStates":[],"instantActionStates":[],"powerSupply":{"stateOfCharge":100,"charging":true},"operatingMode":"AUTOMATIC","errors":[],
	"information":[],"safetyState":{"activeEmergencyStop":"NONE","fieldViolation":false}}`))
	if err != nil || idle.Report.Pending || !idle.Report.Charging || idle.Report.MapID != "" {
		t.Fatalf("idle = %+v %v", idle, err)
	}
	for _, bad := range []string{`{`, `{"timestamp":"yesterday","manufacturer":"EMU","serialNumber":"R1","version":"3.0.0"}`,
		`{"timestamp":"2026-11-20T09:00:00.000Z","manufacturer":"EMU","serialNumber":"R1","version":"2.0.0"}`,
		`{"timestamp":"2026-11-20T09:00:00.000Z","manufacturer":"","serialNumber":"R1","version":"3.0.0"}`} {
		if _, err := vda5050.DecodeState([]byte(bad)); err == nil {
			t.Fatalf("DecodeState(%s) accepted", bad)
		}
	}
}

func TestDecodeConnectionAndFactsheet(t *testing.T) {
	c, err := vda5050.DecodeConnection([]byte(`{"headerId":3,"timestamp":"2026-11-20T09:00:00.000Z","version":"3.0.0","manufacturer":"EMU","serialNumber":"R1","connectionState":"CONNECTION_BROKEN"}`))
	if err != nil || c.Robot != r1 || c.State != robot.ConnectionBroken || !c.At.Equal(time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("connection = %+v %v", c, err)
	}
	if _, err := vda5050.DecodeConnection([]byte(`{"timestamp":"2026-11-20T09:00:00.000Z","version":"3.0.0","manufacturer":"EMU","serialNumber":"R1","connectionState":"ASLEEP"}`)); err == nil {
		t.Fatal("an unknown connectionState was accepted")
	}
	f, err := vda5050.DecodeFactsheet([]byte(`{"headerId":0,"timestamp":"2026-11-20T09:00:00.000Z","version":"3.0.0","manufacturer":"EMU","serialNumber":"R1",
	"typeSpecification":{"seriesName":"EMU-AMR","maximumLoadMass":50,"localizationTypes":["NATURAL"],"navigationTypes":["VIRTUAL_LINE_GUIDED"]},
	"physicalParameters":{"minimumSpeed":0,"maximumSpeed":1.5}}`))
	if err != nil || f.Robot != r1 || f.Factsheet != (robot.Factsheet{SeriesName: "EMU-AMR", MaxSpeed: 1.5, MaxLoadMass: 50}) {
		t.Fatalf("factsheet = %+v %v", f, err)
	}
}

// carry (Task 13 review): battery_percent is published with minimum 0 and maximum 100 (asyncapi RobotStateChanged,
// openapi Robot), so a robot reporting a stateOfCharge outside 0-100 must not produce an event the contract (and
// plan 04's consumer) refuses: the codec clamps it at the ACL; values inside the range pass unchanged.
func TestDecodeStateClampsTheBatteryToZeroToHundred(t *testing.T) {
	for raw, want := range map[string]float64{"104.2": 100, "100": 100, "-3": 0, "-0.01": 0, "0": 0, "64.5": 64.5, "99.99": 99.99} {
		b := []byte(`{"headerId":1,"timestamp":"2026-11-20T09:00:00.000Z","version":"3.0.0","manufacturer":"EMU","serialNumber":"R1",
		"powerSupply":{"stateOfCharge":` + raw + `,"charging":false}}`)
		st, err := vda5050.DecodeState(b)
		if err != nil || st.Report.Battery != want {
			t.Errorf("stateOfCharge %s -> battery %v (%v), want %v", raw, st.Report.Battery, err, want)
		}
	}
}
