package llm

import (
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	openaioption "github.com/openai/openai-go/option"
)

// DefaultProviderHTTPTimeout is the explicit per-request-attempt timeout applied
// to both LLM provider SDK HTTP clients. Provider SDK retries can span multiple
// attempts, so callers that need a whole-call bound must also use a context
// deadline; the Hosted Genesis MicroVM workload does so and retains enough of
// its outer runtime envelope to persist a guarded typed failure (G8).
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
// This is the transport seam the in-VM hosted-genesis workload and tests use;
// it complements, but does not replace, the workload's whole-call context
// deadline. Streaming and parsing behavior is otherwise unchanged.
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
