package mission_test

import (
	"testing"
	"time"
)

// carry (Task 8 review): a CANCEL_REQUESTED leg's SentAt and Resends count its cancelOrder
// messages, so the use cases re-send an unanswered cancel and bound it like an order (D19).
func TestRequestCancelRestartsTheSendCountForTheCancelOrder(t *testing.T) {
	a := assigned(t)
	a.MarkSent(t0)
	a.Resent(t0.Add(5 * time.Second))
	a.Resent(t0.Add(10 * time.Second))
	at := t0.Add(12 * time.Second)
	if _, _, err := a.RequestCancel(at); err != nil {
		t.Fatal(err)
	}
	if a.Resends() != 0 || !a.SentAt().Equal(at) {
		t.Fatalf("after RequestCancel: resends %d, sent at %v", a.Resends(), a.SentAt())
	}
	if _, _, err := a.RequestCancel(at.Add(time.Second)); err != nil || !a.SentAt().Equal(at) {
		t.Fatalf("a repeated RequestCancel keeps the cancel's send time: %v %v", a.SentAt(), err)
	}
}
