package mqtt

import "testing"

// carry (Task 11 review): connection and factsheet are subscribed before state, so each robot's
// retained replay reaches the worker before its live state.
func TestUplinkTopicsAreSubscribedConnectionFactsheetThenState(t *testing.T) {
	var got []string
	if _, err := subscribeAll(func(f string) error {
		got = append(got, f)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"vda5050/v3/+/+/connection", "vda5050/v3/+/+/factsheet", "vda5050/v3/+/+/state"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("subscription order = %v, want %v", got, want)
	}
}
