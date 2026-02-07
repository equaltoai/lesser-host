package payments

// NewProvider constructs a payments Provider by name.
func NewProvider(name string) Provider {
	switch normalizeProviderName(name) {
	case providerNameStripe:
		return stripeProvider{}
	default:
		return noopProvider{}
	}
}
