package http_test

import (
	"testing"

	ahttp "github.com/IQVO/amr-fleet-control/internal/adapters/inbound/http"
)

// The catalogue is the relationship-4 table of plan 03 (plan 04 is written against it).
func TestTheProblemCatalogueIsTheRelationship4Table(t *testing.T) {
	want := map[string]struct {
		status int
		class  string
	}{
		"idempotency-key-required": {400, "FINAL"}, "idempotency-key-reused": {409, "FINAL"},
		"internal-error": {500, "RETRYABLE"}, "dependency-unavailable": {503, "RETRYABLE"},
		"bad-request": {400, "FINAL"}, "invalid-parameter": {400, "FINAL"},
		"fleet-not-found": {404, "FINAL"}, "leg-not-found": {404, "FINAL"}, "robot-not-found": {404, "FINAL"},
		"unknown-handoff-point": {422, "FINAL"}, "handoff-unreachable": {422, "RETRYABLE"},
		"container-busy": {422, "RETRYABLE"}, "fleet-unavailable": {422, "RETRYABLE"},
		"leg-not-cancellable": {409, "RETRYABLE"}, "leg-ended": {409, "RETRYABLE"},
	}
	got := ahttp.Catalogue()
	if len(got) != len(want) {
		t.Fatalf("catalogue has %d entries, want %d: %+v", len(got), len(want), got)
	}
	for _, e := range got {
		w, ok := want[e.Slug]
		if !ok || w.status != e.Status || w.class != e.Class {
			t.Errorf("%s = %d %s, want %+v", e.Slug, e.Status, e.Class, w)
		}
	}
}
