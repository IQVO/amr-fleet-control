package mqtt

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeToken is a paho token whose SUBACK result the test chooses.
type fakeToken struct {
	done   bool
	err    error
	result map[string]byte
}

func (f fakeToken) Wait() bool                     { return f.done }
func (f fakeToken) WaitTimeout(time.Duration) bool { return f.done }
func (f fakeToken) Error() error                   { return f.err }
func (f fakeToken) Result() map[string]byte        { return f.result }
func (f fakeToken) Done() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

// carry (Task 11 review): paho v1.5.1 completes a refused subscription without an error.
func TestARefusedSubackIsAnError(t *testing.T) {
	const f = "vda5050/v3/+/+/factsheet"
	if err := awaitSubscribe(f, fakeToken{done: true, result: map[string]byte{f: 0x80}}, time.Second); err == nil ||
		!strings.Contains(err.Error(), "refused") || !strings.Contains(err.Error(), f) {
		t.Fatalf("refused SUBACK: err = %v", err)
	}
	if err := awaitSubscribe(f, fakeToken{done: true, result: map[string]byte{f: 1}}, time.Second); err != nil {
		t.Fatalf("granted QoS 1: err = %v", err)
	}
	if err := awaitSubscribe(f, fakeToken{done: false}, time.Millisecond); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("no SUBACK: err = %v", err)
	}
	boom := errors.New("boom")
	if err := awaitSubscribe(f, fakeToken{done: true, err: boom}, time.Second); !errors.Is(err, boom) {
		t.Fatalf("token error: err = %v", err)
	}
}

func quietLink() *FleetLink {
	return newFleetLink(&recUplink{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// carry (Task 11 review): the first round's errors reach NewFleetLink (errors.Join).
func TestTheFirstRoundReportsEverySubscribeError(t *testing.T) {
	l := quietLink()
	go l.connected(func(f string) error {
		if strings.HasSuffix(f, "/factsheet") || strings.HasSuffix(f, "/state") {
			return errors.New("refused " + f)
		}
		return nil
	}, func() bool { return true })
	err := awaitFirstRound(l.firstRound, 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "refused vda5050/v3/+/+/factsheet") ||
		!strings.Contains(err.Error(), "refused vda5050/v3/+/+/state") {
		t.Fatalf("first round err = %v", err)
	}
}

// carry (Task 11 review): the subscribeWait timeout path of NewFleetLink.
func TestTheFirstRoundWaitTimesOut(t *testing.T) {
	err := awaitFirstRound(make(chan error), 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no subscription round within 20ms") {
		t.Fatalf("err = %v", err)
	}
	ok := make(chan error, 1)
	ok <- nil
	if err := awaitFirstRound(ok, time.Second); err != nil {
		t.Fatalf("a clean round: err = %v", err)
	}
}

// flaky refuses a filter fails times, then grants it.
type flaky struct {
	mu       sync.Mutex
	suffix   string
	fails    int
	attempts int
}

func (f *flaky) sub(filter string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasSuffix(filter, f.suffix) {
		return nil
	}
	f.attempts++
	if f.attempts <= f.fails {
		return errors.New("refused " + filter)
	}
	return nil
}

// carry (Task 11 review): a later round retries a failed filter every retryEvery while connected.
func TestALaterRoundRetriesAFailedFilterWhileConnected(t *testing.T) {
	l := quietLink()
	l.retryEvery = time.Millisecond
	l.connected(func(string) error { return nil }, func() bool { return true })
	if err := awaitFirstRound(l.firstRound, time.Second); err != nil {
		t.Fatal(err)
	}
	f := &flaky{suffix: "/state", fails: 2}
	l.connected(f.sub, func() bool { return true })
	if f.attempts != 3 {
		t.Fatalf("attempts on the failed filter = %d, want 3 (one per round plus two retries)", f.attempts)
	}
	down := &flaky{suffix: "/state", fails: 100}
	l.connected(down.sub, func() bool { return false })
	if down.attempts != 1 {
		t.Fatalf("attempts while disconnected = %d, want 1 (no retry once the connection closed)", down.attempts)
	}
}

type fakeDisconnect struct{ n int }

func (f *fakeDisconnect) Disconnect(uint) { f.n++ }

// carry (Task 11 review): NewFleetLink's tail: a refused first round and the subscribeWait
// timeout both disconnect and fail; a clean round keeps the client.
func TestNewFleetLinkFailsAndDisconnectsOnAFailedOrMissingFirstRound(t *testing.T) {
	refused := quietLink()
	go refused.connected(func(f string) error {
		return awaitSubscribe(f, fakeToken{done: true, result: map[string]byte{f: 0x80}}, time.Second)
	}, func() bool { return true })
	c := &fakeDisconnect{}
	if err := refused.awaitSubscribed(c, 2*time.Second); err == nil || !strings.Contains(err.Error(), "SUBACK 0x80") || c.n != 1 {
		t.Fatalf("refused round: err = %v, disconnects = %d", err, c.n)
	}
	silent, c := quietLink(), &fakeDisconnect{}
	if err := silent.awaitSubscribed(c, 20*time.Millisecond); err == nil || !strings.Contains(err.Error(), "no subscription round") || c.n != 1 {
		t.Fatalf("no round: err = %v, disconnects = %d", err, c.n)
	}
	clean, c := quietLink(), &fakeDisconnect{}
	go clean.connected(func(string) error { return nil }, func() bool { return true })
	if err := clean.awaitSubscribed(c, 2*time.Second); err != nil || c.n != 0 {
		t.Fatalf("clean round: err = %v, disconnects = %d", err, c.n)
	}
}

// carry (Task 11 review): a reconnect during a retry sleep starts a new round, and the old
// round's retry loop stops instead of retrying next to the new one.
func TestARetryLoopStopsOnceANewRoundStarted(t *testing.T) {
	l := quietLink()
	l.retryEvery = time.Millisecond
	l.connected(func(string) error { return nil }, func() bool { return true })
	if err := awaitFirstRound(l.firstRound, time.Second); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	attempts := 0
	failing := func(string) error {
		mu.Lock()
		defer mu.Unlock()
		attempts++
		if attempts == 5 { // the round's 3 filters, then 2 retries
			l.connects.Add(1) // the connection came back: a new round took over
		}
		return errors.New("subscribe refused")
	}
	done := make(chan struct{})
	go func() {
		l.connected(failing, func() bool { return true })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("the old round kept retrying after a new round started (%d attempts)", attempts)
	}
}
