package modelselection

import (
	"errors"
	"strings"
	"testing"
)

func TestAliasDefinitionsAreExactlyTheOperatorChoices(t *testing.T) {
	if got := ValidAliases(); len(got) != 2 || got[0] != AliasOpenAI || got[1] != AliasAnthropic {
		t.Fatalf("unexpected hosted genesis aliases: %#v", got)
	}

	tests := []struct {
		alias    string
		provider string
		model    string
	}{
		{alias: AliasOpenAI, provider: ProviderOpenAI, model: "gpt-5.6-luna"},
		{alias: AliasAnthropic, provider: ProviderAnthropic, model: "claude-sonnet-5"},
	}
	for _, test := range tests {
		definition, err := ResolveAlias(test.alias)
		if err != nil {
			t.Fatalf("resolve %q: %v", test.alias, err)
		}
		if definition.Provider != test.provider || definition.ConcreteModel != test.model || definition.ReasoningEffort != ReasoningEffortMedium {
			t.Fatalf("definition for %q = %#v", test.alias, definition)
		}
	}
}

func TestResolveAliasRejectsEmptyAndProviderPrefixedInput(t *testing.T) {
	for _, input := range []string{"", " ", "gpt-5-mini", "openai:gpt-5-mini", "claude-sonnet-5-medium"} {
		_, err := ResolveAlias(input)
		var aliasErr *AliasError
		if !errors.As(err, &aliasErr) {
			t.Fatalf("ResolveAlias(%q) error = %T %v, want AliasError", input, err, err)
		}
		for _, alias := range ValidAliases() {
			if !strings.Contains(err.Error(), alias) {
				t.Fatalf("ResolveAlias(%q) error %q does not name valid alias %q", input, err, alias)
			}
		}
	}
}

func TestResolveModelSetRetainsAdvancedEscapeHatch(t *testing.T) {
	definition, err := ResolveModelSet("OpenAI:legacy-model")
	if err != nil {
		t.Fatalf("advanced model set rejected: %v", err)
	}
	if definition.Alias != "" || definition.Provider != ProviderOpenAI || definition.ConcreteModel != "legacy-model" || definition.ReasoningEffort != "" {
		t.Fatalf("unexpected advanced definition: %#v", definition)
	}

	if got := CanonicalModelSet(AliasAnthropic); got != "anthropic:claude-sonnet-5" {
		t.Fatalf("canonical alias model set = %q", got)
	}
	if got := CanonicalModelSet("openai:legacy-model"); got != "openai:legacy-model" {
		t.Fatalf("advanced model set changed = %q", got)
	}
}

func TestDefaultAliasResolvesToOpenAI(t *testing.T) {
	definition, err := ResolveAlias(DefaultAlias)
	if err != nil {
		t.Fatalf("default alias rejected: %v", err)
	}
	if definition.Provider != ProviderOpenAI {
		t.Fatalf("default alias provider = %q", definition.Provider)
	}
}
