package httpx

import (
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

const testQueryValueOne = "one"

func TestParseJSON(t *testing.T) {
	t.Parallel()

	var dest struct {
		Value string `json:"value"`
	}

	if err := ParseJSON(nil, &dest); err == nil {
		t.Fatalf("expected error for nil ctx")
	}

	ctx := &apptheory.Context{}
	if err := ParseJSON(ctx, &dest); err == nil {
		t.Fatalf("expected error for empty body")
	}

	ctx.Request.Body = []byte("{")
	if err := ParseJSON(ctx, &dest); err == nil {
		t.Fatalf("expected error for invalid json")
	}

	ctx.Request.Body = []byte(`{"value":"x"}`)
	if err := ParseJSON(ctx, &dest); err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if dest.Value != "x" {
		t.Fatalf("expected parsed value, got %#v", dest)
	}
}

func TestBearerToken(t *testing.T) {
	t.Parallel()

	if got := BearerToken(nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	headers := map[string][]string{
		"authorization": {"  Bearer   tok  "},
	}
	if got := BearerToken(headers); got != "tok" {
		t.Fatalf("expected tok, got %q", got)
	}

	headers = map[string][]string{
		"Authorization": {"Bearer tok"},
	}
	if got := BearerToken(headers); got != "tok" {
		t.Fatalf("expected tok with case-insensitive header name, got %q", got)
	}

	headers = map[string][]string{
		"authorization": {"Basic abc"},
	}
	if got := BearerToken(headers); got != "" {
		t.Fatalf("expected empty for non-bearer, got %q", got)
	}

	headers = map[string][]string{
		"authorization": {"Bearer"},
	}
	if got := BearerToken(headers); got != "" {
		t.Fatalf("expected empty for missing token, got %q", got)
	}
}

func TestFirstHeaderValue(t *testing.T) {
	t.Parallel()

	if got := FirstHeaderValue(nil, "x"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := FirstHeaderValue(map[string][]string{"x": {"v"}}, " "); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	headers := map[string][]string{
		"X": {"a", "b"},
	}
	if got := FirstHeaderValue(headers, "x"); got != "a" {
		t.Fatalf("expected a, got %q", got)
	}
	if got := FirstHeaderValue(headers, "X"); got != "a" {
		t.Fatalf("expected a, got %q", got)
	}
}

func TestFirstQueryValue(t *testing.T) {
	t.Parallel()

	if got := FirstQueryValue(nil, "a"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := FirstQueryValue(map[string][]string{"a": {"x"}}, " "); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	q := map[string][]string{
		"a":     {testQueryValueOne, "two"},
		"lower": {"ok"},
		" A ":   {"spaced"},
	}
	if got := FirstQueryValue(q, "a"); got != testQueryValueOne {
		t.Fatalf("unexpected value: %q", got)
	}
	if got := FirstQueryValue(q, "LOWER"); got != "ok" {
		t.Fatalf("unexpected lower-case fallback: %q", got)
	}
	if got := FirstQueryValue(q, "a "); got != testQueryValueOne {
		t.Fatalf("unexpected trimmed key: %q", got)
	}
	if got := FirstQueryValue(q, "A"); got != testQueryValueOne {
		t.Fatalf("unexpected case-insensitive scan: %q", got)
	}

	q2 := map[string][]string{" X ": {"v"}}
	if got := FirstQueryValue(q2, "x"); got != "v" {
		t.Fatalf("unexpected scan match: %q", got)
	}
}
