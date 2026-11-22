package http_test

import (
	"reflect"
	"strings"
	"testing"

	ahttp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/http"
	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/memory"
	"github.com/IQVO/amr-fleet-control/internal/application/ports"
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
	ahttp.NewIdempotency(memory.NewIdempotency(s), unitOfWorkOf{memory.NewUnitOfWork(s)}, memory.NewClock(t0))
}

// ruling P3-R15: the middleware runs in the Service's own serialized unit of work instance.
func TestNewIdempotencyRunsInTheServicesUnitOfWork(t *testing.T) {
	a := newAPI(t)
	if m := ahttp.NewIdempotency(memory.NewIdempotency(memory.NewStore()), a.svc, memory.NewClock(t0)); m.UoW != a.svc.UnitOfWork() {
		t.Fatalf("UoW = %v, want the Service's %v", m.UoW, a.svc.UnitOfWork())
	}
}

// unitOfWorkOf stands in for the Service where none exists (ruling P3-R15): the replay-class
// template's adapter and the middleware-only tests.
type unitOfWorkOf struct{ uow ports.UnitOfWork }

func (u unitOfWorkOf) UnitOfWork() ports.UnitOfWork { return u.uow }

// ruling P3-R15: the guard binds the INSTANCE, not the type. A second
// usecases.NewSerialUnitOfWork(raw) has the Serialized marker but its own mutex, so the Service's
// decorator never joins it and the ABBA deadlock against the Tick comes back. NewIdempotency
// therefore takes the Service (what hands out its unit of work), never a unit of work.
func TestNewIdempotencyCannotTakeASecondSerialDecorator(t *testing.T) {
	in := reflect.TypeOf(ahttp.NewIdempotency).In(1)
	second := reflect.TypeOf(usecases.NewSerialUnitOfWork(memory.NewUnitOfWork(memory.NewStore())))
	if second.AssignableTo(in) {
		t.Fatalf("NewIdempotency's second parameter %v accepts a second %v (its own mutex: the ABBA deadlock)", in, second)
	}
	if svc := reflect.TypeOf((*usecases.Service)(nil)); !svc.AssignableTo(in) {
		t.Fatalf("NewIdempotency's second parameter %v does not accept the %v", in, svc)
	}
}
