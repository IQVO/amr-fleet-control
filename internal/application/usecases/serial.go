package usecases

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/IQVO/amr-fleet-control/internal/application/ports"
)

// SerialUnitOfWork is a ports.UnitOfWork that runs its OUTERMOST units one at a time. ruling
// P3-R10 (amended; carry (Task 11 review)): a Postgres commit releases the fleet row lock before
// the after-commit hooks hand the robot commands to the Downlink, so without one lock held from
// before the transaction to after its hooks a later unit could enqueue first (a cancel
// overtaking its order on the wire). The lock is taken outside the transaction, so the lock
// order is always mutex -> tx -> fleet row.
//
// Every adapter that opens a unit of work (the link worker through the Service, Task 14's
// Idempotency middleware) MUST be given the SAME decorated unit of work (Service.UnitOfWork):
// an outer unit opened on the inner one would take the transaction before this lock.
type SerialUnitOfWork struct {
	inner ports.UnitOfWork
	mu    sync.Mutex
}

type serialKey struct{}

// serialFrame marks a ctx as running inside one outermost Do of owner, until it returns.
type serialFrame struct {
	owner *SerialUnitOfWork
	done  atomic.Bool
}

// NewSerialUnitOfWork decorates inner.
func NewSerialUnitOfWork(inner ports.UnitOfWork) *SerialUnitOfWork {
	return &SerialUnitOfWork{inner: inner}
}

// Do locks unless ctx already runs inside one of this decorator's open units (a joined unit),
// and holds the lock through inner.Do, its commit and its after-commit hooks included.
func (s *SerialUnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if f, ok := ctx.Value(serialKey{}).(*serialFrame); ok && f.owner == s && !f.done.Load() {
		return s.inner.Do(ctx, fn)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f := &serialFrame{owner: s}
	defer f.done.Store(true)
	return s.inner.Do(context.WithValue(ctx, serialKey{}, f), fn)
}

// AfterCommit is the inner unit of work's.
func (s *SerialUnitOfWork) AfterCommit(ctx context.Context, fn func()) { s.inner.AfterCommit(ctx, fn) }

// serialized wraps u unless it already is a SerialUnitOfWork.
func serialized(u ports.UnitOfWork) *SerialUnitOfWork {
	if s, ok := u.(*SerialUnitOfWork); ok {
		return s
	}
	return NewSerialUnitOfWork(u)
}
