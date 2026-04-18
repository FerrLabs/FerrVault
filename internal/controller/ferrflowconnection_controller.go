// Package controller — FerrFlowConnection reconciler.
//
// Probes the configured FerrFlow API URL on a timer and reflects the outcome
// in `status.conditions[type=Ready]` + `status.lastCheckedAt`. Useful on its
// own (users debugging a freshly-applied connection can `kubectl get ffc`
// without creating a FerrFlowSecret first) and as a backstop — when the
// token Secret is rotated, the watch here re-reconciles immediately instead
// of waiting for the next scheduled poll.
package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ffv1alpha1 "github.com/FerrFlow-Org/FerrFlow-Operator/api/v1alpha1"
	"github.com/FerrFlow-Org/FerrFlow-Operator/internal/ferrflow"
)

// Probe cadence. Ten minutes is a sensible default — reachability doesn't
// usually flip on smaller timescales, and users changing the URL or token
// trigger immediate reconciles via the watches in `SetupWithManager`.
const connectionProbeInterval = 10 * time.Minute

// FerrFlowConnectionReconciler reports whether a FerrFlowConnection's
// configured FerrFlow API instance is reachable and whether the referenced
// token Secret is readable.
type FerrFlowConnectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=ferrflow.io,resources=ferrflowconnections,verbs=get;list;watch
// +kubebuilder:rbac:groups=ferrflow.io,resources=ferrflowconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile probes the connection and updates status. Errors are captured in
// the Ready condition rather than returned, so controller-runtime doesn't
// enter a tight retry loop on a long-lived misconfiguration.
func (r *FerrFlowConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("ferrflowconnection", req.NamespacedName)

	var conn ffv1alpha1.FerrFlowConnection
	if err := r.Get(ctx, req.NamespacedName, &conn); err != nil {
		if apierrors.IsNotFound(err) {
			// Drop the gauge series so deleted connections don't linger.
			DeleteConnectionReady(req.Namespace, req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("load FerrFlowConnection: %w", err)
	}

	status, reason, message := r.probe(ctx, &conn)
	logger.Info("probe finished", "ready", status, "reason", reason)

	now := metav1.Now()
	conn.Status.LastCheckedAt = &now
	setCondition(&conn.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  status,
		Reason:  reason,
		Message: message,
	})
	if err := r.Status().Update(ctx, &conn); err != nil {
		return ctrl.Result{}, fmt.Errorf("update status: %w", err)
	}
	SetConnectionReady(conn.Namespace, conn.Name, status == metav1.ConditionTrue)
	return ctrl.Result{RequeueAfter: connectionProbeInterval}, nil
}

// probe returns `(status, reason, message)` describing the Ready condition
// to stamp on the CR. Ordered checks, first failure wins:
//  1. Token Secret must be readable.
//  2. Base URL must parse.
//  3. Health endpoint must answer 200.
func (r *FerrFlowConnectionReconciler) probe(
	ctx context.Context,
	conn *ffv1alpha1.FerrFlowConnection,
) (metav1.ConditionStatus, string, string) {
	token, err := r.loadTokenForConnection(ctx, conn)
	if err != nil {
		return metav1.ConditionFalse, "TokenUnreadable", err.Error()
	}
	ffc, err := ferrflow.New(conn.Spec.URL, token)
	if err != nil {
		return metav1.ConditionFalse, "InvalidConnection", err.Error()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := ffc.Probe(probeCtx); err != nil {
		return metav1.ConditionFalse, "Unreachable", err.Error()
	}
	// We deliberately don't test the token here — authentication is reported
	// per-vault by the FerrFlowSecret reconciler, which knows the
	// org/project/vault context. This CR only claims "I can see the API".
	return metav1.ConditionTrue, "Reachable", fmt.Sprintf("%s responded to /health", conn.Spec.URL)
}

// loadTokenForConnection resolves the token Secret the connection points at.
// Duplicates FerrFlowSecretReconciler.loadToken intentionally — the
// signatures diverged enough that sharing via a common helper would add more
// noise than it removes.
func (r *FerrFlowConnectionReconciler) loadTokenForConnection(
	ctx context.Context,
	conn *ffv1alpha1.FerrFlowConnection,
) (string, error) {
	var tokenSecret corev1.Secret
	key := types.NamespacedName{
		Namespace: conn.Namespace,
		Name:      conn.Spec.TokenSecretRef.Name,
	}
	if err := r.Get(ctx, key, &tokenSecret); err != nil {
		return "", fmt.Errorf("load token Secret %s: %w", key, err)
	}
	raw, ok := tokenSecret.Data[conn.Spec.TokenSecretRef.Key]
	if !ok {
		return "", fmt.Errorf("key %q missing from token Secret %s",
			conn.Spec.TokenSecretRef.Key, key)
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("token Secret %s has empty value at key %q",
			key, conn.Spec.TokenSecretRef.Key)
	}
	return string(raw), nil
}

// SetupWithManager wires the reconciler and adds a watch on the referenced
// token Secrets so updates flow through immediately. Matches the pattern
// users expect — rotating the token in the Secret reconciles within seconds,
// not when the ten-minute probe tick lands.
func (r *FerrFlowConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Index FerrFlowConnections by the token Secret name so we can find the
	// affected CR(s) cheaply on a Secret update event.
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&ffv1alpha1.FerrFlowConnection{},
		".spec.tokenSecretRef.name",
		func(obj client.Object) []string {
			c := obj.(*ffv1alpha1.FerrFlowConnection)
			return []string{c.Spec.TokenSecretRef.Name}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&ffv1alpha1.FerrFlowConnection{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				var list ffv1alpha1.FerrFlowConnectionList
				if err := r.List(ctx, &list,
					client.InNamespace(obj.GetNamespace()),
					client.MatchingFields{".spec.tokenSecretRef.name": obj.GetName()},
				); err != nil {
					return nil
				}
				reqs := make([]reconcile.Request, 0, len(list.Items))
				for i := range list.Items {
					reqs = append(reqs, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Namespace: list.Items[i].Namespace,
							Name:      list.Items[i].Name,
						},
					})
				}
				return reqs
			}),
			builder.WithPredicates(),
		).
		Named("ferrflowconnection").
		Complete(r)
}

// unused import guard — silence if IDE removes it. `fields` is kept
// intentionally because future PRs will add selector-based listing.
var _ = fields.Everything
