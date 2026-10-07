package controller

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
)

const missingKeysRetry = time.Minute

func targetName(cr *fvv1alpha1.FerrVaultSecret) string {
	if cr.Spec.Target.Name != "" {
		return cr.Spec.Target.Name
	}
	return cr.Name
}

func connectionTokenKey(conn *fvv1alpha1.FerrVaultConnection, cr *fvv1alpha1.FerrVaultSecret) (string, bool) {
	ref := conn.Spec.TokenSecretRef
	if ref == nil || ref.Name != targetName(cr) {
		return "", false
	}
	return ref.Key, true
}

func (r *FerrVaultSecretReconciler) targetExists(ctx context.Context, cr *fvv1alpha1.FerrVaultSecret) (bool, error) {
	var secret corev1.Secret
	key := types.NamespacedName{Namespace: cr.Namespace, Name: targetName(cr)}
	if err := r.Get(ctx, key, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func missingKeysRequeue(refresh time.Duration) time.Duration {
	if refresh < missingKeysRetry {
		return refresh
	}
	return missingKeysRetry
}

func withoutOwner(refs []metav1.OwnerReference, uid types.UID) []metav1.OwnerReference {
	kept := refs[:0:0]
	for _, ref := range refs {
		if ref.UID != uid {
			kept = append(kept, ref)
		}
	}
	return kept
}
