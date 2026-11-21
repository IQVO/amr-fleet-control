package memory

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/IQVO/amr-fleet-control/internal/application/idempotency"
	"github.com/IQVO/amr-fleet-control/internal/application/vda"
)

// Events is the in-memory EventSink: it records the domain events in the unit of work.
type Events struct{ s *Store }

func NewEvents(s *Store) *Events { return &Events{s: s} }

func (e *Events) Append(_ context.Context, events ...any) error {
	e.s.with(func(t *tables) { t.events = append(t.events, events...) })
	return nil
}

// All returns every committed event in order.
func (e *Events) All() []any {
	var out []any
	e.s.with(func(t *tables) { out = append(out, t.events...) })
	return out
}

// Idempotency is the in-memory ports.IdempotencyStore.
type Idempotency struct{ s *Store }

func NewIdempotency(s *Store) *Idempotency { return &Idempotency{s: s} }

func (r *Idempotency) Lookup(_ context.Context, key string, notBefore time.Time) (idempotency.IdempotencyRecord, bool, error) {
	var rec idempotency.IdempotencyRecord
	var ok bool
	r.s.with(func(t *tables) { rec, ok = t.idem[key] })
	if !ok || rec.CreatedAt.Before(notBefore) {
		return idempotency.IdempotencyRecord{}, false, nil
	}
	return rec, true, nil
}

func (r *Idempotency) Save(_ context.Context, rec idempotency.IdempotencyRecord, notBefore time.Time) error {
	r.s.with(func(t *tables) {
		if prev, ok := t.idem[rec.Key]; ok && !prev.CreatedAt.Before(notBefore) {
			return
		}
		t.idem[rec.Key] = rec
	})
	return nil
}

func (r *Idempotency) Purge(_ context.Context, before time.Time) (int64, error) {
	var n int64
	r.s.with(func(t *tables) {
		for k, rec := range t.idem {
			if rec.CreatedAt.Before(before) {
				delete(t.idem, k)
				n++
			}
		}
	})
	return n, nil
}

// Rows is 1 when key has a stored answer, else 0.
func (r *Idempotency) Rows(key string) int {
	n := 0
	r.s.with(func(t *tables) {
		if _, ok := t.idem[key]; ok {
			n = 1
		}
	})
	return n
}

// Clock is a settable clock.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

func NewClock(t time.Time) *Clock { return &Clock{t: t} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// IDs mints prefix1, prefix2, ...
type IDs struct {
	mu     sync.Mutex
	prefix string
	n      int
}

func NewIDs(prefix string) *IDs { return &IDs{prefix: prefix} }

func (i *IDs) NewID() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.n++
	return i.prefix + strconv.Itoa(i.n)
}

// Downlink records every command; Fail makes the next sends answer err.
type Downlink struct {
	mu      sync.Mutex
	orders  []vda.Order
	instant []vda.InstantActions
	err     error
}

func NewDownlink() *Downlink { return &Downlink{} }

func (d *Downlink) SendOrder(_ context.Context, o vda.Order) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return d.err
	}
	d.orders = append(d.orders, o)
	return nil
}

func (d *Downlink) SendInstantActions(_ context.Context, a vda.InstantActions) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return d.err
	}
	d.instant = append(d.instant, a)
	return nil
}

func (d *Downlink) Fail(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.err = err
}

func (d *Downlink) Orders() []vda.Order {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]vda.Order(nil), d.orders...)
}

func (d *Downlink) Instant() []vda.InstantActions {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]vda.InstantActions(nil), d.instant...)
}

// Metrics records "<outcome>/<reason>" per ended leg.
type Metrics struct {
	mu    sync.Mutex
	ended []string
}

func NewMetrics() *Metrics { return &Metrics{} }

func (m *Metrics) LegEnded(_ context.Context, outcome, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ended = append(m.ended, outcome+"/"+reason)
}

func (m *Metrics) Ended() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.ended...)
}
