package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestTargetSecretIsStoredAsDataAndNotRewrittenWhenUnchanged(t *testing.T) {
	secretWrites := 0
	f := newReconcileFixture(t, map[string]string{"A": "a", "B": "b"}, interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				secretWrites++
			}
			return c.Update(ctx, obj, opts...)
		},
	})
	ctx := context.Background()

	if _, err := f.r.Reconcile(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	writesAfterFirstPass := secretWrites
	if _, err := f.r.Reconcile(ctx, f.req); err != nil {
		t.Fatal(err)
	}

	if secretWrites != writesAfterFirstPass {
		t.Fatalf("unchanged content rewrote the target Secret %d more time(s)", secretWrites-writesAfterFirstPass)
	}
	var target corev1.Secret
	if err := f.client.Get(ctx, client.ObjectKey{Namespace: "default", Name: "runtime"}, &target); err != nil {
		t.Fatal(err)
	}
	if len(target.StringData) != 0 {
		t.Fatalf("target must not carry write-only StringData, got %v", target.StringData)
	}
	if string(target.Data["A"]) != "a" || string(target.Data["B"]) != "b" {
		t.Fatalf("target Data not populated: %v", target.Data)
	}
}
