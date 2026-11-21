package events

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/outbox"
	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

type rec struct{ msgs []outbox.Message }

func (r *rec) Insert(_ context.Context, m ...outbox.Message) error {
	r.msgs = append(r.msgs, m...)
	return nil
}

var (
	at  = time.Date(2026, 11, 20, 8, 0, 1, 0, time.UTC)
	sim = time.Date(2026, 11, 20, 9, 0, 10, 500_000_000, time.UTC)
	r1  = robot.ID{Manufacturer: "EMU", SerialNumber: "R1"}
)

func TestEveryFactIsOneCloudEventOnBothTopics(t *testing.T) {
	cases := []struct {
		ev                 any
		typ, subject, data string
	}{
		{mission.LegAssigned{LegID: "leg-1", ContainerID: "C1", From: "ST1", To: "ASRS1-IN1", Reference: "tl-9", Robot: r1, MapVersion: "v1", At: at},
			"mission.LegAssigned", "leg-1",
			`{"leg_id":"leg-1","fleet_id":"AMR1","container_id":"C1","from_handoff_point_id":"ST1","to_handoff_point_id":"ASRS1-IN1","reference":"tl-9","robot":{"manufacturer":"EMU","serial_number":"R1"},"map_version":"v1","assigned_at":"2026-11-20T08:00:01Z"}`},
		{mission.ContainerPickedUp{LegID: "leg-1", ContainerID: "C1", From: "ST1", Robot: r1, At: at, RobotTime: sim},
			"mission.ContainerPickedUp", "leg-1",
			`{"leg_id":"leg-1","fleet_id":"AMR1","container_id":"C1","from_handoff_point_id":"ST1","robot":{"manufacturer":"EMU","serial_number":"R1"},"picked_up_at":"2026-11-20T08:00:01Z","robot_timestamp":"2026-11-20T09:00:10.5Z"}`},
		{mission.LegCompleted{LegID: "leg-1", ContainerID: "C1", To: "ASRS1-IN1", Reference: "tl-9", Robot: r1, At: at, RobotTime: sim},
			"mission.LegCompleted", "leg-1",
			`{"leg_id":"leg-1","fleet_id":"AMR1","container_id":"C1","to_handoff_point_id":"ASRS1-IN1","reference":"tl-9","robot":{"manufacturer":"EMU","serial_number":"R1"},"completed_at":"2026-11-20T08:00:01Z","robot_timestamp":"2026-11-20T09:00:10.5Z"}`},
		{mission.LegFailed{LegID: "leg-2", ContainerID: "C2", From: "ST1", To: "ST2", Reason: mission.ReasonPickFailed, RobotError: "LOAD_NOT_AVAILABLE", Robot: r1, At: at},
			"mission.LegFailed", "leg-2",
			`{"leg_id":"leg-2","fleet_id":"AMR1","container_id":"C2","from_handoff_point_id":"ST1","to_handoff_point_id":"ST2","robot":{"manufacturer":"EMU","serial_number":"R1"},"reason":"PICK_FAILED","robot_error":"LOAD_NOT_AVAILABLE","container_on_robot":false,"failed_at":"2026-11-20T08:00:01Z"}`},
		{mission.LegFailed{LegID: "leg-3", ContainerID: "C3", From: "ST1", To: "ST2", Reason: mission.ReasonCancelled, At: at},
			"mission.LegFailed", "leg-3",
			`{"leg_id":"leg-3","fleet_id":"AMR1","container_id":"C3","from_handoff_point_id":"ST1","to_handoff_point_id":"ST2","reason":"CANCELLED","container_on_robot":false,"failed_at":"2026-11-20T08:00:01Z"}`},
		{robot.RobotStateChanged{Robot: r1, State: robot.Held, Previous: robot.Execute, Connection: robot.ConnectionOnline, Battery: 42.5, ErrorTypes: []string{"ROBOT_BLOCKED"}, At: at, RobotTime: sim},
			"robot.RobotStateChanged", "EMU/R1",
			`{"fleet_id":"AMR1","robot":{"manufacturer":"EMU","serial_number":"R1"},"state":"HELD","previous_state":"EXECUTE","connection":"ONLINE","battery_percent":42.5,"charging":false,"error_types":["ROBOT_BLOCKED"],"changed_at":"2026-11-20T08:00:01Z","robot_timestamp":"2026-11-20T09:00:10.5Z"}`},
		{robot.RobotStateChanged{Robot: r1, State: robot.Stopped, Previous: robot.Idle, Connection: robot.ConnectionOnline, ErrorTypes: []string{}, At: at},
			"robot.RobotStateChanged", "EMU/R1",
			`{"fleet_id":"AMR1","robot":{"manufacturer":"EMU","serial_number":"R1"},"state":"STOPPED","previous_state":"IDLE","connection":"ONLINE","battery_percent":0,"charging":false,"error_types":[],"changed_at":"2026-11-20T08:00:01Z"}`},
		{capacity.FleetCapacityChanged{Snapshot: capacity.Snapshot{RobotsTotal: 3, RobotsOnline: 2, RobotsAvailable: 1, RobotsCharging: 1, LegsQueued: 4, LegsActive: 1, At: at}},
			"fleet.FleetCapacityChanged", "AMR1",
			`{"fleet_id":"AMR1","robots_total":3,"robots_online":2,"robots_available":1,"robots_charging":1,"robots_held":0,"legs_queued":4,"legs_active":1,"changed_at":"2026-11-20T08:00:01Z"}`},
		{fleetmap.DeadlockDetected{DeadlockID: "dl-1", Robots: []string{"EMU/R1", "EMU/R2"}, Nodes: []string{"M2", "M1"}, At: at},
			"fleetmap.DeadlockDetected", "AMR1",
			`{"fleet_id":"AMR1","deadlock_id":"dl-1","robots":[{"manufacturer":"EMU","serial_number":"R1"},{"manufacturer":"EMU","serial_number":"R2"}],"node_ids":["M2","M1"],"detected_at":"2026-11-20T08:00:01Z"}`},
	}
	for i, c := range cases {
		out := &rec{}
		n := 0
		s := NewSink(out, func() string { n++; return "id-" + strconv.Itoa(i) + "-" + strconv.Itoa(n) }, "AMR1")
		if err := s.Append(context.Background(), c.ev); err != nil {
			t.Fatalf("%s: %v", c.typ, err)
		}
		if len(out.msgs) != 2 || out.msgs[0].Topic != TopicEvents || out.msgs[1].Topic != TopicAnalytics {
			t.Fatalf("%s: rows %+v", c.typ, out.msgs)
		}
		checkTwins(t, c, out)
	}
}

// checkTwins holds the per-row assertions of TestEveryFactIsOneCloudEventOnBothTopics, moved
// out verbatim (one tab less) so the test stays under cyclop 15; no assertion changed.
func checkTwins(t *testing.T, c struct {
	ev                 any
	typ, subject, data string
}, out *rec) {
	t.Helper()
	for k, m := range out.msgs {
		stream := []string{"events", "analytics"}[k]
		var env map[string]any
		if err := json.Unmarshal(m.Value, &env); err != nil {
			t.Fatal(err)
		}
		wantType := "com.warehouse.wes.amr-fleet-control." + c.typ
		if env["type"] != wantType || m.EventType != wantType || env["subject"] != c.subject || string(m.Key) != c.subject ||
			env["source"] != "/warehouse/amr-fleet-control" || env["specversion"] != "1.0" || env["id"] != m.EventID ||
			env["dataschema"] != "urn:warehouse:amr-fleet-control:"+stream+":"+name(c.typ)+":v1" {
			t.Fatalf("%s %s: envelope %v", c.typ, stream, env)
		}
		var want any
		_ = json.Unmarshal([]byte(c.data), &want)
		if !reflect.DeepEqual(env["data"], want) {
			got, _ := json.Marshal(env["data"])
			t.Fatalf("%s data =\n%s\nwant\n%s", c.typ, got, c.data)
		}
	}
	if out.msgs[0].EventID != out.msgs[1].EventID {
		t.Fatalf("%s: the twins must share one id", c.typ)
	}
}

func name(typ string) string {
	for i := len(typ) - 1; i >= 0; i-- {
		if typ[i] == '.' {
			return typ[i+1:]
		}
	}
	return typ
}

func TestAnUnknownEventWritesNothing(t *testing.T) {
	out := &rec{}
	s := NewSink(out, func() string { return "x" }, "AMR1")
	if err := s.Append(context.Background(), mission.LegAssigned{LegID: "leg-1", At: at}, struct{}{}); err == nil || len(out.msgs) != 0 {
		t.Fatalf("an unknown event must fail the whole append: %v, %d rows", err, len(out.msgs))
	}
	if err := s.Append(context.Background()); err != nil || len(out.msgs) != 0 {
		t.Fatal("an empty append writes nothing")
	}
}
