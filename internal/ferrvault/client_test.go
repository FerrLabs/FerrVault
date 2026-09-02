package ferrvault

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient wires a client to the given server with retry delays short
// enough that the full test suite stays snappy, and a fixed random source so
// jitter-sensitive assertions are deterministic.
func newTestClient(t *testing.T, baseURL string, policy RetryPolicy) *Client {
	t.Helper()
	c, err := New(baseURL, "fft_test", WithRetry(policy))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func fastPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		Backoff:     []time.Duration{1 * time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond},
		Jitter:      0.25,
		rand:        rand.New(rand.NewSource(1)),
	}
}

func TestBulkReveal_5xxThenSuccess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"secrets":{"FOO":"bar"},"missing":[],"vault":{"id":"v1","name":"n","environment":"dev"}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, fastPolicy())
	resp, err := c.BulkReveal(context.Background(), "o", "p", "n", "ns", nil)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got := resp.Secrets["FOO"]; got != "bar" {
		t.Fatalf("unexpected secret: %q", got)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestBulkReveal_TransportErrorThenSuccess(t *testing.T) {
	// We close the first connection to trigger a transport error, then serve
	// a real 200 on the retry. The simplest way is a server that tracks the
	// call count and hijacks+closes the first request's connection.
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatalf("hijack not supported")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			_ = conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"secrets":{},"missing":[],"vault":{"id":"v1","name":"n","environment":"dev"}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, fastPolicy())
	if _, err := c.BulkReveal(context.Background(), "o", "p", "n", "ns", nil); err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestBulkReveal_4xxNoRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"vault not found"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, fastPolicy())
	_, err := c.BulkReveal(context.Background(), "o", "p", "n", "ns", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsNotFound(err) {
		t.Fatalf("expected NotFoundError, got %T: %v", err, err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected exactly 1 request (no retry on 4xx), got %d", got)
	}
}

func TestBulkReveal_All5xxReturnsAPIErrorWithAttempts(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, fastPolicy())
	_, err := c.BulkReveal(context.Background(), "o", "p", "n", "ns", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", apiErr.Status)
	}
	if apiErr.Attempts != 3 {
		t.Fatalf("attempts = %d, want 3", apiErr.Attempts)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("expected 3 calls, got %d", got)
	}
}

func TestBulkReveal_ContextCancelledMidRetry(t *testing.T) {
	// Server always 500s, so the client will want to retry. We cancel the
	// context after the first failure lands, which should abort the pending
	// backoff and return ctx.Err().
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// Generous backoff so the cancel clearly races against the sleep.
	policy := RetryPolicy{
		MaxAttempts: 5,
		Backoff:     []time.Duration{500 * time.Millisecond, 500 * time.Millisecond, 500 * time.Millisecond, 500 * time.Millisecond},
		Jitter:      0,
	}
	c := newTestClient(t, srv.URL, policy)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := c.BulkReveal(ctx, "o", "p", "n", "ns", nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("cancel did not abort promptly: %v", elapsed)
	}
}

func TestBackoffFor_JitterRange(t *testing.T) {
	c := &Client{retry: RetryPolicy{
		Backoff: []time.Duration{100 * time.Millisecond},
		Jitter:  0.25,
		rand:    rand.New(rand.NewSource(42)),
	}}
	base := 100 * time.Millisecond
	low := time.Duration(float64(base) * 0.75)
	high := time.Duration(float64(base) * 1.25)
	for i := 0; i < 500; i++ {
		d := c.backoffFor(1)
		if d < low || d > high {
			t.Fatalf("delay %v outside [%v, %v]", d, low, high)
		}
	}
}

func TestBackoffFor_NoJitter(t *testing.T) {
	c := &Client{retry: RetryPolicy{
		Backoff: []time.Duration{100 * time.Millisecond, 400 * time.Millisecond, 1600 * time.Millisecond},
		Jitter:  0,
	}}
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 100 * time.Millisecond},
		{2, 400 * time.Millisecond},
		{3, 1600 * time.Millisecond},
		// Attempts past the schedule clamp to the last entry.
		{4, 1600 * time.Millisecond},
	}
	for _, tc := range cases {
		if got := c.backoffFor(tc.attempt); got != tc.want {
			t.Fatalf("backoffFor(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestMaxAttempts1DisablesRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, RetryPolicy{MaxAttempts: 1})
	_, err := c.BulkReveal(context.Background(), "o", "p", "n", "ns", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 call, got %d", got)
	}
}

func TestProbe_RetriesOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("expected /healthz, got %s", r.URL.Path)
		}
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, fastPolicy())
	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 calls, got %d", got)
	}
}

func TestRevealFromVault_PostsBodyAndMapsHits(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/operator/secrets/reveal" {
			t.Errorf("expected /operator/secrets/reveal, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer fft_test" {
			t.Errorf("missing or wrong Authorization header: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hits":[{"name":"DATABASE_URL","value":"postgres://u:p@h/db","version":3,"fetched_at":"2026-05-23T08:00:00Z"},{"name":"JWT_SECRET","value":"deadbeef","version":1,"fetched_at":"2026-05-23T08:00:00Z"}],"missing":["MISSING_KEY"]}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, fastPolicy())
	resp, err := c.RevealFromVault(context.Background(), "production", []string{"DATABASE_URL", "JWT_SECRET", "MISSING_KEY"})
	if err != nil {
		t.Fatalf("RevealFromVault: %v", err)
	}
	if got := resp.Secrets["DATABASE_URL"]; got != "postgres://u:p@h/db" {
		t.Errorf("DATABASE_URL mismatch: %q", got)
	}
	if got := resp.Secrets["JWT_SECRET"]; got != "deadbeef" {
		t.Errorf("JWT_SECRET mismatch: %q", got)
	}
	if len(resp.Missing) != 1 || resp.Missing[0] != "MISSING_KEY" {
		t.Errorf("missing slice mismatch: %v", resp.Missing)
	}
	if resp.Vault.Name != "production" {
		t.Errorf("expected echoed vault name, got %q", resp.Vault.Name)
	}
}

func TestRevealFromVault_403ReturnsAuthError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"wrong vault"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, fastPolicy())
	_, err := c.RevealFromVault(context.Background(), "production", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) || authErr.Kind != AuthForbidden {
		t.Errorf("expected AuthForbidden, got %T %v", err, err)
	}
}

// IsRateLimited doit reconnaitre le 429 tel que le serveur le renvoie
// reellement. Le message observe en production est
// « ferrvault api error 429: Too Many Requests », produit par la branche
// `default` du classement de statut.
func TestIsRateLimited(t *testing.T) {
	if !IsRateLimited(&APIError{Status: 429, Message: "Too Many Requests"}) {
		t.Fatal("un 429 doit etre reconnu comme une limitation de debit")
	}
	// Un 429 est un report, pas une panne : il ne doit pas etre confondu avec
	// les erreurs qui exigent une intervention humaine.
	for _, e := range []error{
		&APIError{Status: 500, Message: "boom"},
		&APIError{Status: 400, Message: "bad request"},
		&AuthError{Kind: AuthUnauthorized, Message: "nope"},
		&NotFoundError{Message: "absent"},
	} {
		if IsRateLimited(e) {
			t.Fatalf("%v ne doit pas etre pris pour une limitation de debit", e)
		}
	}
	if IsRateLimited(nil) {
		t.Fatal("nil n'est pas une limitation de debit")
	}
}
