package soulemail

import "strings"

const (
	// DefaultStage is the fail-safe stage for local scripts. It intentionally
	// maps to the lab bridge so operator dry-runs never forward lab provider
	// state to the live inbound bridge by omission.
	DefaultStage = "lab"

	LabInboundDomain  = "lab.lessersoul.ai"
	LiveInboundDomain = "inbound.lessersoul.ai"
)

// NormalizeStage canonicalizes the deployment stage used by operator scripts.
// Unknown or blank stages fail toward lab rather than live.
func NormalizeStage(stage string) string {
	normalized := strings.ToLower(strings.TrimSpace(stage))
	if normalized == "" {
		return DefaultStage
	}
	return normalized
}

// DefaultInboundDomainForStage returns the shared SES bridge recipient domain
// for a host stage. Only live receives the live bridge; every other stage uses
// the lab bridge to avoid accidental live forwarding from lab tooling.
func DefaultInboundDomainForStage(stage string) string {
	if NormalizeStage(stage) == "live" {
		return LiveInboundDomain
	}
	return LabInboundDomain
}

// InboundDomainFromEnvOrStage derives SOUL_EMAIL_INBOUND_DOMAIN the same way
// operator scripts should: explicit environment value first, stage default
// second. The returned value is lower-cased and trimmed.
func InboundDomainFromEnvOrStage(getenv func(string) string, stage string) string {
	if getenv != nil {
		if configured := strings.ToLower(strings.TrimSpace(getenv("SOUL_EMAIL_INBOUND_DOMAIN"))); configured != "" {
			return configured
		}
	}
	return DefaultInboundDomainForStage(stage)
}
