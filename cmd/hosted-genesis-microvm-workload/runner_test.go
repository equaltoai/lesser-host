package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/completion"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// fakeTurnStore is an in-memory turnStore for runner tests.
type fakeTurnStore struct {
	session *models.HostedGenesisSession
	conv    *models.SoulAgentMintConversation
	reg     *models.SoulAgentRegistration
}

func (f *fakeTurnStore) GetHostedGenesisSession(_ context.Context, _, _ string) (*models.HostedGenesisSession, error) {
	if f.session == nil {
		return nil, errNotFound
	}
	return f.session, nil
}
func (f *fakeTurnStore) GetSoulAgentMintConversation(_ context.Context, _, _ string) (*models.SoulAgentMintConversation, error) {
	if f.conv == nil {
		return nil, errNotFound
	}
	return f.conv, nil
}
func (f *fakeTurnStore) GetSoulAgentRegistration(_ context.Context, _ string) (*models.SoulAgentRegistration, error) {
	if f.reg == nil {
		return nil, errNotFound
	}
	return f.reg, nil
}

// fakeCompletionStore records completion writes in-memory, applying the
// conditional version+status advance so the real CompletionWriter's idempotency
// is exercised end-to-end.
type fakeCompletionStore struct {
	session   *models.HostedGenesisSession
	lastWrite *models.HostedGenesisSession
}

func (f *fakeCompletionStore) GetHostedGenesisSession(_ context.Context, _, _ string) (*models.HostedGenesisSession, error) {
	if f.session == nil {
		return nil, errNotFound
	}
	c := *f.session
	return &c, nil
}
func (f *fakeCompletionStore) UpdateHostedGenesisSession(_ context.Context, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status) error {
	if hostedgenesis.NormalizeStatus(f.session.Status) != expectedStatus || f.session.Version != expectedVersion {
		return errConflict
	}
	c := *item
	c.Version = expectedVersion + 1
	f.session = &c
	f.lastWrite = &c
	return nil
}

var (
	errNotFound = &errString{"not found"}
	errConflict = &errString{"conditional write failed"}
)

type errString struct{ s string }

func (e *errString) Error() string { return e.s }

func baseTurnInput() (*fakeTurnStore, *fakeCompletionStore, completion.CompletionTurn) {
	messages := `[{"role":"user","content":"hello"}]`
	turn := completion.CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}
	session := &models.HostedGenesisSession{
		InstanceSlug: "acme", ConversationID: "conv-1", RegistrationID: "reg-1", AgentID: "agent-1",
		Status: string(hostedgenesis.StatusInProgress), LatestTurnID: "turn-1",
		TurnLedger:   []hostedgenesis.TurnLedgerEntry{{TurnID: "turn-1", MessageCount: 1, AcceptedAt: time.Now().UTC()}},
		MessageCount: 1, Version: 2, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	conv := &models.SoulAgentMintConversation{
		AgentID: "agent-1", ConversationID: "conv-1", Model: "openai:gpt-test",
		Messages: models.EncodeSoulMintConversationBlob(messages),
	}
	reg := &models.SoulAgentRegistration{ID: "reg-1", AgentID: "agent-1", DomainNormalized: "acme.example", LocalID: "acme"}
	return &fakeTurnStore{session: session, conv: conv, reg: reg},
		&fakeCompletionStore{session: &models.HostedGenesisSession{
			InstanceSlug: "acme", ConversationID: "conv-1", RegistrationID: "reg-1", AgentID: "agent-1",
			Status: string(hostedgenesis.StatusInProgress), LatestTurnID: "turn-1",
			TurnLedger:   []hostedgenesis.TurnLedgerEntry{{TurnID: "turn-1", MessageCount: 1, AcceptedAt: time.Now().UTC()}},
			MessageCount: 1, Version: 2, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}},
		turn
}

// openaiStreamServer returns an httptest server that emits a one-chunk OpenAI
// streaming response with the given assistant content.
func openaiStreamServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	chunk := "data: " + mustMarshal(map[string]any{
		"id": "chatcmpl_test", "object": "chat.completion.chunk", "created": 1, "model": "gpt-test",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": content}, "finish_reason": nil}},
	}) + "\n\ndata: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		_, _ = w.Write([]byte(chunk))
	}))
	return srv
}

func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// withOpenAIBaseURL points the OpenAI adapter at the given server and restores
// the prior env on cleanup.
func withOpenAIBaseURL(t *testing.T, url, apiKey string) {
	t.Helper()
	oldBase := os.Getenv("OPENAI_BASE_URL")
	oldKey := os.Getenv("OPENAI_API_KEY")
	t.Cleanup(func() {
		_ = os.Setenv("OPENAI_BASE_URL", oldBase)
		_ = os.Setenv("OPENAI_API_KEY", oldKey)
	})
	_ = os.Setenv("OPENAI_BASE_URL", url)
	_ = os.Setenv("OPENAI_API_KEY", apiKey)
}

// TestRunTurnAndPersist_AssistantTurnReadyThenDeclarationReady proves the run
// hook's full path: a successful assistant turn persists assistant_turn_ready,
// then declaration extraction persists declaration_ready, with the explicit HTTP
// timeout configured on the LLM clients.
func TestRunTurnAndPersist_AssistantTurnReadyThenDeclarationReady(t *testing.T) {
	// Two LLM calls happen (assistant stream + declarations JSON). Route both
	// through one server that serves the streaming assistant response for the
	// streaming call and the declarations JSON for the non-streaming call. The
	// openai-go SDK sets an Accept: text/event-stream header only for streaming
	// calls, so dispatch on that header.
	assistantChunk := "data: " + mustMarshal(map[string]any{
		"id": "chatcmpl_test", "object": "chat.completion.chunk", "created": 1, "model": "gpt-test",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "I am acme."}, "finish_reason": nil}},
	}) + "\n\ndata: [DONE]\n\n"
	declBody := mustMarshal(map[string]any{
		"id": "chatcmpl_test", "object": "chat.completion", "created": 1, "model": "gpt-test",
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": mustMarshal(validDeclarationDraft())}}},
		"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 7, "total_tokens": 12},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if strings.Contains(string(bodyBytes), `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(assistantChunk))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(declBody))
	}))
	t.Cleanup(srv.Close)
	withOpenAIBaseURL(t, srv.URL, "sk-test")

	// Install an explicit (but generous) timeout so the test exercises the
	// configured-client seam without flaking.
	llm.ConfigureProviderHTTPClient(&http.Client{Timeout: 5 * time.Second})
	t.Cleanup(func() { llm.ConfigureProviderHTTPClient(nil) })

	turnStore, compStore, turn := baseTurnInput()
	writer := completion.NewCompletionWriter(compStore, func() time.Time { return time.Unix(3000, 0).UTC() })
	runner := &turnRunner{store: turnStore, writer: writer, nowFunc: func() time.Time { return time.Unix(3000, 0).UTC() }}

	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatalf("runTurnAndPersist failed: %v", err)
	}
	if got := hostedgenesis.NormalizeStatus(compStore.session.Status); got != hostedgenesis.StatusDeclarationReady {
		t.Fatalf("expected declaration_ready, got %q (last write status=%q)", got, compStore.lastWrite.Status)
	}
	if compStore.session.DeclarationCheckpoint == nil {
		t.Fatalf("expected declaration checkpoint persisted")
	}
	if !strings.HasPrefix(compStore.session.DeclarationCheckpoint.DeclarationHash, "sha256:") {
		t.Fatalf("expected sha256 declaration hash, got %q", compStore.session.DeclarationCheckpoint.DeclarationHash)
	}
}

// TestRunTurnAndPersist_MissingSessionRecordsFailure proves a missing
// authoritative session surfaces as a typed invalid_completion_state failure,
// not a silent success.
func TestRunTurnAndPersist_MissingSessionRecordsFailure(t *testing.T) {
	withOpenAIBaseURL(t, "https://openai.example.test", "sk-test")
	turnStore := &fakeTurnStore{session: nil} // missing
	compStore := &fakeCompletionStore{session: &models.HostedGenesisSession{
		InstanceSlug: "acme", ConversationID: "conv-1", RegistrationID: "reg-1", AgentID: "agent-1",
		Status: string(hostedgenesis.StatusInProgress), LatestTurnID: "turn-1",
		TurnLedger:   []hostedgenesis.TurnLedgerEntry{{TurnID: "turn-1", MessageCount: 1, AcceptedAt: time.Now().UTC()}},
		MessageCount: 1, Version: 2, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}
	writer := completion.NewCompletionWriter(compStore, nil)
	runner := &turnRunner{store: turnStore, writer: writer}
	turn := completion.CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: "turn-1", RequestID: "req-1"}

	err := runner.runTurnAndPersist(context.Background(), turn)
	if err != nil {
		t.Fatalf("runTurnAndPersist should record failure and return nil, got %v", err)
	}
	if got := hostedgenesis.NormalizeStatus(compStore.session.Status); got != hostedgenesis.StatusFailed {
		t.Fatalf("expected failed, got %q", got)
	}
	if compStore.session.Failure == nil || compStore.session.Failure.Code != hostedgenesis.FailureCodeInvalidCompletionState {
		t.Fatalf("expected invalid_completion_state failure, got %+v", compStore.session.Failure)
	}
}

// TestRunTurnAndPersist_EmptyAssistantRecordsFailure proves an empty assistant
// response surfaces as assistant_turn_failed, not a silent success.
func TestRunTurnAndPersist_EmptyAssistantRecordsFailure(t *testing.T) {
	srv := openaiStreamServer(t, "   ") // empty content after trim
	t.Cleanup(srv.Close)
	withOpenAIBaseURL(t, srv.URL, "sk-test")
	llm.ConfigureProviderHTTPClient(&http.Client{Timeout: 5 * time.Second})
	t.Cleanup(func() { llm.ConfigureProviderHTTPClient(nil) })

	turnStore, compStore, turn := baseTurnInput()
	writer := completion.NewCompletionWriter(compStore, nil)
	runner := &turnRunner{store: turnStore, writer: writer}

	err := runner.runTurnAndPersist(context.Background(), turn)
	if err != nil {
		t.Fatalf("runTurnAndPersist should record failure and return nil, got %v", err)
	}
	if got := hostedgenesis.NormalizeStatus(compStore.session.Status); got != hostedgenesis.StatusFailed {
		t.Fatalf("expected failed, got %q", got)
	}
	if compStore.session.Failure == nil || compStore.session.Failure.Code != hostedgenesis.FailureCodeAssistantTurnFailed {
		t.Fatalf("expected assistant_turn_failed, got %+v", compStore.session.Failure)
	}
}

// TestRunTurnAndPersist_ReplayRejected proves idempotency: a second run for the
// same turn against a session that has already advanced to assistant_turn_ready
// does not silently re-apply — the assistant_turn_ready write conflicts.
func TestRunTurnAndPersist_ReplayRejected(t *testing.T) {
	srv := openaiStreamServer(t, "I am acme.")
	t.Cleanup(srv.Close)
	withOpenAIBaseURL(t, srv.URL, "sk-test")
	llm.ConfigureProviderHTTPClient(&http.Client{Timeout: 5 * time.Second})
	t.Cleanup(func() { llm.ConfigureProviderHTTPClient(nil) })

	turnStore, compStore, turn := baseTurnInput()
	// Pre-advance the completion store's session to assistant_turn_ready so the
	// first assistant_turn_ready write must conflict.
	compStore.session.Status = string(hostedgenesis.StatusAssistantTurnReady)
	compStore.session.Version = 3
	writer := completion.NewCompletionWriter(compStore, nil)
	runner := &turnRunner{store: turnStore, writer: writer}

	err := runner.runTurnAndPersist(context.Background(), turn)
	if err == nil {
		t.Fatal("expected conflict error on replay, got nil")
	}
	if !strings.Contains(err.Error(), "record assistant turn ready") {
		t.Fatalf("expected record assistant turn ready conflict, got %v", err)
	}
	// Session must remain at assistant_turn_ready (not overwritten to failed).
	if got := hostedgenesis.NormalizeStatus(compStore.session.Status); got != hostedgenesis.StatusAssistantTurnReady {
		t.Fatalf("expected session to remain assistant_turn_ready on replay conflict, got %q", got)
	}
}

func validDeclarationDraft() map[string]any {
	return map[string]any{
		"selfDescription": map[string]any{
			"purpose":      "I help with tasks.",
			"authoredBy":   "agent",
			"mintingModel": "openai:gpt-test",
		},
		"capabilities": []any{map[string]any{
			"capability": "reasoning", "scope": "general", "claimLevel": "self-declared",
		}},
		"boundaries": []any{map[string]any{
			"category": "scope_limit", "statement": "I will not harm.",
		}},
		"transparency": map[string]any{"notes": "test"},
	}
}
