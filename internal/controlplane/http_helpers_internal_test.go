package controlplane

import (
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

func TestParseJSON(t *testing.T) {
	t.Parallel()

	var dest struct {
		Value string `json:"value"`
	}

	if err := parseJSON(nil, &dest); err == nil {
		t.Fatalf("expected error for nil ctx")
	}

	ctx := &apptheory.Context{}
	if err := parseJSON(ctx, &dest); err == nil {
		t.Fatalf("expected error for empty body")
	}

	ctx.Request.Body = []byte("{")
	if err := parseJSON(ctx, &dest); err == nil {
		t.Fatalf("expected error for invalid json")
	}

	ctx.Request.Body = []byte(`{"value":"x"}`)
	if err := parseJSON(ctx, &dest); err != nil {
		t.Fatalf("parseJSON: %v", err)
	}
	if dest.Value != "x" {
		t.Fatalf("expected parsed value, got %#v", dest)
	}
}

func TestHeaderHelpers(t *testing.T) {
	t.Parallel()

	if got := firstHeaderValue(nil, "x"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	headers := map[string][]string{
		"authorization": {"  Bearer   tok  "},
	}
	if got := bearerToken(headers); got != "tok" {
		t.Fatalf("expected tok, got %q", got)
	}

	headers["authorization"] = []string{"Basic abc"}
	if got := bearerToken(headers); got != "" {
		t.Fatalf("expected empty for non-bearer, got %q", got)
	}
}

