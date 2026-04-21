// Package controller — token broker.
//
// Central resolver for the bearer token the operator presents on FerrFlow
// API calls. Two modes:
//
//   - **tokenSecretRef** — long-lived `ffclust_` / `fft_` stored in a k8s
//     Secret. Direct read-through, no caching beyond what the client cache
//     already does on the Secret.
//   - **oidc** — projected ServiceAccount token exchanged for a short-lived
//     `ffclust_` via FerrFlow's `POST /clusters/oidc-exchange`. Cached with
//     a 60-second early-refresh window so we never hand out a token that's
//     about to expire mid-request.
//
// The broker is shared between both reconcilers (FerrFlowSecret and
// FerrFlowConnection) so they don't duplicate the resolution logic or the
// OIDC cache.

package controller

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ffv1alpha1 "github.com/FerrLabs/FerrFlow-Operator/api/v1alpha1"
	"github.com/FerrLabs/FerrFlow-Operator/internal/ferrflow"
)

// oidcRefreshLeeway is how early (before expiry) we refresh the cached
// bearer. Matches the FerrFlow JWT leeway on the server side so clients
// don't present a token that's one clock-skew-tick past valid.
const oidcRefreshLeeway = 60 * time.Second

// tokenReader reads the projected SA token off disk. Defaults to the real
// filesystem; tests override it to inject a deterministic string without
// creating a tempfile dance. Plain function instead of an interface — the
// surface is one operation.
type tokenReader func(path string) (string, error)

// oidcExchanger is the per-connection client function that posts an SA JWT
// to FerrFlow and returns a short-lived bearer + its expiry. Abstracted so
// tests don't need to spin up an HTTP server.
type oidcExchanger func(ctx context.Context, baseURL, clusterID, saToken string) (string, time.Time, error)

// TokenBroker resolves bearer tokens for FerrFlowConnections. Safe for
// concurrent use by both reconcilers.
//
// The OIDC cache is keyed by `(namespace, connection name)` — two
// connections in different namespaces that happen to share a cluster ID
// still get separate cache entries because they read separate SA tokens
// (namespace-scoped ServiceAccounts).
type TokenBroker struct {
	Client client.Client

	// ReadToken is the filesystem read for the projected SA token. Set to
	// nil to use the default (`os.ReadFile`).
	ReadToken tokenReader

	// Exchange is the HTTP POST to FerrFlow's `/clusters/oidc-exchange`.
	// Set to nil to use the real ferrflow.Client. Tests inject fakes.
	Exchange oidcExchanger

	mu    sync.Mutex
	cache map[cacheKey]*cachedBearer
}

type cacheKey struct {
	namespace string
	name      string
}

type cachedBearer struct {
	token     string
	expiresAt time.Time
}

// NewTokenBroker builds a broker wired to the real filesystem and FerrFlow
// HTTP client. Test code should construct `TokenBroker{...}` directly and
// fill the `ReadToken` / `Exchange` hooks.
func NewTokenBroker(c client.Client) *TokenBroker {
	return &TokenBroker{
		Client:    c,
		ReadToken: readFileString,
		Exchange:  realExchange,
		cache:     make(map[cacheKey]*cachedBearer),
	}
}

// readFileString is the default token reader — os.ReadFile wrapped to
// return the string type the broker expects.
func readFileString(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// TokenFor resolves the bearer for a connection. Called on every reconcile
// that needs to hit the FerrFlow API.
//
// Returns an error when:
//   - Neither `tokenSecretRef` nor `oidc` is set (spec validation failure).
//   - Both are set (spec validation failure — we refuse to guess).
//   - The configured mode fails to produce a token (Secret missing, SA
//     token file missing, exchange rejected).
func (b *TokenBroker) TokenFor(
	ctx context.Context,
	conn *ffv1alpha1.FerrFlowConnection,
) (string, error) {
	hasToken := conn.Spec.TokenSecretRef != nil
	hasOIDC := conn.Spec.OIDC != nil
	switch {
	case !hasToken && !hasOIDC:
		return "", fmt.Errorf("FerrFlowConnection %s/%s: set either tokenSecretRef or oidc",
			conn.Namespace, conn.Name)
	case hasToken && hasOIDC:
		return "", fmt.Errorf("FerrFlowConnection %s/%s: set only one of tokenSecretRef or oidc, not both",
			conn.Namespace, conn.Name)
	case hasToken:
		return b.loadFromSecret(ctx, conn)
	default:
		return b.loadFromOIDC(ctx, conn)
	}
}

func (b *TokenBroker) loadFromSecret(
	ctx context.Context,
	conn *ffv1alpha1.FerrFlowConnection,
) (string, error) {
	ref := conn.Spec.TokenSecretRef
	var tokenSecret corev1.Secret
	key := types.NamespacedName{Namespace: conn.Namespace, Name: ref.Name}
	if err := b.Client.Get(ctx, key, &tokenSecret); err != nil {
		return "", fmt.Errorf("load token Secret %s: %w", key, err)
	}
	raw, ok := tokenSecret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("key %q missing from token Secret %s", ref.Key, key)
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("token Secret %s has empty value at key %q", key, ref.Key)
	}
	return string(raw), nil
}

func (b *TokenBroker) loadFromOIDC(
	ctx context.Context,
	conn *ffv1alpha1.FerrFlowConnection,
) (string, error) {
	// Cache hit? Skip the disk read + HTTP exchange entirely.
	if cached := b.cacheGet(conn); cached != nil {
		return *cached, nil
	}

	// Read the projected SA token. Kubelet refreshes the file in place on
	// rotation, so we read every cache miss rather than caching the JWT
	// itself.
	path := conn.Spec.OIDC.TokenPath
	if path == "" {
		path = ffv1alpha1.DefaultTokenPath
	}
	saToken, err := b.ReadToken(path)
	if err != nil {
		return "", fmt.Errorf("read projected SA token at %s: %w", path, err)
	}
	saToken = strings.TrimSpace(saToken)
	if saToken == "" {
		return "", fmt.Errorf("projected SA token at %s is empty", path)
	}

	bearer, expiresAt, err := b.Exchange(ctx, conn.Spec.URL, conn.Spec.OIDC.ClusterID, saToken)
	if err != nil {
		return "", fmt.Errorf("OIDC exchange failed: %w", err)
	}
	b.cachePut(conn, bearer, expiresAt)
	return bearer, nil
}

func (b *TokenBroker) cacheGet(conn *ffv1alpha1.FerrFlowConnection) *string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cache == nil {
		b.cache = make(map[cacheKey]*cachedBearer)
		return nil
	}
	entry, ok := b.cache[cacheKey{conn.Namespace, conn.Name}]
	if !ok {
		return nil
	}
	if time.Until(entry.expiresAt) <= oidcRefreshLeeway {
		// About to expire — drop and force a refresh so we don't hand out
		// a token that's going to fail mid-request on the downstream API.
		delete(b.cache, cacheKey{conn.Namespace, conn.Name})
		return nil
	}
	token := entry.token
	return &token
}

func (b *TokenBroker) cachePut(
	conn *ffv1alpha1.FerrFlowConnection,
	token string,
	expiresAt time.Time,
) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cache == nil {
		b.cache = make(map[cacheKey]*cachedBearer)
	}
	b.cache[cacheKey{conn.Namespace, conn.Name}] = &cachedBearer{
		token:     token,
		expiresAt: expiresAt,
	}
}

// Invalidate drops the cache entry for a connection. Called when the
// connection is deleted or its OIDC config changes, so stale bearers don't
// outlive the config that authorised them.
func (b *TokenBroker) Invalidate(namespace, name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.cache, cacheKey{namespace, name})
}

// realExchange is the production exchanger — builds a ferrflow.Client with
// an empty bearer (the exchange endpoint is public) and calls OIDCExchange.
func realExchange(
	ctx context.Context,
	baseURL, clusterID, saToken string,
) (string, time.Time, error) {
	// ferrflow.New requires a non-empty token; we pass a dummy "anonymous"
	// string because the exchange endpoint doesn't inspect the Bearer at
	// all — the body IS the auth.
	c, err := ferrflow.New(baseURL, "anonymous")
	if err != nil {
		return "", time.Time{}, err
	}
	return c.OIDCExchange(ctx, clusterID, saToken)
}

// compile-time check that the default reader matches the interface shape.
var _ tokenReader = readFileString
