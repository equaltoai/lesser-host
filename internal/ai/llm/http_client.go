package llm

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
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
// The value is sized for the longest legitimate typed-section conversation
// phase. It bounds a single
// HTTP round trip; streaming callers still receive incremental deltas up to the
// deadline.
const DefaultProviderHTTPTimeout = hostedgenesis.DefaultProviderHTTPTimeout

var ErrInvalidProviderHTTPTimeout = errors.New("provider HTTP timeout must be positive")

// DefaultProviderSDKRetryBudget is pinned explicitly on both provider SDKs.
// It matches their documented default while making durable attempt evidence
// unambiguous and preventing an SDK update from silently changing the budget.
const DefaultProviderSDKRetryBudget = 2

type providerAttemptContextKey struct{}

type providerAttemptContext struct {
	retryBudget int
	ordinal     atomic.Int64
	observe     func(ProviderTelemetryEvent)
}

func withProviderAttemptTelemetry(ctx context.Context, retryBudget int, observe func(ProviderTelemetryEvent)) context.Context {
	if ctx == nil || observe == nil {
		return ctx
	}
	return context.WithValue(ctx, providerAttemptContextKey{}, &providerAttemptContext{retryBudget: retryBudget, observe: observe})
}

type providerAttemptRoundTripper struct{ base http.RoundTripper }

func (t providerAttemptRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	started := time.Now()
	response, err := base.RoundTrip(request)
	tracker, _ := request.Context().Value(providerAttemptContextKey{}).(*providerAttemptContext)
	if tracker != nil && tracker.observe != nil {
		event := ProviderTelemetryEvent{
			EventType: "sdk_http_attempt", SDKAttemptOrdinal: tracker.ordinal.Add(1),
			SDKRetryBudget: tracker.retryBudget, DurationMS: time.Since(started).Milliseconds(),
		}
		if response != nil {
			event.HTTPStatus = response.StatusCode
			event.ProviderRequestID = boundedProviderRequestID(firstProviderRequestID(response.Header))
		}
		if err != nil {
			event.FailureClass = ProviderFailureClass(err)
		}
		tracker.observe(event)
	}
	return response, err
}

func firstProviderRequestID(header http.Header) string {
	for _, name := range []string{"x-request-id", "request-id", "anthropic-request-id"} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func boundedProviderRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 160 {
		return ""
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("-_.:", char) {
			continue
		}
		return ""
	}
	return value
}

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
		Transport: providerAttemptRoundTripper{base: http.DefaultTransport},
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
