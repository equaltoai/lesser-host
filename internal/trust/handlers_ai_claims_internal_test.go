package trust

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestClaimVerifyRetrievalMode(t *testing.T) {
	t.Parallel()

	if got := claimVerifyRetrievalMode(nil); got != ai.ClaimVerifyRetrievalModeProvidedOnly {
		t.Fatalf("expected default mode %q, got %q", ai.ClaimVerifyRetrievalModeProvidedOnly, got)
	}

	if got := claimVerifyRetrievalMode(&ai.ClaimVerifyRetrievalV1{Mode: "  openai_web_search  "}); got != ai.ClaimVerifyRetrievalModeOpenAIWebSearch {
		t.Fatalf("expected trimmed mode %q, got %q", ai.ClaimVerifyRetrievalModeOpenAIWebSearch, got)
	}

	if got := claimVerifyRetrievalMode(&ai.ClaimVerifyRetrievalV1{Mode: "   "}); got != ai.ClaimVerifyRetrievalModeProvidedOnly {
		t.Fatalf("expected empty -> %q, got %q", ai.ClaimVerifyRetrievalModeProvidedOnly, got)
	}
}

func TestValidateClaimVerifyRequest(t *testing.T) {
	t.Parallel()

	if err := validateClaimVerifyRequest("", nil, nil, ai.ClaimVerifyRetrievalModeProvidedOnly); err == nil {
		t.Fatalf("expected error for empty text+claims")
	}

	if err := validateClaimVerifyRequest("x", nil, nil, ai.ClaimVerifyRetrievalModeProvidedOnly); err == nil {
		t.Fatalf("expected error for missing evidence in provided_only")
	}

	if err := validateClaimVerifyRequest("x", nil, nil, ai.ClaimVerifyRetrievalModeOpenAIWebSearch); err != nil {
		t.Fatalf("expected ok for missing evidence in openai_web_search, got %v", err)
	}

	if err := validateClaimVerifyRequest("", []string{"a"}, []claimVerifyEvidenceRequest{{SourceID: "s", Text: "t"}}, ai.ClaimVerifyRetrievalModeProvidedOnly); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}

func TestEstimateClaimVerifyCredits(t *testing.T) {
	t.Parallel()

	{
		got := estimateClaimVerifyCredits("some text", nil, nil, ai.ClaimVerifyRetrievalModeProvidedOnly, 0)
		if got <= 0 {
			t.Fatalf("expected positive credits, got %d", got)
		}
	}

	{
		ret := &ai.ClaimVerifyRetrievalV1{Mode: ai.ClaimVerifyRetrievalModeOpenAIWebSearch, MaxSources: 10}
		got := estimateClaimVerifyCredits("some text", nil, ret, ai.ClaimVerifyRetrievalModeOpenAIWebSearch, 0)
		// Should include openai_web_search overhead.
		base := estimateClaimVerifyCredits("some text", nil, nil, ai.ClaimVerifyRetrievalModeProvidedOnly, 0)
		if got <= base {
			t.Fatalf("expected retrieval credits > base (got %d, base %d)", got, base)
		}
	}
}

func TestClaimVerifyModelSet(t *testing.T) {
	t.Parallel()

	{
		modelSet, err := claimVerifyModelSet(instanceTrustConfig{AIEnabled: false, AIModelSet: ""}, ai.ClaimVerifyRetrievalModeProvidedOnly)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if modelSet != "deterministic" {
			t.Fatalf("expected deterministic, got %q", modelSet)
		}
	}

	{
		_, err := claimVerifyModelSet(instanceTrustConfig{AIEnabled: false, AIModelSet: ""}, ai.ClaimVerifyRetrievalModeOpenAIWebSearch)
		if err == nil {
			t.Fatalf("expected error for openai_web_search without openai model")
		}
	}

	{
		modelSet, err := claimVerifyModelSet(instanceTrustConfig{AIEnabled: true, AIModelSet: "OpenAI:gpt"}, ai.ClaimVerifyRetrievalModeOpenAIWebSearch)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if strings.ToLower(modelSet) != testModelSetOpenAIGPT {
			t.Fatalf("expected openai model set preserved, got %q", modelSet)
		}
	}
}

func TestSanitizeClaimVerifyClaims(t *testing.T) {
	t.Parallel()

	in := []string{
		"  ",
		" a ",
		strings.Repeat("x", 300),
	}
	out := sanitizeClaimVerifyClaims(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 claims, got %#v", out)
	}
	if out[0] != "a" {
		t.Fatalf("expected trimmed claim, got %#v", out)
	}
	if len(out[1]) != 240 {
		t.Fatalf("expected claim truncated to 240 chars, got %d", len(out[1]))
	}
}

func TestBuildClaimVerifyEvidence(t *testing.T) {
	t.Parallel()

	t.Run("TooManyItems", func(t *testing.T) {
		t.Parallel()

		req := make([]claimVerifyEvidenceRequest, claimVerifyMaxEvidenceItems+1)
		_, _, err := buildClaimVerifyEvidence(req)
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("MissingSourceID", func(t *testing.T) {
		t.Parallel()

		_, _, err := buildClaimVerifyEvidence([]claimVerifyEvidenceRequest{{SourceID: " ", Text: "x"}})
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("DuplicateSourceID", func(t *testing.T) {
		t.Parallel()

		_, _, err := buildClaimVerifyEvidence([]claimVerifyEvidenceRequest{
			{SourceID: "a", Text: "x"},
			{SourceID: "a", Text: "y"},
		})
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("InvalidRenderID", func(t *testing.T) {
		t.Parallel()

		_, _, err := buildClaimVerifyEvidence([]claimVerifyEvidenceRequest{{SourceID: "a", RenderID: "nope"}})
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("MissingTextAndRenderID", func(t *testing.T) {
		t.Parallel()

		_, _, err := buildClaimVerifyEvidence([]claimVerifyEvidenceRequest{{SourceID: "a"}})
		if err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("Success_Text", func(t *testing.T) {
		t.Parallel()

		evidence, total, err := buildClaimVerifyEvidence([]claimVerifyEvidenceRequest{{SourceID: "a", URL: " u ", Title: " t ", Text: " hello "}})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(evidence) != 1 {
			t.Fatalf("expected 1 item, got %#v", evidence)
		}
		if evidence[0].SourceID != "a" || evidence[0].Text != "hello" {
			t.Fatalf("unexpected evidence: %#v", evidence[0])
		}
		if total <= 0 {
			t.Fatalf("expected positive total bytes, got %d", total)
		}
	})
}

func TestNormalizeClaimVerifyRetrieval(t *testing.T) {
	t.Parallel()

	if got := normalizeClaimVerifyRetrieval(nil); got != nil {
		t.Fatalf("expected nil, got %#v", got)
	}

	got := normalizeClaimVerifyRetrieval(&claimVerifyRetrievalRequest{
		Mode:              "OPENAI_WEB_SEARCH",
		MaxSources:        999,
		SearchContextSize: "bad",
	})
	if got == nil {
		t.Fatalf("expected value")
	}
	if got.Mode != ai.ClaimVerifyRetrievalModeOpenAIWebSearch {
		t.Fatalf("unexpected mode: %#v", got)
	}
	if got.MaxSources != claimVerifyMaxEvidenceItems {
		t.Fatalf("expected maxSources clamped to %d, got %d", claimVerifyMaxEvidenceItems, got.MaxSources)
	}
	if got.SearchContextSize != "" {
		t.Fatalf("expected invalid search_context_size to clear, got %q", got.SearchContextSize)
	}
}

func TestClampEvidenceText(t *testing.T) {
	t.Parallel()

	if _, _, err := clampEvidenceText(" ", 10); err == nil {
		t.Fatalf("expected error")
	}

	trimmed, b, err := clampEvidenceText(" hello ", 10)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if trimmed != "hello" || b != int64(len([]byte("hello"))) {
		t.Fatalf("unexpected output: %q (%d)", trimmed, b)
	}

	long := strings.Repeat("x", 50)
	trimmed, b, err = clampEvidenceText(long, 10)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len([]byte(trimmed)) > 10 {
		t.Fatalf("expected <= 10 bytes, got %d", len([]byte(trimmed)))
	}
	if b != int64(len([]byte(trimmed))) {
		t.Fatalf("expected bytes to match, got %d want %d", b, len([]byte(trimmed)))
	}
}

func TestEstimateClaimVerifyBaseCredits(t *testing.T) {
	t.Parallel()

	if got := estimateClaimVerifyBaseCredits(1, 0); got != claimVerifyBaseCreditsMin+(1*claimVerifyBaseCreditsPerClaim) {
		t.Fatalf("unexpected credits: %d", got)
	}

	// +1 credit per 16KiB evidence.
	if got := estimateClaimVerifyBaseCredits(1, 16*1024); got != claimVerifyBaseCreditsMin+(1*claimVerifyBaseCreditsPerClaim)+1 {
		t.Fatalf("unexpected credits: %d", got)
	}
}

func TestEnqueueAIJobIfQueued(t *testing.T) {
	t.Parallel()

	s := &Server{}
	ctx := &apptheory.Context{}

	if err := s.enqueueAIJobIfQueued(ctx, ai.Response{Status: ai.JobStatusOK}); err != nil {
		t.Fatalf("expected no-op, got %v", err)
	}

	if err := s.enqueueAIJobIfQueued(ctx, ai.Response{Status: ai.JobStatusQueued, JobID: "j"}); err == nil {
		t.Fatalf("expected error for missing queue client")
	}
}

func TestBuildAIClaimVerifyResponse(t *testing.T) {
	t.Parallel()

	{
		out := buildAIClaimVerifyResponse(ai.Response{Status: ai.JobStatusQueued, JobID: " j "}, "model", "hash", " att ", " url ")
		if out.Status != string(ai.JobStatusQueued) || out.JobID != "j" {
			t.Fatalf("unexpected output: %#v", out)
		}
		if out.Contract.Module != ai.ClaimVerifyLLMModule || out.AttestationID != "att" || out.AttestationURL != "url" {
			t.Fatalf("unexpected contract fields: %#v", out)
		}
	}

	{
		now := time.Now().UTC()
		out := buildAIClaimVerifyResponse(ai.Response{
			Status: ai.JobStatusOK,
			Result: &models.AIResult{
				ResultJSON: `{"ok":true}`,
				CreatedAt:  now,
				ExpiresAt:  now.Add(time.Hour),
			},
		}, "model", "hash", "", "")
		if out.Result == nil {
			t.Fatalf("expected result parsed")
		}
		if out.Contract.CreatedAt.IsZero() || out.Contract.ExpiresAt.IsZero() {
			t.Fatalf("expected contract timestamps set: %#v", out.Contract)
		}
	}
}

func TestRequireAIHandler(t *testing.T) {
	t.Parallel()

	s := &Server{ai: nil, store: nil}
	if _, err := s.requireAIHandler(&apptheory.Context{AuthIdentity: "x"}); err == nil {
		t.Fatalf("expected error for missing deps")
	}

	s = &Server{ai: &ai.Service{}, store: store.New(nil)}
	if _, err := s.requireAIHandler(nil); err == nil {
		t.Fatalf("expected error for nil ctx")
	}

	if _, err := s.requireAIHandler(&apptheory.Context{}); err == nil {
		t.Fatalf("expected unauthorized for empty identity")
	}

	got, err := s.requireAIHandler(&apptheory.Context{AuthIdentity: " inst "})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "inst" {
		t.Fatalf("expected trimmed identity, got %q", got)
	}
}

func TestHandleAIClaimVerify_OpenAIWebSearchRequiresOpenAIModel(t *testing.T) {
	t.Parallel()

	s := &Server{
		ai:    &ai.Service{},
		store: store.New(nil), // trust config store not ready -> default config (AI disabled)
	}

	body, _ := json.Marshal(claimVerifyRequest{
		Text: "hello",
		Retrieval: &claimVerifyRetrievalRequest{
			Mode: ai.ClaimVerifyRetrievalModeOpenAIWebSearch,
		},
	})
	ctx := &apptheory.Context{
		AuthIdentity: "inst",
		Request:      apptheory.Request{Body: body},
	}

	_, err := s.handleAIClaimVerify(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	if appErr, ok := err.(*apptheory.AppError); !ok || appErr.Code != "app.bad_request" {
		t.Fatalf("expected bad_request, got %T: %v", err, err)
	}
}

func TestHandleAIClaimVerify_ReturnsInternalErrorWhenAIServiceNotReady(t *testing.T) {
	t.Parallel()

	s := &Server{
		ai:    ai.NewService(nil),
		store: store.New(nil), // trust config store not ready -> default config
	}

	body, _ := json.Marshal(claimVerifyRequest{
		Claims: []string{"hello"},
		Evidence: []claimVerifyEvidenceRequest{
			{SourceID: "s1", Text: "evidence"},
		},
	})
	ctx := &apptheory.Context{
		AuthIdentity: "inst",
		RequestID:    "rid",
		Request:      apptheory.Request{Body: body},
	}

	_, err := s.handleAIClaimVerify(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	if appErr, ok := err.(*apptheory.AppError); !ok || appErr.Code != "app.internal" {
		t.Fatalf("expected app.internal, got %T: %v", err, err)
	}
}
