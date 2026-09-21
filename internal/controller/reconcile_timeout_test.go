package controller

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestAReconcileStuckOnAWorkloadReadReturnsWhenItsContextExpires(t *testing.T) {
	var workloadReads atomic.Int32
	var blocking atomic.Bool
	blocking.Store(true)
	f := newReconcileFixture(t, map[string]string{"API_KEY": "rotated"}, blockWorkloadReadsUntilCancelled(&workloadReads, &blocking))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = f.r.Reconcile(ctx, f.req)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the reconcile kept waiting on the workload after its context expired, " +
			"so --reconcile-timeout would not have freed the work queue")
	}
	if workloadReads.Load() == 0 {
		t.Fatal("the reconcile never reached the rollout, so this proved nothing about it")
	}
}

func TestARolloutCancelledByTheTimeoutIsRetriedOnTheNextPass(t *testing.T) {
	var workloadReads atomic.Int32
	var blocking atomic.Bool
	blocking.Store(true)
	f := newReconcileFixture(t, map[string]string{"API_KEY": "rotated"}, blockWorkloadReadsUntilCancelled(&workloadReads, &blocking))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	_, _ = f.r.Reconcile(ctx, f.req)
	cancel()
	if workloadReads.Load() == 0 {
		t.Fatal("the first pass never reached the rollout")
	}

	blocking.Store(false)
	if _, err := f.r.Reconcile(context.Background(), f.req); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if f.restartedAt(t) == "" {
		t.Fatal("the rollout the timeout cancelled was never retried: the pending restart " +
			"was only held in memory, and the status write that would have kept it failed " +
			"on the expired context")
	}
}
