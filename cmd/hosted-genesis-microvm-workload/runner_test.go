package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/completion"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/mintprompt"
	"github.com/equaltoai/lesser-host/internal/soul"
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
func (f *fakeTurnStore) PutSoulAgentMintConversation(_ context.Context, item *models.SoulAgentMintConversation) error {
	if item == nil {
		return errNotFound
	}
	c := *item
	f.conv = &c
	return nil
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
	session      *models.HostedGenesisSession
	conversation *models.SoulAgentMintConversation
	lastWrite    *models.HostedGenesisSession
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

func (f *fakeCompletionStore) GetSoulAgentMintConversation(_ context.Context, agentID, conversationID string) (*models.SoulAgentMintConversation, error) {
	if f.session == nil || f.session.AgentID != agentID || f.session.ConversationID != conversationID {
		return nil, errNotFound
	}
	if f.conversation == nil {
		return &models.SoulAgentMintConversation{AgentID: agentID, ConversationID: conversationID, Status: models.SoulMintConversationStatusInProgress}, nil
	}
	c := *f.conversation
	return &c, nil
}

func (f *fakeCompletionStore) FailHostedGenesisSessionAndConversation(_ context.Context, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, conversation *models.SoulAgentMintConversation) error {
	if f.session == nil || hostedgenesis.NormalizeStatus(f.session.Status) != expectedStatus || f.session.Version != expectedVersion {
		return errConflict
	}
	c := *item
	c.Version = expectedVersion + 1
	f.session = &c
	f.lastWrite = &c
	if conversation != nil {
		copy := *conversation
		f.conversation = &copy
	}
	return nil
}

var (
	errNotFound = &errString{"not found"}
	errConflict = &errString{"conditional write failed"}
)

type errString struct{ s string }

func (e *errString) Error() string { return e.s }

// setFiveBodyContractEnv selects the five-body declaration contract the way the
// deployed MicroVM image env does. Fresh hosted-genesis production has no
// legacy lane, so every turn-driving test must opt in explicitly.
func setFiveBodyContractEnv(t *testing.T) {
	t.Helper()
	t.Setenv(hostedgenesis.EnvDeclarationSchemaVersion, hostedgenesis.DeclarationSchemaVersionV2)
	t.Setenv(hostedgenesis.EnvGuidanceVersion, hostedgenesis.GuidanceVersionV2)
}

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
		}, conversation: &models.SoulAgentMintConversation{AgentID: "agent-1", ConversationID: "conv-1", Status: models.SoulMintConversationStatusInProgress}},
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

// TestRunTurnAndPersist_InProgressRecordsAssistantTurnReady proves the run hook
// executes exactly the assistant step for an in_progress HostedGenesisSession.
// Declaration extraction is a separate complete-driven dispatch once the user
// accepts the assistant transcript.
func TestRunTurnAndPersist_InProgressRecordsAssistantTurnReady(t *testing.T) {
	setFiveBodyContractEnv(t)
	srv := openaiStreamServer(t, "I am acme.")
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
	if got := hostedgenesis.NormalizeStatus(compStore.session.Status); got != hostedgenesis.StatusAssistantTurnReady {
		t.Fatalf("expected assistant_turn_ready, got %q (last write status=%q)", got, compStore.lastWrite.Status)
	}
	decodedMessages := models.DecodeSoulMintConversationBlob(turnStore.conv.Messages)
	if !strings.Contains(decodedMessages, `"role":"assistant"`) || !strings.Contains(decodedMessages, "I am acme.") {
		t.Fatalf("expected assistant transcript persisted to conversation, got %s", decodedMessages)
	}
	if compStore.session.DeclarationCheckpoint != nil || models.DecodeSoulMintConversationBlob(turnStore.conv.ProducedDeclarations) != "" {
		t.Fatalf("in_progress turn must not persist declarations yet: session=%#v declarations=%q", compStore.session, turnStore.conv.ProducedDeclarations)
	}
	assertVMCheckpoint(t, compStore.session.VMCheckpoint, actorActionAsk, actorStepAssistantTurn, hostedgenesis.StatusInProgress, hostedgenesis.StatusAssistantTurnReady, "turn-1")
}

func TestRunTurnAndPersist_DeclarationExtractionPendingRecordsDeclarationReady(t *testing.T) {
	setFiveBodyContractEnv(t)
	declBody := mustMarshal(map[string]any{
		"id": "chatcmpl_test", "object": "chat.completion", "created": 1, "model": "gpt-test",
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": mustMarshal(validDeclarationDraft())}}},
		"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 7, "total_tokens": 12},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if strings.Contains(string(bodyBytes), `"stream":true`) {
			t.Fatalf("declaration extraction must not run an assistant streaming turn")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(declBody))
	}))
	t.Cleanup(srv.Close)
	withOpenAIBaseURL(t, srv.URL, "sk-test")
	llm.ConfigureProviderHTTPClient(&http.Client{Timeout: 5 * time.Second})
	t.Cleanup(func() { llm.ConfigureProviderHTTPClient(nil) })

	turnStore, compStore, turn := baseTurnInput()
	transcript := `[{"role":"user","content":"hello"},{"role":"assistant","content":"I am acme."}]`
	turnStore.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	turnStore.conv.Messages = models.EncodeSoulMintConversationBlob(transcript)
	compStore.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	writer := completion.NewCompletionWriter(compStore, func() time.Time { return time.Unix(3000, 0).UTC() })
	runner := &turnRunner{store: turnStore, writer: writer, nowFunc: func() time.Time { return time.Unix(3000, 0).UTC() }}

	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatalf("runTurnAndPersist extraction failed: %v", err)
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
	assertVMCheckpoint(t, compStore.session.VMCheckpoint, actorActionExtractFinalize, actorStepDeclarationExtract, hostedgenesis.StatusDeclarationExtractionPending, hostedgenesis.StatusDeclarationReady, "turn-1")
	decodedDeclarations := models.DecodeSoulMintConversationBlob(turnStore.conv.ProducedDeclarations)
	if decodedDeclarations == "" || !strings.Contains(decodedDeclarations, `"selfDescription"`) {
		t.Fatalf("expected produced declarations persisted to conversation, got %s", decodedDeclarations)
	}
	gotHash, _, err := hashDeclarationJSON(decodedDeclarations)
	if err != nil {
		t.Fatalf("hash produced declarations: %v", err)
	}
	if gotHash != compStore.session.DeclarationCheckpoint.DeclarationHash {
		t.Fatalf("checkpoint hash must match persisted declarations: got %s want %s", compStore.session.DeclarationCheckpoint.DeclarationHash, gotHash)
	}
}

func TestRunTurnAndPersist_TwoUserTurnsVMActorOwnsDecisionsAndCheckpoint(t *testing.T) {
	setFiveBodyContractEnv(t)
	const secondTurnID = "turn-2"

	declBody := mustMarshal(map[string]any{
		"id": "chatcmpl_test", "object": "chat.completion", "created": 1, "model": "gpt-test",
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": mustMarshal(validDeclarationDraft())}}},
		"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 7, "total_tokens": 12},
	})
	streamChunk := "data: " + mustMarshal(map[string]any{
		"id": "chatcmpl_test", "object": "chat.completion.chunk", "created": 1, "model": "gpt-test",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": mintprompt.CanonicalFinalAffirmationQuestion}, "finish_reason": nil}},
	}) + "\n\ndata: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if strings.Contains(string(bodyBytes), `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(streamChunk))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(declBody))
	}))
	t.Cleanup(srv.Close)
	withOpenAIBaseURL(t, srv.URL, "sk-test")
	llm.ConfigureProviderHTTPClient(&http.Client{Timeout: 5 * time.Second})
	t.Cleanup(func() { llm.ConfigureProviderHTTPClient(nil) })

	turnStore, compStore, firstTurn := baseTurnInput()
	writer := completion.NewCompletionWriter(compStore, func() time.Time { return time.Unix(3000, 0).UTC() })
	runner := &turnRunner{store: turnStore, writer: writer, nowFunc: func() time.Time { return time.Unix(3000, 0).UTC() }}
	if err := runner.runTurnAndPersist(context.Background(), firstTurn); err != nil {
		t.Fatalf("first VM actor turn failed: %v", err)
	}
	firstCheckpoint := *compStore.session.VMCheckpoint
	assertVMCheckpoint(t, &firstCheckpoint, actorActionAsk, actorStepAssistantTurn, hostedgenesis.StatusInProgress, hostedgenesis.StatusAssistantTurnReady, "turn-1")

	// Host remains the debit/status/version source of truth: the final owner
	// affirmation is accepted as an ordinary paid in_progress turn, then the VM
	// actor decides whether to extract/finalize under the latest turn guards.
	transcript := models.DecodeSoulMintConversationBlob(turnStore.conv.Messages)
	var messages []llm.MintConversationMessage
	if err := json.Unmarshal([]byte(transcript), &messages); err != nil {
		t.Fatalf("decode first turn transcript: %v", err)
	}
	messages = append(messages, llm.MintConversationMessage{Role: "user", Content: "I affirm"})
	encoded, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshal second turn transcript: %v", err)
	}
	turnStore.conv.Messages = models.EncodeSoulMintConversationBlob(string(encoded))
	turnStore.conv.Status = models.SoulMintConversationStatusInProgress
	turnStore.conv.LatestTurnID = secondTurnID
	turnStore.session.Status = string(hostedgenesis.StatusInProgress)
	turnStore.session.LatestTurnID = secondTurnID
	turnStore.session.MessageCount = len(messages)
	turnStore.session.Version = compStore.session.Version + 1
	turnStore.session.TurnLedger = append(turnStore.session.TurnLedger, hostedgenesis.TurnLedgerEntry{TurnID: secondTurnID, MessageCount: len(messages), AcceptedAt: time.Unix(3001, 0).UTC()})
	compStore.session.Status = string(hostedgenesis.StatusInProgress)
	compStore.session.LatestTurnID = secondTurnID
	compStore.session.MessageCount = len(messages)
	compStore.session.Version = turnStore.session.Version
	compStore.session.TurnLedger = append(compStore.session.TurnLedger, hostedgenesis.TurnLedgerEntry{TurnID: secondTurnID, MessageCount: len(messages), AcceptedAt: time.Unix(3001, 0).UTC()})
	compStore.conversation.Status = models.SoulMintConversationStatusInProgress

	secondTurn := completion.CompletionTurn{InstanceSlug: "acme", ConversationID: "conv-1", TurnID: secondTurnID, RequestID: "req-2"}
	if err := runner.runTurnAndPersist(context.Background(), secondTurn); err != nil {
		t.Fatalf("second VM actor turn failed: %v", err)
	}
	assertVMCheckpoint(t, compStore.session.VMCheckpoint, actorActionExtractFinalize, actorStepDeclarationExtract, hostedgenesis.StatusInProgress, hostedgenesis.StatusDeclarationReady, secondTurnID)
	if compStore.session.VMCheckpoint.Hash == firstCheckpoint.Hash || compStore.session.VMCheckpoint.Ref == firstCheckpoint.Ref {
		t.Fatalf("second turn checkpoint must advance safely, first=%#v second=%#v", firstCheckpoint, compStore.session.VMCheckpoint)
	}
	if got := hostedgenesis.NormalizeStatus(compStore.session.Status); got != hostedgenesis.StatusDeclarationReady {
		t.Fatalf("expected declaration_ready after second turn, got %q", got)
	}
}

func TestRunTurnAndPersist_DeclarationExtractionDoesNotSynthesizeDeclaredCapabilities(t *testing.T) {
	setFiveBodyContractEnv(t)
	draft := validDeclarationDraft()
	draft.Capabilities = nil
	declBody := mustMarshal(map[string]any{
		"id": "chatcmpl_test", "object": "chat.completion", "created": 1, "model": "gpt-test",
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": mustMarshal(draft)}}},
		"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 7, "total_tokens": 12},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(declBody))
	}))
	t.Cleanup(srv.Close)
	withOpenAIBaseURL(t, srv.URL, "sk-test")
	llm.ConfigureProviderHTTPClient(&http.Client{Timeout: 5 * time.Second})
	t.Cleanup(func() { llm.ConfigureProviderHTTPClient(nil) })

	turnStore, compStore, turn := baseTurnInput()
	turnStore.reg.Capabilities = []string{"simulacrum.hosted-first-default"}
	transcript := `[{"role":"user","content":"hello"},{"role":"assistant","content":"I am acme."}]`
	turnStore.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	turnStore.conv.Messages = models.EncodeSoulMintConversationBlob(transcript)
	compStore.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	writer := completion.NewCompletionWriter(compStore, func() time.Time { return time.Unix(3000, 0).UTC() })
	runner := &turnRunner{store: turnStore, writer: writer, nowFunc: func() time.Time { return time.Unix(3000, 0).UTC() }}

	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatalf("runTurnAndPersist extraction failed: %v", err)
	}
	if got := hostedgenesis.NormalizeStatus(compStore.session.Status); got != hostedgenesis.StatusDeclarationReady {
		t.Fatalf("expected declaration_ready, got %q", got)
	}
	decodedDeclarations := models.DecodeSoulMintConversationBlob(turnStore.conv.ProducedDeclarations)
	if strings.Contains(decodedDeclarations, "simulacrum.hosted-first-default") || !strings.Contains(decodedDeclarations, `"capabilities":[]`) {
		t.Fatalf("expected empty placeholder-free capabilities, got %s", decodedDeclarations)
	}
}

func TestRunTurnAndPersist_DeclarationExtractionFailureFailsConversationProjection(t *testing.T) {
	setFiveBodyContractEnv(t)
	respBytes, err := json.Marshal(map[string]any{
		"id":      "chatcmpl_test",
		"object":  "chat.completion",
		"created": 1,
		"model":   "gpt-test",
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": `{`}}},
		"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6},
	})
	if err != nil {
		t.Fatalf("marshal openai failure response: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(respBytes)
	}))
	t.Cleanup(srv.Close)
	withOpenAIBaseURL(t, srv.URL, "sk-test")
	llm.ConfigureProviderHTTPClient(&http.Client{Timeout: 5 * time.Second})
	t.Cleanup(func() { llm.ConfigureProviderHTTPClient(nil) })

	turnStore, compStore, turn := baseTurnInput()
	turnStore.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	turnStore.conv.Status = models.SoulMintConversationStatusDeclarationExtractionPending
	turnStore.conv.Messages = models.EncodeSoulMintConversationBlob(`[{"role":"user","content":"describe"},{"role":"assistant","content":"assistant ready"}]`)
	compStore.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	compStore.conversation = &models.SoulAgentMintConversation{
		AgentID:        compStore.session.AgentID,
		ConversationID: compStore.session.ConversationID,
		Status:         models.SoulMintConversationStatusDeclarationExtractionPending,
	}
	writer := completion.NewCompletionWriter(compStore, func() time.Time { return time.Unix(3000, 0).UTC() })
	runner := &turnRunner{store: turnStore, writer: writer, nowFunc: func() time.Time { return time.Unix(3000, 0).UTC() }}

	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatalf("declaration extraction failure should be durably recorded, got %v", err)
	}
	if hostedgenesis.NormalizeStatus(compStore.session.Status) != hostedgenesis.StatusFailed || compStore.session.Failure == nil || compStore.session.Failure.Code != hostedgenesis.FailureCodeDeclarationExtractionFailed {
		t.Fatalf("expected terminal declaration extraction failure on session, got %#v", compStore.session)
	}
	if compStore.conversation == nil || compStore.conversation.Status != models.SoulMintConversationStatusFailed || compStore.conversation.StatusReason != string(hostedgenesis.FailureCodeDeclarationExtractionFailed) {
		t.Fatalf("expected stale declaration_extraction_pending conversation reconciled to sanitized failed status, got %#v", compStore.conversation)
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
	setFiveBodyContractEnv(t)
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
	setFiveBodyContractEnv(t)
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

func TestLoadTurnInputFiveBodyFlagPinsPromptAndExtractionInput(t *testing.T) {
	t.Setenv(hostedgenesis.EnvDeclarationSchemaVersion, "v2")
	turnStore, _, turn := baseTurnInput()
	runner := &turnRunner{store: turnStore, nowFunc: func() time.Time { return time.Unix(3000, 0).UTC() }}
	in, err := runner.loadTurnInput(context.Background(), turn)
	if err != nil {
		t.Fatalf("loadTurnInput: %v", err)
	}
	if !in.contract.IsFiveBody() || !strings.Contains(in.systemPrompt, hostedgenesis.DeclarationSchemaVersionV2) || !strings.Contains(in.systemPrompt, "Phase 1 — identity") {
		t.Fatalf("expected v2 contract prompt, contract=%#v prompt=%q", in.contract, in.systemPrompt)
	}
	if in.contract.SchemaVersion != hostedgenesis.DeclarationSchemaVersionV2 || in.contract.GuidanceVersion != hostedgenesis.GuidanceVersionV2 {
		t.Fatalf("expected v2 extraction versions, got %#v", in.contract)
	}
}

// TestLoadTurnInputFailsClosedWithoutContract proves a fresh hosted-genesis
// turn cannot select the legacy declaration lane: a missing/unknown contract
// env is a load-time error, before any provider or extraction work.
func TestLoadTurnInputFailsClosedWithoutContract(t *testing.T) {
	t.Setenv(hostedgenesis.EnvDeclarationSchemaVersion, "")
	t.Setenv(hostedgenesis.EnvGuidanceVersion, "")
	turnStore, _, turn := baseTurnInput()
	runner := &turnRunner{store: turnStore, nowFunc: func() time.Time { return time.Unix(3000, 0).UTC() }}
	_, err := runner.loadTurnInput(context.Background(), turn)
	if !errors.Is(err, hostedgenesis.ErrDeclarationContractUnconfigured) {
		t.Fatalf("expected unconfigured contract load error, got %v", err)
	}
}

// TestRunTurnAndPersist_UnconfiguredContractRecordsOperatorAction proves the
// full turn path records operator_action_required — never the legacy builder's
// invalid_produced_declarations/boundaries.required lane — when the contract
// env does not explicitly select five-body.
func TestRunTurnAndPersist_UnconfiguredContractRecordsOperatorAction(t *testing.T) {
	t.Setenv(hostedgenesis.EnvDeclarationSchemaVersion, "")
	t.Setenv(hostedgenesis.EnvGuidanceVersion, "")
	turnStore, compStore, turn := baseTurnInput()
	writer := completion.NewCompletionWriter(compStore, nil)
	runner := &turnRunner{store: turnStore, writer: writer}

	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatalf("runTurnAndPersist should record failure and return nil, got %v", err)
	}
	if got := hostedgenesis.NormalizeStatus(compStore.session.Status); got != hostedgenesis.StatusFailed {
		t.Fatalf("expected failed, got %q", got)
	}
	failure := compStore.session.Failure
	if failure == nil || failure.Code != hostedgenesis.FailureCodeOperatorActionRequired || failure.Retryable {
		t.Fatalf("expected terminal operator_action_required failure, got %+v", failure)
	}
	if failure.Recovery.Action != hostedgenesis.RecoveryActionOperatorAction || failure.Recovery.Reason != string(hostedgenesis.FailureCodeOperatorActionRequired) {
		t.Fatalf("expected operator_action recovery with sanitized reason, got %+v", failure.Recovery)
	}
	if failure.Recovery.Reason == string(hostedgenesis.DeclarationCodeBoundaries) {
		t.Fatalf("legacy boundaries.required must be unreachable for fresh hosted genesis, got %+v", failure.Recovery)
	}
}

// TestBuildProducedDeclarationsJSONRejectsNonFiveBodyContract proves the
// workload builder has exactly one lane: a contract that does not name the
// five-body lane fails closed with the unconfigured-contract error instead of
// producing declarations.
func TestBuildProducedDeclarationsJSONRejectsNonFiveBodyContract(t *testing.T) {
	runner := &turnRunner{nowFunc: func() time.Time { return time.Unix(3000, 0).UTC() }}
	for _, contract := range []hostedgenesis.DeclarationContract{
		{},
		{SchemaVersion: "soul-mint-conversation-declaration.v1", GuidanceVersion: "soul-mint-conversation-guidance.v1"},
	} {
		body, err := runner.buildProducedDeclarationsJSON(historicalV1ShapedDeclarationDraft(), "openai:gpt-test", contract)
		if !errors.Is(err, hostedgenesis.ErrDeclarationContractUnconfigured) || body != "" {
			t.Fatalf("expected unconfigured contract error for %#v, got body=%q err=%v", contract, body, err)
		}
	}
}

func TestBuildProducedDeclarationsJSONFiveBodyPinsVersionsAndReview(t *testing.T) {
	runner := &turnRunner{nowFunc: func() time.Time { return time.Unix(3000, 0).UTC() }}
	body, err := runner.buildProducedDeclarationsJSON(validFiveBodyDeclarationDraft(), "openai:gpt-test", hostedgenesis.FiveBodyDeclarationContract())
	if err != nil {
		t.Fatalf("buildProducedDeclarationsJSON: %v", err)
	}
	for _, want := range []string{hostedgenesis.DeclarationSchemaVersionV2, hostedgenesis.GuidanceVersionV2, `"fiveBodies"`, `"adversarialReview"`, `"boundaries"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected v2 declaration body to contain %q, got %s", want, body)
		}
	}
	if strings.Count(body, "closest safe path") < 3 {
		t.Fatalf("expected three mapped concrete refusal boundaries, got %s", body)
	}
}

func TestBuildProducedDeclarationsJSONFiveBodyConformsToPublishedSchemaWithSparseOptionalEvidence(t *testing.T) {
	runner := &turnRunner{nowFunc: func() time.Time { return time.Unix(3000, 0).UTC() }}
	body, err := runner.buildProducedDeclarationsJSON(validFiveBodyDeclarationDraft(), "openai:gpt-test", hostedgenesis.FiveBodyDeclarationContract())
	if err != nil {
		t.Fatalf("buildProducedDeclarationsJSON: %v", err)
	}
	for _, omitted := range []string{`"notes"`, `"lastValidated"`, `"validationRef"`, `"degradesTo"`} {
		if strings.Contains(body, omitted) {
			t.Fatalf("expected sparse v2 declaration to omit optional %s, got %s", omitted, body)
		}
	}
	validatePublishedFiveBodySchema(t, []byte(body))
}

func TestBuildProducedDeclarationsJSONFiveBodyRejectsRefusalFloor(t *testing.T) {
	runner := &turnRunner{nowFunc: func() time.Time { return time.Unix(3000, 0).UTC() }}
	draft := validFiveBodyDeclarationDraft()
	draft.FiveBodies.Soul.Refusals = draft.FiveBodies.Soul.Refusals[:2]
	_, err := runner.buildProducedDeclarationsJSON(draft, "openai:gpt-test", hostedgenesis.FiveBodyDeclarationContract())
	if got := hostedgenesis.DeclarationValidationCodeFromError(err); got != hostedgenesis.DeclarationCodeSoulRefusals {
		t.Fatalf("expected refusal floor error, got err=%v code=%q", err, got)
	}
}

func TestBuildProducedDeclarationsJSONFiveBodyRequiresRunContractEvidence(t *testing.T) {
	runner := &turnRunner{nowFunc: func() time.Time { return time.Unix(3000, 0).UTC() }}
	draft := validFiveBodyDeclarationDraft()
	draft.SchemaVersion = ""
	_, err := runner.buildProducedDeclarationsJSON(draft, "openai:gpt-test", hostedgenesis.FiveBodyDeclarationContract())
	if got := hostedgenesis.DeclarationValidationCodeFromError(err); got != hostedgenesis.DeclarationCodeInvalid {
		t.Fatalf("expected missing v2 schema evidence to fail closed, got err=%v code=%q", err, got)
	}
}

func historicalV1ShapedDeclarationDraft() llm.MintConversationDeclarationsDraft {
	return llm.MintConversationDeclarationsDraft{
		SelfDescription: soul.SelfDescriptionV2{
			Purpose:     "I help operators reason about hosted genesis state.",
			Constraints: "test only",
			Commitments: "be concise",
			Limitations: "unit test",
			AuthoredBy:  "agent",
		},
		Capabilities: []soul.CapabilityV2{{Capability: "reasoning", Scope: "general", ClaimLevel: "self-declared"}},
		Boundaries: []llm.MintConversationBoundaryDraft{
			{Category: "scope_limit", Statement: "I will not reveal credentials.", Rationale: "safety"},
		},
		Transparency: map[string]any{"modelProviderUncertainty": "test", "operationalNotes": "test"},
	}
}

func validFiveBodyDeclarationDraft() llm.MintConversationDeclarationsDraft {
	contract := hostedgenesis.FiveBodyDeclarationContract()
	return llm.MintConversationDeclarationsDraft{
		SchemaVersion:   contract.SchemaVersion,
		GuidanceVersion: contract.GuidanceVersion,
		FiveBodies: hostedgenesis.FiveBodyDeclaration{
			Identity:   hostedgenesis.FiveBodySection{Summary: "I am Acme Steward, a hosted/off-chain agent for operator support."},
			Philosophy: hostedgenesis.FiveBodySection{Summary: "I value narrow authority, auditability, and direct statements of uncertainty."},
			Discipline: hostedgenesis.FiveBodySection{Summary: "I use the named cadence, keep evidence, and pause on unclear authority."},
			Boundaries: hostedgenesis.FiveBodySection{Summary: "I protect tenant isolation, credentials, and human publish gates."},
			Soul: hostedgenesis.FiveBodySoulBody{
				Summary: "I preserve Host safety invariants even when asked to move faster.",
				Refusals: []hostedgenesis.FiveBodyRefusalRule{
					{Bypass: "Skip checksum verification for a managed release", Invariant: "consumer release verification must run before deploy", ClosestSafePath: "run managed-release-certification on the published artifact"},
					{Bypass: "Return a raw Instance API key on reread", Invariant: "Host stores and compares only sha256 key hashes", ClosestSafePath: "issue a new key through controlled rotation and show only the hash id"},
					{Bypass: "Finalize without explicit human authorization", Invariant: "hosted genesis keeps a human publish gate", ClosestSafePath: "return declaration_ready and wait for the finalize call"},
				},
			},
		},
		Capabilities: []soul.CapabilityV2{{Capability: "operator_support", Scope: "Help operators reason about hosted genesis state.", ClaimLevel: "self-declared"}},
		Transparency: map[string]any{"modelProviderUncertainty": "unit test", "operationalNotes": "deterministic", "selfDeclaredNotice": "self-declared"},
	}
}

func validatePublishedFiveBodySchema(t *testing.T, body []byte) {
	t.Helper()
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("parse produced declarations: %v\n%s", err, string(body))
	}
	if err := mustCompilePublishedFiveBodySchema(t).Validate(parsed); err != nil {
		t.Fatalf("produced declarations did not validate against published schema: %v\n%s", err, string(body))
	}
}

func mustCompilePublishedFiveBodySchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	schemaPath := filepath.Join("..", "..", "docs", "contracts", "soul-five-body.schema.v2.json")
	raw, readErr := os.ReadFile(schemaPath)
	if readErr != nil {
		t.Fatalf("read published five-body schema: %v", readErr)
	}
	var doc any
	if unmarshalErr := json.Unmarshal(raw, &doc); unmarshalErr != nil {
		t.Fatalf("parse published five-body schema: %v", unmarshalErr)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if addErr := compiler.AddResource(schemaPath, doc); addErr != nil {
		t.Fatalf("add published five-body schema resource: %v", addErr)
	}
	schema, compileErr := compiler.Compile(schemaPath)
	if compileErr != nil {
		t.Fatalf("compile published five-body schema: %v", compileErr)
	}
	return schema
}

func assertVMCheckpoint(t *testing.T, checkpoint *hostedgenesis.VMCheckpointMetadata, action actorAction, step string, from hostedgenesis.Status, to hostedgenesis.Status, turnID string) {
	t.Helper()
	if checkpoint == nil {
		t.Fatal("expected VM actor checkpoint metadata")
		return
	}
	if err := checkpoint.Validate(); err != nil {
		t.Fatalf("VM checkpoint should validate: %#v err=%v", checkpoint, err)
	}
	if checkpoint.Action != string(action) || checkpoint.Step != step || checkpoint.StatusFrom != string(from) || checkpoint.StatusTo != string(to) || checkpoint.LatestTurnID != turnID {
		t.Fatalf("unexpected VM checkpoint: %#v", checkpoint)
	}
	if !strings.HasPrefix(checkpoint.Ref, "checkpoint://hosted-genesis/vm-actor/") || !strings.HasPrefix(checkpoint.Hash, "sha256:") || checkpoint.Runtime != hostedGenesisMicroVMActorRuntime {
		t.Fatalf("unexpected VM checkpoint ref/hash/runtime: %#v", checkpoint)
	}
}

// validDeclarationDraft is the model-response fixture for extraction-path
// tests. Fresh hosted-genesis extraction is five-body-only, so the fixture is
// the five-body draft shape the llm clients parse back from the provider.
func validDeclarationDraft() llm.MintConversationDeclarationsDraft {
	return validFiveBodyDeclarationDraft()
}
