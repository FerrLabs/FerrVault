package controller

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
)

func TestRolloutDue(t *testing.T) {
	cases := []struct {
		name                         string
		lastRolledOut, prev, current string
		want                         bool
	}{
		{"a Secret created just now needs no restart", "", "", "h1", false},
		{"first pass after an upgrade, content unchanged", "", "h1", "h1", false},
		{"first pass after an upgrade, content changed", "", "h0", "h1", true},
		{"content changed", "h0", "h0", "h1", true},
		{"a failed restart is retried once the Secret already holds the new content", "h0", "h1", "h1", true},
		{"restarted for the current content", "h1", "h1", "h1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rolloutDue(c.lastRolledOut, c.prev, c.current); got != c.want {
				t.Fatalf("rolloutDue(%q, %q, %q) = %v, want %v", c.lastRolledOut, c.prev, c.current, got, c.want)
			}
		})
	}
}

var errPatchRefused = errors.New("patch refused")

func TestAFailedRolloutIsRetriedOnTheNextPass(t *testing.T) {
	upstream := map[string]string{"API_KEY": "rotated"}
	var refusePatch atomic.Bool
	refusePatch.Store(true)
	f := newReconcileFixture(t, upstream, interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if _, ok := obj.(*appsv1.Deployment); ok && refusePatch.Load() {
				return errPatchRefused
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
	})
	ctx := context.Background()

	if _, err := f.r.Reconcile(ctx, f.req); !errors.Is(err, errPatchRefused) {
		t.Fatalf("a failed rollout must fail the reconcile so it is retried, got %v", err)
	}
	cr := f.load(t)
	if cr.Status.LastRolloutHash == hashSecretData(upstream) {
		t.Fatal("the failed rollout was recorded as done")
	}
	if !meta.IsStatusConditionFalse(cr.Status.Conditions, conditionRolloutRestarted) {
		t.Fatalf("RolloutRestarted should be False after a failed rollout, got %+v", cr.Status.Conditions)
	}
	if f.restartedAt(t) != "" {
		t.Fatal("the workload was annotated although the patch was refused")
	}

	refusePatch.Store(false)
	if _, err := f.r.Reconcile(ctx, f.req); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if f.restartedAt(t) == "" {
		t.Fatal("the rollout was not retried: the Secret already held the new content, " +
			"so the change was forgotten and the workload kept the old values")
	}
	cr = f.load(t)
	if cr.Status.LastRolloutHash != hashSecretData(upstream) {
		t.Fatalf("lastRolloutHash = %q, want the current content hash", cr.Status.LastRolloutHash)
	}
	if !meta.IsStatusConditionTrue(cr.Status.Conditions, conditionRolloutRestarted) {
		t.Fatalf("RolloutRestarted should be True once the rollout went through, got %+v", cr.Status.Conditions)
	}
}

func TestAnUnchangedSecretIsNotRestartedAgain(t *testing.T) {
	upstream := map[string]string{"API_KEY": "rotated"}
	var patches atomic.Int32
	f := newReconcileFixture(t, upstream, interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if _, ok := obj.(*appsv1.Deployment); ok {
				patches.Add(1)
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
	})
	ctx := context.Background()

	for pass := 1; pass <= 3; pass++ {
		if _, err := f.r.Reconcile(ctx, f.req); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	if got := patches.Load(); got != 1 {
		t.Fatalf("the workload was restarted %d times for one content change, want 1", got)
	}
}

func (f reconcileFixture) load(t *testing.T) fvv1alpha1.FerrVaultSecret {
	t.Helper()
	var cr fvv1alpha1.FerrVaultSecret
	if err := f.client.Get(context.Background(), f.req.NamespacedName, &cr); err != nil {
		t.Fatal(err)
	}
	return cr
}

func (f reconcileFixture) restartedAt(t *testing.T) string {
	t.Helper()
	var d appsv1.Deployment
	if err := f.client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "api"}, &d); err != nil {
		t.Fatal(err)
	}
	return d.Spec.Template.Annotations[fvAnnotationRestartedAt]
}
