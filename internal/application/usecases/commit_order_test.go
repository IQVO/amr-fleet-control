package usecases_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/ports"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
)

type lateKey struct{}

type lateFrame struct{ hooks []func() }

// lateHooks is a unit of work that, like Postgres, runs its AfterCommit hooks only after the
// commit released the fleet row lock. Once armed, the first unit's hooks wait (up to hold) for
// a second unit to commit and send, which is the interleaving ruling P3-R10 forbids.
type lateHooks struct {
	inner  ports.UnitOfWork
	hold   time.Duration
	mu     sync.Mutex
	armed  bool
	units  int
	first  chan struct{} // closed once the first armed unit committed
	second chan struct{} // closed once a later armed unit ran its hooks
}

func (l *lateHooks) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	f := &lateFrame{}
	if err := l.inner.Do(context.WithValue(ctx, lateKey{}, f), fn); err != nil {
		return err
	}
	l.mu.Lock()
	armed := l.armed
	l.units++
	n := l.units
	l.mu.Unlock()
	if armed && n == 1 {
		close(l.first)
		select {
		case <-l.second:
		case <-time.After(l.hold):
		}
	}
	for _, h := range f.hooks {
		h()
	}
	if armed && n == 2 {
		close(l.second)
	}
	return nil
}

func (l *lateHooks) AfterCommit(ctx context.Context, fn func()) {
	if f, ok := ctx.Value(lateKey{}).(*lateFrame); ok {
		f.hooks = append(f.hooks, fn)
		return
	}
	fn()
}

func (l *lateHooks) arm() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.armed, l.units = true, 0
}

// seqDownlink records every command in call (enqueue) order.
type seqDownlink struct {
	ports.Downlink
	mu  sync.Mutex
	got []string
}

func (d *seqDownlink) SendOrder(ctx context.Context, o vda.Order) error {
	d.add("order " + o.OrderID)
	return d.Downlink.SendOrder(ctx, o)
}

func (d *seqDownlink) SendInstantActions(ctx context.Context, a vda.InstantActions) error {
	for _, x := range a.Actions {
		d.add(x.Type + " " + x.ID)
	}
	return d.Downlink.SendInstantActions(ctx, a)
}

func (d *seqDownlink) add(s string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.got = append(d.got, s)
}

// ruling P3-R10: T0 commits a leg and its order; T1, committing after T0, cancels that leg. The
// commands reach the Downlink (the link's FIFO) in commit order: order, then cancelOrder, even
// when T0's after-commit hooks run late.
func TestCommandsReachTheDownlinkInCommitOrder(t *testing.T) {
	late := &lateHooks{hold: 300 * time.Millisecond, first: make(chan struct{}), second: make(chan struct{})}
	seq := &seqDownlink{}
	h := newHarnessWith(t, floor(t), nil, func(d *usecases.Deps) {
		late.inner, d.UoW = d.UoW, late
		seq.Downlink, d.Downlink = d.Downlink, seq
	})
	svc := h.svc
	h.online("R1", "P1", 90)
	late.arm()
	t0 := make(chan usecases.LegView, 1)
	go func() {
		v, err := svc.RequestLeg(ctx, usecases.LegRequest{FleetID: "AMR1", ContainerID: "C1", From: "ST1", To: "ASRS1-IN1"})
		if err != nil {
			t.Error(err)
		}
		t0 <- v
	}()
	<-late.first
	open, err := h.missions.Open(ctx)
	if err != nil || len(open) != 1 {
		t.Fatalf("open legs after T0 committed = %d, %v", len(open), err)
	}
	legID := open[0].ID()
	if _, _, err := svc.CancelLeg(ctx, "AMR1", legID); err != nil {
		t.Fatal(err)
	}
	<-t0
	seq.mu.Lock()
	defer seq.mu.Unlock()
	if len(seq.got) != 2 || seq.got[0] != "order "+legID || seq.got[1] != "cancelOrder "+legID+"-cancel" {
		t.Fatalf("downlink order = %v, want the order of %s then its cancelOrder", seq.got, legID)
	}
}
