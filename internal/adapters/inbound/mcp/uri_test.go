package mcp

import "testing"

// The fleet URI parse is strict: exactly one non-empty segment after the prefix.
func TestParseFleetURI(t *testing.T) {
	for uri, want := range map[string]string{
		"warehouse://amr-fleet-control/fleets/AMR1":        "AMR1",
		"warehouse://amr-fleet-control/fleets/":            "",
		"warehouse://amr-fleet-control/fleets/AMR1/robots": "",
		"warehouse://asrs-control/fleets/AMR1":             "",
	} {
		got, ok := parseFleetURI(uri)
		if got != want || ok != (want != "") {
			t.Errorf("parseFleetURI(%q) = %q, %v; want %q", uri, got, ok, want)
		}
	}
}
