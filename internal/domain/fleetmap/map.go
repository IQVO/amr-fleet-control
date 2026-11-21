// Package fleetmap is the VDA 5050 node-and-edge map of one fleet, one version per process
// (ADR 0002), and the FleetMap aggregate that reserves its nodes and edges: a node or edge in a
// released base is reserved by exactly one robot (responsibility 2).
package fleetmap

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// ErrInvalidMap is returned for a map that cannot be driven.
var ErrInvalidMap = errors.New("invalid fleet map")

// NodeKind is what a node is used for.
type NodeKind string

const (
	Waypoint NodeKind = "WAYPOINT"
	Parking  NodeKind = "PARKING"
	Charger  NodeKind = "CHARGER"
	Port     NodeKind = "PORT"
	Station  NodeKind = "STATION"
)

// Node is one map node; X and Y are metres in the map's frame. HandoffPoint is the node's public
// name (a port or a station) — the only map vocabulary material-flow-control uses.
type Node struct {
	ID           string
	X, Y         float64
	Kind         NodeKind
	HandoffPoint string
}

// Edge is one undirected corridor between two nodes; MaxSpeed 0 means no limit (m/s).
type Edge struct {
	ID       string
	A, B     string
	MaxSpeed float64
}

// Spec describes one map version.
type Spec struct {
	FleetID, MapID, Version string
	Nodes                   []Node
	Edges                   []Edge
}

type link struct {
	to, edge string
	length   float64
}

// Map is one immutable map version.
type Map struct {
	fleetID, mapID, version string
	nodes                   map[string]Node
	edges                   map[string]Edge
	adj                     map[string][]link
	handoffs                map[string]string
	pairs                   map[[2]string]string
}

// New validates s and builds the map.
func New(s Spec) (*Map, error) {
	if s.FleetID == "" || s.MapID == "" || s.Version == "" {
		return nil, fmt.Errorf("%w: fleetId, mapId and version are required", ErrInvalidMap)
	}
	if len(s.Nodes) == 0 {
		return nil, fmt.Errorf("%w: the map has no node", ErrInvalidMap)
	}
	m := &Map{fleetID: s.FleetID, mapID: s.MapID, version: s.Version, nodes: map[string]Node{},
		edges: map[string]Edge{}, adj: map[string][]link{}, handoffs: map[string]string{}, pairs: map[[2]string]string{}}
	for _, n := range s.Nodes {
		if err := m.addNode(n); err != nil {
			return nil, err
		}
	}
	for _, e := range s.Edges {
		if err := m.addEdge(e); err != nil {
			return nil, err
		}
	}
	for id, links := range m.adj {
		sort.Slice(links, func(i, j int) bool { return links[i].to < links[j].to })
		m.adj[id] = links
	}
	return m, nil
}

func (m *Map) addNode(n Node) error {
	if n.ID == "" {
		return fmt.Errorf("%w: a node has no id", ErrInvalidMap)
	}
	if _, dup := m.nodes[n.ID]; dup {
		return fmt.Errorf("%w: node %s is declared twice", ErrInvalidMap, n.ID)
	}
	switch n.Kind {
	case Waypoint, Parking, Charger, Port, Station:
	default:
		return fmt.Errorf("%w: node %s has kind %q", ErrInvalidMap, n.ID, n.Kind)
	}
	if n.HandoffPoint != "" {
		if n.Kind != Port && n.Kind != Station {
			return fmt.Errorf("%w: node %s of kind %s cannot be a handoff point", ErrInvalidMap, n.ID, n.Kind)
		}
		if prev, dup := m.handoffs[n.HandoffPoint]; dup {
			return fmt.Errorf("%w: handoff point %s is on nodes %s and %s", ErrInvalidMap, n.HandoffPoint, prev, n.ID)
		}
		m.handoffs[n.HandoffPoint] = n.ID
	}
	m.nodes[n.ID] = n
	return nil
}

func (m *Map) addEdge(e Edge) error {
	if e.ID == "" {
		return fmt.Errorf("%w: an edge has no id", ErrInvalidMap)
	}
	if _, dup := m.edges[e.ID]; dup {
		return fmt.Errorf("%w: edge %s is declared twice", ErrInvalidMap, e.ID)
	}
	a, okA := m.nodes[e.A]
	b, okB := m.nodes[e.B]
	if !okA || !okB {
		return fmt.Errorf("%w: edge %s joins an unknown node", ErrInvalidMap, e.ID)
	}
	if e.A == e.B {
		return fmt.Errorf("%w: edge %s is a loop", ErrInvalidMap, e.ID)
	}
	if e.MaxSpeed < 0 {
		return fmt.Errorf("%w: edge %s has a negative maxSpeed", ErrInvalidMap, e.ID)
	}
	key := pairKey(e.A, e.B)
	if prev, dup := m.pairs[key]; dup {
		return fmt.Errorf("%w: edges %s and %s join the same nodes", ErrInvalidMap, prev, e.ID)
	}
	length := math.Hypot(b.X-a.X, b.Y-a.Y)
	if length == 0 {
		return fmt.Errorf("%w: edge %s has zero length", ErrInvalidMap, e.ID)
	}
	m.pairs[key], m.edges[e.ID] = e.ID, e
	m.adj[e.A] = append(m.adj[e.A], link{to: e.B, edge: e.ID, length: length})
	m.adj[e.B] = append(m.adj[e.B], link{to: e.A, edge: e.ID, length: length})
	return nil
}

func pairKey(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

func (m *Map) FleetID() string { return m.fleetID }
func (m *Map) MapID() string   { return m.mapID }
func (m *Map) Version() string { return m.version }

// Node returns the node with id.
func (m *Map) Node(id string) (Node, bool) {
	n, ok := m.nodes[id]
	return n, ok
}

// Edge returns the edge with id.
func (m *Map) Edge(id string) (Edge, bool) {
	e, ok := m.edges[id]
	return e, ok
}

// NodeOfHandoff returns the node a handoff point names.
func (m *Map) NodeOfHandoff(h string) (string, bool) {
	n, ok := m.handoffs[h]
	return n, ok
}

// HandoffPoints returns every handoff point, sorted.
func (m *Map) HandoffPoints() []string { return sortedKeys(m.handoffs) }

// NodeIDs returns every node id, sorted.
func (m *Map) NodeIDs() []string { return sortedKeys(m.nodes) }

// Nearest returns the node closest to (x, y) within tolerance metres; equidistant nodes resolve
// to the lower id.
func (m *Map) Nearest(x, y, tolerance float64) (string, bool) {
	best, bestD := "", math.Inf(1)
	for _, id := range m.NodeIDs() {
		n := m.nodes[id]
		if d := math.Hypot(n.X-x, n.Y-y); d <= tolerance+1e-9 && d < bestD-1e-9 {
			best, bestD = id, d
		}
	}
	return best, best != ""
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
