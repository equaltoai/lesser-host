package trust

import (
	"testing"

	"github.com/equaltoai/lesser-host/internal/httpx"
)

const testQueryValueOne = "one"

func TestFirstQueryValue(t *testing.T) {
	t.Parallel()

	if got := httpx.FirstQueryValue(nil, "a"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := httpx.FirstQueryValue(map[string][]string{"a": {"x"}}, " "); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	q := map[string][]string{
		"a":     {testQueryValueOne, "two"},
		"lower": {"ok"},
		" A ":   {"spaced"},
	}
	if got := httpx.FirstQueryValue(q, "a"); got != testQueryValueOne {
		t.Fatalf("unexpected value: %q", got)
	}
	if got := httpx.FirstQueryValue(q, "LOWER"); got != "ok" {
		t.Fatalf("unexpected lower-case fallback: %q", got)
	}
	if got := httpx.FirstQueryValue(q, "a "); got != testQueryValueOne {
		t.Fatalf("unexpected trimmed key: %q", got)
	}
	if got := httpx.FirstQueryValue(q, "A"); got != testQueryValueOne {
		t.Fatalf("unexpected case-insensitive scan: %q", got)
	}

	q2 := map[string][]string{" X ": {"v"}}
	if got := httpx.FirstQueryValue(q2, "x"); got != "v" {
		t.Fatalf("unexpected scan match: %q", got)
	}
}
