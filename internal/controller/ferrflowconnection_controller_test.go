package controller

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ffv1alpha1 "github.com/FerrFlow-Org/FerrFlow-Operator/api/v1alpha1"
)

// newConnReconciler wires a FerrFlowConnectionReconciler on top of a fake
// client that has the same field indexers registered as the real manager.
// Kept local to this file to avoid entangling the FerrFlowSecret test harness
// with Connection-specific bookkeeping.
func newConnReconciler(t *testing.T, objs ...client.Object) *FerrFlowConnectionReconciler {
	t.Helper()
	t.Cleanup(func() {
		ConnectionReady.Reset()
	})
	scheme := newTestScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&ffv1alpha1.FerrFlowConnection{}).
		WithIndex(&ffv1alpha1.FerrFlowSecret{}, connectionRefIndexKey,
			func(obj client.Object) []string {
				return []string{obj.(*ffv1alpha1.FerrFlowSecret).Spec.ConnectionRef.Name}
			}).
		Build()
	return &FerrFlowConnectionReconciler{Client: c, Scheme: scheme}
}

func TestConnectionFinalizer_BlocksDeleteWhileInUse(t *testing.T) {
	// A FerrFlowSecret still references this Connection. Delete should be
	// rejected (finalizer stays, status reflects "DeletionBlocked").
	now := metav1.Now()
	conn := baseConn()
	conn.Finalizers = []string{ferrFlowConnectionFinalizer}
	conn.DeletionTimestamp = &now
	cr := baseCR()

	r := newConnReconciler(t, conn, cr)

	req := ctrl.Request{NamespacedName: types.NamespacedName{
		Namespace: conn.Namespace,
		Name:      conn.Name,
	}}
	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	// Status should surface the block.
	var after ffv1alpha1.FerrFlowConnection
	if err := r.Get(context.Background(), req.NamespacedName, &after); err != nil {
		t.Fatalf("expected CR still present while blocked, got: %v", err)
	}
	if !slicesContains(after.Finalizers, ferrFlowConnectionFinalizer) {
		t.Fatalf("finalizer should stay while CR is in use, got %v", after.Finalizers)
	}
	ready := findReady(after.Status.Conditions)
	if ready == nil || ready.Reason != "DeletionBlocked" {
		t.Fatalf("expected DeletionBlocked condition, got %+v", ready)
	}
	if res.RequeueAfter == 0 {
		t.Fatalf("expected requeue so the delete retries when dependants clear")
	}
}

func TestConnectionFinalizer_CompletesWhenNoDependants(t *testing.T) {
	// No FerrFlowSecret references this Connection — finalizer should be
	// stripped and the fake client GCs the object.
	now := metav1.Now()
	conn := baseConn()
	conn.Finalizers = []string{ferrFlowConnectionFinalizer}
	conn.DeletionTimestamp = &now

	r := newConnReconciler(t, conn)

	req := ctrl.Request{NamespacedName: types.NamespacedName{
		Namespace: conn.Namespace,
		Name:      conn.Name,
	}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var after ffv1alpha1.FerrFlowConnection
	err := r.Get(context.Background(), req.NamespacedName, &after)
	if err == nil {
		t.Fatalf("expected CR to be GC'd after finalizer removal, got: %+v", after)
	}
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected NotFound, got: %v", err)
	}
}
