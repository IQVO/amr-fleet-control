package fleetmap

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

var (
	// ErrReserved is returned when an element is held by another robot.
	ErrReserved = errors.New("reserved by another robot")
	// ErrUnknownElement is returned for an element this map version does not have, or a
	// reservation without a robot.
	ErrUnknownElement = errors.New("not an element of this map version")
)

// Wait is a robot stopped at the end of its base: Node is the node it needs next, Blocker the
// robot holding it, Since when the wait began.
type Wait struct {
	Node, Blocker string
	Since         time.Time
}

// State is the persisted form of a FleetMap: element id -> robot key, robot key -> wait, and the
// keys of the deadlocks already reported.
type State struct {
	Nodes, Edges map[string]string
	Waits        map[string]Wait
	Deadlocks    []string
}

// FleetMap is the aggregate of responsibility 2: within one map version, every node and edge is
// held by at most one robot.
type FleetMap struct {
	m            *Map
	nodes, edges map[string]string
	waits        map[string]Wait
	reported     map[string]bool
}

// NewFleetMap starts with nothing reserved.
func NewFleetMap(m *Map) *FleetMap {
	return &FleetMap{m: m, nodes: map[string]string{}, edges: map[string]string{}, waits: map[string]Wait{}, reported: map[string]bool{}}
}

// Rehydrate rebuilds a FleetMap; every element must belong to m.
func Rehydrate(m *Map, s State) (*FleetMap, error) {
	f := NewFleetMap(m)
	for id, r := range s.Nodes {
		if _, ok := m.nodes[id]; !ok || r == "" {
			return nil, fmt.Errorf("%w: node %s", ErrUnknownElement, id)
		}
		f.nodes[id] = r
	}
	for id, r := range s.Edges {
		if _, ok := m.edges[id]; !ok || r == "" {
			return nil, fmt.Errorf("%w: edge %s", ErrUnknownElement, id)
		}
		f.edges[id] = r
	}
	for r, w := range s.Waits {
		if _, ok := m.nodes[w.Node]; !ok || r == "" || w.Blocker == "" {
			return nil, fmt.Errorf("%w: wait of %s on %s", ErrUnknownElement, r, w.Node)
		}
		f.waits[r] = w
	}
	for _, k := range s.Deadlocks {
		f.reported[k] = true
	}
	return f, nil
}

// Map returns the map version this aggregate reserves on.
func (f *FleetMap) Map() *Map { return f.m }

// State returns a copy of the persisted form.
func (f *FleetMap) State() State {
	s := State{Nodes: map[string]string{}, Edges: map[string]string{}, Waits: map[string]Wait{}, Deadlocks: []string{}}
	for k, v := range f.nodes {
		s.Nodes[k] = v
	}
	for k, v := range f.edges {
		s.Edges[k] = v
	}
	for k, v := range f.waits {
		s.Waits[k] = v
	}
	s.Deadlocks = append(s.Deadlocks, sortedKeys(f.reported)...)
	return s
}

// NodeHolder returns the robot holding node id.
func (f *FleetMap) NodeHolder(id string) (string, bool) {
	r, ok := f.nodes[id]
	return r, ok
}

// EdgeHolder returns the robot holding edge id.
func (f *FleetMap) EdgeHolder(id string) (string, bool) {
	r, ok := f.edges[id]
	return r, ok
}

// Reserve holds every listed element for robot, or none of them: an element held by another
// robot is ErrReserved, an element of no map is ErrUnknownElement.
func (f *FleetMap) Reserve(robot string, nodes, edges []string) error {
	if robot == "" {
		return fmt.Errorf("%w: a reservation needs a robot", ErrUnknownElement)
	}
	if err := f.check(robot, "node", nodes, f.nodes, f.m.hasNode); err != nil {
		return err
	}
	if err := f.check(robot, "edge", edges, f.edges, f.m.hasEdge); err != nil {
		return err
	}
	for _, id := range nodes {
		f.nodes[id] = robot
	}
	for _, id := range edges {
		f.edges[id] = robot
	}
	return nil
}

func (f *FleetMap) check(robot, what string, ids []string, held map[string]string, known func(string) bool) error {
	for _, id := range ids {
		if !known(id) {
			return fmt.Errorf("%w: %s %s", ErrUnknownElement, what, id)
		}
		if h, ok := held[id]; ok && h != robot {
			return fmt.Errorf("%w: %s %s is held by %s", ErrReserved, what, id, h)
		}
	}
	return nil
}

// Retain releases every element robot holds that is not listed.
func (f *FleetMap) Retain(robot string, nodes, edges []string) {
	retain(f.nodes, robot, nodes)
	retain(f.edges, robot, edges)
}

func retain(held map[string]string, robot string, keep []string) {
	k := map[string]bool{}
	for _, id := range keep {
		k[id] = true
	}
	for id, h := range held {
		if h == robot && !k[id] {
			delete(held, id)
		}
	}
}

func (m *Map) hasNode(id string) bool {
	_, ok := m.nodes[id]
	return ok
}

func (m *Map) hasEdge(id string) bool {
	_, ok := m.edges[id]
	return ok
}

// ReleaseRobot releases everything robot holds and forgets its wait.
func (f *FleetMap) ReleaseRobot(robot string) {
	f.Retain(robot, nil, nil)
	delete(f.waits, robot)
}

// Held returns what robot holds, sorted.
func (f *FleetMap) Held(robot string) (nodes, edges []string) {
	return heldBy(f.nodes, robot), heldBy(f.edges, robot)
}

func heldBy(held map[string]string, robot string) []string {
	var out []string
	for id, h := range held {
		if h == robot {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// SetWait records that robot waits for node, held by blocker; an unchanged wait keeps its time.
func (f *FleetMap) SetWait(robot, node, blocker string, at time.Time) {
	if w, ok := f.waits[robot]; ok && w.Node == node && w.Blocker == blocker {
		return
	}
	f.waits[robot] = Wait{Node: node, Blocker: blocker, Since: at}
}

// ClearWait forgets robot's wait.
func (f *FleetMap) ClearWait(robot string) { delete(f.waits, robot) }

// WaitOf returns robot's wait.
func (f *FleetMap) WaitOf(robot string) (Wait, bool) {
	w, ok := f.waits[robot]
	return w, ok
}
