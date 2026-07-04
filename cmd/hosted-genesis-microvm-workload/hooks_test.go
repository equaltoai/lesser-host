package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	runtimemicrovm "github.com/theory-cloud/apptheory/runtime/microvm"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/completion"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func validEvent(hook runtimemicrovm.LifecycleHook, state runtimemicrovm.LifecycleState) runtimemicrovm.LifecycleEvent {
	return runtimemicrovm.LifecycleEvent{
		RequestID: "req-1",
		TenantID:  "slug:acme",
		Namespace: hostedgenesis.MicroVMNamespace,
		SessionID: "conv-1",
		Hook:      hook,
		State:     state,
		Metadata:  map[string]string{"conversation_id": "conv-1", "turn_id": "turn-1"},
	}
}

// TestHookServer_NonRunHooksDriveAdapter proves each non-run hook path drives
// the AppTheory LifecycleAdapter with the real M16 contract and returns the
// adapter's LifecycleResult (success state).
func TestHookServer_NonRunHooksDriveAdapter(t *testing.T) {
	srv := newTestHookServer(t, nil)
	cases := []struct {
		path      string
		hook      runtimemicrovm.LifecycleHook
		state     runtimemicrovm.LifecycleState
		wantState runtimemicrovm.LifecycleState
	}{
		{hookPathPrefix + "/validate", runtimemicrovm.HookValidate, runtimemicrovm.StateRequested, runtimemicrovm.StateValidated},
		{hookPathPrefix + "/ready", runtimemicrovm.HookReady, runtimemicrovm.StateRunning, runtimemicrovm.StateReady},
		{hookPathPrefix + "/suspend", runtimemicrovm.HookSuspend, runtimemicrovm.StateReady, runtimemicrovm.StateSuspended},
		{hookPathPrefix + "/resume", runtimemicrovm.HookResume, runtimemicrovm.StateSuspended, runtimemicrovm.StateReady},
		{hookPathPrefix + "/terminate", runtimemicrovm.HookTerminate, runtimemicrovm.StateReady, runtimemicrovm.StateTerminated},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			event := validEvent(c.hook, c.state)
			body, _ := json.Marshal(event)
			req := httptest.NewRequest(http.MethodPost, c.path, bytes.NewReader(body))
			rec := httptest.NewRecorder()
			srv.routes().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			var result runtimemicrovm.LifecycleResult
			if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
				t.Fatalf("decode result: %v", err)
			}
			if result.State != c.wantState {
				t.Fatalf("expected state %q, got %q (err=%v)", c.wantState, result.State, result.Error)
			}
			if result.Hook != c.hook {
				t.Fatalf("expected hook %q, got %q", c.hook, result.Hook)
			}
		})
	}
}

// TestHookServer_UnknownHook404 proves an unknown hook path is rejected.
func TestHookServer_UnknownHook404(t *testing.T) {
	srv := newTestHookServer(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/bogus", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// TestHookServer_Healthz proves the liveness endpoint responds ok.
func TestHookServer_Healthz(t *testing.T) {
	srv := newTestHookServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// TestHookServer_BadNamespaceRejected proves the validate hook fails closed for
// a non-hosted-genesis namespace, surfacing a failed lifecycle result.
func TestHookServer_BadNamespaceRejected(t *testing.T) {
	srv := newTestHookServer(t, nil)
	event := validEvent(runtimemicrovm.HookValidate, runtimemicrovm.StateRequested)
	event.Namespace = "other-namespace"
	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, hookPathPrefix+"/validate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	var result runtimemicrovm.LifecycleResult
	_ = json.NewDecoder(rec.Body).Decode(&result)
	if result.State != runtimemicrovm.StateFailed {
		t.Fatalf("expected failed state for bad namespace, got %q", result.State)
	}
	if result.Error == nil {
		t.Fatalf("expected a safe error for bad namespace")
	}
}

// TestHookServer_RunHookExecutesTurn proves the /run hook drives the adapter to
// running and executes the assistant turn + declaration extraction, persisting
// declaration_ready to session truth.
func TestHookServer_RunHookExecutesTurn(t *testing.T) {
	assistantChunk := "data: " + mustMarshal(map[string]any{
		"id": "chatcmpl_test", "object": "chat.completion.chunk", "created": 1, "model": "gpt-test",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "I am acme."}, "finish_reason": nil}},
	}) + "\n\ndata: [DONE]\n\n"
	declBody := mustMarshal(map[string]any{
		"id": "chatcmpl_test", "object": "chat.completion", "created": 1, "model": "gpt-test",
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": mustMarshal(validDeclarationDraft())}}},
		"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 7, "total_tokens": 12},
	})
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	t.Cleanup(llmSrv.Close)
	withOpenAIBaseURL(t, llmSrv.URL, "sk-test")
	llm.ConfigureProviderHTTPClient(&http.Client{Timeout: 5 * time.Second})
	t.Cleanup(func() { llm.ConfigureProviderHTTPClient(nil) })

	turnStore, compStore, _ := baseTurnInput()
	writer := completion.NewCompletionWriter(compStore, func() time.Time { return time.Unix(3000, 0).UTC() })
	runner := &turnRunner{store: turnStore, writer: writer, nowFunc: func() time.Time { return time.Unix(3000, 0).UTC() }}
	srv := newTestHookServer(t, runner)

	event := validEvent(runtimemicrovm.HookRun, runtimemicrovm.StateValidated)
	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, hookPathPrefix+"/run", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var result runtimemicrovm.LifecycleResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.State != runtimemicrovm.StateRunning {
		t.Fatalf("expected running state from run hook, got %q (err=%v)", result.State, result.Error)
	}
	if got := hostedgenesis.NormalizeStatus(compStore.session.Status); got != hostedgenesis.StatusDeclarationReady {
		t.Fatalf("expected session declaration_ready after run hook, got %q", got)
	}
}

// TestHookServer_RunHookMissingBindingFails proves the /run hook fails closed
// (failed lifecycle result) when the lifecycle event metadata is missing the
// hosted-genesis ids, rather than silently succeeding.
func TestHookServer_RunHookMissingBindingFails(t *testing.T) {
	srv := newTestHookServer(t, &turnRunner{store: &fakeTurnStore{}, writer: completion.NewCompletionWriter(&fakeCompletionStore{}, nil)})
	event := validEvent(runtimemicrovm.HookRun, runtimemicrovm.StateValidated)
	event.Metadata = nil // missing ids
	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, hookPathPrefix+"/run", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	var result runtimemicrovm.LifecycleResult
	_ = json.NewDecoder(rec.Body).Decode(&result)
	if result.State != runtimemicrovm.StateFailed {
		t.Fatalf("expected failed state for missing binding, got %q", result.State)
	}
}

// TestNewHookServer_UsesRealLifecycleAdapter proves newHookServer constructs
// AppTheory's M16 real lifecycle adapter rather than a local transition engine.
// A valid adapter call through the canonical real contract succeeds for each
// non-run route already covered above; this direct call confirms the adapter is
// available and wired.
func TestNewHookServer_UsesRealLifecycleAdapter(t *testing.T) {
	srv, err := newHookServer(nil, hostedgenesis.MicroVMNamespace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if srv == nil || srv.adapter == nil {
		t.Fatal("expected AppTheory lifecycle adapter constructed")
	}
	result, err := srv.adapter.Handle(context.Background(), validEvent(runtimemicrovm.HookReady, runtimemicrovm.StateRunning))
	if err != nil {
		t.Fatalf("adapter handle ready: %v", err)
	}
	if result.State != runtimemicrovm.StateReady {
		t.Fatalf("expected adapter ready result, got %#v", result)
	}
}

func newTestHookServer(t *testing.T, runner *turnRunner) *hookServer {
	t.Helper()
	srv, err := newHookServer(runner, hostedgenesis.MicroVMNamespace)
	if err != nil {
		t.Fatalf("newHookServer: %v", err)
	}
	return srv
}

// Ensure unused imports in this test file are referenced.
var _ = context.Background
var _ = os.Getenv
var _ = models.EncodeSoulMintConversationBlob
