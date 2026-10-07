package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
	"github.com/FerrLabs/FerrVault/internal/ferrvault"
)

const guardOwnerUID = "0b7e9c52-3f1d-4a6e-8c2b-5d9f1e7a3c40"

type partialVault struct {
	secrets map[string]string
	missing []string
}

func (f partialVault) BulkReveal(context.Context, string, string, string, string, []string) (*ferrvault.BulkRevealResponse, error) {
	return &ferrvault.BulkRevealResponse{Secrets: f.secrets, Missing: f.missing}, nil
}

func (f partialVault) RevealFromVault(context.Context, string, []string) (*ferrvault.BulkRevealResponse, error) {
	return &ferrvault.BulkRevealResponse{Secrets: f.secrets, Missing: f.missing}, nil
}

type guardFixture struct {
	r      *FerrVaultSecretReconciler
	client client.Client
}

func newGuardFixture(t *testing.T, target string, transforms []fvv1alpha1.SecretTransform, upstream partialVault, extra ...client.Object) guardFixture {
	t.Helper()
	scheme := newTestScheme(t)
	objects := append([]client.Object{
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "fv-token"},
			Data:       map[string][]byte{"token": []byte("fvsat_old")},
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
				Name:       "sync",
				UID:        guardOwnerUID,
				Finalizers: []string{fvSecretFinalizer},
			},
			Spec: fvv1alpha1.SecretSpec{
				ConnectionRef: fvv1alpha1.LocalObjectReference{Name: "conn"},
				Vault:         "app",
				Target:        fvv1alpha1.SecretTarget{Name: target},
				Transforms:    transforms,
			},
		},
	}, extra...)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&fvv1alpha1.FerrVaultSecret{}).
		Build()
	return guardFixture{
		r: &FerrVaultSecretReconciler{
			Client: c,
			Scheme: scheme,
			ClientFactory: func(string, string) (ferrvaultClient, error) {
				return upstream, nil
			},
			DefaultRefreshInterval: time.Hour,
			Heartbeat:              NewHeartbeat(time.Now()),
		},
		client: c,
	}
}

func (f guardFixture) reconcile(t *testing.T) ctrl.Result {
	t.Helper()
	res, err := f.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "sync"}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return res
}

func (f guardFixture) secret(t *testing.T, name string) (*corev1.Secret, bool) {
	t.Helper()
	var s corev1.Secret
	err := f.client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: name}, &s)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return &s, true
}

func (f guardFixture) ready(t *testing.T) *metav1.Condition {
	t.Helper()
	var cr fvv1alpha1.FerrVaultSecret
	if err := f.client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "sync"}, &cr); err != nil {
		t.Fatal(err)
	}
	return meta.FindStatusCondition(cr.Status.Conditions, "Ready")
}

func secretValue(s *corev1.Secret, key string) string {
	if v, ok := s.StringData[key]; ok {
		return v
	}
	return string(s.Data[key])
}

func TestMissingKeysLeaveAnExistingTargetUntouched(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "runtime"},
		Data:       map[string][]byte{"A": []byte("a-old"), "B": []byte("b-old")},
	}
	f := newGuardFixture(t, "runtime", nil, partialVault{secrets: map[string]string{"A": "a-new"}, missing: []string{"B"}}, existing)

	res := f.reconcile(t)

	s, _ := f.secret(t, "runtime")
	if secretValue(s, "A") != "a-old" || secretValue(s, "B") != "b-old" {
		t.Fatalf("target was rewritten despite a missing key: %v %v", s.Data, s.StringData)
	}
	cond := f.ready(t)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "MissingKeys" {
		t.Fatalf("want Ready=False MissingKeys, got %+v", cond)
	}
	if res.RequeueAfter > missingKeysRetry {
		t.Fatalf("missing keys must be retried within %s, got %s", missingKeysRetry, res.RequeueAfter)
	}
}

func TestNothingFoundDoesNotCreateAnEmptyTarget(t *testing.T) {
	f := newGuardFixture(t, "runtime", nil, partialVault{secrets: map[string]string{}, missing: []string{"A"}})

	f.reconcile(t)

	if _, ok := f.secret(t, "runtime"); ok {
		t.Fatal("an empty target Secret was created")
	}
}

func TestPartialRevealStillCreatesANewTarget(t *testing.T) {
	f := newGuardFixture(t, "runtime", nil, partialVault{secrets: map[string]string{"A": "a"}, missing: []string{"B"}})

	res := f.reconcile(t)

	s, ok := f.secret(t, "runtime")
	if !ok || secretValue(s, "A") != "a" {
		t.Fatal("new target with the keys found was not created")
	}
	if res.RequeueAfter > missingKeysRetry {
		t.Fatalf("missing keys must be retried within %s, got %s", missingKeysRetry, res.RequeueAfter)
	}
}

func TestConnectionTokenTargetIsRotatedWithoutOwnership(t *testing.T) {
	rename := []fvv1alpha1.SecretTransform{{Type: "rename", From: "FERRVAULT_OPERATOR_TOKEN", To: "token"}}
	f := newGuardFixture(t, "fv-token", rename, partialVault{secrets: map[string]string{"FERRVAULT_OPERATOR_TOKEN": "fvsat_new"}})

	f.reconcile(t)
	f.reconcile(t)

	s, _ := f.secret(t, "fv-token")
	if secretValue(s, "token") != "fvsat_new" {
		t.Fatalf("token not rotated: %v %v", s.Data, s.StringData)
	}
	for _, ref := range s.OwnerReferences {
		if ref.UID == guardOwnerUID {
			t.Fatal("the connection token Secret is owned by the FerrVaultSecret, deleting it would garbage-collect the token")
		}
	}
}

func TestConnectionTokenTargetDropsAPreviousOwnerReference(t *testing.T) {
	controller := true
	owned := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "fv-token-owned", OwnerReferences: []metav1.OwnerReference{{
			APIVersion: fvv1alpha1.GroupVersion.String(), Kind: "FerrVaultSecret", Name: "sync", UID: guardOwnerUID, Controller: &controller,
		}}},
		Data: map[string][]byte{"token": []byte("fvsat_old")},
	}
	conn := &fvv1alpha1.FerrVaultConnection{}
	f := newGuardFixture(t, "fv-token-owned", nil, partialVault{secrets: map[string]string{"token": "fvsat_new"}}, owned)
	if err := f.client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "conn"}, conn); err != nil {
		t.Fatal(err)
	}
	conn.Spec.TokenSecretRef.Name = "fv-token-owned"
	if err := f.client.Update(context.Background(), conn); err != nil {
		t.Fatal(err)
	}

	f.reconcile(t)

	s, _ := f.secret(t, "fv-token-owned")
	if len(s.OwnerReferences) != 0 {
		t.Fatalf("owner reference kept on the connection token Secret: %+v", s.OwnerReferences)
	}
}

func TestConnectionTokenTargetWithoutTheTokenKeyIsLeftAlone(t *testing.T) {
	f := newGuardFixture(t, "fv-token", nil, partialVault{secrets: map[string]string{"FERRVAULT_OPERATOR_TOKEN": "fvsat_new"}})

	f.reconcile(t)

	s, _ := f.secret(t, "fv-token")
	if secretValue(s, "token") != "fvsat_old" {
		t.Fatal("the connection token was overwritten by data without a token key")
	}
	cond := f.ready(t)
	if cond == nil || cond.Reason != "TokenKeyMissing" {
		t.Fatalf("want TokenKeyMissing, got %+v", cond)
	}
}

func TestOrdinaryTargetIsStillOwned(t *testing.T) {
	f := newGuardFixture(t, "runtime", nil, partialVault{secrets: map[string]string{"A": "a"}})

	f.reconcile(t)

	s, _ := f.secret(t, "runtime")
	if len(s.OwnerReferences) != 1 || s.OwnerReferences[0].UID != guardOwnerUID {
		t.Fatalf("ordinary target lost its controller reference: %+v", s.OwnerReferences)
	}
}
