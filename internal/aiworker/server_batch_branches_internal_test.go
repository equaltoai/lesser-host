package aiworker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func testQueuedAIJob(id string, module string, inputsJSON string) *models.AIJob {
	now := time.Now().UTC()
	return &models.AIJob{
		ID:            strings.TrimSpace(id),
		InstanceSlug:  "inst",
		Module:        module,
		PolicyVersion: "v1",
		ModelSet:      deterministicValue,
		InputsHash:    "hash",
		InputsJSON:    inputsJSON,
		Status:        models.AIJobStatusQueued,
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	}
}

func TestGetQueuedAIJob_FiltersJobs(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_100, 0).UTC()

	require.Nil(t, (*Server)(nil).getQueuedAIJob(context.Background(), "job", now))
	require.Nil(t, (&Server{}).getQueuedAIJob(context.Background(), "job", now))

	st := &fakeAIStore{
		jobs: map[string]*models.AIJob{
			"expired": {
				ID:        "expired",
				Status:    models.AIJobStatusQueued,
				ExpiresAt: now.Add(-time.Minute),
			},
			"done": {
				ID:        "done",
				Status:    models.AIJobStatusOK,
				ExpiresAt: now.Add(time.Minute),
			},
			"queued": {
				ID:        "queued",
				Status:    models.AIJobStatusQueued,
				ExpiresAt: now.Add(time.Minute),
			},
		},
	}
	srv := &Server{store: st}

	require.Nil(t, srv.getQueuedAIJob(context.Background(), "missing", now))
	require.Nil(t, srv.getQueuedAIJob(context.Background(), "expired", now))
	require.Nil(t, srv.getQueuedAIJob(context.Background(), "done", now))

	got := srv.getQueuedAIJob(context.Background(), " queued ", now)
	require.NotNil(t, got)
	require.Equal(t, "queued", got.ID)
}

func TestParseRenderSummaryJob_Branches(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_200, 0).UTC()

	var nilServer *Server
	_, ok := nilServer.parseRenderSummaryJob(context.Background(), "req", now, testQueuedAIJob(strings.Repeat("0", 64), "render_summary_llm", `{"text":"hello"}`))
	require.False(t, ok)

	srv := &Server{store: &fakeAIStore{results: map[string]*models.AIResult{}}}

	job := testQueuedAIJob(strings.Repeat("1", 64), "render_summary_llm", `{"text":"hello"}`)
	job.Status = models.AIJobStatusOK
	_, ok = srv.parseRenderSummaryJob(context.Background(), "req", now, job)
	require.False(t, ok)

	job = testQueuedAIJob(strings.Repeat("2", 64), "render_summary_llm", `{"text":"hello"}`)
	job.ExpiresAt = now.Add(-time.Second)
	_, ok = srv.parseRenderSummaryJob(context.Background(), "req", now, job)
	require.False(t, ok)

	st := &fakeAIStore{
		results: map[string]*models.AIResult{
			strings.Repeat("3", 64): {ID: strings.Repeat("3", 64), ResultJSON: `{"kind":"render_summary","version":"v1","short_summary":"cached"}`},
		},
	}
	srv = &Server{store: st}
	job = testQueuedAIJob(strings.Repeat("3", 64), "render_summary_llm", `{"text":"hello"}`)
	_, ok = srv.parseRenderSummaryJob(context.Background(), "req-existing", now, job)
	require.False(t, ok)

	storedJob, err := st.GetAIJob(context.Background(), job.ID)
	require.NoError(t, err)
	require.NotNil(t, storedJob)
	require.Equal(t, models.AIJobStatusOK, storedJob.Status)
	require.Equal(t, "req-existing", storedJob.RequestID)

	srv = &Server{store: &fakeAIStore{results: map[string]*models.AIResult{}}}
	job = testQueuedAIJob(strings.Repeat("4", 64), "render_summary_llm", "{")
	_, ok = srv.parseRenderSummaryJob(context.Background(), "req", now, job)
	require.False(t, ok)

	job = testQueuedAIJob(strings.Repeat("5", 64), "render_summary_llm", `{"normalized_url":"https://example.com","text":"hello"}`)
	parsed, ok := srv.parseRenderSummaryJob(context.Background(), "req", now, job)
	require.True(t, ok)
	require.NotNil(t, parsed.Job)
	require.Equal(t, job.ID, parsed.Job.ID)
	require.Equal(t, "https://example.com", parsed.Input.NormalizedURL)
	require.Equal(t, "hello", parsed.Input.Text)
}

func TestProcessRenderSummaryBatch_GroupAndResultBranches(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_300, 0).UTC()
	ctx := context.Background()

	require.Error(t, (&Server{}).processRenderSummaryBatchV1(ctx, "req", []*models.AIJob{testQueuedAIJob(strings.Repeat("6", 64), "render_summary_llm", `{"text":"hello"}`)}))

	srv := &Server{store: &fakeAIStore{results: map[string]*models.AIResult{}}}
	require.NoError(t, srv.processRenderSummaryBatchV1(ctx, "req", nil))
	require.NoError(t, srv.processRenderSummaryGroup(ctx, "req", now, deterministicValue, nil))
	require.NoError(t, srv.processRenderSummaryGroup(ctx, "req", now, deterministicValue, []renderSummaryParsedJob{{}}))

	st := &failingPutResultStore{
		fakeAIStore: &fakeAIStore{results: map[string]*models.AIResult{}},
		err:         errors.New("put failed"),
	}
	srv = &Server{store: st}
	err := srv.processRenderSummaryBatchV1(ctx, "req", []*models.AIJob{
		testQueuedAIJob(strings.Repeat("7", 64), "render_summary_llm", `{"normalized_url":"https://example.com","text":"hello"}`),
	})
	require.EqualError(t, err, "put failed")
}

func TestPutRenderSummaryResult_FallbackAndGuards(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_400, 0).UTC()
	ctx := context.Background()

	require.NoError(t, (*Server)(nil).putRenderSummaryResult(ctx, "req", now, renderSummaryParsedJob{}, deterministicValue, nil, models.AIUsage{}, nil))

	st := &fakeAIStore{
		jobs:    map[string]*models.AIJob{},
		results: map[string]*models.AIResult{},
	}
	srv := &Server{store: st}

	require.NoError(t, srv.putRenderSummaryResult(ctx, "req", now, renderSummaryParsedJob{
		Job: &models.AIJob{ID: "   "},
	}, deterministicValue, nil, models.AIUsage{}, nil))

	job := testQueuedAIJob(strings.Repeat("8", 64), "render_summary_llm", `{"normalized_url":"https://example.com","text":"hello install wallet"}`)
	pj := renderSummaryParsedJob{
		Job: job,
		Input: ai.RenderSummaryInputsV1{
			NormalizedURL: "https://example.com",
			Text:          "hello install wallet",
		},
	}
	err := srv.putRenderSummaryResult(ctx, "req-fallback", now, pj, " openai:gpt-test ", map[string]ai.RenderSummaryResultV1{}, models.AIUsage{Provider: "openai", Model: "gpt-test"}, []models.AIError{
		{Code: "common_warn", Message: "shared warning"},
	})
	require.NoError(t, err)

	result, err := st.GetAIResult(ctx, job.ID)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "openai:gpt-test", result.ModelSet)
	require.Len(t, result.Errors, 2)
	require.Equal(t, "common_warn", result.Errors[0].Code)
	require.Equal(t, aiErrorCodeLLMMissingOutput, result.Errors[1].Code)
	require.Contains(t, result.ResultJSON, `"short_summary"`)

	storedJob, err := st.GetAIJob(ctx, job.ID)
	require.NoError(t, err)
	require.NotNil(t, storedJob)
	require.Equal(t, models.AIJobStatusOK, storedJob.Status)
	require.Equal(t, "req-fallback", storedJob.RequestID)
}

func TestProcessAIBatch_FiltersAndProcessesNonRenderSummaryOnly(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	eligible := &models.AIJob{
		ID:            strings.Repeat("9", 64),
		InstanceSlug:  "inst",
		Module:        "evidence_text_comprehend",
		PolicyVersion: "v1",
		ModelSet:      "aws:comprehend",
		InputsHash:    "hash",
		InputsJSON:    `{"text":"hello"}`,
		Status:        models.AIJobStatusQueued,
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	}
	expired := &models.AIJob{
		ID:            strings.Repeat("a", 64),
		InstanceSlug:  "inst",
		Module:        "render_summary_llm",
		PolicyVersion: "v1",
		ModelSet:      deterministicValue,
		InputsHash:    "hash",
		InputsJSON:    `{"text":"expired"}`,
		Status:        models.AIJobStatusQueued,
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(-time.Minute),
	}
	done := &models.AIJob{
		ID:            strings.Repeat("b", 64),
		InstanceSlug:  "inst",
		Module:        "evidence_text_comprehend",
		PolicyVersion: "v1",
		ModelSet:      "aws:comprehend",
		InputsHash:    "hash",
		InputsJSON:    `{"text":"done"}`,
		Status:        models.AIJobStatusOK,
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	}

	st := &fakeAIStore{
		jobs: map[string]*models.AIJob{
			eligible.ID: eligible,
			expired.ID:  expired,
			done.ID:     done,
		},
		results: map[string]*models.AIResult{},
	}
	srv := NewServer(config.Config{}, st, nil, fakeComprehend{}, fakeRekognition{})

	err := srv.processAIBatch(context.Background(), "req-batch", []string{
		" ",
		"missing",
		expired.ID,
		done.ID,
		eligible.ID,
	})
	require.NoError(t, err)

	result, err := st.GetAIResult(context.Background(), eligible.ID)
	require.NoError(t, err)
	require.NotNil(t, result)

	_, err = st.GetAIResult(context.Background(), expired.ID)
	require.Error(t, err)
	_, err = st.GetAIResult(context.Background(), done.ID)
	require.Error(t, err)
}

func TestClaimVerifyAndHelperFallbackBranches(t *testing.T) {
	evidenceIDs := map[string]struct{}{"s1": {}}
	evidenceText := map[string]string{
		"s1": "this evidence quote supports the claim with enough detail to match the citation text",
	}

	invalid := sanitizeClaimVerifyClaim(ai.ClaimVerifyClaimV1{
		ClaimID:        " c1 ",
		Text:           strings.Repeat("x", 260),
		Classification: "bad",
		Verdict:        "SUPPORTED",
		Confidence:     2,
		Reason:         strings.Repeat("r", 260),
		Citations: []ai.ClaimVerifyCitationV1{
			{SourceID: "s1", Quote: "missing from evidence"},
		},
	}, evidenceIDs, evidenceText)
	require.Equal(t, "c1", invalid.ClaimID)
	require.Len(t, invalid.Text, 240)
	require.Equal(t, "unclear", invalid.Classification)
	require.Equal(t, claimVerdictInconclusive, invalid.Verdict)
	require.Equal(t, 0.0, invalid.Confidence)
	require.Empty(t, invalid.Citations)
	require.Contains(t, invalid.Reason, "missing citations")

	valid := sanitizeClaimVerifyClaim(ai.ClaimVerifyClaimV1{
		ClaimID:        "c2",
		Text:           "  fact  ",
		Classification: "checkable",
		Verdict:        "supported",
		Confidence:     -1,
		Reason:         " ok ",
		Citations: []ai.ClaimVerifyCitationV1{
			{SourceID: "s1", Quote: "supports the claim with enough detail"},
		},
	}, evidenceIDs, evidenceText)
	require.Equal(t, 0.0, valid.Confidence)
	require.Equal(t, "supported", valid.Verdict)
	require.Len(t, valid.Citations, 1)

	var many []ai.ClaimVerifyClaimV1
	for i := 0; i < 12; i++ {
		many = append(many, ai.ClaimVerifyClaimV1{
			ClaimID:        "c",
			Text:           "fact",
			Classification: "checkable",
			Verdict:        "supported",
			Citations: []ai.ClaimVerifyCitationV1{
				{SourceID: "s1", Quote: "supports the claim with enough detail"},
			},
		})
	}
	require.Len(t, sanitizeClaimVerifyClaims(many, evidenceIDs, evidenceText), 10)

	require.Equal(t, "", sqsQueueNameFromURL(""))
	require.Equal(t, "", sqsQueueNameFromURL("http://[::1"))

	t.Setenv("ANTHROPIC_API_KEY", "primary")
	t.Setenv("CLAUDE_API_KEY", "secondary")
	k, err := anthropicAPIKey(context.Background())
	require.NoError(t, err)
	require.Equal(t, "primary", k)

	t.Setenv("ANTHROPIC_API_KEY", "")
	k, err = anthropicAPIKey(context.Background())
	require.NoError(t, err)
	require.Equal(t, "secondary", k)
}
