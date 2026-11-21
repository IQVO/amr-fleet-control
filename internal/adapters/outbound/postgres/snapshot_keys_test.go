package postgres

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/domain/capacity"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

func keys(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// A failing case here means a stored jsonb column changed shape: write a migration first.
func TestStoredSnapshotKeysArePinned(t *testing.T) {
	cases := []struct {
		v    any
		want []string
	}{
		{fleetmap.State{}, []string{"Deadlocks", "Edges", "Nodes", "Waits"}},
		{capacity.Snapshot{}, []string{"At", "LegsActive", "LegsQueued", "RobotsAvailable", "RobotsCharging", "RobotsHeld", "RobotsOnline", "RobotsTotal"}},
		{robot.Snapshot{}, []string{"AwaitingAction", "AwaitingSince", "Connection", "Factsheet", "HasFactsheet", "HasReport", "HeldSince", "ID", "LastSeen",
			"MissionID", "Node", "OfflineSince", "Report", "Stale", "State", "StopChargeSent", "StopChargeSentAt"}},
		{robot.Report{}, []string{"Actions", "At", "Battery", "Charging", "Driving", "Errors", "InstantActions", "LastNodeID", "LastNodeSeq",
			"LoadIDs", "MapID", "OrderID", "OrderUpdateID", "Pending", "X", "Y"}},
		{mission.State{}, []string{"AcceptedAt", "AssignedAt", "BaseEnd", "ContainerID", "DropNode", "EndedAt", "From", "ID", "Kind",
			"MapVersion", "OrderStart", "PickedUpAt", "PickupNode", "Reached", "Reason", "Reference", "Resends", "Robot", "RobotError",
			"Route", "SeenUpdate", "Sent", "SentAt", "Seq", "Status", "To", "UpdateID"}},
	}
	for _, c := range cases {
		if got := keys(t, c.v); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%T keys = %v", c.v, got)
		}
	}
}
