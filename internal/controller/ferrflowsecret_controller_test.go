package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ffv1alpha1 "github.com/FerrFlow-Org/FerrFlow-Operator/api/v1alpha1"
	"github.com/FerrFlow-Org/FerrFlow-Operator/internal/ferrflow"
)

// fakeFerrFlow is a test double for ferrflowClient. Each test sets bulkReveal
// to shape the reveal call's response or error.
type fakeFerrFlow struct {
	bulkReveal func(ctx context.Context, org, project, vault, namespace string, names []string) (*ferrflow.BulkRevealResponse, error)
	// calls is incremented on every invocation so tests can assert the
	// reconciler hit the client at all (or didn't).
	calls int
}

func (f *fakeFerrFlow) BulkReveal(
	ctx context.Context,
	org, project, vault, namespace string,
	names []string,
) (*ferrflow.BulkRevealResponse, error) {
	f.calls++
	if f.bulkReveal == nil {
		return &ferrflow.BulkRevealResponse{Secrets: map[string]string{}}, nil
	}
	return f.bulkReveal(ctx, org, project, vault, namespace, names)
}

const (
	testNamespace     = "ns1"
	testCRName        = "ffs-1"
	testConnName      = "conn-1"
	testTokenSecret   = "conn-token"
	testTokenKey      = "token"
	testTokenValue    = "fft_abc"
	testOrg           = "my-org"
	testProject       = "my-proj"
	testVault         = "prod"
	testTargetName    = "my-target"
	testRefreshString = "30m"
)

// newTestScheme registers the types the reconciler reads/writes so the fake
// client can encode them.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := ffv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add ffv1alpha1 to scheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatalf("add appsv1 to scheme: %v", err)
	}
	return s
}

// baseCR returns a FerrFlowSecret populated with the test defaults. The caller
// can mutate before handing it to the builder.
func baseCR() *ffv1alpha1.FerrFlowSecret {
	return &ffv1alpha1.FerrFlowSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:       testCRName,
			Namespace:  testNamespace,
			UID:        "cr-uid",
			Generation: 1,
		},
		Spec: ffv1alpha1.FerrFlowSecretSpec{
			ConnectionRef:   ffv1alpha1.LocalObjectReference{Name: testConnName},
			Project:         testProject,
			Vault:           testVault,
			Selector:        ffv1alpha1.SecretSelector{Names: []string{"K"}},
			Target:          ffv1alpha1.SecretTarget{Name: testTargetName},
			RefreshInterval: testRefreshString,
		},
	}
}

func baseConn() *ffv1alpha1.FerrFlowConnection {
	return &ffv1alpha1.FerrFlowConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testConnName,
			Namespace: testNamespace,
		},
		Spec: ffv1alpha1.FerrFlowConnectionSpec{
			URL:          "https://ferrflow.example.com",
			Organization: testOrg,
			TokenSecretRef: ffv1alpha1.SecretKeyRef{
				Name: testTokenSecret,
				Key:  testTokenKey,
			},
		},
	}
}

func baseTokenSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testTokenSecret,
			Namespace: testNamespace,
		},
		Data: map[string][]byte{
			testTokenKey: []byte(testTokenValue),
		},
	}
}

// newTestReconciler builds a fake-client-backed reconciler with the provided
// objects pre-seeded and the fake FerrFlow client wired as the factory.
//
// Metrics are reset on test cleanup so reconciler tests don't pollute the
// process-wide Prometheus collectors that metrics_test.go asserts against.
func newTestReconciler(t *testing.T, objs []client.Object, fakeFF *fakeFerrFlow) *FerrFlowSecretReconciler {
	t.Helper()
	t.Cleanup(func() {
		SyncDuration.Reset()
		SyncErrors.Reset()
		LastSyncTimestamp.Reset()
		ConnectionReady.Reset()
	})
	scheme := newTestScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&ffv1alpha1.FerrFlowSecret{}, &ffv1alpha1.FerrFlowConnection{}).
		Build()
	return &FerrFlowSecretReconciler{
		Client:                 c,
		Scheme:                 scheme,
		DefaultRefreshInterval: time.Hour,
		ClientFactory: func(baseURL, token string) (ferrflowClient, error) {
			return fakeFF, nil
		},
	}
}

// reconcileOnce drives one Reconcile and refreshes `cr` from the fake cluster
// so callers can assert on post-reconcile status.
func reconcileOnce(t *testing.T, r *FerrFlowSecretReconciler, cr *ffv1alpha1.FerrFlowSecret) (ctrl.Result, error) {
	t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{
		Namespace: cr.Namespace,
		Name:      cr.Name,
	}}
	res, err := r.Reconcile(context.Background(), req)
	// Re-fetch the CR so the caller sees updated status.
	_ = r.Get(context.Background(), req.NamespacedName, cr)
	return res, err
}

// findCondition returns the Ready condition or nil.
func findReady(conds []metav1.Condition) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == "Ready" {
			return &conds[i]
		}
	}
	return nil
}

func TestReconcile_HappyPath(t *testing.T) {
	cr := baseCR()
	conn := baseConn()
	tok := baseTokenSecret()

	fakeFF := &fakeFerrFlow{
		bulkReveal: func(_ context.Context, _, _, _, _ string, _ []string) (*ferrflow.BulkRevealResponse, error) {
			return &ferrflow.BulkRevealResponse{
				Secrets: map[string]string{"K": "V"},
			}, nil
		},
	}
	r := newTestReconciler(t, []client.Object{cr, conn, tok}, fakeFF)

	res, err := reconcileOnce(t, r, cr)
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Fatalf("expected non-zero RequeueAfter, got %v", res.RequeueAfter)
	}

	// Target Secret exists with StringData=={K:V}.
	var got corev1.Secret
	if err := r.Get(context.Background(), types.NamespacedName{
		Namespace: testNamespace, Name: testTargetName,
	}, &got); err != nil {
		t.Fatalf("get target Secret: %v", err)
	}
	if got.StringData["K"] != "V" {
		t.Fatalf("StringData[K] = %q, want %q", got.StringData["K"], "V")
	}
	if len(got.StringData) != 1 {
		t.Fatalf("StringData len = %d, want 1", len(got.StringData))
	}

	// Owner reference pointing at the CR, Controller=true.
	if len(got.OwnerReferences) != 1 {
		t.Fatalf("owner refs = %d, want 1", len(got.OwnerReferences))
	}
	or := got.OwnerReferences[0]
	if or.UID != cr.UID {
		t.Fatalf("owner UID = %q, want %q", or.UID, cr.UID)
	}
	if or.Controller == nil || !*or.Controller {
		t.Fatalf("owner Controller = %v, want true", or.Controller)
	}

	// Content-hash annotation set.
	if got.Annotations[annotationContentHash] == "" {
		t.Fatalf("content-hash annotation empty")
	}

	// Ready=True, Reason=Synced.
	ready := findReady(cr.Status.Conditions)
	if ready == nil {
		t.Fatalf("Ready condition missing")
	}
	if ready.Status != metav1.ConditionTrue || ready.Reason != "Synced" {
		t.Fatalf("Ready = %v/%v, want True/Synced", ready.Status, ready.Reason)
	}

	if len(cr.Status.SyncedKeys) != 1 || cr.Status.SyncedKeys[0] != "K" {
		t.Fatalf("SyncedKeys = %v, want [K]", cr.Status.SyncedKeys)
	}
	if len(cr.Status.MissingKeys) != 0 {
		t.Fatalf("MissingKeys = %v, want empty", cr.Status.MissingKeys)
	}
	if cr.Status.LastSyncedAt == nil {
		t.Fatalf("LastSyncedAt is nil")
	}
	if cr.Status.ObservedGeneration != cr.Generation {
		t.Fatalf("ObservedGeneration = %d, want %d", cr.Status.ObservedGeneration, cr.Generation)
	}
}

func TestReconcile_MissingTokenSecret(t *testing.T) {
	cr := baseCR()
	conn := baseConn()
	// Deliberately skip the token Secret.
	r := newTestReconciler(t, []client.Object{cr, conn}, &fakeFerrFlow{})

	if _, err := reconcileOnce(t, r, cr); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	ready := findReady(cr.Status.Conditions)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "TokenUnreadable" {
		t.Fatalf("Ready = %+v, want False/TokenUnreadable", ready)
	}
}

func TestReconcile_EmptyTokenValue(t *testing.T) {
	cr := baseCR()
	conn := baseConn()
	tok := baseTokenSecret()
	tok.Data[testTokenKey] = []byte("")
	r := newTestReconciler(t, []client.Object{cr, conn, tok}, &fakeFerrFlow{})

	if _, err := reconcileOnce(t, r, cr); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	ready := findReady(cr.Status.Conditions)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "TokenUnreadable" {
		t.Fatalf("Ready = %+v, want False/TokenUnreadable", ready)
	}
}

func TestReconcile_AuthErrors(t *testing.T) {
	cases := []struct {
		name string
		kind ferrflow.AuthKind
	}{
		{"401Unauthorized", ferrflow.AuthUnauthorized},
		{"403Forbidden", ferrflow.AuthForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cr := baseCR()
			conn := baseConn()
			tok := baseTokenSecret()
			fakeFF := &fakeFerrFlow{
				bulkReveal: func(_ context.Context, _, _, _, _ string, _ []string) (*ferrflow.BulkRevealResponse, error) {
					return nil, &ferrflow.AuthError{Kind: tc.kind, Message: "nope"}
				},
			}
			r := newTestReconciler(t, []client.Object{cr, conn, tok}, fakeFF)

			res, err := reconcileOnce(t, r, cr)
			if err != nil {
				t.Fatalf("Reconcile err: %v", err)
			}
			if res.RequeueAfter != 5*time.Minute {
				t.Fatalf("RequeueAfter = %v, want 5m", res.RequeueAfter)
			}
			ready := findReady(cr.Status.Conditions)
			if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "AuthFailed" {
				t.Fatalf("Ready = %+v, want False/AuthFailed", ready)
			}
		})
	}
}

func TestReconcile_NotFoundError(t *testing.T) {
	cr := baseCR()
	conn := baseConn()
	tok := baseTokenSecret()
	fakeFF := &fakeFerrFlow{
		bulkReveal: func(_ context.Context, _, _, _, _ string, _ []string) (*ferrflow.BulkRevealResponse, error) {
			return nil, &ferrflow.NotFoundError{Message: "no such vault"}
		},
	}
	r := newTestReconciler(t, []client.Object{cr, conn, tok}, fakeFF)

	res, err := reconcileOnce(t, r, cr)
	if err != nil {
		t.Fatalf("Reconcile err: %v", err)
	}
	// spec.refreshInterval = 30m
	if res.RequeueAfter != 30*time.Minute {
		t.Fatalf("RequeueAfter = %v, want 30m (spec refreshInterval)", res.RequeueAfter)
	}
	ready := findReady(cr.Status.Conditions)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "VaultNotFound" {
		t.Fatalf("Ready = %+v, want False/VaultNotFound", ready)
	}
}

func TestReconcile_TransportError(t *testing.T) {
	cr := baseCR()
	conn := baseConn()
	tok := baseTokenSecret()
	fakeFF := &fakeFerrFlow{
		bulkReveal: func(_ context.Context, _, _, _, _ string, _ []string) (*ferrflow.BulkRevealResponse, error) {
			return nil, &ferrflow.TransportError{Underlying: errors.New("dial: timeout")}
		},
	}
	r := newTestReconciler(t, []client.Object{cr, conn, tok}, fakeFF)

	res, err := reconcileOnce(t, r, cr)
	if err != nil {
		t.Fatalf("Reconcile err: %v", err)
	}
	if res.RequeueAfter != 30*time.Minute {
		t.Fatalf("RequeueAfter = %v, want 30m", res.RequeueAfter)
	}
	ready := findReady(cr.Status.Conditions)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "Unreachable" {
		t.Fatalf("Ready = %+v, want False/Unreachable", ready)
	}
}

func TestReconcile_MissingKeys(t *testing.T) {
	cr := baseCR()
	cr.Spec.Selector.Names = []string{"K1", "K2"}
	conn := baseConn()
	tok := baseTokenSecret()
	fakeFF := &fakeFerrFlow{
		bulkReveal: func(_ context.Context, _, _, _, _ string, _ []string) (*ferrflow.BulkRevealResponse, error) {
			return &ferrflow.BulkRevealResponse{
				Secrets: map[string]string{"K1": "V1"},
				Missing: []string{"K2"},
			}, nil
		},
	}
	r := newTestReconciler(t, []client.Object{cr, conn, tok}, fakeFF)

	if _, err := reconcileOnce(t, r, cr); err != nil {
		t.Fatalf("Reconcile err: %v", err)
	}

	// Target Secret still written with the partial data.
	var got corev1.Secret
	if err := r.Get(context.Background(), types.NamespacedName{
		Namespace: testNamespace, Name: testTargetName,
	}, &got); err != nil {
		t.Fatalf("get target Secret: %v", err)
	}
	if got.StringData["K1"] != "V1" {
		t.Fatalf("StringData[K1] = %q, want V1", got.StringData["K1"])
	}

	ready := findReady(cr.Status.Conditions)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "MissingKeys" {
		t.Fatalf("Ready = %+v, want False/MissingKeys", ready)
	}
	if !containsSubstring(ready.Message, "K2") {
		t.Fatalf("Ready.Message = %q, want to contain K2", ready.Message)
	}
	if len(cr.Status.MissingKeys) != 1 || cr.Status.MissingKeys[0] != "K2" {
		t.Fatalf("MissingKeys = %v, want [K2]", cr.Status.MissingKeys)
	}
}

func TestReconcile_ConnectionNotFound(t *testing.T) {
	cr := baseCR()
	// No connection, no token Secret.
	r := newTestReconciler(t, []client.Object{cr}, &fakeFerrFlow{})

	if _, err := reconcileOnce(t, r, cr); err != nil {
		t.Fatalf("Reconcile err: %v", err)
	}
	ready := findReady(cr.Status.Conditions)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "ConnectionNotFound" {
		t.Fatalf("Ready = %+v, want False/ConnectionNotFound", ready)
	}
}

func TestReconcile_TargetSecretDriftCorrection(t *testing.T) {
	cr := baseCR()
	conn := baseConn()
	tok := baseTokenSecret()

	// Pre-seed the Secret with WRONG data. Reconcile should replace, not merge.
	garbage := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testTargetName,
			Namespace: testNamespace,
		},
		StringData: map[string]string{"K": "WRONG", "STALE": "X"},
	}
	fakeFF := &fakeFerrFlow{
		bulkReveal: func(_ context.Context, _, _, _, _ string, _ []string) (*ferrflow.BulkRevealResponse, error) {
			return &ferrflow.BulkRevealResponse{Secrets: map[string]string{"K": "V"}}, nil
		},
	}
	r := newTestReconciler(t, []client.Object{cr, conn, tok, garbage}, fakeFF)

	if _, err := reconcileOnce(t, r, cr); err != nil {
		t.Fatalf("Reconcile err: %v", err)
	}

	var got corev1.Secret
	if err := r.Get(context.Background(), types.NamespacedName{
		Namespace: testNamespace, Name: testTargetName,
	}, &got); err != nil {
		t.Fatalf("get target Secret: %v", err)
	}
	if got.StringData["K"] != "V" {
		t.Fatalf("StringData[K] = %q, want V (drift not corrected)", got.StringData["K"])
	}
	if _, ok := got.StringData["STALE"]; ok {
		t.Fatalf("StringData still contains STALE — data merged instead of replaced")
	}
	if len(got.StringData) != 1 {
		t.Fatalf("StringData len = %d, want 1", len(got.StringData))
	}
}

func TestReconcile_OwnerReferencePropagation(t *testing.T) {
	cr := baseCR()
	cr.UID = "specific-uid-123"
	conn := baseConn()
	tok := baseTokenSecret()
	fakeFF := &fakeFerrFlow{
		bulkReveal: func(_ context.Context, _, _, _, _ string, _ []string) (*ferrflow.BulkRevealResponse, error) {
			return &ferrflow.BulkRevealResponse{Secrets: map[string]string{"K": "V"}}, nil
		},
	}
	r := newTestReconciler(t, []client.Object{cr, conn, tok}, fakeFF)

	if _, err := reconcileOnce(t, r, cr); err != nil {
		t.Fatalf("Reconcile err: %v", err)
	}

	var got corev1.Secret
	if err := r.Get(context.Background(), types.NamespacedName{
		Namespace: testNamespace, Name: testTargetName,
	}, &got); err != nil {
		t.Fatalf("get target Secret: %v", err)
	}
	if len(got.OwnerReferences) != 1 {
		t.Fatalf("owner refs = %d, want 1", len(got.OwnerReferences))
	}
	or := got.OwnerReferences[0]
	if or.UID != cr.UID {
		t.Fatalf("owner UID = %q, want %q", or.UID, cr.UID)
	}
	if or.Controller == nil || !*or.Controller {
		t.Fatalf("owner Controller = %v, want true", or.Controller)
	}
	if or.Kind != "FerrFlowSecret" {
		t.Fatalf("owner Kind = %q, want FerrFlowSecret", or.Kind)
	}
}

// containsSubstring is a tiny helper so we don't drag in strings for a one-off
// `Contains` check in the MissingKeys test.
func containsSubstring(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
