package llm

import (
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	openaioption "github.com/openai/openai-go/option"
)

// DefaultProviderHTTPTimeout is the explicit per-provider HTTP timeout applied
// to every LLM provider call. It is intentionally shorter than the Lambda
// envelope that hosts a call so a hanging provider fails at the client boundary
// with a typed net/http deadline-exceeded error instead of being killed by the
// runtime and surfaced as an opaque platform timeout (G8).
//
// The value is sized for the longest legitimate single mint-conversation turn
// (streaming assistant response + declaration extraction). It bounds a single
// HTTP round trip; streaming callers still receive incremental deltas up to the
// deadline.
const DefaultProviderHTTPTimeout = 120 * time.Second

// ConfigureProviderHTTPClient installs an explicit-timeout HTTP client on both
// the OpenAI and Anthropic provider adapters. Pass a nil client to reset to the
// SDK defaults (no explicit timeout). The client's Transport is left to the
// caller; only the Timeout field is load-bearing here.
//
// This is the single seam the in-VM hosted-genesis workload and tests use to
// guarantee provider calls carry an explicit HTTP deadline rather than relying
// on the surrounding Lambda/context envelope. Streaming and parsing behavior is
// unchanged: the same request bodies, stream decoders, and usage accounting run
// underneath; only the transport-level deadline is bounded.
func ConfigureProviderHTTPClient(c *http.Client) {
	if c == nil {
		openAIHTTPClient = nil
		anthropicHTTPClient = nil
		return
	}
	openAIHTTPClient = c
	anthropicHTTPClient = c
}

// newDefaultProviderHTTPClient returns an *http.Client with the explicit
// DefaultProviderHTTPTimeout applied and a default transport. It is the client
// the hosted-genesis MicroVM workload installs at startup.
func newDefaultProviderHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   DefaultProviderHTTPTimeout,
		Transport: http.DefaultTransport,
	}
}

// ConfigureDefaultProviderHTTPClient installs the default explicit-timeout
// provider HTTP client. It is safe to call once at process startup; later
// calls replace the prior client.
func ConfigureDefaultProviderHTTPClient() {
	ConfigureProviderHTTPClient(newDefaultProviderHTTPClient())
}

// ProviderHTTPClientTimeout reports the configured timeout for the OpenAI
// provider client, or zero if no explicit client is installed. It exists for
// tests that prove the explicit-timeout invariant without depending on a live
// provider call.
func ProviderHTTPClientTimeout() time.Duration {
	if openAIHTTPClient == nil {
		return 0
	}
	if c, ok := openAIHTTPClient.(*http.Client); ok {
		return c.Timeout
	}
	return 0
}

// ensure the per-package option types are referenced so this file documents the
// dual-SDK seam even if future refactors move the vars.
var (
	_ openaioption.HTTPClient = (*http.Client)(nil)
	_ option.HTTPClient       = (*http.Client)(nil)
)
