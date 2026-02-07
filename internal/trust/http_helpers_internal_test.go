package trust

import "testing"

func TestFirstQueryValue(t *testing.T) {
	t.Parallel()

	if got := firstQueryValue(nil, "a"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := firstQueryValue(map[string][]string{"a": {"x"}}, " "); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	q := map[string][]string{
		"a":     {"one", "two"},
		"lower": {"ok"},
		" A ":   {"spaced"},
	}
	if got := firstQueryValue(q, "a"); got != "one" {
		t.Fatalf("unexpected value: %q", got)
	}
	if got := firstQueryValue(q, "LOWER"); got != "ok" {
		t.Fatalf("unexpected lower-case fallback: %q", got)
	}
	if got := firstQueryValue(q, "a "); got != "one" {
		t.Fatalf("unexpected trimmed key: %q", got)
	}
	if got := firstQueryValue(q, "A"); got != "one" {
		t.Fatalf("unexpected case-insensitive scan: %q", got)
	}

	q2 := map[string][]string{" X ": {"v"}}
	if got := firstQueryValue(q2, "x"); got != "v" {
		t.Fatalf("unexpected scan match: %q", got)
	}
}
