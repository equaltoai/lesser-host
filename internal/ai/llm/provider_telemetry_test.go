package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
)

const providerTelemetryDeclarationSchemaName = "soul_five_body_declarations"

func TestProviderFailureClassesAreCanonicalAndDoNotCarryErrorText(t *testing.T) {
	privateDetail := "private-provider-response-body"
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "timeout", err: fmt.Errorf("%s: %w", privateDetail, context.DeadlineExceeded), want: string(hostedgenesis.FailureClassProviderTimeout)},
		{name: "provider api", err: fmt.Errorf("%s", privateDetail), want: string(hostedgenesis.FailureClassProviderAPIFailure)},
		{name: "invalid provider output", err: withProviderFailureClass(fmt.Errorf("%s", privateDetail), string(hostedgenesis.FailureClassInvalidProviderOutput)), want: string(hostedgenesis.FailureClassInvalidProviderOutput)},
		{name: "parse validation", err: withProviderFailureClass(fmt.Errorf("%s", privateDetail), string(hostedgenesis.FailureClassParseValidation)), want: string(hostedgenesis.FailureClassParseValidation)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			class := ProviderFailureClass(test.err)
			if class != test.want {
				t.Fatalf("class=%q want=%q", class, test.want)
			}
			raw, err := json.Marshal(ProviderTelemetryEvent{EventType: "provider_call_failed", LastEvent: true, FailureClass: class})
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte(privateDetail)) {
				t.Fatalf("canonical failure telemetry leaked error text: %s", raw)
			}
		})
	}
}

func TestProviderStreamTelemetryOpenAIAndAnthropicIsPerEventAndRedacted(t *testing.T) {
	const (
		privatePrompt = "private prompt must never be telemetry"
		privateOutput = "private output must never be telemetry"
		privateKey    = "provider-key-must-never-be-telemetry"
	)
	oldOpenAIBase := os.Getenv("OPENAI_BASE_URL")
	oldAnthropicBase := os.Getenv("ANTHROPIC_BASE_URL")
	t.Cleanup(func() {
		_ = os.Setenv("OPENAI_BASE_URL", oldOpenAIBase)
		_ = os.Setenv("ANTHROPIC_BASE_URL", oldAnthropicBase)
		openAIHTTPClient = nil
		anthropicHTTPClient = nil
	})
	_ = os.Setenv("OPENAI_BASE_URL", "https://openai.example.test")
	_ = os.Setenv("ANTHROPIC_BASE_URL", "https://anthropic.example.test")

	openAISSE := "data: " + mustProviderTelemetryJSON(map[string]any{
		"id": "chunk_1", "object": "chat.completion.chunk", "created": 1, "model": "gpt-test",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": privateOutput}, "finish_reason": "stop"}},
	}) + "\n\ndata: " + mustProviderTelemetryJSON(map[string]any{
		"id": "chunk_1", "object": "chat.completion.chunk", "created": 1, "model": "gpt-test",
		"choices": []any{}, "usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5},
	}) + "\n\ndata: [DONE]\n\n"
	openAIHTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, r.Body)
		return providerTelemetryHTTPResponse(r, "text/event-stream", []byte(openAISSE)), nil
	})}
	var openAIEvents []ProviderTelemetryEvent
	out, _, err := StreamMintConversationOpenAIWithTelemetry(t.Context(), privateKey, "openai:gpt-test", privatePrompt, []MintConversationMessage{{Role: "user", Content: privatePrompt}}, nil, func(event ProviderTelemetryEvent) {
		openAIEvents = append(openAIEvents, event)
	})
	if err != nil || out != privateOutput {
		t.Fatalf("OpenAI stream: out=%q err=%v", out, err)
	}
	assertProviderTelemetryEventsRedacted(t, openAIEvents, privatePrompt, privateOutput, privateKey)
	if !hasProviderTelemetryEvent(openAIEvents, "chat.completion.chunk") {
		t.Fatalf("expected every OpenAI SDK chunk to be observed: %#v", openAIEvents)
	}

	anthropicSSE := strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-test\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"" + privateOutput + "\"}}\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":2,\"output_tokens\":3}}\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n",
	}, "\n") + "\n"
	anthropicHTTPClient = &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, r.Body)
		return providerTelemetryHTTPResponse(r, "text/event-stream", []byte(anthropicSSE)), nil
	})}
	var anthropicEvents []ProviderTelemetryEvent
	out, _, err = StreamMintConversationAnthropicWithTelemetry(t.Context(), privateKey, "anthropic:claude-test", privatePrompt, []MintConversationMessage{{Role: "user", Content: privatePrompt}}, nil, func(event ProviderTelemetryEvent) {
		anthropicEvents = append(anthropicEvents, event)
	})
	if err != nil || out != privateOutput {
		t.Fatalf("Anthropic stream: out=%q err=%v", out, err)
	}
	assertProviderTelemetryEventsRedacted(t, anthropicEvents, privatePrompt, privateOutput, privateKey)
	for _, eventType := range []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"} {
		if !hasProviderTelemetryEvent(anthropicEvents, eventType) {
			t.Fatalf("expected Anthropic SDK event %q: %#v", eventType, anthropicEvents)
		}
	}
	assertDeterministicProviderTelemetry(t, openAIEvents, privateOutput)
	assertDeterministicProviderTelemetry(t, anthropicEvents, privateOutput)
}

func assertProviderRequestPayloadMetadata(t *testing.T, event ProviderTelemetryEvent, bytes int, hash string) {
	t.Helper()
	if event.PayloadBytes != bytes || event.PayloadSHA256 != hash {
		t.Fatalf("unexpected content-free request payload metadata: got %#v want bytes=%d hash=%s", event, bytes, hash)
	}
}

func assertDeterministicProviderTelemetry(t *testing.T, events []ProviderTelemetryEvent, output string) {
	t.Helper()
	wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(output)))
	firstSDKEvents := 0
	for i, event := range events {
		if event.Sequence != int64(i+1) {
			t.Fatalf("telemetry sequence drift at %d: %#v", i, events)
		}
		if event.ElapsedMS < 0 || event.IdleMS < 0 {
			t.Fatalf("telemetry timing must be non-negative: %#v", event)
		}
		if event.FirstSDKEvent {
			firstSDKEvents++
		}
	}
	if firstSDKEvents != 1 {
		t.Fatalf("expected exactly one first SDK event marker, got %d: %#v", firstSDKEvents, events)
	}
	last := events[len(events)-1]
	if last.OutputBytes != len(output) || last.OutputRunes != len([]rune(output)) || last.OutputSHA256 != wantHash {
		t.Fatalf("unexpected content-free output metadata: got %#v want bytes=%d runes=%d hash=%s", last, len(output), len([]rune(output)), wantHash)
	}
}

func assertProviderTelemetryEventsRedacted(t *testing.T, events []ProviderTelemetryEvent, forbidden ...string) {
	t.Helper()
	if len(events) < 2 || !events[0].FirstEvent || !events[len(events)-1].LastEvent {
		t.Fatalf("expected explicit first/last events: %#v", events)
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range forbidden {
		if value != "" && bytes.Contains(raw, []byte(value)) {
			t.Fatalf("provider telemetry leaked forbidden value %q: %s", value, raw)
		}
	}
	if events[len(events)-1].OutputSHA256 == "" || events[len(events)-1].OutputBytes == 0 {
		t.Fatalf("expected content-free output length/hash metadata: %#v", events[len(events)-1])
	}
}

func hasProviderTelemetryEvent(events []ProviderTelemetryEvent, eventType string) bool {
	for _, event := range events {
		if event.EventType == eventType {
			return true
		}
	}
	return false
}

func providerTelemetryHTTPResponse(r *http.Request, contentType string, body []byte) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}
}

func mustProviderTelemetryJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
