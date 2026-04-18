package controller

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Custom Prometheus collectors for the FerrFlow controllers. Registered with
// controller-runtime's shared registry so they show up on the manager's
// existing :8080/metrics endpoint — no separate HTTP server needed.
var (
	// SyncDuration records wall-clock reconcile latency for FerrFlowSecret,
	// partitioned by outcome. Default buckets are fine: reconciles are
	// dominated by one HTTP round-trip to the FerrFlow API, not sub-ms work.
	SyncDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ferrflow_secret_sync_duration_seconds",
			Help:    "Duration of FerrFlowSecret reconciles, labelled by result (success/failure).",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"result"},
	)

	// SyncErrors counts failed reconciles by the Reason stamped on the Ready
	// condition — same vocabulary users already see in `kubectl describe`, so
	// alerts and dashboards line up with CR status.
	SyncErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ferrflow_secret_sync_errors_total",
			Help: "FerrFlowSecret reconcile failures, labelled by reason.",
		},
		[]string{"reason"},
	)

	// LastSyncTimestamp is the unix time of the most recent successful sync
	// per CR. Useful for "nothing synced in the last hour" style alerts.
	LastSyncTimestamp = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ferrflow_secret_last_sync_timestamp_seconds",
			Help: "Unix timestamp of the last successful FerrFlowSecret sync.",
		},
		[]string{"namespace", "name"},
	)

	// ConnectionReady is 1 when a FerrFlowConnection's Ready condition is
	// True, 0 otherwise.
	ConnectionReady = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ferrflow_connection_ready",
			Help: "Whether a FerrFlowConnection is Ready (1) or not (0).",
		},
		[]string{"namespace", "name"},
	)
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		SyncDuration,
		SyncErrors,
		LastSyncTimestamp,
		ConnectionReady,
	)
}

// ObserveReconcile records the elapsed time since `begin` under the given
// result label. Intended for use in a deferred closure where the caller
// flips `result` between "success" and "failure" as branches return.
func ObserveReconcile(begin time.Time, result string) {
	SyncDuration.WithLabelValues(result).Observe(time.Since(begin).Seconds())
}

// IncSyncError bumps the failure counter for the given reason.
func IncSyncError(reason string) {
	SyncErrors.WithLabelValues(reason).Inc()
}

// SetLastSyncTimestamp stamps the current time on the per-CR gauge.
func SetLastSyncTimestamp(namespace, name string) {
	LastSyncTimestamp.WithLabelValues(namespace, name).SetToCurrentTime()
}

// DeleteLastSyncTimestamp drops the series for a CR that no longer exists,
// so the gauge doesn't leak labels forever.
func DeleteLastSyncTimestamp(namespace, name string) {
	LastSyncTimestamp.DeleteLabelValues(namespace, name)
}

// SetConnectionReady sets the ready gauge to 1 or 0.
func SetConnectionReady(namespace, name string, ready bool) {
	v := 0.0
	if ready {
		v = 1.0
	}
	ConnectionReady.WithLabelValues(namespace, name).Set(v)
}

// DeleteConnectionReady drops the gauge series for a deleted connection.
func DeleteConnectionReady(namespace, name string) {
	ConnectionReady.DeleteLabelValues(namespace, name)
}
