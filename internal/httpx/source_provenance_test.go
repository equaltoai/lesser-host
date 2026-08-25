package httpx

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	"github.com/theory-cloud/apptheory/v4/testkit"
)

const (
	testProviderLambdaURL      = "lambda-url"
	testProviderRequestContext = "provider_request_context"
)

func TestTrustedSourceUsesAppTheoryProviderContext(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{
		Headers: map[string][]string{
			"x-forwarded-for": {"203.0.113.250"},
			"forwarded":       {"for=203.0.113.251"},
		},
		SourceProvenance: apptheory.SourceProvenance{
			SourceIP: "2001:db8::1",
			Provider: testProviderLambdaURL,
			Source:   testProviderRequestContext,
			Valid:    true,
		},
	}}

	source := TrustedSource(ctx)
	if !source.Valid || source.SourceIP != "2001:db8::1" || source.Provider != testProviderLambdaURL || source.Source != testProviderRequestContext {
		t.Fatalf("unexpected source: %#v", source)
	}
	if source.SourceIP == "203.0.113.250" || source.SourceIP == "203.0.113.251" {
		t.Fatalf("forwarded header was trusted: %#v", source)
	}
}

func TestTrustedSourceUnknownForHeaderOnlyContext(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{
		"x-forwarded-for": {"198.51.100.42"},
	}}}
	source := TrustedSource(ctx)
	if source.Valid || source.SourceIP != "" || source.Provider != trustedSourceUnknown || source.Source != trustedSourceUnknown {
		t.Fatalf("expected unknown source, got %#v", source)
	}
	if got := SourceRateLimitIdentifier(ctx, "auth"); got != "auth:source:unknown" {
		t.Fatalf("unexpected unknown identifier: %q", got)
	}
}

func TestSetTrustedSourceStoresProviderContext(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{SourceProvenance: apptheory.SourceProvenance{
		SourceIP: "198.51.100.77",
		Provider: testProviderLambdaURL,
		Source:   testProviderRequestContext,
		Valid:    true,
	}}}
	stored := SetTrustedSource(ctx)
	ctx.Request.SourceProvenance = apptheory.SourceProvenance{}

	fromCtx := TrustedSourceFromContext(ctx)
	if fromCtx != stored || fromCtx.SourceIP != "198.51.100.77" || !fromCtx.Valid {
		t.Fatalf("expected stored trusted source, got %#v stored=%#v", fromCtx, stored)
	}
}

func TestSourceRateLimitIdentifierFingerprintsProviderSourceIP(t *testing.T) {
	t.Parallel()

	ctxA := &apptheory.Context{Request: apptheory.Request{SourceProvenance: apptheory.SourceProvenance{
		SourceIP: "198.51.100.10",
		Provider: testProviderLambdaURL,
		Source:   testProviderRequestContext,
		Valid:    true,
	}}}
	ctxB := &apptheory.Context{Request: apptheory.Request{SourceProvenance: apptheory.SourceProvenance{
		SourceIP: "198.51.100.11",
		Provider: testProviderLambdaURL,
		Source:   testProviderRequestContext,
		Valid:    true,
	}}}

	idA := SourceRateLimitIdentifier(ctxA, "auth")
	idB := SourceRateLimitIdentifier(ctxB, "auth")
	if idA == idB {
		t.Fatalf("expected distinct identifiers, got %q", idA)
	}
	if !strings.HasPrefix(idA, "auth:source:lambda-url:") || strings.Contains(idA, "198.51.100.10") {
		t.Fatalf("unexpected identifier %q", idA)
	}
	meta := SourceRateLimitMetadata(ctxA)
	if meta["source_provider"] != testProviderLambdaURL || meta["source"] != testProviderRequestContext || meta["source_valid"] != "true" {
		t.Fatalf("unexpected metadata: %#v", meta)
	}
	if meta["source_ip_sha256"] == "" || strings.Contains(meta["source_ip_sha256"], "198.51.100.10") {
		t.Fatalf("expected source fingerprint metadata, got %#v", meta)
	}
}

func TestTrustedSourceFromAppTheoryTestkitHTTPEvent(t *testing.T) {
	t.Parallel()

	env := testkit.New()
	app := env.App()
	app.Get("/source", func(ctx *apptheory.Context) (*apptheory.Response, error) {
		return apptheory.MustJSON(200, TrustedSource(ctx)), nil
	})

	event := testkit.LambdaFunctionURLRequest("GET", "/source", testkit.HTTPEventOptions{
		SourceIP: "198.51.100.88",
		Headers: map[string]string{
			"X-Forwarded-For": "203.0.113.10",
		},
	})
	resp := env.InvokeLambdaFunctionURL(context.Background(), app, event)
	if resp.StatusCode != 200 {
		t.Fatalf("unexpected status: %d body=%s", resp.StatusCode, resp.Body)
	}
	var source TrustedSourceInfo
	if err := json.Unmarshal([]byte(resp.Body), &source); err != nil {
		t.Fatalf("unmarshal source: %v", err)
	}
	if !source.Valid || source.SourceIP != "198.51.100.88" || source.Provider != testProviderLambdaURL {
		t.Fatalf("unexpected source: %#v", source)
	}
}
