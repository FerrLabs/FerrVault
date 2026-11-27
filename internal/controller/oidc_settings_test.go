package controller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fvv1alpha1 "github.com/FerrLabs/FerrVault/api/ferrvault/v1alpha1"
)

type oidcFixture struct {
	server    *httptest.Server
	requests  *atomic.Int32
	bodies    chan string
	reads     *atomic.Int32
	tokenFile string
}

func newOIDCFixture(t *testing.T) *oidcFixture {
	t.Helper()
	f := &oidcFixture{requests: &atomic.Int32{}, reads: &atomic.Int32{}, bodies: make(chan string, 4)}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		f.bodies <- string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"ffclust_minted","expires_at":"2099-01-01T00:00:00Z","token_type":"Bearer"}`))
	}))
	t.Cleanup(f.server.Close)
	f.tokenFile = filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(f.tokenFile, []byte("sa-secret-jwt"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *oidcFixture) broker(t *testing.T, settings OIDCSettings) *TokenBroker {
	t.Helper()
	b := brokerWithFakes(t, func(path string) (string, error) {
		f.reads.Add(1)
		return readFileString(path)
	}, realExchange)
	b.OIDC = settings
	return b
}

func oidcConn(url, tokenPath string) *fvv1alpha1.FerrVaultConnection {
	return &fvv1alpha1.FerrVaultConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "conn", Namespace: "ns"},
		Spec: fvv1alpha1.ConnectionSpec{
			URL: url,
			OIDC: &fvv1alpha1.OIDCAuth{
				ClusterID: "00000000-0000-0000-0000-000000000001",
				TokenPath: tokenPath,
			},
		},
	}
}

func TestBroker_OIDCRefusesForeignTokenPathWithoutReadOrSend(t *testing.T) {
	f := newOIDCFixture(t)
	b := f.broker(t, OIDCSettings{URL: f.server.URL})

	_, err := b.TokenFor(context.Background(), oidcConn(f.server.URL, f.tokenFile))

	var notAllowed *OIDCNotAllowedError
	if !errors.As(err, &notAllowed) || notAllowed.Field != "tokenPath" {
		t.Fatalf("err = %v, want OIDCNotAllowedError on tokenPath", err)
	}
	if f.reads.Load() != 0 || f.requests.Load() != 0 {
		t.Fatalf("reads=%d requests=%d, want 0 and 0", f.reads.Load(), f.requests.Load())
	}
}

func TestBroker_OIDCRefusesForeignURLWithoutReadOrSend(t *testing.T) {
	f := newOIDCFixture(t)
	b := f.broker(t, OIDCSettings{TokenPath: f.tokenFile, URL: "https://ferrvault.example.com"})

	_, err := b.TokenFor(context.Background(), oidcConn(f.server.URL, ""))

	var notAllowed *OIDCNotAllowedError
	if !errors.As(err, &notAllowed) || notAllowed.Field != "url" {
		t.Fatalf("err = %v, want OIDCNotAllowedError on url", err)
	}
	if f.reads.Load() != 0 || f.requests.Load() != 0 {
		t.Fatalf("reads=%d requests=%d, want 0 and 0", f.reads.Load(), f.requests.Load())
	}
}

func TestBroker_OIDCRefusesEveryURLWhenOperatorURLUnset(t *testing.T) {
	f := newOIDCFixture(t)
	b := f.broker(t, OIDCSettings{TokenPath: f.tokenFile})

	_, err := b.TokenFor(context.Background(), oidcConn(f.server.URL, ""))

	var notAllowed *OIDCNotAllowedError
	if !errors.As(err, &notAllowed) || notAllowed.Field != "url" {
		t.Fatalf("err = %v, want OIDCNotAllowedError on url", err)
	}
	if f.reads.Load() != 0 || f.requests.Load() != 0 {
		t.Fatalf("reads=%d requests=%d, want 0 and 0", f.reads.Load(), f.requests.Load())
	}
}

func TestBroker_OIDCAcceptsOperatorPathAndURL(t *testing.T) {
	f := newOIDCFixture(t)
	b := f.broker(t, OIDCSettings{TokenPath: f.tokenFile, URL: f.server.URL})

	for _, connPath := range []string{"", f.tokenFile} {
		b.Invalidate("ns", "conn")
		got, err := b.TokenFor(context.Background(), oidcConn(f.server.URL, connPath))
		if err != nil {
			t.Fatalf("tokenPath %q: %v", connPath, err)
		}
		if got != "ffclust_minted" {
			t.Fatalf("tokenPath %q: got %q", connPath, got)
		}
		if body := <-f.bodies; !strings.Contains(body, "sa-secret-jwt") {
			t.Fatalf("exchange body %q does not carry the operator token", body)
		}
	}
}

func TestBroker_OIDCDefaultSettingsUseDefaultTokenPath(t *testing.T) {
	var readPath string
	b := brokerWithFakes(t, func(path string) (string, error) {
		readPath = path
		return "jwt", nil
	}, func(_ context.Context, _, _, _ string) (string, time.Time, error) {
		return "bearer", time.Now().Add(15 * time.Minute), nil
	})

	if _, err := b.TokenFor(context.Background(), oidcConn("https://ferrvault.example.com", "")); err != nil {
		t.Fatal(err)
	}
	if readPath != fvv1alpha1.DefaultTokenPath {
		t.Fatalf("read %q, want %q", readPath, fvv1alpha1.DefaultTokenPath)
	}
}

func TestBroker_TokenSecretRefIgnoresOIDCSettings(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tok", Namespace: "ns"},
		Data:       map[string][]byte{"token": []byte("fvsat_abc")},
	}
	b := brokerWithFakes(t, nil, nil, secret)
	b.OIDC = OIDCSettings{}
	conn := &fvv1alpha1.FerrVaultConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "conn", Namespace: "ns"},
		Spec: fvv1alpha1.ConnectionSpec{
			URL:            "https://other.example.com",
			TokenSecretRef: &fvv1alpha1.SecretKeyRef{Name: "tok", Key: "token"},
		},
	}

	got, err := b.TokenFor(context.Background(), conn)
	if err != nil || got != "fvsat_abc" {
		t.Fatalf("got %q, %v, want fvsat_abc", got, err)
	}
}

func TestConnectionProbe_ReportsOIDCNotAllowedReason(t *testing.T) {
	f := newOIDCFixture(t)
	broker := f.broker(t, OIDCSettings{URL: "https://ferrvault.example.com"})
	r := &FerrVaultConnectionReconciler{Client: broker.Client, Broker: broker}

	status, reason, message, err := r.probe(context.Background(), oidcConn(f.server.URL, ""))

	if status != metav1.ConditionFalse || reason != "OIDCNotAllowed" || err == nil {
		t.Fatalf("status=%s reason=%s err=%v", status, reason, err)
	}
	if !strings.Contains(message, f.server.URL) {
		t.Fatalf("message %q does not name the refused url", message)
	}
	if f.requests.Load() != 0 {
		t.Fatalf("requests=%d, want 0", f.requests.Load())
	}
}
