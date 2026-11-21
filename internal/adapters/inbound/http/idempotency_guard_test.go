package http_test

import (
	"strings"
	"testing"

	ahttp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/http"
	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/memory"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
)

// carry (Task 14 brief amendment, ruling P3-R10 amended): the middleware opens the OUTERMOST unit
// of work of every POST, so it must be the Service's serialized one. A raw unit of work takes the
// transaction before the serial lock (ABBA deadlock against the link worker's Tick; after-commit
// robot commands leave the lock and can overtake each other on the wire), so it is refused at
// construction.
func TestNewIdempotencyRefusesAUnitOfWorkThatIsNotSerialized(t *testing.T) {
	s := memory.NewStore()
	defer func() {
		p := recover()
		msg, _ := p.(string)
		if p == nil || !strings.Contains(msg, "Serialized") {
			t.Fatalf("NewIdempotency over the raw memory unit of work: recover() = %v, want a panic naming Serialized", p)
		}
	}()
	ahttp.NewIdempotency(memory.NewIdempotency(s), memory.NewUnitOfWork(s), memory.NewClock(t0))
}

func TestNewIdempotencyAcceptsTheSerialUnitOfWork(t *testing.T) {
	s := memory.NewStore()
	uow := usecases.NewSerialUnitOfWork(memory.NewUnitOfWork(s))
	if m := ahttp.NewIdempotency(memory.NewIdempotency(s), uow, memory.NewClock(t0)); m.UoW != uow {
		t.Fatalf("UoW = %v, want the serial unit of work it was given", m.UoW)
	}
}
