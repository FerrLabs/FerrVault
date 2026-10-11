package controller

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
)

func TestANewConnectionIsProbedOnTheReconcileThatAddsItsFinalizer(t *testing.T) {
	t.Cleanup(func() { ConnectionReady.Reset() })
	scheme := newTestScheme(t)
	key := types.NamespacedName{Namespace: "default", Name: "conn"}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&fvv1alpha1.FerrVaultConnection{
			ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
			Spec: fvv1alpha1.ConnectionSpec{
				URL:            "https://vault.example",
				TokenSecretRef: &fvv1alpha1.SecretKeyRef{Name: "missing", Key: "token"},
			},
		}).
		WithStatusSubresource(&fvv1alpha1.FerrVaultConnection{}).
		Build()
	r := &FerrVaultConnectionReconciler{Client: c, Scheme: scheme, Heartbeat: NewHeartbeat(time.Now())}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}

	var conn fvv1alpha1.FerrVaultConnection
	if err := c.Get(context.Background(), key, &conn); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(&conn, fvConnectionFinalizer) {
		t.Fatal("finalizer not added")
	}
	if meta.FindStatusCondition(conn.Status.Conditions, "Ready") == nil {
		t.Fatal("the first reconcile only added the finalizer; GenerationChangedPredicate drops " +
			"that update, so the connection waits for an unrelated event to ever be probed")
	}
}
