package hostedgenesis

import (
	"sort"
	"strings"

	"github.com/equaltoai/lesser-host/internal/soul"
)

const declaredCapabilityFallbackScope = "Capability declared during hosted genesis registration."
const declaredCapabilityClaimLevel = "self-declared"

// MergeDeclaredCapabilities preserves valid model-extracted capabilities and
// adds any registration-declared capability labels that extraction omitted. The
// fallback rows are self-declared because the registration begin contract only
// accepts self-declared capability claims.
func MergeDeclaredCapabilities(capabilities []soul.CapabilityV2, declared []string) []soul.CapabilityV2 {
	out := make([]soul.CapabilityV2, 0, len(capabilities)+len(declared))
	seen := map[string]struct{}{}
	for _, capability := range capabilities {
		capability.Capability = normalizeDeclaredCapabilityName(capability.Capability)
		capability.Scope = strings.TrimSpace(capability.Scope)
		capability.ClaimLevel = strings.ToLower(strings.TrimSpace(capability.ClaimLevel))
		if capability.ClaimLevel == "" {
			capability.ClaimLevel = declaredCapabilityClaimLevel
		}
		if err := capability.Validate(); err != nil {
			continue
		}
		if _, ok := seen[capability.Capability]; ok {
			continue
		}
		seen[capability.Capability] = struct{}{}
		out = append(out, capability)
	}

	for _, name := range normalizeDeclaredCapabilityNames(declared) {
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

func normalizeDeclaredCapabilityNames(declared []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(declared))
	for _, raw := range declared {
		name := normalizeDeclaredCapabilityName(raw)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func normalizeDeclaredCapabilityName(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" || len(name) > 64 || strings.ContainsAny(name, " \t\r\n") {
		return ""
	}
	return name
}
