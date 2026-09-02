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
	// A TRANSIENT failure that follows a successful sync of the same generation
	// says nothing about the data already written: the Secret is current, the
	// next pass will confirm it. Reporting Ready=False there is what made
	// fourteen up-to-date secrets look broken.
	//
	// Restricted to transient reasons on purpose. `AuthFailed` and
	// `VaultNotFound` are real breakage — a revoked token, a deleted vault —
	// and hiding them behind an earlier success would be far worse than the
	// bug this fixes: the operator would stay green while it can no longer
	// read anything.
	status := metav1.ConditionFalse
	if isTransient(reason) && syncedThisGeneration(cr) {
		status = metav1.ConditionTrue
		message = fmt.Sprintf(
			"%s (last successful sync at %s)",
			message, cr.Status.LastSyncedAt.Format(time.RFC3339),
		)
	}
	setCondition(&cr.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  status,
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

// isTransient reports whether a failure reason describes a condition that
// resolves on its own, without a human changing anything.
//
// Deliberately a short allow-list rather than a deny-list: a reason added later
// is treated as real breakage until someone decides otherwise, which is the
// safe default for a status the operator relies on.
func isTransient(reason string) bool {
	switch reason {
	case "Unreachable", "RateLimited":
		return true
	default:
		return false
	}
}

// syncedThisGeneration reports whether the spec currently on the object has
// already been synced successfully.
//
// `ObservedGeneration` is what makes this safe: a spec edited since the last
// success bumps `Generation`, so a failure on the new spec is reported as such
// rather than hidden behind an old success.
func syncedThisGeneration(cr *fvv1alpha1.FerrVaultSecret) bool {
	return cr.Status.LastSyncedAt != nil && cr.Status.ObservedGeneration == cr.Generation
}
