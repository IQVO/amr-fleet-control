package fleetmap

import "slices"

// Path is a route: Edges[i] joins Nodes[i] and Nodes[i+1].
type Path struct {
	Nodes  []string
	Edges  []string
	Length float64
}

const eps = 1e-9

// Route is the shortest path from from to to (Dijkstra). It never enters a node in avoid, except
// to itself. Among equal lengths the node with the lower id is settled first and a predecessor is
// replaced only by a strictly shorter one, so the answer is deterministic.
func (m *Map) Route(from, to string, avoid map[string]bool) (Path, bool) {
	if _, ok := m.nodes[from]; !ok {
		return Path{}, false
	}
	if _, ok := m.nodes[to]; !ok {
		return Path{}, false
	}
	if from == to {
		return Path{Nodes: []string{from}}, true
	}
	dist := map[string]float64{from: 0}
	prev := map[string]link{}
	done := map[string]bool{}
	for {
		cur, ok := closest(dist, done)
		if !ok {
			return Path{}, false
		}
		if cur == to {
			break
		}
		done[cur] = true
		for _, l := range m.adj[cur] {
			if done[l.to] || (avoid[l.to] && l.to != to) {
				continue
			}
			d := dist[cur] + l.length
			if old, seen := dist[l.to]; !seen || d < old-eps {
				dist[l.to] = d
				prev[l.to] = link{to: cur, edge: l.edge}
			}
		}
	}
	return rebuild(from, to, prev, dist[to]), true
}

func rebuild(from, to string, prev map[string]link, length float64) Path {
	nodes := []string{to}
	var edges []string
	for n := to; n != from; {
		p := prev[n]
		edges = append(edges, p.edge)
		nodes = append(nodes, p.to)
		n = p.to
	}
	slices.Reverse(nodes)
	slices.Reverse(edges)
	return Path{Nodes: nodes, Edges: edges, Length: length}
}

// closest is the unsettled node with the shortest distance; distances within eps are equal and resolve to the
// lower id: the ids are visited in order and a later one wins only when it is nearer by more than eps.
func closest(dist map[string]float64, done map[string]bool) (string, bool) {
	best, found := "", false
	for _, n := range sortedKeys(dist) {
		if done[n] {
			continue
		}
		if !found || dist[n] < dist[best]-eps {
			best, found = n, true
		}
	}
	return best, found
}
