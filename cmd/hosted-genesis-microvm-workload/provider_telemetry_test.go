package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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

type providerBlockingTransport struct{}

func (providerBlockingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, fmt.Errorf("private-provider-key private response: %w", r.Context().Err())
}

func TestDefaultProviderHeartbeatIntervalIsTenSeconds(t *testing.T) {
	if defaultProviderHeartbeatInterval != 10*time.Second {
		t.Fatalf("provider heartbeat interval = %s, want 10s", defaultProviderHeartbeatInterval)
	}
}

func TestRuntimeTelemetryEmitsRequestHeartbeatResponseToolAndTerminalEventsContentFree(t *testing.T) {
	const privateContent = "private transcript and provider body"
	var logs bytes.Buffer
	priorLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(priorLogger) })

	turn := completion.CompletionTurn{InstanceSlug: "demo", ConversationID: "conv-safe", TurnID: "turn-safe", RequestID: "req-safe"}
	in := turnInput{
		modelSet: "anthropic:claude-test",
		session:  &models.HostedGenesisSession{RegistrationID: "reg-safe", TraceIDs: &hostedgenesis.TraceIDs{CorrelationID: "corr-safe"}},
		messages: []llm.MintConversationMessage{{Role: "user", Content: privateContent}},
	}
	lifecycle := newTurnLifecycleTelemetry(turn)
	lifecycle.bind(in)
	lifecycle.emit("turn", "turn_accepted", "extract_declarations", "")
	provider := newProviderCallTelemetry(turn, in, "declaration_extraction")
	provider.observe(llm.ProviderTelemetryEvent{Provider: "anthropic", Model: "claude-test", Phase: "declaration_extraction", EventType: "request_start", Sequence: 1, FirstEvent: true, ToolName: "soul_five_body_declarations"})
	provider.heartbeat()
	provider.observe(llm.ProviderTelemetryEvent{Provider: "anthropic", Model: "claude-test", Phase: "declaration_extraction", EventType: "response_received", Sequence: 2, StopReason: "tool_use", ToolCalls: 1, ToolName: "soul_five_body_declarations"})
	provider.observe(llm.ProviderTelemetryEvent{Provider: "anthropic", Model: "claude-test", Phase: "declaration_extraction", EventType: "tool_input_received", Sequence: 3, StopReason: "tool_use", ToolCalls: 1, ToolName: "soul_five_body_declarations"})
	provider.observe(llm.ProviderTelemetryEvent{Provider: "anthropic", Model: "claude-test", Phase: "declaration_extraction", EventType: "provider_call_completed", Sequence: 4, LastEvent: true, StopReason: "tool_use", ToolCalls: 1, ToolName: "soul_five_body_declarations"})

	text := logs.String()
	for _, want := range []string{
		"hosted-genesis-microvm-workload: turn lifecycle",
		"hosted-genesis-microvm-workload: provider_sdk_event",
		"hosted-genesis-microvm-workload: provider_call_heartbeat",
		`"event_type":"request_start"`, `"event_type":"heartbeat"`,
		`"event_type":"response_received"`, `"stop_reason":"tool_use"`,
		`"event_type":"tool_input_received"`, `"tool_name":"soul_five_body_declarations"`,
		`"event_type":"provider_call_completed"`, `"last_event":true`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("runtime telemetry missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, privateContent) {
		t.Fatalf("runtime telemetry leaked private content: %s", text)
	}
}

func TestHungProviderHeartbeatsThenPersistsTypedFailureBeforeMicroVMEnvelope(t *testing.T) {
	setFiveBodyContractEnv(t)
	oldBase := os.Getenv("OPENAI_BASE_URL")
	oldKey := os.Getenv("OPENAI_API_KEY")
	t.Cleanup(func() {
		_ = os.Setenv("OPENAI_BASE_URL", oldBase)
		_ = os.Setenv("OPENAI_API_KEY", oldKey)
		llm.ConfigureProviderHTTPClient(nil)
	})
	_ = os.Setenv("OPENAI_BASE_URL", "https://hung-provider.example.test")
	_ = os.Setenv("OPENAI_API_KEY", "private-provider-key")
	llm.ConfigureProviderHTTPClient(&http.Client{Transport: providerBlockingTransport{}, Timeout: 5 * time.Second})

	var logs bytes.Buffer
	priorLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(priorLogger) })

	turnStore, compStore, turn := baseTurnInput()
	turnStore.session.TraceIDs = &hostedgenesis.TraceIDs{CorrelationID: "corr-timeout-test"}
	writer := completion.NewCompletionWriter(compStore, func() time.Time { return time.Unix(3000, 0).UTC() })
	runner := &turnRunner{
		store: turnStore, writer: writer,
		providerCallTimeout:       45 * time.Millisecond,
		providerHeartbeatInterval: 5 * time.Millisecond,
	}
	started := time.Now()
	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatalf("hung provider failure should be durably recorded: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("whole-call timeout did not bound SDK retry lifecycle: %s", elapsed)
	}
	assertHungProviderDurableFailure(t, compStore)
	assertHungProviderLogs(t, logs.String())
}

func assertHungProviderDurableFailure(t *testing.T, compStore *fakeCompletionStore) {
	t.Helper()
	if compStore.session.Failure == nil || compStore.session.Failure.Code != hostedgenesis.FailureCodeAssistantTurnFailed || !compStore.session.Failure.Retryable || compStore.session.Failure.Recovery.Action != hostedgenesis.RecoveryActionRetrySameStep {
		t.Fatalf("expected typed recoverable assistant failure, got %#v", compStore.session)
	}
	if got := compStore.session.Failure.Message; got != hostedgenesis.FailureMessage(hostedgenesis.FailureCodeAssistantTurnFailed) {
		t.Fatalf("durable provider failure must use the canonical typed message, got %q", got)
	}
	if got := compStore.session.Failure.Class; got != hostedgenesis.FailureClassProviderTimeout {
		t.Fatalf("durable provider timeout class mismatch: %q", got)
	}
	if got := providerFailureMessage("assistant_stream", fmt.Errorf("private-provider-key private response: %w", context.DeadlineExceeded)); got != "assistant_stream timeout" {
		t.Fatalf("provider failure detail must be content-free, got %q", got)
	}
	persistedFailure, err := json.Marshal(compStore.session.Failure)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(persistedFailure, []byte("private-provider-key")) || bytes.Contains(persistedFailure, []byte("private response")) {
		t.Fatalf("durable provider failure leaked SDK error material: %s", persistedFailure)
	}
	if compStore.conversation == nil || compStore.conversation.Status != "failed" {
		t.Fatalf("expected atomically failed compatibility projection, got %#v", compStore.conversation)
	}
}

func assertHungProviderLogs(t *testing.T, text string) {
	t.Helper()
	for _, want := range []string{`"event_type":"turn_accepted"`, `"event_type":"store_preflight_completed"`, `"event_type":"actor_decision_completed"`, `"event_type":"heartbeat"`, `"last_sdk_event_at":`, `"event_type":"provider_call_failed"`, `"failure_class":"provider_timeout"`, `"event_type":"failure_persist_completed"`, `"correlation_id":"corr-timeout-test"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing safe hung-provider telemetry %s: %s", want, text)
		}
	}
	for _, forbidden := range []string{"private-provider-key", "private prompt", "hello"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("hung-provider telemetry leaked %q: %s", forbidden, text)
		}
	}
}

func TestDeclarationParseFailureTelemetryIsCorrelatedRedactedAndDurable(t *testing.T) {
	setFiveBodyContractEnv(t)
	response, err := json.Marshal(map[string]any{
		"id": "chatcmpl_private", "object": "chat.completion", "created": 1, "model": "gpt-test",
		"choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": `{`}}},
		"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(response)
	}))
	t.Cleanup(srv.Close)
	oldBase := os.Getenv("OPENAI_BASE_URL")
	oldKey := os.Getenv("OPENAI_API_KEY")
	t.Cleanup(func() {
		_ = os.Setenv("OPENAI_BASE_URL", oldBase)
		_ = os.Setenv("OPENAI_API_KEY", oldKey)
		llm.ConfigureProviderHTTPClient(nil)
	})
	_ = os.Setenv("OPENAI_BASE_URL", srv.URL)
	_ = os.Setenv("OPENAI_API_KEY", "private-declaration-key")
	llm.ConfigureProviderHTTPClient(&http.Client{Timeout: time.Second})

	var logs bytes.Buffer
	priorLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(priorLogger) })

	turnStore, compStore, turn := baseTurnInput()
	turnStore.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	turnStore.session.TraceIDs = &hostedgenesis.TraceIDs{CorrelationID: "corr-declaration-test"}
	turnStore.conv.Status = "declaration_extraction_pending"
	turnStore.conv.Messages = encodeTestConversation(t, []llm.MintConversationMessage{{Role: "user", Content: "private declaration transcript"}, {Role: "assistant", Content: "ready"}})
	compStore.session.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	compStore.conversation.Status = "declaration_extraction_pending"
	runner := &turnRunner{store: turnStore, writer: completion.NewCompletionWriter(compStore, func() time.Time { return time.Unix(3000, 0).UTC() }), providerCallTimeout: time.Second}
	if err := runner.runTurnAndPersist(context.Background(), turn); err != nil {
		t.Fatalf("parse failure should be durably recorded: %v", err)
	}
	if compStore.session.Failure == nil || compStore.session.Failure.Code != hostedgenesis.FailureCodeDeclarationExtractionFailed {
		t.Fatalf("expected typed declaration extraction failure, got %#v", compStore.session)
	}
	if compStore.session.Failure.Class != hostedgenesis.FailureClassParseValidation {
		t.Fatalf("expected durable parse/validation class, got %#v", compStore.session.Failure)
	}
	text := logs.String()
	for _, want := range []string{`"phase":"declaration_extraction"`, `"failure_class":"parse_validation_failure"`, `"schema_name":"soul_five_body_declarations"`, `"correlation_id":"corr-declaration-test"`, `"event_type":"failure_persist_completed"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing declaration failure telemetry %s: %s", want, text)
		}
	}
	for _, forbidden := range []string{"private declaration transcript", "private-declaration-key"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("declaration failure telemetry leaked %q: %s", forbidden, text)
		}
	}
}

func encodeTestConversation(t *testing.T, messages []llm.MintConversationMessage) string {
	t.Helper()
	raw, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	return models.EncodeSoulMintConversationBlob(string(raw))
}
