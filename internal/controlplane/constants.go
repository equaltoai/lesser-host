package controlplane

import "github.com/equaltoai/lesser-host/internal/ai/modelselection"

const (
	defaultControlPlaneStage = "lab"

	defaultManagedParentDomain = "greater.website"
	defaultAIModelSet          = modelselection.DefaultAlias

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
