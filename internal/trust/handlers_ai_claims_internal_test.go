package trust

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
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

func TestHandleAIClaimVerify_DisabledShortCircuitsProvider(t *testing.T) {
	t.Parallel()

	st := &store.Store{}
	s := &Server{store: st, ai: ai.NewService(st)}
	body, _ := json.Marshal(claimVerifyRequest{
		Text: testHello,
		Evidence: []claimVerifyEvidenceRequest{{
			SourceID: "src",
			Text:     testHello,
		}},
	})
	resp, err := s.handleAIClaimVerify(&apptheory.Context{
		AuthIdentity: testBudgetInstanceSlug,
		Request:      apptheory.Request{Body: body},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	var out aiClaimVerifyResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status != statusDisabled || out.Budget.Reason != aiDisabledForInstanceReason {
		t.Fatalf("expected disabled claim response before provider use, got %#v", out)
	}
	if out.Contract.Module != ai.ClaimVerifyLLMModule || out.Contract.ModelSet != modelSetDeterministic || out.Contract.InputsHash == "" {
		t.Fatalf("unexpected contract: %#v", out.Contract)
	}
}

func TestNormalizeClaimVerifyTextBounds(t *testing.T) {
	t.Parallel()

	text, err := normalizeClaimVerifyText(" hello ")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if text != testHello {
		t.Fatalf("expected trim, got %q", text)
	}

	if _, err := normalizeClaimVerifyText(strings.Repeat("x", int(claimVerifyMaxTextBytes)+1)); err == nil {
		t.Fatalf("expected oversized text error")
	}
}

func TestBuildClaimVerifyEvidenceRejectsInvalidRequests(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		req  []claimVerifyEvidenceRequest
	}{
		{"TooManyItems", make([]claimVerifyEvidenceRequest, claimVerifyMaxEvidenceItems+1)},
		{"MissingSourceID", []claimVerifyEvidenceRequest{{SourceID: " ", Text: "x"}}},
		{"DuplicateSourceID", []claimVerifyEvidenceRequest{{SourceID: "a", Text: "x"}, {SourceID: "a", Text: "y"}}},
		{"InvalidRenderID", []claimVerifyEvidenceRequest{{SourceID: "a", RenderID: "nope"}}},
		{"MissingTextAndRenderID", []claimVerifyEvidenceRequest{{SourceID: "a"}}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := buildClaimVerifyEvidence(tc.req); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestBuildClaimVerifyEvidenceRejectsOversizedMetadata(t *testing.T) {
	t.Parallel()

	cases := []claimVerifyEvidenceRequest{
		{SourceID: strings.Repeat("s", int(claimVerifyMaxSourceIDBytes)+1), Text: "x"},
		{SourceID: "s", URL: strings.Repeat("u", int(claimVerifyMaxURLBytes)+1), Text: "x"},
		{SourceID: "s", Title: strings.Repeat("t", int(claimVerifyMaxTitleBytes)+1), Text: "x"},
		{SourceID: "s", Text: strings.Repeat("x", int(claimVerifyMaxEvidenceBytes)+1)},
	}
	for _, tc := range cases {
		if _, _, err := buildClaimVerifyEvidence([]claimVerifyEvidenceRequest{tc}); err == nil {
			t.Fatalf("expected oversized metadata/text error for %#v", tc)
		}
	}
}

func TestBuildClaimVerifyEvidenceSuccessText(t *testing.T) {
	t.Parallel()

	evidence, total, err := buildClaimVerifyEvidence([]claimVerifyEvidenceRequest{{SourceID: "a", URL: " u ", Title: " t ", Text: " hello "}})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(evidence) != 1 {
		t.Fatalf("expected 1 item, got %#v", evidence)
	}
	if evidence[0].SourceID != "a" || evidence[0].Text != testHello {
		t.Fatalf("unexpected evidence: %#v", evidence[0])
	}
	if total <= 0 {
		t.Fatalf("expected positive total bytes, got %d", total)
	}
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
		return
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
	if err == nil {
		t.Fatalf("expected oversized evidence error")
	}
	if trimmed != "" || b != 0 {
		t.Fatalf("expected empty oversized output, got %q (%d)", trimmed, b)
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

	resp, err := s.handleAIClaimVerify(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var out aiClaimVerifyResponse
	if unmarshalErr := json.Unmarshal(resp.Body, &out); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if out.Status != statusDisabled || out.Budget.Allowed {
		t.Fatalf("expected disabled response, got %#v", out)
	}
}

func TestHandleAIClaimVerify_ReturnsInternalErrorWhenAIServiceNotReady(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()
	enabled := true
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst", AIEnabled: &enabled}
	}).Once()

	s := &Server{
		ai:    ai.NewService(nil),
		store: store.New(db),
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
