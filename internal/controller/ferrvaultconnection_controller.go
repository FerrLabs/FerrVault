package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	fvv1alpha1 "github.com/FerrLabs/FerrFlow-Operator/api/ferrvault/v1alpha1"
	ffv1alpha1 "github.com/FerrLabs/FerrFlow-Operator/api/v1alpha1"
	"github.com/FerrLabs/FerrFlow-Operator/internal/ferrflow"
)

const fvConnectionFinalizer = "ferrvault.io/connection-cleanup"

type FerrVaultConnectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Broker *TokenBroker
}

// +kubebuilder:rbac:groups=ferrvault.io,resources=ferrvaultconnections,verbs=get;list;watch
// +kubebuilder:rbac:groups=ferrvault.io,resources=ferrvaultconnections/status,verbs=get;update;patch

func (r *FerrVaultConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("ferrvaultconnection", req.NamespacedName)

	var conn fvv1alpha1.FerrVaultConnection
	if err := r.Get(ctx, req.NamespacedName, &conn); err != nil {
		if apierrors.IsNotFound(err) {
			DeleteConnectionReady(req.Namespace, req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("load FerrVaultConnection: %w", err)
	}

	if conn.DeletionTimestamp.IsZero() {
		if controllerutil.AddFinalizer(&conn, fvConnectionFinalizer) {
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

func (r *FerrVaultConnectionReconciler) handleDelete(
	ctx context.Context,
	conn *fvv1alpha1.FerrVaultConnection,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("ferrvaultconnection", client.ObjectKeyFromObject(conn))

	var dependants fvv1alpha1.FerrVaultSecretList
	if err := r.List(ctx, &dependants,
		client.InNamespace(conn.Namespace),
		client.MatchingFields{fvSecretConnectionRefIndexKey: conn.Name},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("list dependants: %w", err)
	}

	if len(dependants.Items) == 0 {
		logger.Info("no dependants remain, removing finalizer")
		DeleteConnectionReady(conn.Namespace, conn.Name)
		if controllerutil.RemoveFinalizer(conn, fvConnectionFinalizer) {
			if err := r.Update(ctx, conn); err != nil {
				return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

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
		"cannot delete: still referenced by %d FerrVaultSecret(s): %v%s",
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

func (r *FerrVaultConnectionReconciler) probe(
	ctx context.Context,
	conn *fvv1alpha1.FerrVaultConnection,
) (metav1.ConditionStatus, string, string) {
	broker := r.Broker
	if broker == nil {
		broker = NewTokenBroker(r.Client)
	}
	adapter := &ffv1alpha1.FerrFlowConnection{
		ObjectMeta: conn.ObjectMeta,
		Spec:       conn.Spec,
	}
	token, err := broker.TokenFor(ctx, adapter)
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
	return metav1.ConditionTrue, "Reachable", fmt.Sprintf("%s responded to /health", conn.Spec.URL)
}

func (r *FerrVaultConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&fvv1alpha1.FerrVaultConnection{},
		".spec.tokenSecretRef.name",
		func(obj client.Object) []string {
			c := obj.(*fvv1alpha1.FerrVaultConnection)
			if c.Spec.TokenSecretRef == nil {
				return nil
			}
			return []string{c.Spec.TokenSecretRef.Name}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&fvv1alpha1.FerrVaultConnection{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				var list fvv1alpha1.FerrVaultConnectionList
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
		Named("ferrvaultconnection").
		Complete(r)
}
