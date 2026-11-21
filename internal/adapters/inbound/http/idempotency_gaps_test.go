package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ahttp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/http"
	"github.com/IQVO/amr-fleet-control/internal/adapters/outbound/memory"
	"github.com/IQVO/amr-fleet-control/internal/application/idempotency"
	"github.com/IQVO/amr-fleet-control/internal/application/ports"
	"github.com/IQVO/amr-fleet-control/internal/application/usecases"
)

// carry (Task 14 review): kills M1 (path dropped from the fingerprint): the same key on another leg's cancellation is
// another request (409), never a replay of the first leg's answer.
func TestTheSameKeyOnAnotherPathIsReused(t *testing.T) {
	a := newAPI(t, "R1", "R2")
	l1 := decode(t, a.do("POST", "/fleets/AMR1/legs", legBody, "k1"))["leg_id"].(string)
	l2 := decode(t, a.do("POST", "/fleets/AMR1/legs", strings.Replace(legBody, "C1", "C2", 1), "k2"))["leg_id"].(string)
	if rec := a.do("POST", "/fleets/AMR1/legs/"+l1+"/cancellation", "", "c"); rec.Code != http.StatusAccepted {
		t.Fatalf("cancel l1 = %d %s", rec.Code, rec.Body)
	}
	rec := a.do("POST", "/fleets/AMR1/legs/"+l2+"/cancellation", "", "c")
	if rec.Code != http.StatusConflict || decode(t, rec)["code"] != "idempotency-key-reused" {
		t.Fatalf("same key, other leg = %d replayed=%q %s, want 409 idempotency-key-reused", rec.Code, rec.Header().Get("Idempotency-Replayed"), rec.Body)
	}
}

// carry (Task 14 review): kills M6 (key length bound): 128 characters is a key, 129 is refused.
func TestTheIdempotencyKeyIsAtMost128Characters(t *testing.T) {
	a := newAPI(t, "R1")
	if rec := a.do("POST", "/fleets/AMR1/legs", legBody, strings.Repeat("é", 129)); rec.Code != http.StatusBadRequest || decode(t, rec)["code"] != "idempotency-key-required" {
		t.Fatalf("129-character key = %d %s", rec.Code, rec.Body)
	}
	if rec := a.do("POST", "/fleets/AMR1/legs", legBody, strings.Repeat("é", 128)); rec.Code != http.StatusAccepted {
		t.Fatalf("128-character key = %d %s", rec.Code, rec.Body)
	}
}

type countingHandler struct{ calls int }

func (h *countingHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.calls++
	w.WriteHeader(http.StatusCreated)
}

func postTo(h http.Handler, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/fleets/AMR1/legs", strings.NewReader(`{"n":1}`))
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// carry (Task 14 review): kills M4/M5 (the 24 h window): replayed just inside 24 h, executed again just after it.
func TestAnAnswerIsReplayedFor24HoursOnly(t *testing.T) {
	s := memory.NewStore()
	clock := memory.NewClock(t0)
	h := &countingHandler{}
	mw := ahttp.NewIdempotency(memory.NewIdempotency(s), unitOfWorkOf{usecases.NewSerialUnitOfWork(memory.NewUnitOfWork(s))}, clock).Middleware(h)
	_ = postTo(mw, "k")
	clock.Advance(24*time.Hour - time.Second)
	if rec := postTo(mw, "k"); rec.Header().Get("Idempotency-Replayed") != "true" || h.calls != 1 {
		t.Fatalf("at 24h-1s: replayed=%q calls=%d, want a replay", rec.Header().Get("Idempotency-Replayed"), h.calls)
	}
	clock.Advance(2 * time.Second)
	if rec := postTo(mw, "k"); rec.Header().Get("Idempotency-Replayed") == "true" || h.calls != 2 {
		t.Fatalf("at 24h+1s: replayed=%q calls=%d, want a fresh execution", rec.Header().Get("Idempotency-Replayed"), h.calls)
	}
}

// racingStore stores a competing 201 under the same key and fingerprint just before the FINAL
// refusal's own Save, as a concurrent duplicate that won the race would.
type racingStore struct {
	ports.IdempotencyStore
	raced bool
}

func (s *racingStore) Save(ctx context.Context, rec idempotency.IdempotencyRecord, nb time.Time) error {
	if rec.Status >= 400 && !s.raced {
		s.raced = true
		w := rec
		w.Status, w.Body, w.CreatedAt = http.StatusCreated, []byte(`{"body":"e30="}`), rec.CreatedAt.Add(-time.Microsecond)
		if err := s.IdempotencyStore.Save(ctx, w, nb); err != nil {
			return err
		}
	}
	return s.IdempotencyStore.Save(ctx, rec, nb)
}

// carry (Task 14 review): kills M8 (the lost-race re-read of a FINAL refusal): the stored winner is replayed.
func TestAFinalRefusalThatLostTheRaceReplaysTheWinner(t *testing.T) {
	s := memory.NewStore()
	store := &racingStore{IdempotencyStore: memory.NewIdempotency(s)}
	refuse := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ahttp.Refuse(w, r, "bad-request") })
	mw := ahttp.NewIdempotency(store, unitOfWorkOf{usecases.NewSerialUnitOfWork(memory.NewUnitOfWork(s))}, memory.NewClock(t0)).Middleware(refuse)
	if rec := postTo(mw, "k"); rec.Code != http.StatusCreated || rec.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("lost race = %d replayed=%q %s, want the winner's replayed 201", rec.Code, rec.Header().Get("Idempotency-Replayed"), rec.Body)
	}
}
