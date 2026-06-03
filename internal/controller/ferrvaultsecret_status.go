package controller

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	fvv1alpha1 "github.com/FerrLabs/FerrFlow-Operator/api/ferrvault/v1alpha1"
)

func (r *FerrVaultSecretReconciler) failReady(
	ctx context.Context,
	cr *fvv1alpha1.FerrVaultSecret,
	reason, message string,
) (ctrl.Result, error) {
	return r.failReadyWithRequeue(ctx, cr, reason, message, r.refreshInterval(cr))
}

func (r *FerrVaultSecretReconciler) failReadyWithRequeue(
	ctx context.Context,
	cr *fvv1alpha1.FerrVaultSecret,
	reason, message string,
	after time.Duration,
) (ctrl.Result, error) {
	IncSyncError(reason)
	setCondition(&cr.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
	cr.Status.ObservedGeneration = cr.Generation
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("update status with %s: %w", reason, err)
	}
	return ctrl.Result{RequeueAfter: after}, nil
}
