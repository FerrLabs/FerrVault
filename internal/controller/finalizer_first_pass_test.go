package controller

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
)

func TestANewSecretSyncsOnTheReconcileThatAddsItsFinalizer(t *testing.T) {
	t.Cleanup(func() {
		SyncDuration.Reset()
		SyncErrors.Reset()
		LastSyncTimestamp.Reset()
		RefreshInterval.Reset()
	})
	f := newReconcileFixture(t, map[string]string{"API_KEY": "v"}, interceptor.Funcs{})
	ctx := context.Background()

	var cr fvv1alpha1.FerrVaultSecret
	if err := f.client.Get(ctx, f.req.NamespacedName, &cr); err != nil {
		t.Fatal(err)
	}
	controllerutil.RemoveFinalizer(&cr, fvSecretFinalizer)
	cr.Spec.RolloutRestart = nil
	if err := f.client.Update(ctx, &cr); err != nil {
		t.Fatal(err)
	}

	if _, err := f.r.Reconcile(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	if err := f.client.Get(ctx, f.req.NamespacedName, &cr); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(&cr, fvSecretFinalizer) {
		t.Fatal("finalizer not added")
	}
	if meta.FindStatusCondition(cr.Status.Conditions, "Ready") == nil {
		t.Fatal("the first reconcile only added the finalizer; GenerationChangedPredicate drops " +
			"that update, so the secret would wait for an unrelated event to ever sync")
	}
}
