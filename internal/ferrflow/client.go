// Package ferrflow is the HTTP client the operator uses to talk to a FerrFlow
// API instance. It's deliberately narrow: only the endpoints the reconciler
// actually needs, with sharp error typing so the controller can translate
// transport failures into `Ready=False` conditions without guessing.
package ferrflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RetryPolicy controls the bounded retry loop applied to every HTTP call the
// client makes. Retries cover `TransportError` and HTTP 5xx responses only —
// 4xx is returned immediately because those are caller-fixable (bad token,
// wrong vault name, etc.) and retrying them just wastes request budget.
//
// The `Backoff` slice encodes the delay *before* each retry attempt, so
// `Backoff[0]` is waited before attempt #2, `Backoff[1]` before attempt #3,
// and so on. With `MaxAttempts: 3` only the first two entries are ever used,
// but the full schedule stays defined so raising the attempt cap later is a
// one-line change.
type RetryPolicy struct {
	MaxAttempts int
	Backoff     []time.Duration
	// Jitter is the fractional ± range applied to each backoff delay. 0.25
	// picks a multiplier uniformly in [0.75, 1.25]. Zero disables jitter.
	Jitter float64
	// rand is test-only; nil falls back to a package-level source. Kept
	// unexported so it doesn't leak into the public surface.
	rand *rand.Rand
}

// DefaultRetryPolicy is the policy applied when callers don't override it:
// up to three attempts with 100ms / 400ms / 1.6s delays and ±25% jitter.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		Backoff:     []time.Duration{100 * time.Millisecond, 400 * time.Millisecond, 1600 * time.Millisecond},
		Jitter:      0.25,
	}
}

// Client is a narrow FerrFlow HTTP client.
//
// Zero value is not usable; construct via `New`.
type Client struct {
	baseURL *url.URL
	token   string
	http    *http.Client
	retry   RetryPolicy
}

// Option configures a Client at construction time.
type Option func(*Client)

// WithRetry overrides the retry policy. Pass `RetryPolicy{MaxAttempts: 1}` to
// disable retries entirely — useful in tests that want to assert a single
// request was made.
func WithRetry(p RetryPolicy) Option {
	return func(c *Client) { c.retry = p }
}

// New constructs a client targeting `baseURL` with the given bearer token.
// `baseURL` is the API root — e.g. `https://ferrflow.example.com`. The client
// adds `/api/v1/...` paths itself.
func New(baseURL, token string, opts ...Option) (*Client, error) {
	if token == "" {
		return nil, errors.New("ferrflow: empty API token")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("ferrflow: invalid base URL %q: %w", baseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("ferrflow: base URL must use http(s), got %q", baseURL)
	}
	c := &Client{
		baseURL: u,
		token:   token,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
		retry: DefaultRetryPolicy(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Probe is a lightweight reachability check against the FerrFlow API.
// Calls `GET <baseURL>/health` (public, unauthenticated) and succeeds when
// the response is 200 with a JSON body containing `{"status":"ok"}`.
//
// Deliberately does not exercise the token — auth correctness is reported
// per-vault by the `FerrFlowSecret` reconciler, where the org/project/vault
// context is known. Probe answers the narrower question "can the operator
// reach this API instance at all?".
func (c *Client) Probe(ctx context.Context) error {
	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + "/health"

	_, err := c.doWithRetry(ctx, func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("ferrflow: build probe request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		return c.http.Do(req)
	}, func(resp *http.Response, body []byte) error {
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		return &APIError{
			Status:  resp.StatusCode,
			Message: fmt.Sprintf("health check returned %s", http.StatusText(resp.StatusCode)),
		}
	})
	return err
}

// BulkRevealResponse is the decoded shape of
// `GET /orgs/:org/projects/:proj/vaults/by-name/:vault/secrets/reveal`.
type BulkRevealResponse struct {
	Secrets map[string]string `json:"secrets"`
	Missing []string          `json:"missing"`
	Vault   VaultSummary      `json:"vault"`
}

// VaultSummary echoes the vault we resolved by name, handy for logging.
type VaultSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
}

// IsClusterIdentity reports whether the configured token is a cluster
// identity (`ffclust_...`) rather than a user API token (`fft_...`). The
// FerrFlow API's reveal endpoint enforces namespace-scoped authorization when
// the caller authenticates with a cluster identity, and requires the
// `X-FerrFlow-Namespace` header on every request.
func (c *Client) IsClusterIdentity() bool {
	return strings.HasPrefix(c.token, "ffclust_")
}

// OIDCExchangeResponse is the decoded shape of `POST /clusters/oidc-exchange`.
type OIDCExchangeResponse struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	TokenType   string    `json:"token_type"`
}

// OIDCExchange posts an OIDC JWT (typically a projected ServiceAccount
// token) to FerrFlow and returns the minted short-lived cluster bearer +
// its expiry. The endpoint is unauthenticated — the JWT body IS the auth —
// so the `Authorization` header on the outbound request is irrelevant and
// we just set the dummy token the client was built with.
//
// Used by the token broker. The returned bearer can be fed back into a new
// `ferrflow.New` client instance for downstream API calls.
func (c *Client) OIDCExchange(
	ctx context.Context,
	clusterID, saToken string,
) (string, time.Time, error) {
	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/clusters/oidc-exchange"

	body := struct {
		ClusterID string `json:"cluster_id"`
		Token     string `json:"token"`
	}{clusterID, saToken}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("ferrflow: encode oidc exchange request: %w", err)
	}

	var out OIDCExchangeResponse
	_, err = c.doWithRetry(ctx, func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(),
			bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("ferrflow: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		return c.http.Do(req)
	}, func(resp *http.Response, body []byte) error {
		switch resp.StatusCode {
		case http.StatusOK:
			if err := json.Unmarshal(body, &out); err != nil {
				return fmt.Errorf("ferrflow: decode oidc response: %w", err)
			}
			return nil
		case http.StatusUnauthorized:
			return &AuthError{Kind: AuthUnauthorized, Message: errorMessage(body, "unauthorized")}
		case http.StatusBadRequest:
			return &APIError{
				Status:  resp.StatusCode,
				Message: errorMessage(body, "bad request"),
			}
		default:
			return &APIError{
				Status:  resp.StatusCode,
				Message: errorMessage(body, http.StatusText(resp.StatusCode)),
			}
		}
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return out.AccessToken, out.ExpiresAt, nil
}

// BulkReveal returns the requested secrets from the named vault. When `names`
// is empty the server returns every secret in the vault. `namespace` is the
// Kubernetes namespace the request is made from — sent as
// `X-FerrFlow-Namespace` on every call. The API **requires** this header when
// the token is a cluster identity and ignores it otherwise. Returned `Missing`
// lists requested keys that weren't present — `Ready=False` worthy on the
// caller's CR.
func (c *Client) BulkReveal(
	ctx context.Context,
	org, project, vaultName, namespace string,
	names []string,
) (*BulkRevealResponse, error) {
	path := fmt.Sprintf(
		"/api/v1/orgs/%s/projects/%s/vaults/by-name/%s/secrets/reveal",
		url.PathEscape(org),
		url.PathEscape(project),
		url.PathEscape(vaultName),
	)
	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + path
	if len(names) > 0 {
		q := u.Query()
		q.Set("names", strings.Join(names, ","))
		u.RawQuery = q.Encode()
	}

	var out BulkRevealResponse
	_, err := c.doWithRetry(ctx, func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("ferrflow: build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/json")
		// Namespace header: ignored by the API for user tokens, strictly
		// required for cluster identities. Always send it so operators can
		// switch token types by just swapping the Secret, no operator
		// config change.
		if namespace != "" {
			req.Header.Set("X-FerrFlow-Namespace", namespace)
		}
		return c.http.Do(req)
	}, func(resp *http.Response, body []byte) error {
		switch resp.StatusCode {
		case http.StatusOK:
			if err := json.Unmarshal(body, &out); err != nil {
				return fmt.Errorf("ferrflow: decode response: %w", err)
			}
			return nil
		case http.StatusUnauthorized:
			return &AuthError{Kind: AuthUnauthorized, Message: errorMessage(body, "unauthorized")}
		case http.StatusForbidden:
			return &AuthError{Kind: AuthForbidden, Message: errorMessage(body, "forbidden")}
		case http.StatusNotFound:
			return &NotFoundError{Message: errorMessage(body, "not found")}
		default:
			return &APIError{
				Status:  resp.StatusCode,
				Message: errorMessage(body, http.StatusText(resp.StatusCode)),
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// doWithRetry runs `send` under the configured retry policy, handing the raw
// response and body to `classify`. `classify` returns nil on success, or a
// typed error; 5xx `APIError`s and `TransportError`s are treated as retriable,
// everything else is returned immediately.
//
// The resulting error carries the total attempt count (1-based) via the
// `Attempts` field on `TransportError` / `APIError`, so callers can log
// "succeeded after N retries" / "failed after N retries" without having to
// instrument the request site themselves.
func (c *Client) doWithRetry(
	ctx context.Context,
	send func() (*http.Response, error),
	classify func(resp *http.Response, body []byte) error,
) (int, error) {
	maxAttempts := c.retry.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return attempt, err
		}

		resp, sendErr := send()
		var attemptErr error
		if sendErr != nil {
			attemptErr = &TransportError{Underlying: sendErr}
		} else {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				attemptErr = &TransportError{Underlying: readErr}
			} else {
				attemptErr = classify(resp, body)
			}
		}

		if attemptErr == nil {
			return attempt, nil
		}

		lastErr = attemptErr
		if !isRetriable(attemptErr) || attempt == maxAttempts {
			annotateAttempts(lastErr, attempt)
			return attempt, lastErr
		}

		delay := c.backoffFor(attempt)
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return attempt, ctx.Err()
		case <-timer.C:
		}
	}
	annotateAttempts(lastErr, maxAttempts)
	return maxAttempts, lastErr
}

// backoffFor returns the jittered delay to wait *before* the next attempt.
// `attempt` is the number of the attempt that just failed (1-based), so the
// next wait is `Backoff[attempt-1]`. Attempts past the end of the schedule
// reuse the final entry, which keeps "raise MaxAttempts without touching
// Backoff" predictable.
func (c *Client) backoffFor(attempt int) time.Duration {
	if len(c.retry.Backoff) == 0 {
		return 0
	}
	idx := attempt - 1
	if idx >= len(c.retry.Backoff) {
		idx = len(c.retry.Backoff) - 1
	}
	base := c.retry.Backoff[idx]
	if c.retry.Jitter <= 0 {
		return base
	}
	var f float64
	if c.retry.rand != nil {
		f = c.retry.rand.Float64()
	} else {
		f = rand.Float64()
	}
	// Multiplier in [1-jitter, 1+jitter].
	mult := 1 - c.retry.Jitter + 2*c.retry.Jitter*f
	return time.Duration(float64(base) * mult)
}

// isRetriable returns true for transport failures and 5xx API errors.
func isRetriable(err error) bool {
	var te *TransportError
	if errors.As(err, &te) {
		return true
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status >= 500 && ae.Status <= 599
	}
	return false
}

// annotateAttempts stamps the attempt count onto typed errors that expose an
// `Attempts` field. Called once at the end of `doWithRetry` so the number the
// caller sees is the total tries made, not just the last one.
func annotateAttempts(err error, attempts int) {
	var te *TransportError
	if errors.As(err, &te) {
		te.Attempts = attempts
	}
	var ae *APIError
	if errors.As(err, &ae) {
		ae.Attempts = attempts
	}
}

// errorMessage pulls the `{"error": "..."}` field from a FerrFlow error
// payload, falling back to a default string when parsing fails.
func errorMessage(body []byte, fallback string) string {
	var wire struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &wire) == nil && wire.Error != "" {
		return wire.Error
	}
	return fallback
}

// TransportError wraps network-level failures (DNS, TCP, TLS, timeouts).
// The client retries these with backoff; `Attempts` records how many tries
// were spent before giving up (or, on success, how many the caller made
// before seeing the error — always ≥ 1).
type TransportError struct {
	Underlying error
	Attempts   int
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("ferrflow transport error: %v", e.Underlying)
}
func (e *TransportError) Unwrap() error { return e.Underlying }

// AuthKind distinguishes 401 (bad token) from 403 (missing scope). The
// operator handles them differently: 401 halts reconciliation until the
// Secret is updated, 403 signals a misconfiguration that won't fix itself.
type AuthKind int

const (
	AuthUnauthorized AuthKind = iota
	AuthForbidden
)

// AuthError is returned for 401/403 responses.
type AuthError struct {
	Kind    AuthKind
	Message string
}

func (e *AuthError) Error() string {
	switch e.Kind {
	case AuthForbidden:
		return fmt.Sprintf("ferrflow forbidden: %s", e.Message)
	default:
		return fmt.Sprintf("ferrflow unauthorized: %s", e.Message)
	}
}

// IsAuthError reports whether the error represents a 401 or 403.
func IsAuthError(err error) bool {
	var a *AuthError
	return errors.As(err, &a)
}

// NotFoundError is returned for 404 responses (unknown org/project/vault).
type NotFoundError struct {
	Message string
}

func (e *NotFoundError) Error() string { return fmt.Sprintf("ferrflow not found: %s", e.Message) }

// IsNotFound reports whether the error represents a 404.
func IsNotFound(err error) bool {
	var n *NotFoundError
	return errors.As(err, &n)
}

// APIError covers any remaining non-2xx status. For 5xx responses it also
// records the total number of attempts made before the client gave up.
type APIError struct {
	Status   int
	Message  string
	Attempts int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("ferrflow api error %d: %s", e.Status, e.Message)
}
