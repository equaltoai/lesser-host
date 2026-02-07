package aiworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type attestationStoreTestDB struct {
	db   *ttmocks.MockExtendedDB
	qAtt *ttmocks.MockQuery
}

func newAttestationStoreTestDB() attestationStoreTestDB {
	db := ttmocks.NewMockExtendedDBStrict()
	qAtt := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db)
	db.On("Model", mock.AnythingOfType("*models.Attestation")).Return(qAtt)

	qAtt.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qAtt)
	qAtt.On("ConsistentRead").Return(qAtt)

	return attestationStoreTestDB{db: db, qAtt: qAtt}
}

func TestMergeAIUsage_PrefersNonDeterministicProviderAndAccumulatesTokens(t *testing.T) {
	t.Parallel()

	start := time.Now().Add(-100 * time.Millisecond)

	primary := models.AIUsage{
		Provider: deterministicValue,
		Model:    "",

		InputTokens:  1,
		OutputTokens: 2,
		TotalTokens:  3,
		ToolCalls:    0,
	}
	extra := models.AIUsage{
		Provider:     "openai",
		Model:        "gpt-x",
		InputTokens:  10,
		OutputTokens: 20,
		TotalTokens:  30,
		ToolCalls:    2,
	}

	got := mergeAIUsage(primary, extra, start)
	if got.Provider != "openai" || got.Model != "gpt-x" {
		t.Fatalf("unexpected provider/model: %#v", got)
	}
	if got.InputTokens != 11 || got.OutputTokens != 22 || got.TotalTokens != 33 || got.ToolCalls != 2 {
		t.Fatalf("unexpected token aggregation: %#v", got)
	}
	if got.DurationMs <= 0 {
		t.Fatalf("expected duration set, got %#v", got)
	}

	// Extra provider empty: should not change provider/model/tokens (except duration).
	got2 := mergeAIUsage(models.AIUsage{Provider: "openai", Model: "m", InputTokens: 1}, models.AIUsage{}, start)
	if got2.Provider != "openai" || got2.Model != "m" || got2.InputTokens != 1 {
		t.Fatalf("unexpected merge with empty extra: %#v", got2)
	}
}

func TestAIJobModulePolicy_NormalizesAndValidates(t *testing.T) {
	t.Parallel()

	if _, _, ok := aiJobModulePolicy(nil); ok {
		t.Fatalf("expected false for nil job")
	}

	if _, _, ok := aiJobModulePolicy(&models.AIJob{Module: " ", PolicyVersion: "v1"}); ok {
		t.Fatalf("expected false for missing module")
	}

	module, version, ok := aiJobModulePolicy(&models.AIJob{Module: " Moderation_Text_LLM ", PolicyVersion: " v1 "})
	if !ok || module != "moderation_text_llm" || version != "v1" {
		t.Fatalf("unexpected policy: module=%q version=%q ok=%v", module, version, ok)
	}
}

func TestClaimVerifyQuoteMatchesEvidence_NormalizesWhitespace(t *testing.T) {
	t.Parallel()

	if claimVerifyQuoteMatchesEvidence("", "x") {
		t.Fatalf("expected false for empty evidence")
	}
	if claimVerifyQuoteMatchesEvidence("hello world", "short") {
		t.Fatalf("expected false for short quote")
	}

	if !claimVerifyQuoteMatchesEvidence("Hello   world\nfrom   Alice", "world from alice") {
		t.Fatalf("expected normalized quote to match")
	}
	if claimVerifyQuoteMatchesEvidence("Hello world", "world from bob") {
		t.Fatalf("expected mismatch")
	}
}

func TestClaimVerifyMissingEvidenceResponse_ReturnsDeterministicErrorAndJSON(t *testing.T) {
	t.Parallel()

	start := time.Now()
	in := ai.ClaimVerifyInputsV1{Text: "x"}

	resultJSON, usage, errs, err := claimVerifyMissingEvidenceResponse(in, start)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if strings.TrimSpace(resultJSON) == "" {
		t.Fatalf("expected json output")
	}
	if usage.Provider != deterministicValue || usage.Model != deterministicValue {
		t.Fatalf("unexpected usage: %#v", usage)
	}
	if len(errs) != 1 || errs[0].Code != "invalid_inputs" {
		t.Fatalf("unexpected errs: %#v", errs)
	}

	var parsed ai.ClaimVerifyResultV1
	if err := json.Unmarshal([]byte(resultJSON), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, w := range parsed.Warnings {
		if strings.TrimSpace(w) == "missing_evidence" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected missing_evidence warning, got %#v", parsed.Warnings)
	}
}

func TestClaimVerifyAttestationEvidenceRefs_HashesText(t *testing.T) {
	t.Parallel()

	in := []ai.ClaimVerifyEvidenceV1{
		{SourceID: "s1", URL: "https://example.com", Title: "t", RenderID: "r", Text: " hello "},
		{SourceID: " ", Text: "ignored"},
		{SourceID: "s2", Text: " "},
	}

	got := claimVerifyAttestationEvidenceRefs(in)
	if len(got) != 1 || got[0].SourceID != "s1" {
		t.Fatalf("unexpected refs: %#v", got)
	}

	sum := sha256.Sum256([]byte("hello"))
	if got[0].TextSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("unexpected sha: %q", got[0].TextSHA256)
	}
}

func TestAttestationStoreForAIJob_ReturnsErrorsForNilAndNonStore(t *testing.T) {
	t.Parallel()

	var s *Server
	if _, err := s.attestationStoreForAIJob(); err == nil {
		t.Fatalf("expected error for nil server")
	}

	s2 := &Server{store: &fakeAIStore{}}
	if _, err := s2.attestationStoreForAIJob(); err == nil {
		t.Fatalf("expected error for non-store")
	}
}

func TestAttestationAlreadyExists_ReturnsExpectedStates(t *testing.T) {
	t.Parallel()

	t.Run("exists", func(t *testing.T) {
		tdb := newAttestationStoreTestDB()
		st := store.New(tdb.db)

		tdb.qAtt.On("First", mock.AnythingOfType("*models.Attestation")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.Attestation)
			dest.ID = "a1"
		}).Once()

		s := &Server{}
		exists, errs := s.attestationAlreadyExists(context.Background(), st, "a1")
		if len(errs) != 0 || !exists {
			t.Fatalf("expected exists, got exists=%v errs=%#v", exists, errs)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		tdb := newAttestationStoreTestDB()
		st := store.New(tdb.db)

		tdb.qAtt.On("First", mock.AnythingOfType("*models.Attestation")).Return(theoryErrors.ErrItemNotFound).Once()

		s := &Server{}
		exists, errs := s.attestationAlreadyExists(context.Background(), st, "missing")
		if len(errs) != 0 || exists {
			t.Fatalf("expected not exists, got exists=%v errs=%#v", exists, errs)
		}
	})

	t.Run("db_error", func(t *testing.T) {
		tdb := newAttestationStoreTestDB()
		st := store.New(tdb.db)

		tdb.qAtt.On("First", mock.AnythingOfType("*models.Attestation")).Return(errors.New("boom")).Once()

		s := &Server{}
		exists, errs := s.attestationAlreadyExists(context.Background(), st, "a1")
		if exists || len(errs) != 1 || errs[0].Code != "attestation_failed" {
			t.Fatalf("unexpected: exists=%v errs=%#v", exists, errs)
		}
	})
}

func TestProcessAIJob_RecordsErrorWhenModuleRunFails(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	job := &models.AIJob{
		ID:            strings.Repeat("e", 64),
		InstanceSlug:  "inst",
		Module:        "evidence_text_comprehend",
		PolicyVersion: "v1",
		ModelSet:      "aws:comprehend",
		InputsHash:    "hash",
		InputsJSON:    `{"text":"hello"}`,
		Status:        models.AIJobStatusQueued,
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(1 * time.Hour),
	}

	st := &fakeAIStore{
		jobs: map[string]*models.AIJob{
			job.ID: job,
		},
	}

	srv := NewServer(config.Config{ArtifactBucketName: "bucket"}, st, artifacts.New("bucket"), nil, fakeRekognition{})
	if err := srv.processAIJob(context.Background(), "req", job.ID); err == nil {
		t.Fatalf("expected error")
	}

	j2, _ := st.GetAIJob(context.Background(), job.ID)
	if j2 == nil || strings.TrimSpace(j2.Status) != models.AIJobStatusError {
		t.Fatalf("expected job status error, got %#v", j2)
	}
	if j2.Attempts != 1 || j2.ErrorCode == "" || j2.ErrorMessage == "" || j2.RequestID != "req" {
		t.Fatalf("expected error fields set, got %#v", j2)
	}
}
