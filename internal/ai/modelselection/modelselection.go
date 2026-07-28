// Package modelselection owns the operator-facing model aliases used by AI
// surfaces. Provider-specific callers resolve aliases here instead of
// embedding provider prefixes, concrete identifiers, or reasoning settings
// in their request paths.
package modelselection

import (
	"fmt"
	"strings"
)

const (
	// DefaultAlias is the zero-choice Hosted Genesis model. It is deliberately
	// an alias, not a provider-prefixed model set.
	DefaultAlias = "gpt-5.6-luna"

	AliasOpenAI    = "gpt-5.6-luna"
	AliasAnthropic = "claude-sonnet-5"

	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
	ProviderUnknown   = "unknown"

	ReasoningEffortMedium = "medium"
)

// Definition is the resolved provider configuration for one model choice.
// ConcreteModel is kept in this single table so adding a future provider does
// not require scattering provider/model/effort literals across request paths.
type Definition struct {
	Alias           string
	Provider        string
	ConcreteModel   string
	ReasoningEffort string
}

// AliasError is returned when a Hosted Genesis caller supplies an empty or
// unsupported alias. It is intentionally typed so the control plane can map
// it to a deterministic app.bad_request before any guest turn is queued.
type AliasError struct {
	Input string
}

func (e *AliasError) Error() string {
	if e == nil || strings.TrimSpace(e.Input) == "" {
		return fmt.Sprintf("model alias is required; valid aliases: %s", strings.Join(ValidAliases(), ", "))
	}
	return fmt.Sprintf("unsupported model alias %q; valid aliases: %s", strings.TrimSpace(e.Input), strings.Join(ValidAliases(), ", "))
}

// aliasDefinitions is the only alias -> provider/model/effort mapping. Keep
// rows data-driven: a future provider adds a row and its provider adapter,
// rather than changing every caller's model-selection logic.
var aliasDefinitions = [...]Definition{
	{
		Alias:           AliasOpenAI,
		Provider:        ProviderOpenAI,
		ConcreteModel:   "gpt-5.6-luna",
		ReasoningEffort: ReasoningEffortMedium,
	},
	{
		Alias:           AliasAnthropic,
		Provider:        ProviderAnthropic,
		ConcreteModel:   "claude-sonnet-5",
		ReasoningEffort: ReasoningEffortMedium,
	},
}

// ValidAliases returns the exact operator-facing aliases in configuration
// order. The returned slice is independent of the registry storage.
func ValidAliases() []string {
	aliases := make([]string, 0, len(aliasDefinitions))
	for _, definition := range aliasDefinitions {
		aliases = append(aliases, definition.Alias)
	}
	return aliases
}

// ResolveAlias resolves only the two supported Hosted Genesis aliases. Empty
// input is not treated as an alias; callers that want the zero-choice default
// must select DefaultAlias explicitly.
func ResolveAlias(raw string) (Definition, error) {
	for _, definition := range aliasDefinitions {
		if strings.TrimSpace(raw) == definition.Alias {
			return definition, nil
		}
	}
	return Definition{}, &AliasError{Input: raw}
}

// ResolveModelSet accepts an operator alias or the retained internal advanced
// provider:model escape hatch. The latter is deliberately not accepted by the
// Hosted Genesis external request validator, but remains available to legacy
// stored conversations and internal AI configuration.
func ResolveModelSet(raw string) (Definition, error) {
	raw = strings.TrimSpace(raw)
	if definition, err := ResolveAlias(raw); err == nil {
		return definition, nil
	}

	provider, concreteModel, ok := strings.Cut(raw, ":")
	provider = strings.ToLower(strings.TrimSpace(provider))
	concreteModel = strings.TrimSpace(concreteModel)
	if !ok || concreteModel == "" || (provider != ProviderOpenAI && provider != ProviderAnthropic) {
		return Definition{}, fmt.Errorf("unsupported model set %q", raw)
	}
	return Definition{Provider: provider, ConcreteModel: concreteModel}, nil
}

// ResolveModelSetForProvider resolves an alias or advanced model set and
// ensures it targets the provider adapter that is about to be called.
func ResolveModelSetForProvider(raw string, provider string) (Definition, error) {
	definition, err := ResolveModelSet(raw)
	if err != nil {
		return Definition{}, err
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if definition.Provider != provider {
		return Definition{}, fmt.Errorf("model set %q does not target provider %q", strings.TrimSpace(raw), provider)
	}
	return definition, nil
}

// CanonicalModelSet converts a supported alias to the provider-prefixed form
// expected by the existing generic AI worker. Advanced model sets and other
// deterministic/AWS model identifiers pass through unchanged.
func CanonicalModelSet(raw string) string {
	definition, err := ResolveAlias(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return definition.Provider + ":" + definition.ConcreteModel
}

// ProviderModelSet returns the legacy provider:model spelling for a resolved
// definition. It is used only at provider adapter boundaries and telemetry.
func (d Definition) ProviderModelSet() string {
	if strings.TrimSpace(d.Provider) == "" || strings.TrimSpace(d.ConcreteModel) == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(d.Provider)) + ":" + strings.TrimSpace(d.ConcreteModel)
}
