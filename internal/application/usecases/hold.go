package usecases

import (
	"math"

	"github.com/IQVO/amr-fleet-control/internal/domain/robot"
)

// underway lists what a robot keeps beyond its node and its mission's base while it is driving or
// stands away from its lastNodeId (cancelOrder stops a robot where it is, possibly mid-edge): every
// edge it still holds that leaves its node and passes within NODE_TOLERANCE_METRES of its
// position, and that edge's far node (decision D7).
//
// carry (Task 8 review): before this, a robot stopped mid-edge by a cancel kept only its lastNodeId.
// carry (Task 9 review): hold calls it only for a robot without an active mission.
func (u *unit) underway(r *robot.Robot) (nodes, edges []string) {
	at, ok := u.s.m.Node(r.Node())
	rep := r.Report()
	if !ok || (!rep.Driving && math.Hypot(rep.X-at.X, rep.Y-at.Y) <= u.s.cfg.NodeTolerance) {
		return nil, nil
	}
	_, held := u.fm.Held(r.ID().Key())
	for _, id := range held {
		e, _ := u.s.m.Edge(id)
		far := e.B
		if e.B == at.ID {
			far = e.A
		} else if e.A != at.ID {
			continue
		}
		to, _ := u.s.m.Node(far)
		if segmentDistance(rep.X, rep.Y, at.X, at.Y, to.X, to.Y) <= u.s.cfg.NodeTolerance {
			nodes, edges = append(nodes, far), append(edges, id)
		}
	}
	return nodes, edges
}

// segmentDistance is the distance from (px, py) to the segment (ax, ay)-(bx, by).
func segmentDistance(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	l2 := dx*dx + dy*dy
	f := 0.0
	if l2 > 0 {
		f = math.Max(0, math.Min(1, ((px-ax)*dx+(py-ay)*dy)/l2))
	}
	return math.Hypot(px-(ax+f*dx), py-(ay+f*dy))
}
