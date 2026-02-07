package trust

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const testHello = "hello"

func TestClampModerationText(t *testing.T) {
	t.Parallel()

	if _, err := clampModerationText(" "); err == nil {
		t.Fatalf("expected error")
	}

	got, err := clampModerationText(" " + testHello + " ")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != testHello {
		t.Fatalf("expected trimmed, got %q", got)
	}

	long := strings.Repeat("x", 10001)
	got, err = clampModerationText(long)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len([]byte(got)) != 10000 {
		t.Fatalf("expected truncated to 10k bytes, got %d", len([]byte(got)))
	}
}

func TestModerationModelSet(t *testing.T) {
	t.Parallel()

	if got := moderationModelSet(instanceTrustConfig{AIEnabled: false, AIModelSet: testModelSetOpenAIGPT}); got != modelSetDeterministic {
		t.Fatalf("expected deterministic when AI disabled, got %q", got)
	}

	if got := moderationModelSet(instanceTrustConfig{AIEnabled: true, AIModelSet: " " + testModelSetOpenAIGPT + " "}); got != testModelSetOpenAIGPT {
		t.Fatalf("expected trimmed model set, got %q", got)
	}
}

func TestBuildAIModerationResponse(t *testing.T) {
	t.Parallel()

	out := buildAIModerationResponse(ai.Response{Status: ai.JobStatusQueued, JobID: " j "}, " module ", " pv ", " ms ", " ih ")
	if out.Status != string(ai.JobStatusQueued) || out.JobID != "j" {
		t.Fatalf("unexpected output: %#v", out)
	}
	if out.Contract.Module != "module" || out.Contract.PolicyVersion != "pv" || out.Contract.ModelSet != "ms" || out.Contract.InputsHash != "ih" {
		t.Fatalf("unexpected contract: %#v", out.Contract)
	}

	now := time.Now().UTC()
	out = buildAIModerationResponse(ai.Response{
		Status: ai.JobStatusOK,
		Result: &models.AIResult{
			ResultJSON: `{"ok":true}`,
			CreatedAt:  now,
			ExpiresAt:  now.Add(time.Hour),
		},
	}, "m", "pv", "ms", "ih")
	if out.Result == nil {
		t.Fatalf("expected parsed result")
	}
	if out.Contract.CreatedAt.IsZero() || out.Contract.ExpiresAt.IsZero() {
		t.Fatalf("expected timestamps set: %#v", out.Contract)
	}
}

func TestModerationDisabledResponse(t *testing.T) {
	t.Parallel()

	out := moderationDisabledResponse(" m ", " pv ", " ms ", " ih ", 7, " msg ")
	if out.Status != statusDisabled || out.ErrorCode != statusDisabled || out.ErrorMessage != "msg" {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.Budget.RequestedCredits != 7 || out.Budget.DebitedCredits != 0 || !out.Budget.Allowed {
		t.Fatalf("unexpected budget: %#v", out.Budget)
	}
	if out.Contract.Module != "m" || out.Contract.PolicyVersion != "pv" || out.Contract.ModelSet != "ms" || out.Contract.InputsHash != "ih" {
		t.Fatalf("unexpected contract: %#v", out.Contract)
	}
}

func TestModerationRequestAllowed(t *testing.T) {
	t.Parallel()

	if ok, msg := moderationRequestAllowed(moderationTriggerOnReports, nil, 0, "text"); ok || !strings.Contains(msg, "/ai/moderation/text/report") {
		t.Fatalf("unexpected result: ok=%v msg=%q", ok, msg)
	}

	if ok, _ := moderationRequestAllowed(moderationTriggerLinksMediaOnly, nil, 0, "image"); !ok {
		t.Fatalf("expected image to be allowed for links_media_only")
	}
	if ok, _ := moderationRequestAllowed(moderationTriggerLinksMediaOnly, &aiModerationScanCtxV1{}, 0, "text"); ok {
		t.Fatalf("expected text with no links/media to be blocked")
	}
	if ok, _ := moderationRequestAllowed(moderationTriggerLinksMediaOnly, &aiModerationScanCtxV1{HasLinks: true}, 0, "text"); !ok {
		t.Fatalf("expected text with links to be allowed")
	}

	if ok, _ := moderationRequestAllowed(moderationTriggerVirality, &aiModerationScanCtxV1{ViralityScore: 9}, 10, "text"); ok {
		t.Fatalf("expected below threshold to be blocked")
	}
	if ok, _ := moderationRequestAllowed(moderationTriggerVirality, &aiModerationScanCtxV1{ViralityScore: 10}, 10, "text"); !ok {
		t.Fatalf("expected >= threshold to be allowed")
	}
}

func TestNormalizeModerationImageURL(t *testing.T) {
	t.Parallel()

	if _, err := normalizeModerationImageURL(" "); err == nil {
		t.Fatalf("expected error")
	}
	if _, err := normalizeModerationImageURL("not a url"); err == nil {
		t.Fatalf("expected error")
	}
	u, err := normalizeModerationImageURL("https://bücher.example/path/../?b=2&a=1#frag")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if u == nil || u.Scheme != "https" {
		t.Fatalf("unexpected url: %#v", u)
	}
}

func TestPrepareModerationImageInput_ErrorBranches(t *testing.T) {
	t.Parallel()

	s := &Server{artifacts: nil}
	if _, _, _, _, err := s.prepareModerationImageInput(context.Background(), "inst", aiModerationImageRequest{}); err == nil {
		t.Fatalf("expected error for missing artifacts store")
	}

	s = &Server{artifacts: artifacts.New("")}
	key, _, _, _, err := s.prepareModerationImageInput(context.Background(), "inst", aiModerationImageRequest{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if key != "" {
		t.Fatalf("expected empty key, got %q", key)
	}

	if _, _, _, _, err := s.prepareModerationImageInput(context.Background(), "inst", aiModerationImageRequest{ObjectKey: "bad"}); err == nil {
		t.Fatalf("expected error for invalid prefix")
	}

	if _, _, _, _, err := s.prepareModerationImageInput(context.Background(), "inst", aiModerationImageRequest{ObjectKey: "moderation/inst/abc"}); err == nil {
		t.Fatalf("expected error for missing bucket/object")
	}
}

func TestHandleAIModerationText_DisabledEarlyReturn(t *testing.T) {
	t.Parallel()

	s := &Server{
		ai:    &ai.Service{},
		store: store.New(nil), // trust config store not ready -> default config (moderation disabled)
	}

	body, _ := json.Marshal(aiModerationTextRequest{Text: testHello})
	ctx := &apptheory.Context{
		AuthIdentity: "inst",
		Request:      apptheory.Request{Body: body},
	}
	resp, err := s.handleAIModerationText(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil || resp.Status != 200 {
		t.Fatalf("expected 200 response, got %#v", resp)
	}
	if !strings.Contains(string(resp.Body), "\"status\":\""+statusDisabled+"\"") {
		t.Fatalf("expected disabled body, got %q", string(resp.Body))
	}
}
