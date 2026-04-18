// Package ferrflow is the HTTP client the operator uses to talk to a FerrFlow
// API instance. It's deliberately narrow: only the endpoints the reconciler
// actually needs, with sharp error typing so the controller can translate
// transport failures into `Ready=False` conditions without guessing.
package ferrflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a narrow FerrFlow HTTP client.
//
// Zero value is not usable; construct via `New`.
type Client struct {
	baseURL *url.URL
	token   string
	http    *http.Client
}

// New constructs a client targeting `baseURL` with the given bearer token.
// `baseURL` is the API root — e.g. `https://ferrflow.example.com`. The client
// adds `/api/v1/...` paths itself.
func New(baseURL, token string) (*Client, error) {
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
	return &Client{
		baseURL: u,
		token:   token,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}, nil
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("ferrflow: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	// Namespace header: ignored by the API for user tokens, strictly required
	// for cluster identities. Always send it so operators can switch token
	// types by just swapping the Secret, no operator config change.
	if namespace != "" {
		req.Header.Set("X-FerrFlow-Namespace", namespace)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &TransportError{Underlying: err}
	}
	defer func() { _ = resp.Body.Close() }()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, &TransportError{Underlying: readErr}
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var out BulkRevealResponse
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("ferrflow: decode response: %w", err)
		}
		return &out, nil
	case http.StatusUnauthorized:
		return nil, &AuthError{Kind: AuthUnauthorized, Message: errorMessage(body, "unauthorized")}
	case http.StatusForbidden:
		return nil, &AuthError{Kind: AuthForbidden, Message: errorMessage(body, "forbidden")}
	case http.StatusNotFound:
		return nil, &NotFoundError{Message: errorMessage(body, "not found")}
	default:
		return nil, &APIError{
			Status:  resp.StatusCode,
			Message: errorMessage(body, http.StatusText(resp.StatusCode)),
		}
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
// The reconciler should retry these with backoff.
type TransportError struct {
	Underlying error
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

// APIError covers any remaining non-2xx status.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("ferrflow api error %d: %s", e.Status, e.Message)
}
