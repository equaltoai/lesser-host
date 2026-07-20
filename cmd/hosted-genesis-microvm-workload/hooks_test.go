package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
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

// TestHookServer_CatchAllAcknowledgesUnmatched proves the catch-all on "/"
// logs + returns HTTP 200 on an unmatched path, the diagnostic behavior added
// to reveal whether the AWS service calls a path the workload does not register
// (e.g. a different prefix or a short /<hook> path). The specific hook paths
// still take precedence over the catch-all. This replaces the prior 404
// behavior: during diagnosis an unmatched path is acknowledged (200) so the
// service does not retry it into a build timeout, and the catch-all logs the
// path + body preview so the mismatch is visible in the build log.
func TestHookServer_CatchAllAcknowledgesUnmatched(t *testing.T) {
	srv := newTestHookServer(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/bogus", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from catch-all on unmatched path, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHookServer_CatchAllDoesNotShadowHookPaths proves the specific hook paths
// still take precedence over the "/" catch-all: a request to a registered hook
// path reaches the hook handler (returns a LifecycleResult JSON body), not the
// catch-all (which returns {"status":"unmatched"}). The /ready hook with an
// empty body returns a failed LifecycleResult (the AppTheory envelope is
// incomplete on an empty event), which is the existing fail-closed behavior —
// the point here is that the catch-all did not intercept it.
func TestHookServer_CatchAllDoesNotShadowHookPaths(t *testing.T) {
	srv := newTestHookServer(t, nil)
	req := httptest.NewRequest(http.MethodPost, hookPathPrefix+"/ready", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /ready hook, got %d: %s", rec.Code, rec.Body.String())
	}
	var result runtimemicrovm.LifecycleResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("expected LifecycleResult body (not catch-all), decode error: %v", err)
	}
	if result.Hook != runtimemicrovm.HookReady {
		t.Fatalf("expected hook=ready from /ready hook (not catch-all), got %q", result.Hook)
	}
}

// TestRequestLoggingMiddleware_LogsRequest proves the request-logging middleware
// logs every request before its handler runs and records the status after. It
// uses a test handler so the middleware is exercised independently of the hook
// handlers.
func TestRequestLoggingMiddleware_LogsRequest(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusTeapot, map[string]string{"status": "test"})
	})
	wrapped := requestLoggingMiddleware(handler)
	req := httptest.NewRequest(http.MethodGet, "/test-path", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected 418 from test handler, got %d", rec.Code)
	}
}

// TestRecoverMiddleware_RecoversPanic proves the panic-recovery middleware
// recovers a panicking handler, writes 500, and does not propagate the panic.
func TestRecoverMiddleware_RecoversPanic(t *testing.T) {
	handler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})
	wrapped := recoverMiddleware(handler)
	req := httptest.NewRequest(http.MethodGet, "/panic-path", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from recovered panic, got %d", rec.Code)
	}
}

// TestReadBodyPreview_Capped proves readBodyPreview reads at most n bytes and
// restores the body for downstream readers.
func TestReadBodyPreview_Capped(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello world body"))
	preview := readBodyPreview(req, 5)
	if preview != "hello" {
		t.Fatalf("expected first 5 bytes, got %q", preview)
	}
	rest, _ := io.ReadAll(req.Body)
	if string(rest) != "hello" {
		t.Fatalf("expected restored body to be the previewed bytes, got %q", string(rest))
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

// TestHookServer_ReadyValidateHandlersUnconditional proves the readyHook and
// validateHook handlers return nil unconditionally (no namespace binding),
// matching the AWS service's build-hook contract. The AWS Lambda Microvms
// service invokes /ready and /validate during image creation with NO request
// body (see the OpenAPI spec in the AWS docs at
// https://docs.aws.amazon.com/lambda/latest/dg/microvms-launching.html — neither
// /ready nor /validate defines a requestBody), so there is no namespace to bind;
// the workload is ready/valid once it is serving the hooks. Requiring a
// hosted-genesis namespace here made /ready return a failed lifecycle result,
// which the service retried until the ~120s readyTimeoutInSeconds elapsed and
// the image build failed with CREATE_FAILED "did not stabilize".
func TestHookServer_ReadyValidateHandlersUnconditional(t *testing.T) {
	if err := readyHook(context.Background(), runtimemicrovm.LifecycleEvent{}); err != nil {
		t.Fatalf("readyHook returned error on empty event: %v", err)
	}
	if err := readyHook(context.Background(), runtimemicrovm.LifecycleEvent{Namespace: "other"}); err != nil {
		t.Fatalf("readyHook returned error on non-hosted-genesis namespace: %v", err)
	}
	if err := validateHook(context.Background(), runtimemicrovm.LifecycleEvent{}); err != nil {
		t.Fatalf("validateHook returned error on empty event: %v", err)
	}
	if err := validateHook(context.Background(), runtimemicrovm.LifecycleEvent{Namespace: "other"}); err != nil {
		t.Fatalf("validateHook returned error on non-hosted-genesis namespace: %v", err)
	}
}

// TestDecodeLifecycleEvent_ToleratesEmptyBody proves decodeLifecycleEvent
// returns an empty event with no error when the request body is empty (io.EOF),
// matching the AWS service's build-hook contract (/ready and /validate send NO
// request body). A non-empty but malformed body still surfaces a decode error.
func TestDecodeLifecycleEvent_ToleratesEmptyBody(t *testing.T) {
	// Empty body (nil) → empty event, no error.
	req := httptest.NewRequest(http.MethodPost, hookPathPrefix+"/ready", nil)
	event, err := decodeLifecycleEvent(req)
	if err != nil {
		t.Fatalf("expected no error on empty body, got %v", err)
	}
	if event.Namespace != "" || event.RequestID != "" || event.SessionID != "" {
		t.Fatalf("expected empty event on empty body, got %+v", event)
	}

	// Empty bytes.Reader body → empty event, no error (io.EOF path).
	req = httptest.NewRequest(http.MethodPost, hookPathPrefix+"/ready", bytes.NewReader(nil))
	event, err = decodeLifecycleEvent(req)
	if err != nil {
		t.Fatalf("expected no error on empty bytes body, got %v", err)
	}
	if event.Namespace != "" {
		t.Fatalf("expected empty event on empty bytes body, got %+v", event)
	}

	// Malformed non-empty body → decode error (fail-closed preserved).
	req = httptest.NewRequest(http.MethodPost, hookPathPrefix+"/ready", bytes.NewReader([]byte("{not json")))
	if _, err = decodeLifecycleEvent(req); err == nil {
		t.Fatal("expected error on malformed non-empty body, got nil")
	}
}

// TestHookServer_ReadyHookEmptyBody200 proves the /aws/lambda-microvms/runtime/v1/ready
// route returns HTTP 200 on an empty-body POST, the AWS service's image-build
// readiness contract (POST /ready with no body → 200 once ready, 503 retried).
func TestHookServer_ReadyHookEmptyBody200(t *testing.T) {
	srv := newTestHookServer(t, nil)
	req := httptest.NewRequest(http.MethodPost, hookPathPrefix+"/ready", nil)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on empty-body /ready, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHookServer_RunHookExecutesTurn proves the /run hook drives the adapter to
// running and executes the assistant turn for an in_progress session,
// persisting assistant_turn_ready to session truth.
func TestHookServer_RunHookExecutesTurn(t *testing.T) {
	setFiveBodyContractEnv(t)
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
	if got := hostedgenesis.NormalizeStatus(compStore.session.Status); got != hostedgenesis.StatusAssistantTurnReady {
		t.Fatalf("expected session assistant_turn_ready after run hook, got %q", got)
	}
}

// TestHookServer_TurnEndpointExecutesTurn proves Host's application-level
// MicroVM turn endpoint reaches the same run handler as the lifecycle /run path
// without using the AWS-reserved lifecycle URL as the externally proxied
// application endpoint.
func TestHookServer_TurnEndpointExecutesTurn(t *testing.T) {
	setFiveBodyContractEnv(t)
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
	writer := completion.NewCompletionWriter(compStore, func() time.Time { return time.Unix(3100, 0).UTC() })
	runner := &turnRunner{store: turnStore, writer: writer, nowFunc: func() time.Time { return time.Unix(3100, 0).UTC() }}
	srv := newTestHookServer(t, runner)

	event := validEvent(runtimemicrovm.HookRun, runtimemicrovm.StateRunning)
	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, hostedgenesis.MicroVMTurnEndpointPath, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var result runtimemicrovm.LifecycleResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.State != runtimemicrovm.StateRunning || result.Hook != runtimemicrovm.HookRun {
		t.Fatalf("expected run/running result, got hook=%q state=%q err=%v", result.Hook, result.State, result.Error)
	}
	waitForHostedGenesisStatus(t, compStore, hostedgenesis.StatusAssistantTurnReady)
}

// TestHookServer_TurnEndpointStorePreflightFailureIsControllerVisible proves a
// turn is not acknowledged to the controller until the per-turn store can read
// Host truth. The AI worker treats a failed LifecycleResult as an invoke error
// and persists microvm_unavailable using its own store, so a workload that cannot
// initialize/access DynamoDB cannot leave the conversation stuck in_progress.
func TestHookServer_TurnEndpointStorePreflightFailureIsControllerVisible(t *testing.T) {
	failingStore, compStore, _ := baseTurnInput()
	failingStore.session = nil
	runner := &turnRunner{storeFactory: func(context.Context) (turnStore, *completion.CompletionWriter, error) {
		return failingStore, completion.NewCompletionWriter(compStore, nil), nil
	}}
	srv := newTestHookServer(t, runner)

	event := validEvent(runtimemicrovm.HookRun, runtimemicrovm.StateRunning)
	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, hostedgenesis.MicroVMTurnEndpointPath, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected controller envelope status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var result runtimemicrovm.LifecycleResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.State != runtimemicrovm.StateFailed || result.Error == nil || result.Error.Code != hookErrorCode {
		t.Fatalf("expected controller-visible store preflight failure, got state=%q error=%#v", result.State, result.Error)
	}
}

// TestHookServer_ProcessMemoryCanaryEndpointStoresNonceHashOnlyAndVerifiesSurvival
// proves the lab-only process-memory canary initializes a random nonce inside
// process memory, returns only hash/correlation metadata, and verifies survival
// by hash after a simulated resume invoke. The plaintext nonce never appears in
// the response body, request body, or Host checkpoint marker.
func TestHookServer_ProcessMemoryCanaryEndpointStoresNonceHashOnlyAndVerifiesSurvival(t *testing.T) {
	t.Setenv("STAGE", "lab")
	processMemoryCanaries.reset()
	t.Cleanup(processMemoryCanaries.reset)

	srv := newTestHookServer(t, nil)
	event := validEvent(runtimemicrovm.HookRun, runtimemicrovm.StateRunning)
	event.Metadata["canary"] = processMemoryCanaryMetadataValue
	event.Metadata["checkpoint_marker"] = "checkpoint:host-safe-only"

	init := invokeProcessMemoryCanary(t, srv, event)
	assertCanaryInitializedWithSafeMetadata(t, init, "checkpoint:host-safe-only")

	event.RequestID = "req-resume"
	event.State = runtimemicrovm.StateReady
	event.Metadata["expected_nonce_hash"] = init.NonceHash

	verified := invokeProcessMemoryCanary(t, srv, event)
	assertCanarySurvivedWithSameHash(t, init, verified)
}

func invokeProcessMemoryCanary(t *testing.T, srv *hookServer, event runtimemicrovm.LifecycleEvent) processMemoryCanaryResult {
	t.Helper()
	body, _ := json.Marshal(event)
	rec := httptest.NewRecorder()
	srv.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, hostedgenesis.MicroVMProcessMemoryCanaryEndpointPath, bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected canary 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var result processMemoryCanaryResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode canary result: %v", err)
	}
	return result
}

func assertCanaryInitializedWithSafeMetadata(t *testing.T, init processMemoryCanaryResult, wantCheckpointMarker string) {
	t.Helper()
	if !init.Initialized || init.MemoryPreserved != nil {
		t.Fatalf("expected initialized result without survival verdict, got %#v", init)
	}
	if !strings.HasPrefix(init.NonceHash, "sha256:") || init.CorrelationID == "" {
		t.Fatalf("expected hash + correlation only, got %#v", init)
	}
	if init.CheckpointMarker != wantCheckpointMarker {
		t.Fatalf("expected checkpoint marker to round-trip, got %q", init.CheckpointMarker)
	}
	assertCanaryResultHasSafeVocabulary(t, init)
}

func assertCanaryResultHasSafeVocabulary(t *testing.T, result processMemoryCanaryResult) {
	t.Helper()
	payload, err := marshalCanaryResult(result)
	if err != nil {
		t.Fatalf("marshal canary result: %v", err)
	}
	lower := strings.ToLower(string(payload))
	for _, forbidden := range []string{
		"plaintext_nonce",
		"nonce_plaintext",
		"nonce_value",
		"bearer_token",
		"authorization",
		"aws_access_key_id",
		"aws_secret_access_key",
		"aws_session_token",
		"instance-api-key",
		"provider_secret",
		"wallet_signature",
		"raw transcript",
		"endpoint_token",
		"x-aws-proxy-auth",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("canary response leaked forbidden vocabulary %q: %s", forbidden, lower)
		}
	}
}

func assertCanarySurvivedWithSameHash(t *testing.T, init, verified processMemoryCanaryResult) {
	t.Helper()
	assertCanaryResultHasSafeVocabulary(t, verified)
	if verified.MemoryPreserved == nil || !*verified.MemoryPreserved {
		t.Fatalf("expected memory_preserved=true from same process, got %#v", verified)
	}
	if verified.NonceHash != init.NonceHash || verified.CorrelationID != init.CorrelationID {
		t.Fatalf("expected same hash/correlation after verify, init=%#v verified=%#v", init, verified)
	}
}

// TestProcessMemoryCanaryReportsFalseWhenProcessStateIsMissing proves a resumed
// workload with no in-process entry returns memory_preserved=false using only the
// caller-supplied expected hash. It does not recreate or persist plaintext state.
func TestProcessMemoryCanaryReportsFalseWhenProcessStateIsMissing(t *testing.T) {
	t.Setenv("STAGE", "lab")
	processMemoryCanaries.reset()
	t.Cleanup(processMemoryCanaries.reset)

	event := validEvent(runtimemicrovm.HookRun, runtimemicrovm.StateReady)
	event.Metadata["canary"] = processMemoryCanaryMetadataValue
	event.Metadata["expected_nonce_hash"] = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	result, status, err := processMemoryCanaries.initializeOrObserve(event, event.Metadata["expected_nonce_hash"])
	if err != nil {
		t.Fatalf("expected missing state to be a measured false result, got err=%v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected 200 status, got %d", status)
	}
	if result.MemoryPreserved == nil || *result.MemoryPreserved {
		t.Fatalf("expected memory_preserved=false, got %#v", result)
	}
	if result.Initialized {
		t.Fatalf("expected no new initialization when verifying missing process state, got %#v", result)
	}
}

func TestProcessMemoryCanaryFailsClosedWithoutCanaryMetadata(t *testing.T) {
	t.Setenv("STAGE", "lab")
	processMemoryCanaries.reset()
	t.Cleanup(processMemoryCanaries.reset)

	event := validEvent(runtimemicrovm.HookRun, runtimemicrovm.StateRunning)
	_, status, err := processMemoryCanaries.initializeOrObserve(event, "")
	if err == nil {
		t.Fatal("expected missing canary metadata to fail closed")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing canary metadata, got %d", status)
	}
}

func TestProcessMemoryCanaryFailsClosedOutsideLabStage(t *testing.T) {
	t.Setenv("STAGE", "live")
	processMemoryCanaries.reset()
	t.Cleanup(processMemoryCanaries.reset)

	event := validEvent(runtimemicrovm.HookRun, runtimemicrovm.StateRunning)
	event.Metadata["canary"] = processMemoryCanaryMetadataValue
	_, status, err := processMemoryCanaries.initializeOrObserve(event, "")
	if err == nil {
		t.Fatal("expected non-lab process-memory canary to fail closed")
	}
	if status != http.StatusForbidden {
		t.Fatalf("expected 403 outside lab stage, got %d", status)
	}
}

func TestProcessMemoryCanaryRejectsUnsafeCheckpointMarker(t *testing.T) {
	t.Setenv("STAGE", "lab")
	processMemoryCanaries.reset()
	t.Cleanup(processMemoryCanaries.reset)

	event := validEvent(runtimemicrovm.HookRun, runtimemicrovm.StateRunning)
	event.Metadata["canary"] = processMemoryCanaryMetadataValue
	event.Metadata["checkpoint_marker"] = "checkpoint:bearer-token"
	_, status, err := processMemoryCanaries.initializeOrObserve(event, "")
	if err == nil {
		t.Fatal("expected unsafe checkpoint marker to fail closed")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for unsafe checkpoint marker, got %d", status)
	}
}

func waitForHostedGenesisStatus(t *testing.T, compStore *fakeCompletionStore, want hostedgenesis.Status) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := hostedgenesis.NormalizeStatus(compStore.session.Status); got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := hostedgenesis.NormalizeStatus(compStore.session.Status)
	t.Fatalf("expected session %q after async turn, got %q", want, got)
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

// TestLoggingListener_AcceptLogsRemote proves the logging listener wraps Accept
// so every inbound raw TCP connection to the hook port is logged at the LISTENER
// layer (below HTTP), with the remote address, before the connection is handed
// to srv.Serve. This is the diagnostic added for deploy #9: the build log showed
// "listening" + "registered routes" but ZERO HTTP-layer request logs, so a
// connection that fails HTTP parsing (e.g. a TLS mismatch — the build service
// calling HTTPS on a plain-HTTP server) would be invisible to the request-logging
// middleware. The logging listener sees every Accept regardless of whether the
// connection ever produces a parseable HTTP request.
func TestLoggingListener_AcceptLogsRemote(t *testing.T) {
	// Use an in-process pipe listener so the test does not depend on a real port
	// and so Accept can be driven deterministically. A pipe conn exposes a
	// non-nil RemoteAddr, so the logging branch's conn.RemoteAddr().String() is
	// exercised.
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })
	pipe := newPipeListener(server)

	wrapped := &loggingListener{Listener: pipe}

	// Accept on the wrapped listener returns the injected conn (not nil) and
	// runs the logging branch. The wrapper must not block or alter the conn.
	conn, err := wrapped.Accept()
	if err != nil {
		t.Fatalf("expected Accept to return a conn, got error: %v", err)
	}
	if conn == nil {
		t.Fatal("expected Accept to return a non-nil conn")
	}
	if conn.RemoteAddr() == nil {
		t.Fatal("expected the wrapped conn to expose a RemoteAddr")
	}
	_ = conn.Close()
}

// TestLoggingListener_AcceptErrorPropagates proves the logging listener logs and
// propagates an Accept error (e.g. after the underlying listener is closed)
// rather than swallowing it. srv.Serve relies on a non-nil Accept error to
// return; hiding it would let Serve spin instead of exiting cleanly on shutdown.
func TestLoggingListener_AcceptErrorPropagates(t *testing.T) {
	pipe := newPipeListener(nil)
	wrapped := &loggingListener{Listener: pipe}
	_ = pipe.Close() // close the underlying listener so Accept returns ErrClosed

	if _, err := wrapped.Accept(); err == nil {
		t.Fatal("expected Accept to return an error after the listener was closed, got nil")
	}
}

// TestServeWithListener_KeepaliveDoesNotBlockShutdown proves the keepalive
// goroutine started in serveWithListener stops when srv.Serve returns, so a
// graceful Shutdown (which closes the listener and makes Serve return
// http.ErrServerClosed) does not leak the goroutine or block process exit. The
// hook handlers, request-logging middleware, catch-all, and panic recovery are
// unchanged; this exercises only the listener/keepalive/Serve-return wiring.
func TestServeWithListener_KeepaliveDoesNotBlockShutdown(t *testing.T) {
	srv := newTestHookServer(t, nil)

	// Bind a real listener on an ephemeral port so Serve accepts connections,
	// then shut the server down to drive Serve to return ErrServerClosed. The
	// keepalive goroutine must stop and serveWithListener must return within a
	// bounded time (no leak / no block). serveWithListener binds its own
	// listener from the addr, so shut down via the SAME *http.Server it serves.
	addr := "127.0.0.1:0"
	httpSrv := &http.Server{
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	done := make(chan error, 1)
	go func() {
		done <- srv.serveWithListener(httpSrv, addr)
	}()

	// Give Serve a moment to bind + start, then shut down via the same server so
	// Serve returns ErrServerClosed. The keepalive goroutine must stop on Serve
	// return.
	time.Sleep(30 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	// serveWithListener must return (Serve returned ErrServerClosed) within a
	// bounded time. The keepalive goroutine is stopped inside serveWithListener
	// before it returns, so a hang here would indicate a leak.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil from serveWithListener on graceful shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveWithListener did not return within 5s — keepalive goroutine likely blocked shutdown")
	}
}

// newPipeListener builds an in-process net.Listener whose Accept returns the
// connection fed in at construction. It lets the logging-listener test exercise
// Accept without binding a real port.
func newPipeListener(first net.Conn) *pipeListener {
	pl := &pipeListener{ch: make(chan net.Conn, 4)}
	if first != nil {
		pl.ch <- first
	}
	return pl
}

type pipeListener struct {
	ch     chan net.Conn
	closed bool
}

func (pl *pipeListener) Accept() (net.Conn, error) {
	c, ok := <-pl.ch
	if !ok {
		return nil, net.ErrClosed
	}
	return c, nil
}

func (pl *pipeListener) Close() error {
	pl.closed = true
	close(pl.ch)
	return nil
}

func (pl *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// Ensure unused imports in this test file are referenced.
var _ = context.Background
var _ = os.Getenv
var _ = models.EncodeSoulMintConversationBlob
