package controller

import (
	"context"

	"github.com/FerrLabs/FerrFlow-Operator/internal/ferrflow"
)

// ferrflowClient is the narrow surface of the FerrFlow HTTP client the
// reconciler actually uses. Scoped down so tests can inject a fake without
// needing to stand up a real HTTP server.
type ferrflowClient interface {
	BulkReveal(ctx context.Context, org, project, vault, namespace string, names []string) (*ferrflow.BulkRevealResponse, error)
}

// ClientFactory builds a ferrflowClient for a given base URL and token. Swapped
// out in unit tests to return a fake.
type ClientFactory func(baseURL, token string) (ferrflowClient, error)

// defaultClientFactory is the production factory — hands back a real
// *ferrflow.Client, which satisfies ferrflowClient via its BulkReveal method.
func defaultClientFactory(baseURL, token string) (ferrflowClient, error) {
	return ferrflow.New(baseURL, token)
}
