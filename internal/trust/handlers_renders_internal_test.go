package trust

import (
	"encoding/json"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestRequireCreateRenderAuth(t *testing.T) {
	t.Parallel()

	if _, err := requireCreateRenderAuth(nil, &apptheory.Context{}); err == nil {
		t.Fatalf("expected error for nil server")
	}

	if _, err := requireCreateRenderAuth(&Server{}, &apptheory.Context{}); err == nil {
		t.Fatalf("expected error for nil store")
	}

	db := ttmocks.NewMockExtendedDB()
	s := &Server{store: store.New(db)}
	if _, err := requireCreateRenderAuth(s, nil); err == nil {
		t.Fatalf("expected error for nil ctx")
	}
	if _, err := requireCreateRenderAuth(s, &apptheory.Context{}); err == nil {
		t.Fatalf("expected unauthorized for empty identity")
	}

	instanceSlug, err := requireCreateRenderAuth(s, &apptheory.Context{AuthIdentity: " inst "})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if instanceSlug != "inst" {
		t.Fatalf("expected trimmed identity, got %q", instanceSlug)
	}
}

func TestRenderDisabledResponse(t *testing.T) {
	t.Parallel()

	out := renderDisabledResponse()
	if out.Status != statusError || out.ErrorCode != statusDisabled {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.ExpiresAt.Before(out.CreatedAt) {
		t.Fatalf("expected expiresAt after createdAt: %#v", out)
	}
}

func TestParseCreateRenderRequestInput(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{}
	if _, err := parseCreateRenderRequestInput(ctx); err == nil {
		t.Fatalf("expected error for empty body")
	}

	body, _ := json.Marshal(createRenderRequest{URL: "https://example.com", RetentionClass: "evidence", RetentionDays: 7})
	ctx = &apptheory.Context{Request: apptheory.Request{Body: body}}
	got, err := parseCreateRenderRequestInput(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.URL != "https://example.com" || got.RetentionClass != "evidence" || got.RetentionDays != 7 {
		t.Fatalf("unexpected request: %#v", got)
	}
}

func TestNormalizeCreateRenderURL(t *testing.T) {
	t.Parallel()

	normalized, err := normalizeCreateRenderURL("https://bücher.example/path/../?b=2&a=1#frag")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if normalized != testNormalizedBucherURL {
		t.Fatalf("unexpected normalized: %q", normalized)
	}

	if _, err := normalizeCreateRenderURL("not a url"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestResolveCreateRenderRetention(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 6, 0, 0, 0, 0, time.UTC)
	days, classOut, exp := resolveCreateRenderRetention(now, "evidence", 0)
	if days != 180 || classOut != models.RenderRetentionClassEvidence {
		t.Fatalf("unexpected retention: days=%d class=%q", days, classOut)
	}
	if exp != rendering.ExpiresAtForRetention(now, days) {
		t.Fatalf("unexpected expiry: %v", exp)
	}

	days, classOut, _ = resolveCreateRenderRetention(now, "benign", 12)
	if days != 12 || classOut != models.RenderRetentionClassBenign {
		t.Fatalf("unexpected retention override: days=%d class=%q", days, classOut)
	}
}

func TestRenderArtifactResponseFromModel_StatusAndURLs(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{"host": {"example.com"}}}}

	queued := renderArtifactResponseFromModel(ctx, &models.RenderArtifact{
		ID:            "id",
		PolicyVersion: rendering.RenderPolicyVersion,
		NormalizedURL: "https://example.com",
	}, true, "")
	if queued.Status != "queued" || queued.ThumbnailURL == "" || queued.SnapshotURL == "" {
		t.Fatalf("unexpected queued response: %#v", queued)
	}

	okResp := renderArtifactResponseFromModel(ctx, &models.RenderArtifact{
		ID:                 "id",
		PolicyVersion:      rendering.RenderPolicyVersion,
		NormalizedURL:      "https://example.com",
		ThumbnailObjectKey: "thumb",
	}, true, "")
	if okResp.Status != "ok" {
		t.Fatalf("unexpected ok response: %#v", okResp)
	}

	errResp := renderArtifactResponseFromModel(ctx, &models.RenderArtifact{
		ID:            "id",
		PolicyVersion: rendering.RenderPolicyVersion,
		NormalizedURL: "https://example.com",
		ErrorCode:     "boom",
	}, true, "")
	if errResp.Status != statusError || errResp.ErrorCode != "boom" {
		t.Fatalf("unexpected error response: %#v", errResp)
	}
}
