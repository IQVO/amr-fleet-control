package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/config"
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
)

func TestTheExampleMapLoads(t *testing.T) {
	m, err := config.LoadMap(filepath.Join("..", "..", "config", "map.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if m.FleetID() != "AMR1" || m.MapID() != "S1" || m.Version() != "2026-11-20.1" || len(m.NodeIDs()) != 18 {
		t.Fatalf("map = %s %s %s %d nodes", m.FleetID(), m.MapID(), m.Version(), len(m.NodeIDs()))
	}
	if n, ok := m.NodeOfHandoff("ASRS1-OUT1"); !ok || n != "ASRS1-OUT1" {
		t.Fatalf("port handoff = %q %v", n, ok)
	}
	if p, ok := m.Route("P1", "ST1", nil); !ok || p.Length != 8 {
		t.Fatalf("route = %+v %v", p, ok)
	}
	if n, _ := m.Node("CH1"); n.Kind != fleetmap.Charger {
		t.Fatalf("CH1 = %+v", n)
	}
}

// Task 1 (plan 06): a map file whose graph is an island is refused at load, wrapped with the
// file path (the loader's existing `map file %s: %w` convention).
func TestLoadMapRefusesADisconnectedMap(t *testing.T) {
	p := write(t, "fleetId: A\nmapId: S\nversion: \"1\"\nnodes:\n  - {id: A, x: 0, y: 0, kind: PARKING}\n  - {id: B, x: 3, y: 4, kind: STATION, handoffPoint: B1}\nedges: []\n")
	_, err := config.LoadMap(p)
	if err == nil || !errors.Is(err, fleetmap.ErrInvalidMap) || !strings.Contains(err.Error(), p) {
		t.Fatalf("LoadMap = %v, want ErrInvalidMap naming island node B with the path %s", err, p)
	}
	if err != nil && !strings.Contains(err.Error(), "node B is not reachable from A") {
		t.Fatalf("LoadMap = %v, want the unreachable pair in the error", err)
	}
}

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "map.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMapRefusesUnknownFieldsBadYAMLAndInvalidMaps(t *testing.T) {
	if _, err := config.LoadMap(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("a missing file loaded")
	}
	if _, err := config.LoadMap(write(t, "fleetId: [")); err == nil {
		t.Fatal("bad YAML loaded")
	}
	if _, err := config.LoadMap(write(t, "fleetId: A\nmapId: S\nversion: \"1\"\ncolour: red\nnodes: [{id: P, x: 0, y: 0, kind: PARKING}]\n")); err == nil {
		t.Fatal("an unknown field was accepted")
	}
	if _, err := config.LoadMap(write(t, "fleetId: A\nmapId: S\nversion: \"1\"\nnodes: []\n")); err == nil {
		t.Fatal("an empty map loaded")
	}
	m, err := config.LoadMap(write(t, "fleetId: A\nmapId: S\nversion: \"1\"\nnodes:\n  - {id: P, x: 0, y: 0, kind: PARKING}\n  - {id: Q, x: 3, y: 4, kind: STATION, handoffPoint: Q1}\nedges:\n  - {id: P-Q, a: P, b: Q, maxSpeed: 1.2}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := m.Edge("P-Q"); e.MaxSpeed != 1.2 {
		t.Fatalf("maxSpeed = %v", e.MaxSpeed)
	}
	if p, _ := m.Route("P", "Q", nil); p.Length != 5 {
		t.Fatalf("length = %v", p.Length)
	}
}
