// Package config loads amr-fleet-control's configuration files.
package config

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
)

type mapFile struct {
	FleetID string     `yaml:"fleetId"`
	MapID   string     `yaml:"mapId"`
	Version string     `yaml:"version"`
	Nodes   []nodeFile `yaml:"nodes"`
	Edges   []edgeFile `yaml:"edges"`
}

type nodeFile struct {
	ID           string  `yaml:"id"`
	X            float64 `yaml:"x"`
	Y            float64 `yaml:"y"`
	Kind         string  `yaml:"kind"`
	HandoffPoint string  `yaml:"handoffPoint"`
}

type edgeFile struct {
	ID       string  `yaml:"id"`
	A        string  `yaml:"a"`
	B        string  `yaml:"b"`
	MaxSpeed float64 `yaml:"maxSpeed"`
}

// LoadMap reads and validates the map file (AMR_MAP_FILE, ADR 0002). Unknown fields are refused.
func LoadMap(path string) (*fleetmap.Map, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the path is operator configuration
	if err != nil {
		return nil, fmt.Errorf("read map file: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var f mapFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse map file %s: %w", path, err)
	}
	spec := fleetmap.Spec{FleetID: f.FleetID, MapID: f.MapID, Version: f.Version}
	for _, n := range f.Nodes {
		spec.Nodes = append(spec.Nodes, fleetmap.Node{ID: n.ID, X: n.X, Y: n.Y, Kind: fleetmap.NodeKind(n.Kind), HandoffPoint: n.HandoffPoint})
	}
	for _, e := range f.Edges {
		spec.Edges = append(spec.Edges, fleetmap.Edge{ID: e.ID, A: e.A, B: e.B, MaxSpeed: e.MaxSpeed})
	}
	m, err := fleetmap.New(spec)
	if err != nil {
		return nil, fmt.Errorf("map file %s: %w", path, err)
	}
	return m, nil
}
