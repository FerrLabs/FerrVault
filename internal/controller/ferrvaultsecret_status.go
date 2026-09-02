package controller

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
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
	// Every failure reaching here reports Ready=False, without exception.
	//
	// An earlier draft suppressed that for "transient" reasons when the same
	// generation had already synced. Review caught what it would have cost:
	// `Unreachable` is the catch-all for any reveal error that is neither auth
	// nor 404, and `syncedThisGeneration` stays true forever while the spec is
	// unchanged. A backend down for days, a DNS entry gone, a firewall rule —
	// all would have reported Ready=True indefinitely, with the message as the
	// only hint. That is the very inversion this change exists to prevent,
	// turned the other way round: a real outage indistinguishable from health.
	//
	// The 429 that motivated all this never reaches this function: `Reconcile`
	// returns a plain requeue for it, without writing status at all. That is
	// the right shape — the status is left alone because nothing about the
	// resource changed — and it does not need a general exemption here.
	setCondition(&cr.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
	// Deliberately NOT setting ObservedGeneration here.
	//
	// It records the generation whose data was successfully synced, and this
	// path did not sync anything. Stamping it on failure would make the next
	// pass believe an edited spec had already been applied, so a permanent
	// failure on a NEW spec would be masked by an old success — the exact
	// inversion this change exists to prevent, one reconcile later.
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, fmt.Errorf("update status with %s: %w", reason, err)
	}
	return ctrl.Result{RequeueAfter: after}, nil
}
