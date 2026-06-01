package soul

import (
	"strings"
	"testing"
)

func TestValidateManagedHandle_Grammar(t *testing.T) {
	t.Parallel()

	valid := []string{
		"abc",
		"agent-alice",
		"agent0",
		"a0-b1-c2",
		strings.Repeat("a", 63),
	}
	for _, raw := range valid {
		raw := raw
		t.Run("valid_"+raw, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateManagedHandle(raw)
			if err != nil {
				t.Fatalf("ValidateManagedHandle(%q) unexpected err: %v", raw, err)
			}
			if got != raw {
				t.Fatalf("ValidateManagedHandle(%q) = %q, want unchanged input", raw, got)
			}
		})
	}

	invalid := []string{
		"",
		"a",
		"ab",
		strings.Repeat("a", 64),
		"Agent-Alice",
		"soul_researcher",
		"agent.alice",
		"agent..alice",
		" agent-alice",
		"agent-alice ",
		"agent alice",
		"@agent-alice",
		"agent@alice",
		"agent%2falice",
		"agent/alice",
		"-agent",
		"agent-",
	}
	for _, raw := range invalid {
		raw := raw
		t.Run("invalid_"+strings.ReplaceAll(raw, "/", "_"), func(t *testing.T) {
			t.Parallel()
			if got, err := ValidateManagedHandle(raw); err == nil {
				t.Fatalf("ValidateManagedHandle(%q) = %q, expected err", raw, got)
			}
		})
	}
}

func TestValidateManagedInstanceSlug_MirrorsInstanceSlugRule(t *testing.T) {
	t.Parallel()

	got, err := ValidateManagedInstanceSlug(" Simulacrum ")
	if err != nil {
		t.Fatalf("ValidateManagedInstanceSlug unexpected err: %v", err)
	}
	if got != "simulacrum" {
		t.Fatalf("ValidateManagedInstanceSlug got %q", got)
	}
	got, err = ValidateManagedInstanceSlug("x")
	if err != nil {
		t.Fatalf("ValidateManagedInstanceSlug one-character slug unexpected err: %v", err)
	}
	if got != "x" {
		t.Fatalf("ValidateManagedInstanceSlug one-character slug got %q", got)
	}

	for _, raw := range []string{"bad_slug", "bad.slug", "-bad", "bad-", "ab", strings.Repeat("a", 64)} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, err := ValidateManagedInstanceSlug(raw); err == nil {
				t.Fatalf("ValidateManagedInstanceSlug(%q) expected err", raw)
			}
		})
	}
}

func TestManagedENSName_CanonicalInstanceScopedForm(t *testing.T) {
	t.Parallel()

	got, err := ManagedENSName("agent-alice", "simulacrum")
	if err != nil {
		t.Fatalf("ManagedENSName unexpected err: %v", err)
	}
	if got != "agent-alice.simulacrum.lessersoul.eth" {
		t.Fatalf("ManagedENSName got %q", got)
	}

	for _, tc := range []struct {
		name string
		slug string
	}{
		{name: "Agent-Alice", slug: "simulacrum"},
		{name: "agent.alice", slug: "simulacrum"},
		{name: "agent-alice", slug: "bad_slug"},
	} {
		tc := tc
		t.Run(tc.name+"_"+tc.slug, func(t *testing.T) {
			t.Parallel()
			if _, err := ManagedENSName(tc.name, tc.slug); err == nil {
				t.Fatalf("ManagedENSName(%q, %q) expected err", tc.name, tc.slug)
			}
		})
	}
}
