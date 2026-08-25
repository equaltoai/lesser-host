package provisionworker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestNormalizeVerifyHost(t *testing.T) {
	t.Parallel()

	t.Run("bare host", func(t *testing.T) {
		t.Parallel()
		host, err := normalizeVerifyHost("example.com")
		require.NoError(t, err)
		require.Equal(t, "example.com", host)
	})

	t.Run("url with trailing slash", func(t *testing.T) {
		t.Parallel()
		host, err := normalizeVerifyHost("https://example.com/")
		require.NoError(t, err)
		require.Equal(t, "example.com", host)
	})

	t.Run("url with path", func(t *testing.T) {
		t.Parallel()
		host, err := normalizeVerifyHost("https://127.0.0.1:1234/abc")
		require.NoError(t, err)
		require.Equal(t, "127.0.0.1:1234", host)
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		_, err := normalizeVerifyHost("   ")
		require.Error(t, err)
	})
}

func TestFetchInstanceConfigV2_ParsesFields(t *testing.T) {
	t.Parallel()

	handler := http.NewServeMux()
	handler.HandleFunc("/api/v2/instance", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"configuration": map[string]any{
				"translation": map[string]any{"enabled": true},
				"trust": map[string]any{
					"enabled":  true,
					"base_url": "https://lab.lesser.host",
				},
				"tips": map[string]any{
					"enabled":          true,
					"chain_id":         8453,
					"contract_address": "0xabc",
				},
			},
		})
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	cfg, err := fetchInstanceConfigV2(ctx, ts.Client(), ts.URL)
	require.NoError(t, err)
	require.True(t, cfg.Configuration.Translation.Enabled)
	require.True(t, cfg.Configuration.Trust.Enabled)
	require.Equal(t, "https://lab.lesser.host", cfg.Configuration.Trust.BaseURL)
	require.True(t, cfg.Configuration.Tips.Enabled)
	require.EqualValues(t, 8453, cfg.Configuration.Tips.ChainID)
	require.Equal(t, "0xabc", cfg.Configuration.Tips.ContractAddress)
}

func TestRequireInstanceEndpoint2xx(t *testing.T) {
	t.Parallel()

	handler := http.NewServeMux()
	handler.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/fail", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	require.NoError(t, requireInstanceEndpoint2xx(ctx, ts.Client(), ts.URL, "/ok"))
	require.ErrorContains(t, requireInstanceEndpoint2xx(ctx, ts.Client(), ts.URL, "/fail"), "HTTP 500")
}

func TestVerifyAIEndpoint_AcceptsNotFound(t *testing.T) {
	t.Parallel()

	handler := http.NewServeMux()
	handler.HandleFunc("/api/v1/ai/jobs/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/ai/jobs/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	ok, msg := verifyAIEndpoint(ctx, ts.Client(), ts.URL, "lhk_test", "job1")
	require.True(t, ok)
	require.Empty(t, msg)
}

func TestVerifyAIEndpoint_RejectsUnauthorized(t *testing.T) {
	t.Parallel()

	handler := http.NewServeMux()
	handler.HandleFunc("/api/v1/ai/jobs/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	ok, msg := verifyAIEndpoint(ctx, ts.Client(), ts.URL, "lhk_test", "job1")
	require.False(t, ok)
	require.Contains(t, msg, "unauthorized")
}

func TestVerifyTrustAuthEndpoint_RequiresBearerKey(t *testing.T) {
	t.Parallel()

	handler := http.NewServeMux()
	handler.HandleFunc("/api/v1/trust/verify", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer lhk_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","instance_slug":"slug"}`))
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	ok, msg := verifyTrustAuthEndpoint(ctx, ts.Client(), ts.URL, "lhk_test", "slug")
	require.True(t, ok)
	require.Empty(t, msg)

	ok, msg = verifyTrustAuthEndpoint(ctx, ts.Client(), ts.URL, "wrong", "slug")
	require.False(t, ok)
	require.Contains(t, msg, "unauthorized")

	ok, msg = verifyTrustAuthEndpoint(ctx, ts.Client(), ts.URL, "lhk_test", "other")
	require.False(t, ok)
	require.Contains(t, msg, "instance_slug")
}

func TestManagedUpdateVerificationFailureMessage(t *testing.T) {
	t.Parallel()

	trustOK := false
	aiOK := true
	job := &models.UpdateJob{
		VerifyTrustOK:  &trustOK,
		VerifyTrustErr: "expected instance_slug \"slug\", got \"other\"",
		VerifyAIOK:     &aiOK,
	}
	got := managedUpdateVerificationFailureMessage(job)
	require.Contains(t, got, "managed update verification failed")
	require.Contains(t, got, "trust: expected instance_slug")
	require.NotContains(t, got, "ai:")
	require.Equal(t, "managed update verification failed", managedUpdateVerificationFailureMessage(nil))
	translationOK := false
	got = managedUpdateVerificationFailureMessage(&models.UpdateJob{VerifyTranslationOK: &translationOK})
	require.Contains(t, got, "translation: failed")
}

// capturingRoundTripper records requests without dialing any network and
// returns a canned response, so tests can inspect the request construction.
type capturingRoundTripper struct {
	reqs []*http.Request
	resp *http.Response
}

func (c *capturingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c.reqs = append(c.reqs, req)
	return c.resp, nil
}

// assertRequestTimeout verifies a verify-lane request carries the per-call
// deadline on its request context.
func assertRequestTimeout(t *testing.T, req *http.Request) {
	t.Helper()
	deadline, ok := req.Context().Deadline()
	require.True(t, ok, "verify-lane request must carry a per-call deadline")
	remaining := time.Until(deadline)
	require.Greater(t, remaining, time.Duration(0))
	require.LessOrEqual(t, remaining, updateVerifyHTTPTimeout)
}

func TestVerifyLaneCalls_CarryPerCallTimeout(t *testing.T) {
	t.Parallel()

	t.Run("fetchInstanceConfigV2", func(t *testing.T) {
		t.Parallel()
		capture := &capturingRoundTripper{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{}`)),
			},
		}
		_, err := fetchInstanceConfigV2(context.Background(), &http.Client{Transport: capture}, "https://instance.example")
		require.NoError(t, err)
		require.Len(t, capture.reqs, 1)
		assertRequestTimeout(t, capture.reqs[0])
	})

	t.Run("requireInstanceEndpoint2xx", func(t *testing.T) {
		t.Parallel()
		capture := &capturingRoundTripper{
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
			},
		}
		require.NoError(t, requireInstanceEndpoint2xx(context.Background(), &http.Client{Transport: capture}, "https://instance.example", "/ok"))
		require.Len(t, capture.reqs, 1)
		assertRequestTimeout(t, capture.reqs[0])
	})
}

func TestVerifyLaneCall_SlowEndpointFailsLaneScoped(t *testing.T) {
	t.Parallel()

	// The handler hangs far past the per-call timeout. Without the per-call
	// bound this would block for the whole delay (an invocation-wide hang);
	// with it, each lane call fails on its own in ~updateVerifyHTTPTimeout
	// with a clear lane-scoped error. ts.Client() carries no client-level
	// timeout, so the per-call context timeout is the only bound in play.
	const delay = 30 * time.Second
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
		}
		if r.Context().Err() == nil {
			w.WriteHeader(http.StatusOK)
		}
	})
	ts := httptest.NewTLSServer(handler)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	client := ts.Client()

	t.Run("fetchInstanceConfigV2", func(t *testing.T) {
		t.Parallel()
		start := time.Now()
		_, err := fetchInstanceConfigV2(ctx, client, ts.URL)
		elapsed := time.Since(start)
		require.ErrorContains(t, err, "context deadline exceeded")
		require.GreaterOrEqual(t, elapsed, updateVerifyHTTPTimeout)
		require.Less(t, elapsed, delay, "slow endpoint must fail its own call, not hang the invocation")
	})

	t.Run("requireInstanceEndpoint2xx", func(t *testing.T) {
		t.Parallel()
		start := time.Now()
		err := requireInstanceEndpoint2xx(ctx, client, ts.URL, "/ok")
		elapsed := time.Since(start)
		require.ErrorContains(t, err, "context deadline exceeded")
		require.GreaterOrEqual(t, elapsed, updateVerifyHTTPTimeout)
		require.Less(t, elapsed, delay, "slow endpoint must fail its own call, not hang the invocation")
	})
}
