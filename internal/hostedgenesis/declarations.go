package hostedgenesis

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/equaltoai/lesser-host/internal/soul"
)

const (
	hostedFirstDefaultPlaceholderCapability = "simulacrum.hosted-first-default"
	producedCapabilityDefaultClaimLevel     = "self-declared"
	declaredCapabilityFallbackScope         = "Capability declared during soul registration."
	declaredCapabilityClaimLevel            = "self-declared"
)

const (
	// MaxProducedCapabilities is the provider and Host validation ceiling for
	// fresh Hosted Genesis capability evidence.
	MaxProducedCapabilities = 25
	// MaxProducedCapabilityIdentifierLength bounds both human-readable provider
	// evidence and the canonical identifier derived from it.
	MaxProducedCapabilityIdentifierLength = 64
	// MaxProducedCapabilityScopeLength matches the published five-body schema.
	MaxProducedCapabilityScopeLength = 480
	// MaxProducedCapabilityMetadataLength bounds validationRef and degradesTo.
	MaxProducedCapabilityMetadataLength = 256
	// MaxProducedCapabilityLastValidatedLength bounds an RFC3339 timestamp.
	MaxProducedCapabilityLastValidatedLength = 64

	// ProducedCapabilityIdentifierPattern is the canonical produced identifier
	// grammar stored in registration evidence.
	ProducedCapabilityIdentifierPattern = `^[a-z0-9][a-z0-9._-]{0,63}$`
	// ProducedCapabilityEvidencePattern admits a short human-readable label or
	// an already-canonical identifier. Host deterministically canonicalizes it.
	ProducedCapabilityEvidencePattern = `^[A-Za-z0-9](?:[A-Za-z0-9._/ -]{0,62}[A-Za-z0-9])?$`
	// ProducedCapabilityNonWhitespacePattern prevents whitespace-only scope.
	ProducedCapabilityNonWhitespacePattern = `\S`
	producedCapabilityRFC3339Fragment      = `[0-9]{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](?:\.[0-9]+)?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])`
	// ProducedCapabilityRFC3339Pattern constrains persisted non-empty evidence.
	ProducedCapabilityRFC3339Pattern = `^` + producedCapabilityRFC3339Fragment + `$`
	// ProducedCapabilityOptionalRFC3339Pattern is used by provider schemas that
	// require every key and represent absent validation evidence as "".
	ProducedCapabilityOptionalRFC3339Pattern = `^(?:|` + producedCapabilityRFC3339Fragment + `)$`
)

// DeclarationValidationCode is a bounded, field-level error code. It is safe
// to expose in a status projection or structured log: it contains no model
// output, transcript text, credentials, or private declaration content.
type DeclarationValidationCode string

const (
	DeclarationCodeInvalid                 DeclarationValidationCode = "declarations.invalid"
	DeclarationCodeSelfDescription         DeclarationValidationCode = "self_description.invalid"
	DeclarationCodeCapabilities            DeclarationValidationCode = "capabilities.required"
	DeclarationCodeCapabilitiesBad         DeclarationValidationCode = "capabilities.invalid"
	DeclarationCodeCapabilitiesTooMany     DeclarationValidationCode = "capabilities.too_many"
	DeclarationCodeCapabilityIdentifier    DeclarationValidationCode = "capabilities.capability.invalid"
	DeclarationCodeCapabilityScope         DeclarationValidationCode = "capabilities.scope.invalid"
	DeclarationCodeCapabilityClaimLevel    DeclarationValidationCode = "capabilities.claim_level.invalid"
	DeclarationCodeCapabilityLastValidated DeclarationValidationCode = "capabilities.last_validated.invalid"
	DeclarationCodeCapabilityValidationRef DeclarationValidationCode = "capabilities.validation_ref.invalid"
	DeclarationCodeCapabilityDegradesTo    DeclarationValidationCode = "capabilities.degrades_to.invalid"
	DeclarationCodeBoundaries              DeclarationValidationCode = "boundaries.required"
	DeclarationCodeBoundariesBad           DeclarationValidationCode = "boundaries.invalid"
	DeclarationCodeTransparency            DeclarationValidationCode = "transparency.required"
	DeclarationCodeFiveBodyIdentity        DeclarationValidationCode = "five_body.identity.required"
	DeclarationCodeFiveBodyPhilosophy      DeclarationValidationCode = "five_body.philosophy.required"
	DeclarationCodeFiveBodyDiscipline      DeclarationValidationCode = "five_body.discipline.required"
	DeclarationCodeFiveBodyBoundaries      DeclarationValidationCode = "five_body.boundaries.required"
	DeclarationCodeFiveBodySoul            DeclarationValidationCode = "five_body.soul.required"
	DeclarationCodeSoulRefusals            DeclarationValidationCode = "soul.refusals.required"
	DeclarationCodeSoulRefusalsBad         DeclarationValidationCode = "soul.refusals.invalid"
	DeclarationCodeAdversarialReview       DeclarationValidationCode = "adversarial_review.required"
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
		DeclarationCodeCapabilitiesTooMany,
		DeclarationCodeCapabilityIdentifier,
		DeclarationCodeCapabilityScope,
		DeclarationCodeCapabilityClaimLevel,
		DeclarationCodeCapabilityLastValidated,
		DeclarationCodeCapabilityValidationRef,
		DeclarationCodeCapabilityDegradesTo,
		DeclarationCodeBoundaries,
		DeclarationCodeBoundariesBad,
		DeclarationCodeTransparency,
		DeclarationCodeFiveBodyIdentity,
		DeclarationCodeFiveBodyPhilosophy,
		DeclarationCodeFiveBodyDiscipline,
		DeclarationCodeFiveBodyBoundaries,
		DeclarationCodeFiveBodySoul,
		DeclarationCodeSoulRefusals,
		DeclarationCodeSoulRefusalsBad,
		DeclarationCodeAdversarialReview:
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
// Every other malformed row fails closed with a bounded field code.
func ValidateAndNormalizeProducedCapabilities(capabilities []soul.CapabilityV2) ([]soul.CapabilityV2, error) {
	return normalizeProducedCapabilities(capabilities, true)
}

func normalizeProducedCapabilities(capabilities []soul.CapabilityV2, rejectInvalid bool) ([]soul.CapabilityV2, error) {
	if rejectInvalid && len(capabilities) > MaxProducedCapabilities {
		return nil, newDeclarationValidationError(DeclarationCodeCapabilitiesTooMany)
	}
	out := make([]soul.CapabilityV2, 0, len(capabilities))
	seen := map[string]struct{}{}
	for _, capability := range capabilities {
		if IsPlaceholderCapability(capability.Capability) {
			continue
		}
		normalizedCapability, code := normalizeProducedCapability(capability, rejectInvalid)
		if code != "" {
			if rejectInvalid {
				return nil, newDeclarationValidationError(code)
			}
			continue
		}
		capability = normalizedCapability
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

func normalizeProducedCapability(capability soul.CapabilityV2, strict bool) (soul.CapabilityV2, DeclarationValidationCode) {
	if strict {
		capability.Capability = NormalizeProducedCapabilityIdentifier(capability.Capability)
	} else {
		capability.Capability = normalizeCapabilityName(capability.Capability)
	}
	capability.Scope = strings.TrimSpace(capability.Scope)
	capability.ClaimLevel = strings.ToLower(strings.TrimSpace(capability.ClaimLevel))
	capability.LastValidated = strings.TrimSpace(capability.LastValidated)
	capability.ValidationRef = strings.TrimSpace(capability.ValidationRef)
	capability.DegradesTo = strings.TrimSpace(capability.DegradesTo)
	if capability.ClaimLevel == "" && !strict {
		capability.ClaimLevel = producedCapabilityDefaultClaimLevel
	}
	return capability, validateProducedCapabilityFields(capability, strict)
}

func validateProducedCapabilityFields(capability soul.CapabilityV2, strict bool) DeclarationValidationCode {
	if capability.Capability == "" {
		return DeclarationCodeCapabilityIdentifier
	}
	if capability.Scope == "" {
		return DeclarationCodeCapabilityScope
	}
	if capability.ClaimLevel == "" {
		return DeclarationCodeCapabilityClaimLevel
	}
	if strict {
		if utf8.RuneCountInString(capability.Scope) > MaxProducedCapabilityScopeLength {
			return DeclarationCodeCapabilityScope
		}
		if capability.ClaimLevel != producedCapabilityDefaultClaimLevel {
			return DeclarationCodeCapabilityClaimLevel
		}
		if len(capability.LastValidated) > MaxProducedCapabilityLastValidatedLength {
			return DeclarationCodeCapabilityLastValidated
		}
		if utf8.RuneCountInString(capability.ValidationRef) > MaxProducedCapabilityMetadataLength {
			return DeclarationCodeCapabilityValidationRef
		}
		if utf8.RuneCountInString(capability.DegradesTo) > MaxProducedCapabilityMetadataLength {
			return DeclarationCodeCapabilityDegradesTo
		}
	}
	if capability.LastValidated != "" {
		if _, err := time.Parse(time.RFC3339, capability.LastValidated); err != nil {
			return DeclarationCodeCapabilityLastValidated
		}
	}
	return ""
}

// NormalizeProducedCapabilityIdentifier deterministically translates a short
// human-readable capability label into the Host identifier grammar. Existing
// canonical identifiers retain their separators; human separators collapse to
// underscores. Unsupported or overlong input returns "" so the strict caller
// can emit a bounded field-specific failure without retaining raw content.
func NormalizeProducedCapabilityIdentifier(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" || len(name) > MaxProducedCapabilityIdentifierLength {
		return ""
	}
	if isCanonicalProducedCapabilityIdentifier(name) {
		return name
	}

	var out strings.Builder
	out.Grow(len(name))
	pendingSeparator := false
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if isASCIILowerOrDigit(ch) {
			if pendingSeparator && out.Len() > 0 {
				out.WriteByte('_')
			}
			out.WriteByte(ch)
			pendingSeparator = false
			continue
		}
		switch ch {
		case ' ', '.', '_', '/', '-':
			pendingSeparator = true
		default:
			return ""
		}
	}
	if pendingSeparator || out.Len() == 0 || out.Len() > MaxProducedCapabilityIdentifierLength {
		return ""
	}
	return out.String()
}

func isCanonicalProducedCapabilityIdentifier(name string) bool {
	if name == "" || len(name) > MaxProducedCapabilityIdentifierLength || !isASCIILowerOrDigit(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if isASCIILowerOrDigit(name[i]) {
			continue
		}
		switch name[i] {
		case '.', '_', '-':
		default:
			return false
		}
	}
	return true
}

func isASCIILowerOrDigit(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9'
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
