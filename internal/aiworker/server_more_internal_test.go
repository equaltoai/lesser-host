package aiworker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/stretchr/testify/require"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestProcessAIJob_WritesRenderSummaryResult(t *testing.T) {
	t.Parallel()

	job := &models.AIJob{
		ID:            strings.Repeat("e", 64),
		InstanceSlug:  "inst",
		Module:        "render_summary_llm",
		PolicyVersion: "v1",
		ModelSet:      "deterministic",
		InputsHash:    "hash",
		InputsJSON:    `{"render_id":"r","normalized_url":"https://example.com/","text":"hello\\ninstall wallet"}`,
		Status:        models.AIJobStatusQueued,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		ExpiresAt:     time.Now().UTC().Add(1 * time.Hour),
	}

	parsed := runProcessAIJobAndParseResult(t, job, "render_summary")
	if strings.TrimSpace(testutil.RequireType[string](t, parsed["short_summary"])) == "" {
		t.Fatalf("expected summary, got %#v", parsed)
	}
}

func TestProcessAIJob_WritesClaimVerifyResult_Deterministic(t *testing.T) {
	t.Parallel()

	job := &models.AIJob{
		ID:            strings.Repeat("f", 64),
		InstanceSlug:  "inst",
		Module:        "claim_verify_llm",
		PolicyVersion: "v1",
		ModelSet:      "deterministic",
		InputsHash:    "hash",
		InputsJSON:    `{"text":"Alice is 30 years old.","evidence":[{"source_id":"s1","text":"Alice is 30."}]}`,
		Status:        models.AIJobStatusQueued,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		ExpiresAt:     time.Now().UTC().Add(1 * time.Hour),
	}

	parsed := runProcessAIJobAndParseResult(t, job, "claim_verify")
	if _, ok := parsed["claims"]; !ok {
		t.Fatalf("expected claims, got %#v", parsed)
	}
}

func TestProcessAIBatch_RenderSummaryGroupingAndIdempotency(t *testing.T) {
	t.Parallel()

	st := &fakeAIStore{
		jobs:    map[string]*models.AIJob{},
		results: map[string]*models.AIResult{},
	}

	now := time.Now().UTC()

	rs1 := &models.AIJob{ID: strings.Repeat("a", 64), InstanceSlug: "inst", Module: "render_summary_llm", PolicyVersion: "v1", ModelSet: "deterministic", InputsHash: "h1", InputsJSON: `{"normalized_url":"https://a/","text":"a"}`, Status: models.AIJobStatusQueued, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(1 * time.Hour)}
	rs2 := &models.AIJob{ID: strings.Repeat("b", 64), InstanceSlug: "inst", Module: "render_summary_llm", PolicyVersion: "v1", ModelSet: "deterministic", InputsHash: "h2", InputsJSON: `{"normalized_url":"https://b/","text":"b"}`, Status: models.AIJobStatusQueued, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(1 * time.Hour)}
	rs3 := &models.AIJob{ID: strings.Repeat("c", 64), InstanceSlug: "inst", Module: "render_summary_llm", PolicyVersion: "v1", ModelSet: "deterministic", InputsHash: "h3", InputsJSON: `{"normalized_url":"https://c/","text":"c"}`, Status: models.AIJobStatusQueued, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(1 * time.Hour)}

	other := &models.AIJob{ID: strings.Repeat("d", 64), InstanceSlug: "inst", Module: "evidence_text_comprehend", PolicyVersion: "v1", ModelSet: "aws:comprehend", InputsHash: "h4", InputsJSON: `{"text":"hello"}`, Status: models.AIJobStatusQueued, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(1 * time.Hour)}

	st.jobs[rs1.ID] = rs1
	st.jobs[rs2.ID] = rs2
	st.jobs[rs3.ID] = rs3
	st.jobs[other.ID] = other

	// Pre-existing result triggers idempotency skip for rs1.
	st.results[rs1.ID] = &models.AIResult{ID: rs1.ID, ResultJSON: `{"kind":"render_summary","version":"v1","short_summary":"cached"}`}

	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	if err := srv.processAIBatch(context.Background(), "req", []string{rs1.ID, rs2.ID, rs3.ID, other.ID}); err != nil {
		t.Fatalf("processAIBatch: %v", err)
	}

	for _, id := range []string{rs1.ID, rs2.ID, rs3.ID, other.ID} {
		res, err := st.GetAIResult(context.Background(), id)
		if err != nil || res == nil {
			t.Fatalf("expected result for %s, err=%v", id, err)
		}
		j, _ := st.GetAIJob(context.Background(), id)
		if j == nil || strings.TrimSpace(j.Status) != models.AIJobStatusOK {
			t.Fatalf("expected job ok for %s, got %#v", id, j)
		}
	}
}

func TestClaimVerifyHelperFunctions(t *testing.T) {
	require.Equal(t, "q", sqsQueueNameFromURL(" https://sqs.us-east-1.amazonaws.com/123/q "))

	t.Setenv("OPENAI_API_KEY", "k")
	k, err := openAIAPIKey(context.Background())
	require.NoError(t, err)
	require.Equal(t, "k", k)

	t.Setenv("ANTHROPIC_API_KEY", "a")
	k, err = anthropicAPIKey(context.Background())
	require.NoError(t, err)
	require.Equal(t, "a", k)

	require.Nil(t, normalizeClaimVerifyRetrievalV1(nil))
	norm := normalizeClaimVerifyRetrievalV1(&ai.ClaimVerifyRetrievalV1{Mode: "BAD", MaxSources: 99, SearchContextSize: "nope"})
	require.NotNil(t, norm)
	require.Equal(t, ai.ClaimVerifyRetrievalModeProvidedOnly, norm.Mode)
	require.Equal(t, 5, norm.MaxSources)
	require.Empty(t, norm.SearchContextSize)

	add := uniquifyClaimVerifyEvidenceIDs(
		[]ai.ClaimVerifyEvidenceV1{{SourceID: "s1", Text: "t"}},
		[]ai.ClaimVerifyEvidenceV1{{SourceID: "s1", Text: "x"}, {SourceID: "", Text: "y"}},
	)
	require.Len(t, add, 2)
	require.NotEqual(t, "s1", add[0].SourceID)
	require.True(t, strings.HasPrefix(add[1].SourceID, "web_"))

	trimmed := trimClaimVerifySourcesForOutput([]ai.ClaimVerifyEvidenceV1{
		{SourceID: " ", Text: "x"},
		{SourceID: "s1", Text: " "},
		{SourceID: "s1", Text: " ok "},
	})
	require.Len(t, trimmed, 1)
	require.Equal(t, "s1", trimmed[0].SourceID)
	require.Equal(t, "ok", trimmed[0].Text)

	ids, texts := claimVerifyEvidenceMaps([]ai.ClaimVerifyEvidenceV1{
		{SourceID: "s1", Text: "t1"},
		{SourceID: "s2", Text: ""},
	})
	require.Contains(t, ids, "s1")
	require.Equal(t, "t1", texts["s1"])

	actorURI, objURI, hash, ok := claimVerifyAttestationSubject(ai.ClaimVerifyInputsV1{ActorURI: "a", ObjectURI: "o", ContentHash: "h"})
	require.True(t, ok)
	require.Equal(t, "a", actorURI)
	require.Equal(t, "o", objURI)
	require.Equal(t, "h", hash)

	require.Equal(t, ai.ClaimVerifyRetrievalModeProvidedOnly, claimVerifyAttestationRetrievalMode(nil))

	res := claimVerifyResultForAttestation(ai.ClaimVerifyResultV1{Kind: "claim_verify", Version: "v1", Sources: []ai.ClaimVerifyEvidenceV1{{SourceID: "s1", Text: "t"}}})
	require.Nil(t, res.Sources)
}

func TestHydrateClaimVerifyEvidenceFromRenders_NoAWS(t *testing.T) {
	t.Parallel()

	srv := NewServer(config.Config{}, &fakeAIStore{}, artifacts.New(""), fakeComprehend{}, fakeRekognition{})
	in := &ai.ClaimVerifyInputsV1{
		Evidence: []ai.ClaimVerifyEvidenceV1{
			{SourceID: "s1", RenderID: "not-hex", Text: ""},
			{SourceID: "s2", RenderID: strings.Repeat("a", 64), Text: ""},
		},
	}
	errs := srv.hydrateClaimVerifyEvidenceFromRenders(context.Background(), "inst", in)
	if len(errs) != 2 {
		t.Fatalf("expected errs, got %#v", errs)
	}
}

func TestHydrateClaimVerifyEvidenceFromRenders_RejectsCrossInstanceRenderID(t *testing.T) {
	t.Parallel()

	renderID := strings.Repeat("b", 64)
	srv := NewServer(
		config.Config{},
		&fakeAIStore{
			renders: map[string]*models.RenderArtifact{
				renderID: {
					ID:                renderID,
					RequestedBy:       "other-instance",
					SnapshotObjectKey: "renders/" + renderID + "/snapshot.txt",
				},
			},
		},
		artifacts.New(""),
		fakeComprehend{},
		fakeRekognition{},
	)
	in := &ai.ClaimVerifyInputsV1{
		Evidence: []ai.ClaimVerifyEvidenceV1{
			{SourceID: "s1", RenderID: renderID},
		},
	}

	errs := srv.hydrateClaimVerifyEvidenceFromRenders(context.Background(), "inst", in)
	require.Len(t, errs, 1)
	require.Equal(t, "invalid_inputs", errs[0].Code)
	require.Empty(t, in.Evidence[0].Text)
}

func TestHandleSafetyQueueMessage_BatchKind(t *testing.T) {
	t.Parallel()

	st := &fakeAIStore{jobs: map[string]*models.AIJob{}}
	now := time.Now().UTC()
	job := &models.AIJob{
		ID:            strings.Repeat("9", 64),
		InstanceSlug:  "inst",
		Module:        "render_summary_llm",
		PolicyVersion: "v1",
		ModelSet:      "deterministic",
		InputsHash:    "h",
		InputsJSON:    `{"normalized_url":"https://x/","text":"x"}`,
		Status:        models.AIJobStatusQueued,
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(1 * time.Hour),
	}
	st.jobs[job.ID] = job

	srv := NewServer(config.Config{}, st, artifacts.New(""), fakeComprehend{}, fakeRekognition{})

	body, _ := json.Marshal(ai.JobMessage{Kind: "ai_job_batch", JobIDs: []string{job.ID}})
	evctx := &apptheory.EventContext{RequestID: "rid"}
	if err := srv.handleSafetyQueueMessage(evctx, events.SQSMessage{Body: string(body)}); err != nil {
		t.Fatalf("handleSafetyQueueMessage: %v", err)
	}

	res, err := st.GetAIResult(context.Background(), job.ID)
	if err != nil || res == nil {
		t.Fatalf("expected result written, err=%v res=%v", err, res)
	}
}
