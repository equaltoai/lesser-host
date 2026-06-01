package controlplane

const (
	defaultControlPlaneStage = "lab"

	defaultManagedParentDomain = "greater.website"
	defaultAIModelSet          = "openai:gpt-5-mini-2025-08-07"

	lesserHostDomain = "lesser.host"

	domainVerificationMethodDNSTXT = "dns_txt"

	paymentsProviderStripeName = "stripe"
	paymentsActorStripe        = "stripe"

	appErrCodeBadRequest   = "app.bad_request"
	appErrCodeForbidden    = "app.forbidden"
	appErrCodeUnauthorized = "app.unauthorized"
	appErrCodeConflict     = "app.conflict"

	cacheControlNoStore = "no-store"

	ensGatewayChainMainnet = "mainnet"
	ensGatewayChainSepolia = "sepolia"
)
