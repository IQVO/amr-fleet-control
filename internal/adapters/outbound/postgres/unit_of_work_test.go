package postgres

import (
	"context"
	"testing"
)

func TestAfterCommitOutsideAUnitOfWorkRunsAtOnce(t *testing.T) {
	ran := 0
	NewUnitOfWork(nil).AfterCommit(context.Background(), func() { ran++ })
	if ran != 1 {
		t.Fatalf("hook ran %d times, want 1", ran)
	}
}

func TestAfterCommitInsideAnOpenUnitOfWorkWaits(t *testing.T) {
	h := &hooks{}
	ctx := context.WithValue(context.Background(), hooksKey{}, h)
	ran := 0
	NewUnitOfWork(nil).AfterCommit(ctx, func() { ran++ })
	NewUnitOfWork(nil).AfterCommit(ctx, func() { ran++ })
	if ran != 0 || len(h.fns) != 2 {
		t.Fatalf("ran %d, queued %d; want 0 and 2", ran, len(h.fns))
	}
}
