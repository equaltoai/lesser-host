package provisionworker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
