package llm

import (
	"strings"
	"testing"

	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestClampInt(t *testing.T) {
	t.Parallel()

	if got := clampInt(0, 1, 5); got != 1 {
		t.Fatalf("expected clamp to min, got %d", got)
	}
	if got := clampInt(10, 1, 5); got != 5 {
		t.Fatalf("expected clamp to max, got %d", got)
	}
	if got := clampInt(3, 1, 5); got != 3 {
		t.Fatalf("expected passthrough, got %d", got)
	}
}

func TestClampStringBytes(t *testing.T) {
	t.Parallel()

	if got := clampStringBytes(" ", 3); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := clampStringBytes(" abc ", 0); got != "abc" {
		t.Fatalf("expected trim only, got %q", got)
	}
	if got := clampStringBytes(" abc ", 2); got != "ab" {
		t.Fatalf("expected byte clamp, got %q", got)
	}
}

func TestClaimVerifyWebSearchJSONSchemaV1_BoundsMaxSources(t *testing.T) {
	t.Parallel()

	low := claimVerifyWebSearchJSONSchemaV1(0)
	props := testutil.RequireType[map[string]any](t, low["properties"])
	sources := testutil.RequireType[map[string]any](t, props["sources"])
	if got := testutil.RequireType[int](t, sources["maxItems"]); got != 3 {
		t.Fatalf("expected default 3, got %#v", sources["maxItems"])
	}

	high := claimVerifyWebSearchJSONSchemaV1(999)
	props = testutil.RequireType[map[string]any](t, high["properties"])
	sources = testutil.RequireType[map[string]any](t, props["sources"])
	if got := testutil.RequireType[int](t, sources["maxItems"]); got != 5 {
		t.Fatalf("expected max 5, got %#v", sources["maxItems"])
	}
}

func TestClaimVerifyWebSearchSystemPromptV1_IsNonEmpty(t *testing.T) {
	t.Parallel()

	if got := strings.TrimSpace(claimVerifyWebSearchSystemPromptV1()); got == "" {
		t.Fatalf("expected non-empty prompt")
	}
}
