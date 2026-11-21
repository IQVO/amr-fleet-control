// Package dispatch holds the pure policies of fleet control: which robot takes a leg (decision
// D6), the route a mission drives, how far its base may be released (decision D7), and the
// nearest free charger or parking node (decisions D10, D11).
package dispatch

import (
	"github.com/IQVO/amr-fleet-control/internal/domain/fleetmap"
	"github.com/IQVO/amr-fleet-control/internal/domain/mission"
	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

const eps = 1e-9

// Candidate is an assignable robot and the node it stands on.
type Candidate struct {
	Robot   robot.ID
	Node    string
	Battery float64
}

// Choose returns the candidate with the shortest route to pickup, then the higher battery, then
// the lower robot key; unreachable candidates are skipped.
func Choose(m *fleetmap.Map, pickup string, cands []Candidate) (Candidate, bool) {
	var best Candidate
	bestLen, found := 0.0, false
	for _, c := range cands {
		p, ok := m.Route(c.Node, pickup, nil)
		if !ok {
			continue
		}
		if !found || better(p.Length, c, bestLen, best) {
			best, bestLen, found = c, p.Length, true
		}
	}
	return best, found
}

func better(length float64, c Candidate, bestLen float64, best Candidate) bool {
	switch {
	case length < bestLen-eps:
		return true
	case length > bestLen+eps:
		return false
	case c.Battery != best.Battery:
		return c.Battery > best.Battery
	default:
		return c.Robot.Key() < best.Robot.Key()
	}
}

// Target is a node a route must reach, with the action to run there ("" for none).
type Target struct{ Node, Action string }

// Plan chains shortest paths (avoiding avoid) from start through every target in order; step 0 is
// start. A target equal to the current node puts its action on the current step.
func Plan(m *fleetmap.Map, start string, targets []Target, avoid map[string]bool) ([]mission.Step, bool) {
	if _, ok := m.Node(start); !ok {
		return nil, false
	}
	steps := []mission.Step{{Node: start}}
	cur := start
	for _, t := range targets {
		p, ok := m.Route(cur, t.Node, avoid)
		if !ok {
			return nil, false
		}
		for i := 1; i < len(p.Nodes); i++ {
			steps = append(steps, mission.Step{Node: p.Nodes[i], Edge: p.Edges[i-1]})
		}
		if t.Action != "" {
			last := &steps[len(steps)-1]
			if last.Action != "" {
				return nil, false
			}
			last.Action = t.Action
		}
		cur = t.Node
	}
	return steps, true
}

// Window extends a base from baseEnd towards reached+ahead (capped at the last step) while the
// next node and the edge into it are free or held by self. When it stops at an element held by
// another robot it returns that robot and the node it wanted.
func Window(route []mission.Step, baseEnd, reached, ahead int, self string, nodeHolder, edgeHolder func(string) (string, bool)) (int, string, string) {
	limit := reached + ahead
	if limit > len(route)-1 {
		limit = len(route) - 1
	}
	end := baseEnd
	for end < limit {
		next := route[end+1]
		if h, ok := nodeHolder(next.Node); ok && h != self {
			return end, h, next.Node
		}
		if h, ok := edgeHolder(next.Edge); ok && h != self {
			return end, h, next.Node
		}
		end++
	}
	return end, "", ""
}

// NearestFree returns the node of kind with the shortest route from from among those free says
// are free; equal distances resolve to the lower id.
func NearestFree(m *fleetmap.Map, from string, kind fleetmap.NodeKind, free func(node string) bool) (string, bool) {
	best, bestLen := "", 0.0
	for _, id := range m.NodeIDs() {
		if n, _ := m.Node(id); n.Kind != kind || !free(id) {
			continue
		}
		p, ok := m.Route(from, id, nil)
		if !ok {
			continue
		}
		if best == "" || p.Length < bestLen-eps {
			best, bestLen = id, p.Length
		}
	}
	return best, best != ""
}
