package config_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/config"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"gopkg.in/yaml.v3"
)

// The world side of the pinned floor contract (contracts/equipment-emulator.world.yaml, the byte
// copy of the emulator's config/world.example.yaml): the robots' mapId and the AS/RS port nodeIds.
// The world has no floor graph (plan 06 D5): port nodeIds and robot mapIds ARE the contract.
type pinnedWorld struct {
	Robots []struct {
		MapID string `yaml:"mapId"`
	} `yaml:"robots"`
	Asrs []struct {
		Ports []struct {
			NodeID string `yaml:"nodeId"`
		} `yaml:"ports"`
	} `yaml:"asrs"`
}

func readPinnedWorld(t *testing.T) pinnedWorld {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "equipment-emulator.world.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var w pinnedWorld
	if err := yaml.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	return w
}

func worldPortNodeIDs(w pinnedWorld) []string {
	var out []string
	for _, a := range w.Asrs {
		for _, p := range a.Ports {
			out = append(out, p.NodeID)
		}
	}
	return out
}

// (a) the map's PORT node ids and the world's port nodeIds are the same set, both directions
// (both diffs printed, so a drift names the exact node).
func assertPortsAgree(t *testing.T, m *fleetmap.Map, w pinnedWorld) {
	t.Helper()
	var mapPorts []string
	for _, id := range m.NodeIDs() {
		if n, ok := m.Node(id); ok && n.Kind == fleetmap.Port {
			mapPorts = append(mapPorts, id)
		}
	}
	worldPorts := worldPortNodeIDs(w)
	for _, p := range mapPorts {
		if !slices.Contains(worldPorts, p) {
			t.Errorf("map PORT node %s has no world port nodeId (world: %v)", p, worldPorts)
		}
	}
	for _, p := range worldPorts {
		if !slices.Contains(mapPorts, p) {
			t.Errorf("world port nodeId %s is not a map PORT node (map: %v)", p, mapPorts)
		}
	}
}

// The example map and the pinned emulator world describe one floor (plan 06 D5): the map loads
// through Task 1's connectivity validation, the port sets agree both ways, every world robot
// drives the map's mapId, the floor's handoff points (the AS/RS ports, the stations and plan
// 06's PK1/FWD1/CLR1) exist, and the golden version pins the map release.
func TestTheExampleMapAndThePinnedWorldAgreeOnTheFloor(t *testing.T) {
	m, err := config.LoadMap(filepath.Join("..", "..", "config", "map.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	w := readPinnedWorld(t)
	assertPortsAgree(t, m, w)
	for _, r := range w.Robots {
		if r.MapID != m.MapID() {
			t.Errorf("world robot mapId %q != the map's mapId %q", r.MapID, m.MapID())
		}
	}
	for _, h := range []string{"ASRS1-IN1", "ASRS1-IN2", "ASRS1-OUT1", "ASRS1-OUT2", "ST1", "ST2", "PK1", "FWD1", "CLR1"} {
		if _, ok := m.NodeOfHandoff(h); !ok {
			t.Errorf("handoff point %s is missing from the map (handoffs: %v)", h, m.HandoffPoints())
		}
	}
	if m.Version() != "2026-11-22.1" {
		t.Fatalf("map version = %s, want the golden 2026-11-22.1", m.Version())
	}
}
