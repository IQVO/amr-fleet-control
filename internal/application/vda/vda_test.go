package vda_test

import (
	"testing"

	"github.com/IQVO/amr-fleet-control/internal/application/vda"
)

func TestSequenceIDsAreEvenForNodesAndOddForEdges(t *testing.T) {
	for i, want := range []uint32{0, 2, 4, 6} {
		if got := vda.NodeSeq(i); got != want {
			t.Fatalf("NodeSeq(%d) = %d", i, got)
		}
	}
	for i, want := range map[int]uint32{1: 1, 2: 3, 3: 5} {
		if got := vda.EdgeSeq(i); got != want {
			t.Fatalf("EdgeSeq(%d) = %d", i, got)
		}
	}
	if i, ok := vda.IndexOfSeq(6); !ok || i != 3 {
		t.Fatalf("IndexOfSeq(6) = %d %v", i, ok)
	}
	if _, ok := vda.IndexOfSeq(5); ok {
		t.Fatal("an edge sequence id is not a node index")
	}
	if i, ok := vda.IndexOfSeq(0); !ok || i != 0 {
		t.Fatalf("IndexOfSeq(0) = %d %v", i, ok)
	}
}
