package events

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// carry (Task 13 review): the published contract must be no looser than plan
// 04's consumer (material-flow-control), which dead-letters an empty required
// id, an empty robot field and an empty map_version. Every such string in
// apis/asyncapi.yaml carries minLength: 1, so a contract-valid event is never
// poison downstream.
func TestContractRequiredStringsAreNonEmpty(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "apis", "asyncapi.yaml"))
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]map[string]any `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse contract: %v", err)
	}
	want := map[string][]string{
		"Robot":                {"manufacturer", "serial_number"},
		"LegAssigned":          {"leg_id", "fleet_id", "container_id", "from_handoff_point_id", "to_handoff_point_id", "map_version"},
		"ContainerPickedUp":    {"leg_id", "fleet_id", "container_id", "from_handoff_point_id"},
		"LegCompleted":         {"leg_id", "fleet_id", "container_id", "to_handoff_point_id"},
		"LegFailed":            {"leg_id", "fleet_id", "container_id", "from_handoff_point_id", "to_handoff_point_id"},
		"RobotStateChanged":    {"fleet_id"},
		"FleetCapacityChanged": {"fleet_id"},
		"DeadlockDetected":     {"fleet_id", "deadlock_id"},
	}
	for schema, props := range want {
		for _, p := range props {
			prop := doc.Components.Schemas[schema].Properties[p]
			if prop["minLength"] != 1 {
				t.Errorf("%s.%s: minLength = %v, want 1", schema, p, prop["minLength"])
			}
		}
	}
	items, _ := doc.Components.Schemas["DeadlockDetected"].Properties["node_ids"]["items"].(map[string]any)
	if items["minLength"] != 1 {
		t.Errorf("DeadlockDetected.node_ids.items: minLength = %v, want 1", items["minLength"])
	}
}
