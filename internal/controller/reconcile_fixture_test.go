package controller

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
	"github.com/FerrLabs/FerrVault/internal/ferrvault"
)

type fakeVault struct {
	secrets map[string]string
}

func (f fakeVault) BulkReveal(context.Context, string, string, string, string, []string) (*ferrvault.BulkRevealResponse, error) {
	return &ferrvault.BulkRevealResponse{Secrets: f.secrets}, nil
}

func (f fakeVault) RevealFromVault(context.Context, string, []string) (*ferrvault.BulkRevealResponse, error) {
	return &ferrvault.BulkRevealResponse{Secrets: f.secrets}, nil
}

type reconcileFixture struct {
	r      *FerrVaultSecretReconciler
	req    ctrl.Request
	client client.WithWatch
}

func newReconcileFixture(t *testing.T, upstream map[string]string, funcs interceptor.Funcs) reconcileFixture {
	t.Helper()
	scheme := newTestScheme(t)
	objects := []client.Object{
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "fv-token"},
			Data:       map[string][]byte{"token": []byte("fvsat_test")},
		},
		&fvv1alpha1.FerrVaultConnection{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "conn"},
			Spec: fvv1alpha1.ConnectionSpec{
				URL:            "https://vault.example",
				TokenSecretRef: &fvv1alpha1.SecretKeyRef{Name: "fv-token", Key: "token"},
			},
		},
		&fvv1alpha1.FerrVaultSecret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:  "default",
				Name:       "runtime",
				UID:        "6f1c2a4e-0d3b-4c8f-9a51-2b7e8d4f0c11",
				Finalizers: []string{fvSecretFinalizer},
			},
			Spec: fvv1alpha1.SecretSpec{
				ConnectionRef:  fvv1alpha1.LocalObjectReference{Name: "conn"},
				Vault:          "runtime",
				RolloutRestart: []fvv1alpha1.WorkloadRef{{Kind: "Deployment", Name: "api"}},
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "default",
				Name:        "runtime",
				Annotations: map[string]string{fvAnnotationContentHash: "content-before-the-change"},
			},
		},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "api"}},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&fvv1alpha1.FerrVaultSecret{}).
		WithInterceptorFuncs(funcs).
		Build()
	return reconcileFixture{
		r: &FerrVaultSecretReconciler{
			Client: c,
			Scheme: scheme,
			ClientFactory: func(string, string) (ferrvaultClient, error) {
				return fakeVault{secrets: upstream}, nil
			},
			DefaultRefreshInterval: time.Hour,
			Heartbeat:              NewHeartbeat(time.Now()),
		},
		req:    ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "runtime"}},
		client: c,
	}
}

func blockWorkloadReadsUntilCancelled(reads *atomic.Int32, blocking *atomic.Bool) interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*appsv1.Deployment); ok && blocking.Load() {
				reads.Add(1)
				<-ctx.Done()
				return ctx.Err()
			}
			return c.Get(ctx, key, obj, opts...)
		},
		SubResourceUpdate: func(ctx context.Context, c client.Client, subResource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return c.SubResource(subResource).Update(ctx, obj, opts...)
		},
	}
}
