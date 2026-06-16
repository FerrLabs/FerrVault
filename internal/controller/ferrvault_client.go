package controller

import (
	"context"

	"github.com/FerrLabs/FerrVault/internal/ferrvault"
)

// ferrvaultClient is the narrow surface of the FerrVault HTTP client the
// reconciler actually uses. Scoped down so tests can inject a fake without
// needing to stand up a real HTTP server.
type ferrvaultClient interface {
	// BulkReveal — legacy FerrLabs-Cloud reveal endpoint.
	BulkReveal(ctx context.Context, org, project, vault, namespace string, names []string) (*ferrvault.BulkRevealResponse, error)
	// RevealFromVault — new FerrVault SaaS reveal endpoint (Mode=ferrvault).
	RevealFromVault(ctx context.Context, vault string, names []string) (*ferrvault.BulkRevealResponse, error)
}

// ClientFactory builds a ferrvaultClient for a given base URL and token. Swapped
// out in unit tests to return a fake.
type ClientFactory func(baseURL, token string) (ferrvaultClient, error)

// defaultClientFactory is the production factory — hands back a real
// *ferrvault.Client, which satisfies ferrvaultClient via its BulkReveal method.
func defaultClientFactory(baseURL, token string) (ferrvaultClient, error) {
	return ferrvault.New(baseURL, token)
}
