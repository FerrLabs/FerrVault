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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ffv1alpha1 "github.com/FerrLabs/FerrFlow-Operator/api/v1alpha1"
	"github.com/FerrLabs/FerrFlow-Operator/internal/ferrflow"
)

// Probe cadence. Ten minutes is a sensible default — reachability doesn't
// usually flip on smaller timescales, and users changing the URL or token
// trigger immediate reconciles via the watches in `SetupWithManager`.
const connectionProbeInterval = 10 * time.Minute

// ferrFlowConnectionFinalizer blocks deletion until no FerrFlowSecret in the
// same namespace still references this Connection. Without it, `kubectl
// delete ffc` would silently leave every downstream `ffs` with a dangling
// reference that reconciles into `ConnectionNotFound` errors.
const ferrFlowConnectionFinalizer = "ferrflow.io/connection-cleanup"

// connectionInUseRequeue is how often we re-check whether the Connection is
// still in use during a pending-delete. Keep it moderate — users tearing down
// a namespace see the delete complete within a tick of finishing the last ffs.
const connectionInUseRequeue = 30 * time.Second

// FerrFlowConnectionReconciler reports whether a FerrFlowConnection's
// configured FerrFlow API instance is reachable and whether the referenced
// token source (Secret or OIDC exchange) is usable.
type FerrFlowConnectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Broker resolves the bearer for the connection's configured auth
	// mode. Shared with the FerrFlowSecret reconciler so the OIDC cache
	// isn't split. Defaults to a fresh broker if nil.
	Broker *TokenBroker
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

	// Finalizer bookkeeping runs before the probe. A Connection being deleted
	// shouldn't waste cycles probing upstream — the decision we need is
	// strictly local ("are there still FerrFlowSecrets pointing at me?").
	if conn.DeletionTimestamp.IsZero() {
		if controllerutil.AddFinalizer(&conn, ferrFlowConnectionFinalizer) {
			if err := r.Update(ctx, &conn); err != nil {
				return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
			}
			return ctrl.Result{}, nil
		}
	} else {
		return r.handleDelete(ctx, &conn)
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

// handleDelete runs during the pending-delete phase: refuses to remove the
// finalizer while any FerrFlowSecret in the same namespace still references
// this Connection. The alternative — silently completing the delete — leaves
// every downstream `ffs` reconciling into `ConnectionNotFound` errors, which
// is the failure mode issue #39 was opened against.
//
// When the Connection is still in use, we stamp a user-visible condition on
// the CR so `kubectl get ffc` surfaces *why* the delete is stuck.
func (r *FerrFlowConnectionReconciler) handleDelete(
	ctx context.Context,
	conn *ffv1alpha1.FerrFlowConnection,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("ferrflowconnection", client.ObjectKeyFromObject(conn))

	// Fast path: nothing references us any more → finalize.
	var dependants ffv1alpha1.FerrFlowSecretList
	if err := r.List(ctx, &dependants,
		client.InNamespace(conn.Namespace),
		client.MatchingFields{connectionRefIndexKey: conn.Name},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("list dependants: %w", err)
	}

	if len(dependants.Items) == 0 {
		logger.Info("no dependants remain, removing finalizer")
		DeleteConnectionReady(conn.Namespace, conn.Name)
		if controllerutil.RemoveFinalizer(conn, ferrFlowConnectionFinalizer) {
			if err := r.Update(ctx, conn); err != nil {
				return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Still in use. Name a few of the blockers in the condition so a user
	// running `kubectl describe ffc` can see exactly what's blocking them.
	names := make([]string, 0, len(dependants.Items))
	for i := range dependants.Items {
		names = append(names, dependants.Items[i].Name)
		if len(names) == 5 {
			break
		}
	}
	suffix := ""
	if len(dependants.Items) > len(names) {
		suffix = fmt.Sprintf(" (and %d more)", len(dependants.Items)-len(names))
	}
	message := fmt.Sprintf(
		"cannot delete: still referenced by %d FerrFlowSecret(s): %v%s",
		len(dependants.Items), names, suffix,
	)
	logger.Info("delete blocked", "dependants", len(dependants.Items))

	now := metav1.Now()
	conn.Status.LastCheckedAt = &now
	setCondition(&conn.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  "DeletionBlocked",
		Message: message,
	})
	if err := r.Status().Update(ctx, conn); err != nil {
		return ctrl.Result{}, fmt.Errorf("update status: %w", err)
	}
	return ctrl.Result{RequeueAfter: connectionInUseRequeue}, nil
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
	broker := r.Broker
	if broker == nil {
		broker = NewTokenBroker(r.Client)
	}
	token, err := broker.TokenFor(ctx, conn)
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

// Legacy note: the old `loadTokenForConnection` helper lived here. It was
// folded into the `TokenBroker` so both reconcilers share one resolver +
// one OIDC cache. See `token_broker.go`.

// SetupWithManager wires the reconciler and adds a watch on the referenced
// token Secrets so updates flow through immediately. Matches the pattern
// users expect — rotating the token in the Secret reconciles within seconds,
// not when the ten-minute probe tick lands.
func (r *FerrFlowConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Index FerrFlowConnections by the token Secret name so we can find the
	// affected CR(s) cheaply on a Secret update event. Connections in OIDC
	// mode have no token Secret reference and are excluded from the index —
	// they reconcile on the Connection itself, not on a Secret watch.
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&ffv1alpha1.FerrFlowConnection{},
		".spec.tokenSecretRef.name",
		func(obj client.Object) []string {
			c := obj.(*ffv1alpha1.FerrFlowConnection)
			if c.Spec.TokenSecretRef == nil {
				return nil
			}
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
