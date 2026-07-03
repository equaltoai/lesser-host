package llm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestConfigureProviderHTTPClient_InstallsExplicitTimeout proves the configured
// HTTP client carries an explicit Timeout rather than relying on the SDK default
// (http.DefaultClient, which has no Timeout). This is the G8 invariant: provider
// calls must fail at the configured deadline, not at the Lambda envelope.
func TestConfigureProviderHTTPClient_InstallsExplicitTimeout(t *testing.T) {
	t.Cleanup(func() { ConfigureProviderHTTPClient(nil) })

	ConfigureProviderHTTPClient(&http.Client{Timeout: 7 * time.Second})
	if got := ProviderHTTPClientTimeout(); got != 7*time.Second {
		t.Fatalf("expected configured timeout 7s, got %v", got)
	}

	ConfigureDefaultProviderHTTPClient()
	if got := ProviderHTTPClientTimeout(); got != DefaultProviderHTTPTimeout {
		t.Fatalf("expected default timeout %v, got %v", DefaultProviderHTTPTimeout, got)
	}

	ConfigureProviderHTTPClient(nil)
	if got := ProviderHTTPClientTimeout(); got != 0 {
		t.Fatalf("expected zero timeout after reset, got %v", got)
	}
}

// TestStreamMintConversationOpenAI_TimesOutAtConfiguredDeadline proves a hanging
// provider fails at the configured client deadline, not the surrounding context
// envelope. A 200ms client timeout must abort a 30s-hanging server well before a
// 5s test context would. This is the explicit-timeout proof required by H1.1.
func TestStreamMintConversationOpenAI_TimesOutAtConfiguredDeadline(t *testing.T) {
	assertProviderTimesOutAtConfiguredDeadline(t, "OPENAI_BASE_URL", func(ctx context.Context) error {
		_, _, err := StreamMintConversationOpenAI(ctx, "sk-test", "openai:gpt-test", "system", []MintConversationMessage{
			{Role: "user", Content: "hello"},
		}, func(string) {})
		return err
	})
}

// TestStreamMintConversationAnthropic_TimesOutAtConfiguredDeadline mirrors the
// OpenAI proof for the Anthropic adapter, confirming both provider seams honor
// the single configured HTTP client.
func TestStreamMintConversationAnthropic_TimesOutAtConfiguredDeadline(t *testing.T) {
	assertProviderTimesOutAtConfiguredDeadline(t, "ANTHROPIC_BASE_URL", func(ctx context.Context) error {
		_, _, err := StreamMintConversationAnthropic(ctx, "sk-test", "anthropic:claude-test", "system", []MintConversationMessage{
			{Role: "user", Content: "hello"},
		}, func(string) {})
		return err
	})
}

// assertProviderTimesOutAtConfiguredDeadline is the shared explicit-timeout
// proof: a hanging provider must fail at the configured 200ms client deadline,
// not the surrounding 5s context envelope. The provider call is injected so both
// the OpenAI and Anthropic seams are proven against the single configured client.
func assertProviderTimesOutAtConfiguredDeadline(t *testing.T, baseURLEnv string, call func(ctx context.Context) error) {
	t.Helper()
	// Hanging server: accepts the connection but never writes a response body,
	// so the only thing that can terminate the call is the client timeout. The
	// handler selects on the server context so httptest.Server.Close() does not
	// block for 30s on shutdown.
	hanging := newHangingServer(t)
	t.Cleanup(hanging.Close)

	oldBase := os.Getenv(baseURLEnv)
	t.Cleanup(func() { _ = os.Setenv(baseURLEnv, oldBase) })
	_ = os.Setenv(baseURLEnv, hanging.URL)

	// Explicit short client timeout — the invariant under test.
	ConfigureProviderHTTPClient(&http.Client{Timeout: 200 * time.Millisecond})
	t.Cleanup(func() { ConfigureProviderHTTPClient(nil) })

	// Generous context envelope (5s). If the call waited for the envelope
	// instead of the client deadline, this test would fail the bound below.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err := call(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error from hanging provider, got nil")
	}
	// A net/http client timeout surfaces as a url.Error wrapping "context
	// deadline exceeded" (Client.Timeout uses context cancellation). Accept
	// either the deadline string or a timeout-class error.
	if !isTimeoutError(err) {
		t.Fatalf("expected deadline/timeout-class error, got %v", err)
	}
	// Must abort near the 200ms client deadline, not the 5s envelope. The bound
	// allows margin for SDK retry backoff on slower machines.
	if elapsed > 3500*time.Millisecond {
		t.Fatalf("client did not time out at configured deadline: elapsed %v (expected well under the 5s envelope)", elapsed)
	}
}

// TestConfigureProviderHTTPClient_DoesNotChangeStreamingParsing proves the seam
// only swaps the transport client: a fast, well-formed streaming response still
// parses to the same assistant content as without the configured client.
func TestConfigureProviderHTTPClient_DoesNotChangeStreamingParsing(t *testing.T) {
	// Minimal OpenAI streaming response: one chunk with the full delta then a
	// [DONE] sentinel. The accumulator must surface "hello from openai".
	chunk := `data: {"id":"chatcmpl_test","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":"hello from openai"},"finish_reason":null}]}

data: [DONE]

`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		_, _ = w.Write([]byte(chunk))
	}))
	t.Cleanup(srv.Close)

	oldBase := os.Getenv("OPENAI_BASE_URL")
	t.Cleanup(func() { _ = os.Setenv("OPENAI_BASE_URL", oldBase) })
	_ = os.Setenv("OPENAI_BASE_URL", srv.URL)

	ConfigureProviderHTTPClient(&http.Client{Timeout: 5 * time.Second})
	t.Cleanup(func() { ConfigureProviderHTTPClient(nil) })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	got, _, err := StreamMintConversationOpenAI(ctx, "sk-test", "openai:gpt-test", "system", []MintConversationMessage{
		{Role: "user", Content: "hi"},
	}, func(string) {})
	if err != nil {
		t.Fatalf("stream call failed: %v", err)
	}
	if got != "hello from openai" {
		t.Fatalf("expected parsed assistant content %q, got %q", "hello from openai", got)
	}
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "client timed out")
}

// newHangingServer returns an httptest.Server that accepts requests but never
// writes a response, so only the client timeout can terminate the call. The
// handler selects on its own context so server shutdown is prompt.
func newHangingServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	return srv
}

var _ = bytes.NewReader // retained for parity with sibling adapter tests if extended later
