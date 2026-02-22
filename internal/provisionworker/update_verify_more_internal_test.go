package provisionworker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestVerifyUpdateTranslation_Cases(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	ok, msg := verifyUpdateTranslation(ctx, nil, "example.com", instanceV2Response{}, errors.New("boom"), true)
	require.False(t, ok)
	require.Contains(t, msg, "boom")

	var cfg instanceV2Response
	cfg.Configuration.Translation.Enabled = false
	ok, msg = verifyUpdateTranslation(ctx, nil, "example.com", cfg, nil, true)
	require.False(t, ok)
	require.Contains(t, msg, "expected")

	ok, msg = verifyUpdateTranslation(ctx, nil, "example.com", cfg, nil, false)
	require.True(t, ok)
	require.Empty(t, msg)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/instance/translation_languages", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	ts := httptest.NewTLSServer(mux)
	t.Cleanup(ts.Close)

	cfg.Configuration.Translation.Enabled = true
	ok, msg = verifyUpdateTranslation(ctx, ts.Client(), ts.URL, cfg, nil, true)
	require.True(t, ok)
	require.Empty(t, msg)
}

func TestVerifyUpdateTrust_Cases(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	ok, msg := verifyUpdateTrust(ctx, nil, "example.com", instanceV2Response{}, errors.New("boom"), "https://x")
	require.False(t, ok)
	require.Contains(t, msg, "boom")

	var cfg instanceV2Response
	cfg.Configuration.Trust.Enabled = false
	ok, msg = verifyUpdateTrust(ctx, nil, "example.com", cfg, nil, "https://x")
	require.False(t, ok)
	require.Contains(t, msg, "disabled")

	cfg.Configuration.Trust.Enabled = true
	cfg.Configuration.Trust.BaseURL = "https://a"
	ok, msg = verifyUpdateTrust(ctx, nil, "example.com", cfg, nil, "https://b")
	require.False(t, ok)
	require.Contains(t, msg, "base_url")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/trust/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	ts := httptest.NewTLSServer(mux)
	t.Cleanup(ts.Close)

	cfg.Configuration.Trust.BaseURL = "https://trust.example"
	ok, msg = verifyUpdateTrust(ctx, ts.Client(), ts.URL, cfg, nil, "https://trust.example/")
	require.True(t, ok)
	require.Empty(t, msg)
}

func TestVerifyUpdateTips_Cases(t *testing.T) {
	t.Parallel()

	ok, msg := verifyUpdateTips(instanceV2Response{}, errors.New("boom"), true, 1, "0xabc")
	require.False(t, ok)
	require.Contains(t, msg, "boom")

	var cfg instanceV2Response
	cfg.Configuration.Tips.Enabled = false
	ok, msg = verifyUpdateTips(cfg, nil, true, 1, "0xabc")
	require.False(t, ok)
	require.Contains(t, msg, "expected")

	ok, msg = verifyUpdateTips(cfg, nil, false, 1, "0xabc")
	require.True(t, ok)
	require.Empty(t, msg)

	cfg.Configuration.Tips.Enabled = true
	cfg.Configuration.Tips.ChainID = 2
	cfg.Configuration.Tips.ContractAddress = "0xabc"
	ok, msg = verifyUpdateTips(cfg, nil, true, 1, "0xabc")
	require.False(t, ok)
	require.Contains(t, msg, "chain_id")

	cfg.Configuration.Tips.ChainID = 1
	cfg.Configuration.Tips.ContractAddress = "0xdef"
	ok, msg = verifyUpdateTips(cfg, nil, true, 1, "0xabc")
	require.False(t, ok)
	require.Contains(t, msg, "contract_address")

	cfg.Configuration.Tips.ContractAddress = "0xAbC"
	ok, msg = verifyUpdateTips(cfg, nil, true, 1, "0xabc")
	require.True(t, ok)
	require.Empty(t, msg)
}

func TestVerifyAIEndpoint_MoreBranches(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	ok, msg := verifyAIEndpoint(ctx, &http.Client{}, "", "k", "job1")
	require.False(t, ok)
	require.Contains(t, msg, "base url")

	ok, msg = verifyAIEndpoint(ctx, &http.Client{}, "https://x", "", "job1")
	require.False(t, ok)
	require.Contains(t, msg, "instance key")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/ai/jobs/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewTLSServer(mux)
	t.Cleanup(ts.Close)

	ok, msg = verifyAIEndpoint(ctx, ts.Client(), ts.URL, "k", "job1")
	require.True(t, ok)
	require.Empty(t, msg)

	muxFail := http.NewServeMux()
	muxFail.HandleFunc("/api/v1/ai/jobs/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	tsFail := httptest.NewTLSServer(muxFail)
	t.Cleanup(tsFail.Close)

	ok, msg = verifyAIEndpoint(ctx, tsFail.Client(), tsFail.URL, "k", "job1")
	require.False(t, ok)
	require.Contains(t, msg, "unexpected status")
}

func TestResolveExpectedTrustBaseURL(t *testing.T) {
	t.Parallel()

	if got := resolveExpectedTrustBaseURL(nil, "  https://fallback "); got != "https://fallback" {
		t.Fatalf("unexpected fallback: %q", got)
	}

	job := &models.UpdateJob{LesserHostAttestationsURL: " https://att ", LesserHostBaseURL: " https://base "}
	if got := resolveExpectedTrustBaseURL(job, "x"); got != "https://att" {
		t.Fatalf("unexpected attestations url: %q", got)
	}

	job = &models.UpdateJob{LesserHostAttestationsURL: " ", LesserHostBaseURL: " https://base "}
	if got := resolveExpectedTrustBaseURL(job, "x"); got != "https://base" {
		t.Fatalf("unexpected base url: %q", got)
	}
}

func TestUpdateVerifyDomain(t *testing.T) {
	t.Parallel()

	if got := updateVerifyDomain("example.com", "live"); got != "example.com" {
		t.Fatalf("unexpected live domain: %q", got)
	}
	if got := updateVerifyDomain("example.com", "lab"); got != "dev.example.com" {
		t.Fatalf("unexpected non-live domain: %q", got)
	}
}

func TestServerVerifyUpdateAI_EarlyReturnsAndErrors(t *testing.T) {
	t.Parallel()

	ok, msg := (&Server{}).verifyUpdateAI(context.Background(), &http.Client{}, nil)
	require.False(t, ok)
	require.Contains(t, msg, "internal error")

	job := &models.UpdateJob{AIEnabled: false}
	ok, msg = (&Server{}).verifyUpdateAI(context.Background(), &http.Client{}, job)
	require.True(t, ok)
	require.Empty(t, msg)

	job = &models.UpdateJob{ID: "j", AIEnabled: true}
	ok, msg = (&Server{}).verifyUpdateAI(context.Background(), &http.Client{}, job)
	require.False(t, ok)
	require.Contains(t, msg, "instance key")
}
