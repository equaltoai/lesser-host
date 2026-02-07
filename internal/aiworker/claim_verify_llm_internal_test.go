package aiworker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestClaimVerifyWithLLM_OpenAI_SuccessMissingOutputAndError(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "k")

		jobID := "job-1"
		outPayload, err := json.Marshal(map[string]any{
			"items": []any{map[string]any{
				"item_id": jobID,
				"claims": []any{map[string]any{
					"claim_id":        "c1",
					"text":            "Alice is 30 years old.",
					"classification":  "checkable",
					"verdict":         "supported",
					"confidence":      0.8,
					"reason":          "Evidence indicates Alice is 30.",
					"citations":       []any{map[string]any{"source_id": "s1", "quote": "Alice is 30."}},
					"related_claims":  []any{},
					"contradictions":  []any{},
					"supporting_info": []any{},
				}},
				"warnings": []any{"w1"},
			}},
		})
		if err != nil {
			t.Fatalf("marshal output payload: %v", err)
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_test",
				"object":  "chat.completion",
				"created": 123,
				"model":   "gpt-test",
				"choices": []any{map[string]any{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": string(outPayload),
					},
				}},
				"usage": map[string]any{
					"prompt_tokens":     10,
					"completion_tokens": 20,
					"total_tokens":      30,
				},
			})
		}))
		t.Cleanup(server.Close)
		t.Setenv("OPENAI_BASE_URL", server.URL)

		s := &Server{}
		in := ai.ClaimVerifyInputsV1{
			Text:     "Alice is 30 years old.",
			Evidence: []ai.ClaimVerifyEvidenceV1{{SourceID: "s1", Text: "Alice is 30."}},
		}
		res, usage, errs := s.claimVerifyWithLLM(context.Background(), "openai:gpt-test", jobID, in, time.Now().Add(-10*time.Millisecond))
		if len(errs) != 0 {
			t.Fatalf("expected no errs, got %#v", errs)
		}
		if strings.TrimSpace(res.Kind) != "claim_verify" || strings.TrimSpace(res.Version) != "v1" || len(res.Claims) != 1 {
			t.Fatalf("unexpected result: %#v", res)
		}
		if usage.Provider != "openai" || strings.TrimSpace(usage.Model) == "" || usage.TotalTokens != 30 || usage.ToolCalls != 1 {
			t.Fatalf("unexpected usage: %#v", usage)
		}
	})

	t.Run("missing_output", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "k")

		jobID := "job-1"
		outPayload, err := json.Marshal(map[string]any{
			"items": []any{map[string]any{
				"item_id": "other",
				"claims":  []any{},
				"warnings": []any{
					"w",
				},
			}},
		})
		if err != nil {
			t.Fatalf("marshal output payload: %v", err)
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl_test",
				"object":  "chat.completion",
				"created": 123,
				"model":   "gpt-test",
				"choices": []any{map[string]any{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": string(outPayload),
					},
				}},
				"usage": map[string]any{
					"prompt_tokens":     1,
					"completion_tokens": 1,
					"total_tokens":      2,
				},
			})
		}))
		t.Cleanup(server.Close)
		t.Setenv("OPENAI_BASE_URL", server.URL)

		s := &Server{}
		in := ai.ClaimVerifyInputsV1{
			Text:     "Alice is 30 years old.",
			Evidence: []ai.ClaimVerifyEvidenceV1{{SourceID: "s1", Text: "Alice is 30."}},
		}
		_, _, errs := s.claimVerifyWithLLM(context.Background(), "openai:gpt-test", jobID, in, time.Now())
		if len(errs) != 1 || errs[0].Code != "llm_missing_output" {
			t.Fatalf("expected llm_missing_output, got %#v", errs)
		}
	})

	t.Run("error", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "k")

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		}))
		t.Cleanup(server.Close)
		t.Setenv("OPENAI_BASE_URL", server.URL)

		s := &Server{}
		in := ai.ClaimVerifyInputsV1{
			Text:     "Alice is 30 years old.",
			Evidence: []ai.ClaimVerifyEvidenceV1{{SourceID: "s1", Text: "Alice is 30."}},
		}
		_, _, errs := s.claimVerifyWithLLM(context.Background(), "openai:gpt-test", "job-1", in, time.Now())
		if len(errs) != 1 || errs[0].Code != "llm_failed" {
			t.Fatalf("expected llm_failed, got %#v", errs)
		}
	})
}

func TestMaybeAddClaimVerifyWebSearchEvidence_SkipsAndAdds(t *testing.T) {
	t.Run("skips_when_evidence_limit_reached", func(t *testing.T) {
		s := &Server{}
		in := &ai.ClaimVerifyInputsV1{
			Retrieval: &ai.ClaimVerifyRetrievalV1{Mode: ai.ClaimVerifyRetrievalModeOpenAIWebSearch, MaxSources: 3},
			Evidence: []ai.ClaimVerifyEvidenceV1{
				{SourceID: "1", Text: "t1"},
				{SourceID: "2", Text: "t2"},
				{SourceID: "3", Text: "t3"},
				{SourceID: "4", Text: "t4"},
				{SourceID: "5", Text: "t5"},
			},
		}
		used, _, _, errs := s.maybeAddClaimVerifyWebSearchEvidence(context.Background(), "openai:gpt-test", in)
		if used || len(errs) != 1 || errs[0].Code != "retrieval_skipped" {
			t.Fatalf("expected retrieval_skipped, used=%v errs=%#v", used, errs)
		}
	})

	t.Run("skips_when_modelset_not_openai", func(t *testing.T) {
		s := &Server{}
		in := &ai.ClaimVerifyInputsV1{
			Retrieval: &ai.ClaimVerifyRetrievalV1{Mode: ai.ClaimVerifyRetrievalModeOpenAIWebSearch, MaxSources: 3},
			Claims:    []string{"c"},
			Text:      "t",
			Evidence:   []ai.ClaimVerifyEvidenceV1{{SourceID: "s1", Text: "e"}},
		}
		used, _, _, errs := s.maybeAddClaimVerifyWebSearchEvidence(context.Background(), "anthropic:claude", in)
		if used || len(errs) != 1 || errs[0].Code != "retrieval_unsupported" {
			t.Fatalf("expected retrieval_unsupported, used=%v errs=%#v", used, errs)
		}
	})

	t.Run("returns_error_when_llm_call_fails", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "k")

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.Body.Close()
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
		}))
		t.Cleanup(server.Close)
		t.Setenv("OPENAI_BASE_URL", server.URL)

		s := &Server{}
		in := &ai.ClaimVerifyInputsV1{
			Retrieval: &ai.ClaimVerifyRetrievalV1{Mode: ai.ClaimVerifyRetrievalModeOpenAIWebSearch, MaxSources: 3, SearchContextSize: ai.ClaimVerifySearchContextLow},
			Claims:    []string{"c"},
			Text:      "t",
			Evidence:   []ai.ClaimVerifyEvidenceV1{{SourceID: "s1", Text: "e"}},
		}
		used, _, _, errs := s.maybeAddClaimVerifyWebSearchEvidence(context.Background(), "openai:gpt-test", in)
		if used || len(errs) != 1 || errs[0].Code != "retrieval_failed" || !errs[0].Retryable {
			t.Fatalf("expected retrieval_failed, used=%v errs=%#v", used, errs)
		}
	})

	t.Run("adds_sources_when_successful", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "k")

		outPayload, err := json.Marshal(map[string]any{
			"sources": []any{map[string]any{
				"url":   "https://example.com",
				"title": "Example",
				"text":  "Excerpt",
			}},
			"disclaimer": "disc",
		})
		if err != nil {
			t.Fatalf("marshal output payload: %v", err)
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         "resp_test",
				"object":     "response",
				"created_at": 123,
				"error":      map[string]any{"code": "", "message": ""},
				"incomplete_details": map[string]any{
					"reason": "",
				},
				"instructions": "",
				"metadata":     map[string]any{},
				"model":        "gpt-test",
				"output": []any{map[string]any{
					"id":     "msg_1",
					"type":   "message",
					"role":   "assistant",
					"status": "completed",
					"content": []any{
						map[string]any{
							"type": "output_text",
							"text": string(outPayload),
						},
					},
				}},
				"parallel_tool_calls": false,
				"temperature":         0.2,
				"tool_choice":         "required",
				"tools":               []any{},
				"top_p":               1,
				"usage": map[string]any{
					"input_tokens":          10,
					"input_tokens_details":  map[string]any{"cached_tokens": 0},
					"output_tokens":         20,
					"output_tokens_details": map[string]any{"reasoning_tokens": 0},
					"total_tokens":          30,
				},
			})
		}))
		t.Cleanup(server.Close)
		t.Setenv("OPENAI_BASE_URL", server.URL)

		s := &Server{}
		in := &ai.ClaimVerifyInputsV1{
			Retrieval: &ai.ClaimVerifyRetrievalV1{Mode: ai.ClaimVerifyRetrievalModeOpenAIWebSearch, MaxSources: 3, SearchContextSize: ai.ClaimVerifySearchContextLow},
			Claims:    []string{"c"},
			Text:      "t",
			Evidence:   []ai.ClaimVerifyEvidenceV1{{SourceID: "s1", Text: "e"}},
		}
		used, disclaimer, usage, errs := s.maybeAddClaimVerifyWebSearchEvidence(context.Background(), "openai:gpt-test", in)
		if !used || len(errs) != 0 {
			t.Fatalf("expected used without errs, used=%v errs=%#v", used, errs)
		}
		if disclaimer != "disc" {
			t.Fatalf("unexpected disclaimer: %q", disclaimer)
		}
		if usage.Provider != "openai" || usage.TotalTokens != 30 {
			t.Fatalf("unexpected usage: %#v", usage)
		}
		if len(in.Evidence) != 2 || !strings.HasPrefix(in.Evidence[1].SourceID, "web_") {
			t.Fatalf("expected web evidence appended, got %#v", in.Evidence)
		}
	})
}

func TestIssueClaimVerifyAttestationV1_SignsAndStoresOrReturnsErrors(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	signature := []byte("sig")
	kmsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()

		target := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Amz-Target")))
		if !strings.Contains(target, "sign") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_, _ = w.Write([]byte(`{"Signature":"` + base64.StdEncoding.EncodeToString(signature) + `","KeyId":"key","SigningAlgorithm":"RSASSA_PKCS1_V1_5_SHA_256"}`))
	}))
	t.Cleanup(kmsServer.Close)
	t.Setenv("AWS_ENDPOINT_URL_KMS", kmsServer.URL)

	tdb := newAttestationStoreTestDB()
	tdb.qAtt.On("Create").Return(nil).Maybe()

	st := store.New(tdb.db)

	srv := NewServer(
		config.Config{AttestationSigningKeyID: "key"},
		st,
		artifacts.New(""),
		fakeComprehend{},
		fakeRekognition{},
	)

	now := time.Now().UTC()

	job := &models.AIJob{
		ID:            "job1",
		InstanceSlug:  "inst",
		Module:        "claim_verify_llm",
		PolicyVersion: "v1",
		ModelSet:      "openai:gpt-test",
		InputsHash:    "h",
		InputsJSON:    "{}",
		Status:        models.AIJobStatusQueued,
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(1 * time.Hour),
	}

	in := ai.ClaimVerifyInputsV1{
		ActorURI:    "actor:1",
		ObjectURI:   "object:1",
		ContentHash: "h1",
		Evidence:    []ai.ClaimVerifyEvidenceV1{{SourceID: "s1", Text: "Evidence"}},
		Retrieval:   &ai.ClaimVerifyRetrievalV1{Mode: ai.ClaimVerifyRetrievalModeProvidedOnly},
	}
	res := ai.ClaimVerifyResultV1{Kind: "claim_verify", Version: "v1", Disclaimer: "disc"}

	tdb.qAtt.On("First", mock.AnythingOfType("*models.Attestation")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qAtt.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	if errs := srv.issueClaimVerifyAttestationV1(context.Background(), job, in, res); len(errs) != 0 {
		t.Fatalf("expected no errs on condition failed, got %#v", errs)
	}

	tdb.qAtt.On("First", mock.AnythingOfType("*models.Attestation")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qAtt.On("Create").Return(nil).Once()
	if errs := srv.issueClaimVerifyAttestationV1(context.Background(), job, in, res); len(errs) != 0 {
		t.Fatalf("expected no errs, got %#v", errs)
	}
}

func TestIssueClaimVerifyAttestationV1_ReturnsAttestationUnavailableForNonStore(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_ENDPOINT_URL_KMS", "http://127.0.0.1:1")

	srv := NewServer(
		config.Config{AttestationSigningKeyID: "key"},
		&fakeAIStore{},
		artifacts.New(""),
		fakeComprehend{},
		fakeRekognition{},
	)
	job := &models.AIJob{Module: "claim_verify_llm", PolicyVersion: "v1"}
	in := ai.ClaimVerifyInputsV1{ActorURI: "a", ObjectURI: "o", ContentHash: "h"}
	res := ai.ClaimVerifyResultV1{Kind: "claim_verify", Version: "v1"}
	errs := srv.issueClaimVerifyAttestationV1(context.Background(), job, in, res)
	if len(errs) != 1 || errs[0].Code != "attestation_unavailable" {
		t.Fatalf("expected attestation_unavailable, got %#v", errs)
	}
}

func TestMaybeAddClaimVerifyWebSearchEvidence_DoesNotChangeWhenRetrievalNotRequested(t *testing.T) {
	s := &Server{}
	used, disclaimer, usage, errs := s.maybeAddClaimVerifyWebSearchEvidence(context.Background(), "openai:gpt-test", &ai.ClaimVerifyInputsV1{})
	if used || disclaimer != "" || usage.Provider != "" || len(errs) != 0 {
		t.Fatalf("expected no-op, got used=%v disclaimer=%q usage=%#v errs=%#v", used, disclaimer, usage, errs)
	}
}

func TestClaimVerifyWithLLM_ReturnsDeterministicWhenUnsupportedModelSet(t *testing.T) {
	s := &Server{}
	in := ai.ClaimVerifyInputsV1{
		Text:     "x",
		Evidence: []ai.ClaimVerifyEvidenceV1{{SourceID: "s1", Text: "e"}},
	}
	res, usage, errs := s.claimVerifyWithLLM(context.Background(), "deterministic", "job", in, time.Now())
	if strings.TrimSpace(res.Kind) != "" || usage.Provider != "" || len(errs) != 0 {
		t.Fatalf("expected zero result for unsupported model set, got res=%#v usage=%#v errs=%#v", res, usage, errs)
	}
}

func TestMaybeAddClaimVerifyWebSearchEvidence_ClampsMaxSourcesAndUniquifiesIDs(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "k")

	outPayload, err := json.Marshal(map[string]any{
		"sources": []any{
			map[string]any{"url": "https://a", "title": "A", "text": "A"},
			map[string]any{"url": "https://b", "title": "B", "text": "B"},
		},
		"disclaimer": "",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var (
		mu        sync.Mutex
		lastReq   string
		reqCount  int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		lastReq = string(body)
		reqCount++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         "resp_test",
			"object":     "response",
			"created_at": 123,
			"error":      map[string]any{"code": "", "message": ""},
			"incomplete_details": map[string]any{"reason": ""},
			"instructions":       "",
			"metadata":           map[string]any{},
			"model":              "gpt-test",
			"output": []any{map[string]any{
				"id":     "msg_1",
				"type":   "message",
				"role":   "assistant",
				"status": "completed",
				"content": []any{
					map[string]any{"type": "output_text", "text": string(outPayload)},
				},
			}},
			"parallel_tool_calls": false,
			"temperature":         0.2,
			"tool_choice":         "required",
			"tools":               []any{},
			"top_p":               1,
			"usage": map[string]any{
				"input_tokens":          1,
				"input_tokens_details":  map[string]any{"cached_tokens": 0},
				"output_tokens":         1,
				"output_tokens_details": map[string]any{"reasoning_tokens": 0},
				"total_tokens":          2,
			},
		})
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_BASE_URL", server.URL)

	s := &Server{}
	in := &ai.ClaimVerifyInputsV1{
		Retrieval: &ai.ClaimVerifyRetrievalV1{Mode: ai.ClaimVerifyRetrievalModeOpenAIWebSearch, MaxSources: 999},
		Claims:    []string{"c"},
		Text:      "t",
		Evidence:   []ai.ClaimVerifyEvidenceV1{{SourceID: "web_1", Text: "existing"}},
	}
	used, disc, _, errs := s.maybeAddClaimVerifyWebSearchEvidence(context.Background(), "openai:gpt-test", in)
	if !used || len(errs) != 0 {
		t.Fatalf("expected used without errs, used=%v errs=%#v", used, errs)
	}
	if strings.TrimSpace(disc) == "" {
		t.Fatalf("expected default disclaimer")
	}
	mu.Lock()
	gotReq := lastReq
	gotCount := reqCount
	mu.Unlock()
	if gotCount != 1 {
		t.Fatalf("expected one openai request, got %d", gotCount)
	}
	if !strings.Contains(gotReq, "max_sources") {
		t.Fatalf("expected request to include max_sources, got %q", gotReq)
	}
	if len(in.Evidence) < 2 {
		t.Fatalf("expected evidence appended, got %#v", in.Evidence)
	}
	// Ensure the new sources did not collide with existing IDs.
	for i := range in.Evidence {
		if i == 0 {
			continue
		}
		if in.Evidence[i].SourceID == "web_1" {
			t.Fatalf("expected uniquified source ids, got %#v", in.Evidence)
		}
	}
}
