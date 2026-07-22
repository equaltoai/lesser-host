package llm

import (
	"errors"
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	openaioption "github.com/openai/openai-go/option"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
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
const DefaultProviderHTTPTimeout = hostedgenesis.DefaultProviderHTTPTimeout

var ErrInvalidProviderHTTPTimeout = errors.New("provider HTTP timeout must be positive")

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
func newProviderHTTPClient(timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		return nil, ErrInvalidProviderHTTPTimeout
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: http.DefaultTransport,
	}, nil
}

// ConfigureDefaultProviderHTTPClient installs the default explicit-timeout
// provider HTTP client. It is safe to call once at process startup; later
// calls replace the prior client.
func ConfigureDefaultProviderHTTPClient() {
	client, err := newProviderHTTPClient(DefaultProviderHTTPTimeout)
	if err != nil {
		panic(err)
	}
	ConfigureProviderHTTPClient(client)
}

// ConfigureProviderHTTPTimeout installs a provider client with an explicit
// attempt timeout after validating it. Workload startup uses this with the
// validated execution envelope so transport and whole-call deadlines cannot
// drift independently.
func ConfigureProviderHTTPTimeout(timeout time.Duration) error {
	client, err := newProviderHTTPClient(timeout)
	if err != nil {
		return err
	}
	ConfigureProviderHTTPClient(client)
	return nil
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
