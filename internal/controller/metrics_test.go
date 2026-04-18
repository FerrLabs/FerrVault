package controller

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCollectorsRegistered(t *testing.T) {
	// Each vector starts without series; collecting still works and returns 0
	// samples. This also pulls the init() through, which panics if any
	// collector fails to register with the controller-runtime registry.
	if n := testutil.CollectAndCount(SyncDuration); n != 0 {
		t.Fatalf("SyncDuration: unexpected pre-existing samples: %d", n)
	}
	if n := testutil.CollectAndCount(SyncErrors); n != 0 {
		t.Fatalf("SyncErrors: unexpected pre-existing samples: %d", n)
	}
	if n := testutil.CollectAndCount(LastSyncTimestamp); n != 0 {
		t.Fatalf("LastSyncTimestamp: unexpected pre-existing samples: %d", n)
	}
	if n := testutil.CollectAndCount(ConnectionReady); n != 0 {
		t.Fatalf("ConnectionReady: unexpected pre-existing samples: %d", n)
	}
}

func TestObserveReconcileRecordsSample(t *testing.T) {
	begin := time.Now().Add(-50 * time.Millisecond)
	ObserveReconcile(begin, "success")

	// Histograms report one series per label set once observed.
	if n := testutil.CollectAndCount(SyncDuration); n != 1 {
		t.Fatalf("expected 1 series after observe, got %d", n)
	}
	// Cleanup so other tests aren't polluted by process-wide state.
	t.Cleanup(func() { SyncDuration.Reset() })
}

func TestIncSyncError(t *testing.T) {
	t.Cleanup(func() { SyncErrors.Reset() })

	IncSyncError("AuthFailed")
	if got := testutil.ToFloat64(SyncErrors.WithLabelValues("AuthFailed")); got != 1 {
		t.Fatalf("AuthFailed counter = %v, want 1", got)
	}
	IncSyncError("AuthFailed")
	if got := testutil.ToFloat64(SyncErrors.WithLabelValues("AuthFailed")); got != 2 {
		t.Fatalf("AuthFailed counter = %v, want 2", got)
	}
	// Unrelated label stays zero.
	if got := testutil.ToFloat64(SyncErrors.WithLabelValues("MissingKeys")); got != 0 {
		t.Fatalf("MissingKeys counter = %v, want 0", got)
	}
}

func TestLastSyncTimestampSetAndDelete(t *testing.T) {
	t.Cleanup(func() { LastSyncTimestamp.Reset() })

	SetLastSyncTimestamp("ns", "name")
	if got := testutil.ToFloat64(LastSyncTimestamp.WithLabelValues("ns", "name")); got <= 0 {
		t.Fatalf("timestamp = %v, want > 0", got)
	}
	if n := testutil.CollectAndCount(LastSyncTimestamp); n != 1 {
		t.Fatalf("expected 1 series, got %d", n)
	}

	DeleteLastSyncTimestamp("ns", "name")
	if n := testutil.CollectAndCount(LastSyncTimestamp); n != 0 {
		t.Fatalf("expected 0 series after delete, got %d", n)
	}
}

func TestSetConnectionReady(t *testing.T) {
	t.Cleanup(func() { ConnectionReady.Reset() })

	SetConnectionReady("ns", "conn", true)
	if got := testutil.ToFloat64(ConnectionReady.WithLabelValues("ns", "conn")); got != 1 {
		t.Fatalf("ready gauge (true) = %v, want 1", got)
	}
	SetConnectionReady("ns", "conn", false)
	if got := testutil.ToFloat64(ConnectionReady.WithLabelValues("ns", "conn")); got != 0 {
		t.Fatalf("ready gauge (false) = %v, want 0", got)
	}
}
