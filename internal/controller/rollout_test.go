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
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "runtime"},
		Spec: fvv1alpha1.SecretSpec{
			RolloutRestart: []fvv1alpha1.WorkloadRef{{Kind: "Deployment", Name: "api"}},
		},
	}

	if err := r.triggerRollouts(context.Background(), cr); err != nil {
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
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "runtime"},
		Spec: fvv1alpha1.SecretSpec{
			RolloutRestart: []fvv1alpha1.WorkloadRef{{Kind: "Deployment", Name: "absent"}},
		},
	}

	if err := r.triggerRollouts(context.Background(), cr); err == nil {
		t.Fatal("a rollout of a workload that does not exist reported success")
	}
}
