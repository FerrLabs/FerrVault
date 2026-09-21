package controller

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
)

var errUpdateForbidden = errors.New("update is not granted by the chart's ClusterRole")

func TestRolloutRestartOnlyNeedsPatch(t *testing.T) {
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "api"},
	}
	c := fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(deploy).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
				return errUpdateForbidden
			},
		}).
		Build()
	r := &FerrVaultSecretReconciler{Client: c}
	cr := &fvv1alpha1.FerrVaultSecret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "runtime", UID: "0b6d3f7a-5c21-4e98-8f40-9d1a2c3e4b5f"},
		Spec: fvv1alpha1.SecretSpec{
			RolloutRestart: []fvv1alpha1.WorkloadRef{{Kind: "Deployment", Name: "api"}},
		},
	}

	if err := r.triggerRollouts(context.Background(), cr, "content-hash"); err != nil {
		t.Fatalf("rollout restart failed without the update verb: %v", err)
	}

	var got appsv1.Deployment
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "api"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Template.Annotations[fvAnnotationRestartedAt] == "" {
		t.Fatal("pod template was not annotated, so no rollout was triggered")
	}
}

func TestRolloutRestartReportsAMissingWorkload(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()
	r := &FerrVaultSecretReconciler{Client: c}
	cr := &fvv1alpha1.FerrVaultSecret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "runtime", UID: "0b6d3f7a-5c21-4e98-8f40-9d1a2c3e4b5f"},
		Spec: fvv1alpha1.SecretSpec{
			RolloutRestart: []fvv1alpha1.WorkloadRef{{Kind: "Deployment", Name: "absent"}},
		},
	}

	if err := r.triggerRollouts(context.Background(), cr, "content-hash"); err == nil {
		t.Fatal("a rollout of a workload that does not exist reported success")
	}
}

func TestTwoResourcesSharingAWorkloadKeepSeparateRolloutMarkers(t *testing.T) {
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "api"}}
	patches := 0
	c := fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(deploy).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				patches++
				return c.Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()
	r := &FerrVaultSecretReconciler{Client: c}
	owner := func(name, uid string) *fvv1alpha1.FerrVaultSecret {
		return &fvv1alpha1.FerrVaultSecret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name, UID: types.UID(uid)},
			Spec: fvv1alpha1.SecretSpec{
				RolloutRestart: []fvv1alpha1.WorkloadRef{{Kind: "Deployment", Name: "api"}},
			},
		}
	}
	database := owner("database", "4a1e9c2d-7b35-4f60-8d2a-1c9e5b7f3a60")
	cache := owner("cache", "8c3f5a1b-2e47-4d90-a6b3-5f2c8e1d7b94")
	ctx := context.Background()

	for _, step := range []struct {
		cr   *fvv1alpha1.FerrVaultSecret
		hash string
	}{{database, "db-v2"}, {cache, "cache-v7"}, {database, "db-v2"}} {
		if err := r.triggerRollouts(ctx, step.cr, step.hash); err != nil {
			t.Fatal(err)
		}
	}
	if patches != 2 {
		t.Fatalf("the workload was restarted %d times, want 2: the second resource's marker "+
			"overwrote the first's, so a retry of the first restarted it again", patches)
	}
}
