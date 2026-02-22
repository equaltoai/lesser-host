package payments

import "github.com/equaltoai/lesser-host/internal/secrets"

// NewProvider constructs a payments Provider by name.
// The ssmClient is used by the Stripe provider to fetch API keys from SSM.
// Passing nil falls back to the default AWS SSM client.
func NewProvider(name string, ssmClient secrets.SSMAPI) Provider {
	switch normalizeProviderName(name) {
	case providerNameStripe:
		return stripeProvider{ssmClient: ssmClient}
	default:
		return noopProvider{}
	}
}
