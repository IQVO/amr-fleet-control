package fleetmap

import (
	"slices"
	"sort"
	"strings"
	"time"
)

// DeadlockDetected is raised once per new cycle of waits: Robots sorted by key, Nodes[i] the node
// Robots[i] waits for.
type DeadlockDetected struct {
	DeadlockID string
	Robots     []string
	Nodes      []string
	At         time.Time
}

// Cycles returns every cycle of the wait-for graph (robot -> robot holding the node it waits
// for), each as its sorted robot keys, ordered by their first key.
func (f *FleetMap) Cycles() [][]string {
	seen := map[string]bool{}
	var out [][]string
	for _, start := range sortedKeys(f.waits) {
		var path []string
		index := map[string]int{}
		for cur := start; ; {
			if i, ok := index[cur]; ok {
				cycle := append([]string{}, path[i:]...)
				sort.Strings(cycle)
				if k := strings.Join(cycle, ","); !seen[k] {
					seen[k] = true
					out = append(out, cycle)
				}
				break
			}
			w, waiting := f.waits[cur]
			if !waiting {
				break
			}
			index[cur] = len(path)
			path = append(path, cur)
			cur = w.Blocker
		}
	}
	slices.SortFunc(out, func(a, b []string) int { return strings.Compare(a[0], b[0]) })
	return out
}

// CycleSince is when the whole cycle last came into being: the latest Since of its waits.
func (f *FleetMap) CycleSince(cycle []string) time.Time {
	var latest time.Time
	for _, r := range cycle {
		if w := f.waits[r]; w.Since.After(latest) {
			latest = w.Since
		}
	}
	return latest
}

// DetectDeadlocks returns one event per cycle not reported before and forgets dissolved cycles,
// so a cycle that forms again is reported again.
func (f *FleetMap) DetectDeadlocks(at time.Time, newID func() string) []DeadlockDetected {
	current := map[string]bool{}
	var evs []DeadlockDetected
	for _, cycle := range f.Cycles() {
		k := strings.Join(cycle, ",")
		current[k] = true
		if f.reported[k] {
			continue
		}
		nodes := make([]string, len(cycle))
		for i, r := range cycle {
			nodes[i] = f.waits[r].Node
		}
		evs = append(evs, DeadlockDetected{DeadlockID: newID(), Robots: cycle, Nodes: nodes, At: at})
	}
	f.reported = current
	return evs
}
