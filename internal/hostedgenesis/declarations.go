package hostedgenesis

import (
	"errors"
	"strings"

	"github.com/equaltoai/lesser-host/internal/soul"
)

const (
	hostedFirstDefaultPlaceholderCapability = "simulacrum.hosted-first-default"
	producedCapabilityDefaultClaimLevel     = "self-declared"
	declaredCapabilityFallbackScope         = "Capability declared during soul registration."
	declaredCapabilityClaimLevel            = "self-declared"
)

// DeclarationValidationCode is a bounded, field-level error code. It is safe
// to expose in a status projection or structured log: it contains no model
// output, transcript text, credentials, or private declaration content.
type DeclarationValidationCode string

const (
	DeclarationCodeInvalid         DeclarationValidationCode = "declarations.invalid"
	DeclarationCodeSelfDescription DeclarationValidationCode = "self_description.invalid"
	DeclarationCodeCapabilities    DeclarationValidationCode = "capabilities.required"
	DeclarationCodeCapabilitiesBad DeclarationValidationCode = "capabilities.invalid"
	DeclarationCodeBoundaries      DeclarationValidationCode = "boundaries.required"
	DeclarationCodeBoundariesBad   DeclarationValidationCode = "boundaries.invalid"
	DeclarationCodeTransparency    DeclarationValidationCode = "transparency.required"
)

// DeclarationValidationError is the only declaration-builder error that may
// cross the worker/status boundary. Its Error method intentionally returns the
// stable code rather than the underlying validator/provider error.
type DeclarationValidationError struct {
	Code DeclarationValidationCode
}

func (e DeclarationValidationError) Error() string { return string(e.Code) }

func newDeclarationValidationError(code DeclarationValidationCode) error {
	if !isDeclarationValidationCode(code) {
		code = DeclarationCodeInvalid
	}
	return DeclarationValidationError{Code: code}
}

// NewDeclarationValidationError constructs a sanitized declaration failure for
// callers in the control-plane, AI worker, and MicroVM workload packages.
func NewDeclarationValidationError(code DeclarationValidationCode) error {
	return newDeclarationValidationError(code)
}

// DeclarationValidationCodeFromError returns a safe field-level code for a
// declaration error. Unknown errors collapse to declarations.invalid.
func DeclarationValidationCodeFromError(err error) DeclarationValidationCode {
	if err == nil {
		return ""
	}
	var validationErr DeclarationValidationError
	if errors.As(err, &validationErr) && isDeclarationValidationCode(validationErr.Code) {
		return validationErr.Code
	}
	return DeclarationCodeInvalid
}

func isDeclarationValidationCode(code DeclarationValidationCode) bool {
	switch code {
	case DeclarationCodeInvalid,
		DeclarationCodeSelfDescription,
		DeclarationCodeCapabilities,
		DeclarationCodeCapabilitiesBad,
		DeclarationCodeBoundaries,
		DeclarationCodeBoundariesBad,
		DeclarationCodeTransparency:
		return true
	default:
		return false
	}
}

// IsDeclarationValidationCode reports whether reason is one of the stable
// field-level declaration codes accepted in a recovery envelope.
func IsDeclarationValidationCode(reason string) bool {
	return isDeclarationValidationCode(DeclarationValidationCode(strings.TrimSpace(reason)))
}

// ValidateAndNormalizeProducedCapabilities validates and deduplicates
// model-extracted capabilities without adding registration-declared fallbacks.
// An empty result is meaningful for instance-trust hosted/off-chain
// registration files. Deprecated placeholder rows are filtered before
// validation because they are retired metadata, not a produced capability.
func ValidateAndNormalizeProducedCapabilities(capabilities []soul.CapabilityV2) ([]soul.CapabilityV2, error) {
	return normalizeProducedCapabilities(capabilities, true)
}

func normalizeProducedCapabilities(capabilities []soul.CapabilityV2, rejectInvalid bool) ([]soul.CapabilityV2, error) {
	out := make([]soul.CapabilityV2, 0, len(capabilities))
	seen := map[string]struct{}{}
	for _, capability := range capabilities {
		capability.Capability = normalizeCapabilityName(capability.Capability)
		capability.Scope = strings.TrimSpace(capability.Scope)
		capability.ClaimLevel = strings.ToLower(strings.TrimSpace(capability.ClaimLevel))
		if capability.ClaimLevel == "" {
			capability.ClaimLevel = producedCapabilityDefaultClaimLevel
		}
		if IsPlaceholderCapability(capability.Capability) {
			continue
		}
		if err := capability.Validate(); err != nil {
			if !rejectInvalid {
				continue
			}
			return nil, newDeclarationValidationError(DeclarationCodeCapabilitiesBad)
		}
		if _, ok := seen[capability.Capability]; ok {
			continue
		}
		seen[capability.Capability] = struct{}{}
		out = append(out, capability)
	}
	return out, nil
}

// NormalizeProducedCapabilities is the compatibility helper for callers that
// intentionally discard invalid extracted rows while merging legacy wallet
// registration context. Hosted instance-trust builders use
// ValidateAndNormalizeProducedCapabilities so a malformed model field becomes
// a stable capabilities.invalid failure instead of being silently omitted.
func NormalizeProducedCapabilities(capabilities []soul.CapabilityV2) []soul.CapabilityV2 {
	out, _ := normalizeProducedCapabilities(capabilities, false)
	return out
}

// FilterDeclaredCapabilitiesForPrompt keeps registration-declared capability
// labels as model context only. Deprecated placeholders never enter prompts or
// extraction input, so the model cannot echo them into produced declarations.
func FilterDeclaredCapabilitiesForPrompt(declared []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(declared))
	for _, raw := range declared {
		name := normalizeCapabilityName(raw)
		if name == "" || IsPlaceholderCapability(name) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// IsPlaceholderCapability identifies the retired hosted-first capability. It
// is never a valid produced declaration, even when supplied by old metadata.
func IsPlaceholderCapability(name string) bool {
	return normalizeCapabilityName(name) == hostedFirstDefaultPlaceholderCapability
}

// MergeDeclaredCapabilities remains for wallet-authority legacy conversations:
// registration-declared claims can be used as explicit self-declared context
// there, but deprecated placeholders are always filtered. Hosted instance-trust
// flows must use NormalizeProducedCapabilities instead and never synthesize a
// registration fallback.
func MergeDeclaredCapabilities(capabilities []soul.CapabilityV2, declared []string) []soul.CapabilityV2 {
	out := NormalizeProducedCapabilities(capabilities)
	seen := map[string]struct{}{}
	for _, capability := range out {
		seen[capability.Capability] = struct{}{}
	}
	for _, name := range FilterDeclaredCapabilitiesForPrompt(declared) {
		if _, ok := seen[name]; ok {
			continue
		}
		capability := soul.CapabilityV2{
			Capability: name,
			Scope:      declaredCapabilityFallbackScope,
			ClaimLevel: declaredCapabilityClaimLevel,
		}
		if err := capability.Validate(); err != nil {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, capability)
	}
	return out
}

func normalizeCapabilityName(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" || len(name) > 64 || strings.ContainsAny(name, " \t\r\n") {
		return ""
	}
	return name
}
