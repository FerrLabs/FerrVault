package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ffv1alpha1 "github.com/FerrFlow-Org/FerrFlow-Operator/api/v1alpha1"
)

// brokerWithFakes returns a broker wired to a fake k8s client and the
// provided in-memory reader + exchanger. Lets each test stub the bits
// that would otherwise hit disk or the network.
func brokerWithFakes(
	t *testing.T,
	reader tokenReader,
	exchanger oidcExchanger,
	objs ...client.Object,
) *TokenBroker {
	t.Helper()
	scheme := newTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &TokenBroker{
		Client:    c,
		ReadToken: reader,
		Exchange:  exchanger,
	}
}

func TestBroker_TokenSecretRefHappyPath(t *testing.T) {
	conn := &ffv1alpha1.FerrFlowConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "conn", Namespace: "ns"},
		Spec: ffv1alpha1.FerrFlowConnectionSpec{
			URL:            "https://ferrflow.example.com",
			Organization:   "acme",
			TokenSecretRef: &ffv1alpha1.SecretKeyRef{Name: "tok", Key: "token"},
		},
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tok", Namespace: "ns"},
		Data:       map[string][]byte{"token": []byte("fft_abc123")},
	}
	b := brokerWithFakes(t, nil, nil, sec)
	got, err := b.TokenFor(context.Background(), conn)
	if err != nil {
		t.Fatalf("TokenFor: %v", err)
	}
	if got != "fft_abc123" {
		t.Fatalf("token = %q, want fft_abc123", got)
	}
}

func TestBroker_TokenSecretRefMissingSecretFails(t *testing.T) {
	conn := &ffv1alpha1.FerrFlowConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "conn", Namespace: "ns"},
		Spec: ffv1alpha1.FerrFlowConnectionSpec{
			URL:            "https://ferrflow.example.com",
			Organization:   "acme",
			TokenSecretRef: &ffv1alpha1.SecretKeyRef{Name: "missing", Key: "token"},
		},
	}
	b := brokerWithFakes(t, nil, nil)
	if _, err := b.TokenFor(context.Background(), conn); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestBroker_OIDCHappyPathCaches(t *testing.T) {
	conn := &ffv1alpha1.FerrFlowConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "conn", Namespace: "ns"},
		Spec: ffv1alpha1.FerrFlowConnectionSpec{
			URL:          "https://ferrflow.example.com",
			Organization: "acme",
			OIDC: &ffv1alpha1.OIDCAuth{
				ClusterID: "00000000-0000-0000-0000-000000000001",
				TokenPath: "/ignored/in/fake",
			},
		},
	}
	reader := func(_ string) (string, error) { return "eyJhbGciOi…fake-jwt", nil }
	calls := 0
	exchanger := func(_ context.Context, _, _, _ string) (string, time.Time, error) {
		calls++
		return "ffclust_short_lived", time.Now().Add(15 * time.Minute), nil
	}
	b := brokerWithFakes(t, reader, exchanger)

	for i := 0; i < 3; i++ {
		got, err := b.TokenFor(context.Background(), conn)
		if err != nil {
			t.Fatalf("TokenFor iter %d: %v", i, err)
		}
		if got != "ffclust_short_lived" {
			t.Fatalf("iter %d: got %q, want ffclust_short_lived", i, got)
		}
	}
	if calls != 1 {
		t.Fatalf("exchanger called %d times, want 1 (cache hit on subsequent calls)", calls)
	}
}

func TestBroker_OIDCRefreshesBeforeExpiry(t *testing.T) {
	// Bearer expires within the refresh leeway (60s) — every TokenFor call
	// should re-exchange instead of returning the about-to-die token.
	conn := &ffv1alpha1.FerrFlowConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "conn", Namespace: "ns"},
		Spec: ffv1alpha1.FerrFlowConnectionSpec{
			URL:          "https://ferrflow.example.com",
			Organization: "acme",
			OIDC:         &ffv1alpha1.OIDCAuth{ClusterID: "c"},
		},
	}
	reader := func(_ string) (string, error) { return "jwt", nil }
	calls := 0
	exchanger := func(_ context.Context, _, _, _ string) (string, time.Time, error) {
		calls++
		// Return a bearer that expires in 10s — inside the 60s leeway.
		return "bearer", time.Now().Add(10 * time.Second), nil
	}
	b := brokerWithFakes(t, reader, exchanger)

	for i := 0; i < 3; i++ {
		if _, err := b.TokenFor(context.Background(), conn); err != nil {
			t.Fatalf("TokenFor iter %d: %v", i, err)
		}
	}
	if calls != 3 {
		t.Fatalf("exchanger called %d times, want 3 (each call refreshes inside leeway)", calls)
	}
}

func TestBroker_OIDCExchangeErrorPropagates(t *testing.T) {
	conn := &ffv1alpha1.FerrFlowConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "conn", Namespace: "ns"},
		Spec: ffv1alpha1.FerrFlowConnectionSpec{
			URL:          "https://ferrflow.example.com",
			Organization: "acme",
			OIDC:         &ffv1alpha1.OIDCAuth{ClusterID: "c"},
		},
	}
	reader := func(_ string) (string, error) { return "jwt", nil }
	exchanger := func(_ context.Context, _, _, _ string) (string, time.Time, error) {
		return "", time.Time{}, errors.New("boom")
	}
	b := brokerWithFakes(t, reader, exchanger)
	if _, err := b.TokenFor(context.Background(), conn); err == nil {
		t.Fatalf("expected exchange error to propagate")
	}
}

func TestBroker_RefusesBothAuthModesSet(t *testing.T) {
	conn := &ffv1alpha1.FerrFlowConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "conn", Namespace: "ns"},
		Spec: ffv1alpha1.FerrFlowConnectionSpec{
			URL:            "https://ferrflow.example.com",
			Organization:   "acme",
			TokenSecretRef: &ffv1alpha1.SecretKeyRef{Name: "x", Key: "y"},
			OIDC:           &ffv1alpha1.OIDCAuth{ClusterID: "c"},
		},
	}
	b := brokerWithFakes(t, nil, nil)
	if _, err := b.TokenFor(context.Background(), conn); err == nil {
		t.Fatalf("expected error when both auth modes are set")
	}
}

func TestBroker_RefusesNeitherAuthModeSet(t *testing.T) {
	conn := &ffv1alpha1.FerrFlowConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "conn", Namespace: "ns"},
		Spec: ffv1alpha1.FerrFlowConnectionSpec{
			URL:          "https://ferrflow.example.com",
			Organization: "acme",
		},
	}
	b := brokerWithFakes(t, nil, nil)
	if _, err := b.TokenFor(context.Background(), conn); err == nil {
		t.Fatalf("expected error when no auth mode is set")
	}
}

func TestBroker_InvalidateForcesRefresh(t *testing.T) {
	conn := &ffv1alpha1.FerrFlowConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "conn", Namespace: "ns"},
		Spec: ffv1alpha1.FerrFlowConnectionSpec{
			URL:          "https://ferrflow.example.com",
			Organization: "acme",
			OIDC:         &ffv1alpha1.OIDCAuth{ClusterID: "c"},
		},
	}
	reader := func(_ string) (string, error) { return "jwt", nil }
	calls := 0
	exchanger := func(_ context.Context, _, _, _ string) (string, time.Time, error) {
		calls++
		return "bearer", time.Now().Add(15 * time.Minute), nil
	}
	b := brokerWithFakes(t, reader, exchanger)

	_, _ = b.TokenFor(context.Background(), conn)
	b.Invalidate(conn.Namespace, conn.Name)
	_, _ = b.TokenFor(context.Background(), conn)
	if calls != 2 {
		t.Fatalf("exchanger called %d times, want 2 (once initial, once post-invalidate)", calls)
	}
}
