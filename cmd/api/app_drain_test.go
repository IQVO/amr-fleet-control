package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// carry (Task 16 review): the loops are told to stop WHILE HTTP drains (not after): an in-flight
// request still running sees the loop's cancel; kills "stop() after Shutdown".
func TestTheLoopsStopWhileHTTPDrains(t *testing.T) {
	ev := &journal{}
	loopStopped, inFlight, seen := make(chan struct{}), make(chan struct{}), make(chan bool, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(inFlight)
		select {
		case <-loopStopped:
			seen <- true
		case <-time.After(3 * time.Second):
			seen <- false
		}
		w.WriteHeader(http.StatusOK)
	})
	a := testApp(ev, handler, func(ctx context.Context) { <-ctx.Done(); close(loopStopped) })
	ln := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(a, ctx, ln)
	go func() {
		if resp, err := http.Get("http://" + ln.Addr().String() + "/"); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-inFlight
	cancel()
	if !<-seen {
		t.Fatal("the loops were not told to stop while a request drained")
	}
	if err := waitRun(t, done); err != nil {
		t.Fatal(err)
	}
}

// carry (Task 16 review): the HTTP drain is bounded by shutdownTimeout: a request that never
// finishes does not hold the shutdown; run returns the drain's deadline error after the loops
// stopped and every closer ran. Kills "Shutdown without a deadline".
func TestTheHTTPDrainIsBoundedByShutdownTimeout(t *testing.T) {
	old := shutdownTimeout
	shutdownTimeout = 200 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = old })
	ev := &journal{}
	inFlight, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(inFlight)
		select { // a request stuck far past the drain bound
		case <-release:
		case <-time.After(10 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	})
	a := testApp(ev, handler, func(ctx context.Context) { <-ctx.Done(); ev.add("loop stopped") })
	ln := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(a, ctx, ln)
	go func() {
		if resp, err := http.Get("http://" + ln.Addr().String() + "/"); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-inFlight
	start := time.Now()
	cancel()
	var err error
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("run did not return within 3 s of the cancel with a 200 ms drain bound (events %v)", ev.list())
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("run returned %s after the cancel, want about the 200 ms drain bound", took)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("run = %v, want the drain's context.DeadlineExceeded", err)
	}
	if got := ev.list(); len(got) != 4 || got[0] != "loop stopped" || got[3] != "close pool" {
		t.Fatalf("events = %v, want the loop stopped then the three closers", got)
	}
}
