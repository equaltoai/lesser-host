package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	portalUpdatesPath = "/api/v1/portal/instances/simulacrum/updates"
)

func TestRunCertification_LesserUpdatePasses(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	listCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == portalUpdatesPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"job-lesser-1","kind":"lesser","status":"queued","step":"queued","lesser_version":"v1.2.6"}`))
		case r.Method == http.MethodGet && r.URL.Path == portalUpdatesPath:
			mu.Lock()
			listCalls++
			call := listCalls
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			if call == 1 {
				_, _ = w.Write([]byte(`{"jobs":[{"id":"job-lesser-1","kind":"lesser","status":"running","step":"deploy.wait","lesser_version":"v1.2.6"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"jobs":[{"id":"job-lesser-1","kind":"lesser","status":"ok","step":"done","note":"updated","run_url":"https://example.com/builds/1","deploy_run_url":"https://example.com/builds/1","lesser_version":"v1.2.6"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := cliConfig{
		BaseURL:       server.URL,
		Token:         "token",
		InstanceSlug:  "simulacrum",
		LesserVersion: "v1.2.6",
		PollInterval:  5 * time.Millisecond,
		Timeout:       2 * time.Second,
		OutDir:        t.TempDir(),
	}

	report, err := runCertification(context.Background(), cfg, server.Client())
	if err != nil {
		t.Fatalf("runCertification: %v", err)
	}
	if report.OverallStatus != statusPass {
		t.Fatalf("expected pass, got %#v", report)
	}
	if len(report.Jobs) != 1 || report.Jobs[0].ReceiptKey != "managed/updates/simulacrum/job-lesser-1/state.json" {
		t.Fatalf("unexpected job evidence: %#v", report.Jobs)
	}

	err = writeCertificationOutputs(cfg.OutDir, report)
	if err != nil {
		t.Fatalf("writeCertificationOutputs: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(cfg.OutDir, "managed-release-certification.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var parsed certificationReport
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if parsed.OverallStatus != statusPass {
		t.Fatalf("expected written pass report, got %#v", parsed)
	}
}

func TestRunCertification_LesserUpdateFailurePreservesRetryVisibility(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == portalUpdatesPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"job-lesser-fail","kind":"lesser","status":"queued","step":"queued","lesser_version":"v1.2.6"}`))
		case r.Method == http.MethodGet && r.URL.Path == portalUpdatesPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jobs":[{"id":"job-lesser-fail","kind":"lesser","status":"error","step":"failed","failed_phase":"deploy","error_code":"deploy_failed","error_message":"BUILD: release contract mismatch","note":"BUILD: release contract mismatch","run_url":"https://example.com/builds/fail","deploy_run_url":"https://example.com/builds/fail","lesser_version":"v1.2.6"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := cliConfig{
		BaseURL:       server.URL,
		Token:         "token",
		InstanceSlug:  "simulacrum",
		LesserVersion: "v1.2.6",
		PollInterval:  5 * time.Millisecond,
		Timeout:       2 * time.Second,
		OutDir:        t.TempDir(),
	}

	report, err := runCertification(context.Background(), cfg, server.Client())
	if err != nil {
		t.Fatalf("runCertification: %v", err)
	}
	if report.OverallStatus != statusFail {
		t.Fatalf("expected fail, got %#v", report)
	}
	if len(report.Checks) == 0 {
		t.Fatalf("expected checks, got %#v", report)
	}

	var retryCheck *certificationCheck
	for i := range report.Checks {
		if report.Checks[i].ID == "retry_visibility_present" {
			retryCheck = &report.Checks[i]
			break
		}
	}
	if retryCheck == nil || retryCheck.Status != statusPass {
		t.Fatalf("expected retry visibility pass, got %#v", retryCheck)
	}
	if len(report.Jobs) != 1 || report.Jobs[0].ErrorCode != "deploy_failed" {
		t.Fatalf("expected preserved failure evidence, got %#v", report.Jobs)
	}
}

func TestDeriveReceiptKey(t *testing.T) {
	t.Parallel()

	if got := deriveReceiptKey("lesser", "slug", "job1"); got != "managed/updates/slug/job1/state.json" {
		t.Fatalf("unexpected lesser receipt key: %q", got)
	}
	if got := deriveReceiptKey("lesser-body", "slug", "job1"); got != "managed/updates/slug/job1/body-state.json" {
		t.Fatalf("unexpected lesser-body receipt key: %q", got)
	}
	if got := deriveReceiptKey("mcp", "slug", "job1"); got != "managed/updates/slug/job1/mcp-state.json" {
		t.Fatalf("unexpected mcp receipt key: %q", got)
	}
}

func TestParseCLI_ValidatesArgs(t *testing.T) {
	t.Parallel()

	validArgs := func() []string {
		return []string{
			"--base-url", "https://lab.lesser.host",
			"--token", "token",
			"--instance-slug", "simulacrum",
			"--lesser-version", "v1.2.6",
		}
	}

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing base url",
			args:    []string{"--token", "token", "--instance-slug", "simulacrum", "--lesser-version", "v1.2.6"},
			wantErr: "--base-url is required",
		},
		{
			name:    "invalid scheme",
			args:    []string{"--base-url", "ftp://lab.lesser.host", "--token", "token", "--instance-slug", "simulacrum", "--lesser-version", "v1.2.6"},
			wantErr: "--base-url must use http or https",
		},
		{
			name:    "missing host",
			args:    []string{"--base-url", "https:///missing-host", "--token", "token", "--instance-slug", "simulacrum", "--lesser-version", "v1.2.6"},
			wantErr: "--base-url must include a host",
		},
		{
			name:    "missing token",
			args:    []string{"--base-url", "https://lab.lesser.host", "--instance-slug", "simulacrum", "--lesser-version", "v1.2.6"},
			wantErr: "--token is required",
		},
		{
			name:    "missing instance slug",
			args:    []string{"--base-url", "https://lab.lesser.host", "--token", "token", "--lesser-version", "v1.2.6"},
			wantErr: "--instance-slug is required",
		},
		{
			name:    "missing lesser version",
			args:    []string{"--base-url", "https://lab.lesser.host", "--token", "token", "--instance-slug", "simulacrum"},
			wantErr: "--lesser-version is required",
		},
		{
			name:    "invalid poll interval",
			args:    []string{"--base-url", "https://lab.lesser.host", "--token", "token", "--instance-slug", "simulacrum", "--lesser-version", "v1.2.6", "--poll-interval", "0s"},
			wantErr: "--poll-interval must be positive",
		},
		{
			name:    "invalid timeout",
			args:    []string{"--base-url", "https://lab.lesser.host", "--token", "token", "--instance-slug", "simulacrum", "--lesser-version", "v1.2.6", "--timeout", "0s"},
			wantErr: "--timeout must be positive",
		},
		{
			name:    "missing out dir",
			args:    []string{"--base-url", "https://lab.lesser.host", "--token", "token", "--instance-slug", "simulacrum", "--lesser-version", "v1.2.6", "--out-dir", "   "},
			wantErr: "--out-dir is required",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseCLI(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}

	cfg, err := parseCLI(validArgs())
	if err != nil {
		t.Fatalf("parseCLI valid args: %v", err)
	}
	if cfg.BaseURL != "https://lab.lesser.host" {
		t.Fatalf("unexpected base url: %q", cfg.BaseURL)
	}
	if cfg.PollInterval != 10*time.Second {
		t.Fatalf("unexpected poll interval: %s", cfg.PollInterval)
	}
	if cfg.Timeout != 30*time.Minute {
		t.Fatalf("unexpected timeout: %s", cfg.Timeout)
	}
}

func TestRunCertification_CreateFailureProducesFailReport(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != portalUpdatesPath {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "release contract mismatch", http.StatusBadRequest)
	}))
	defer server.Close()

	cfg := cliConfig{
		BaseURL:       server.URL,
		Token:         "token",
		InstanceSlug:  "simulacrum",
		LesserVersion: "v1.2.6",
		PollInterval:  5 * time.Millisecond,
		Timeout:       2 * time.Second,
		OutDir:        t.TempDir(),
	}

	report, err := runCertification(context.Background(), cfg, server.Client())
	if err != nil {
		t.Fatalf("runCertification: %v", err)
	}
	if report.OverallStatus != statusFail {
		t.Fatalf("expected fail report, got %#v", report)
	}
	if len(report.Checks) != 1 || report.Checks[0].ID != "hosted_update_started" || report.Checks[0].Status != statusFail {
		t.Fatalf("unexpected checks: %#v", report.Checks)
	}
}

func TestCreateUpdate_ErrorResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    string
	}{
		{
			name:       "non success status",
			statusCode: http.StatusBadGateway,
			body:       "runner failed",
			wantErr:    "create update failed (HTTP 502): runner failed",
		},
		{
			name:       "missing job id",
			statusCode: http.StatusAccepted,
			body:       `{"status":"queued"}`,
			wantErr:    "create update response did not include a job id",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Fatalf("unexpected method: %s", r.Method)
				}
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := &certificationClient{
				baseURL: server.URL,
				token:   "token",
				client:  server.Client(),
			}
			_, err := client.createUpdate(context.Background(), "simulacrum", createUpdateJobRequest{LesserVersion: "v1.2.6"})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestCreateUpdate_RequestFailure(t *testing.T) {
	t.Parallel()

	client := &certificationClient{
		baseURL: "http://127.0.0.1:1",
		token:   "token",
		client:  &http.Client{Timeout: 20 * time.Millisecond},
	}

	_, err := client.createUpdate(context.Background(), "simulacrum", createUpdateJobRequest{LesserVersion: "v1.2.6"})
	if err == nil {
		t.Fatal("expected request failure")
	}
}

func TestWaitForJob_TimesOut(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jobs":[{"id":"job-lesser-1","kind":"lesser","status":"running","step":"deploy.wait","lesser_version":"v1.2.6"}]}`))
	}))
	defer server.Close()

	client := &certificationClient{
		baseURL: server.URL,
		token:   "token",
		client:  server.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := client.waitForJob(ctx, "simulacrum", "job-lesser-1", 5*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out waiting for job job-lesser-1") &&
		!strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestListUpdates_ErrorResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{
			name: "non success status",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
			},
			wantErr: "list updates failed (HTTP 503): not ready",
		},
		{
			name: "bad json",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("{"))
			},
			wantErr: "unexpected EOF",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(tt.handler)
			defer server.Close()

			client := &certificationClient{
				baseURL: server.URL,
				token:   "token",
				client:  server.Client(),
			}
			_, err := client.listUpdates(context.Background(), "simulacrum")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestWriteCertificationOutputs_RejectsNilReport(t *testing.T) {
	t.Parallel()

	err := writeCertificationOutputs(t.TempDir(), nil)
	if err == nil || err.Error() != "report is required" {
		t.Fatalf("expected report required error, got %v", err)
	}
}

func TestRenderMarkdownSummary_EscapesInput(t *testing.T) {
	t.Parallel()

	report := &certificationReport{
		GeneratedAt: "2026-03-30T00:00:00Z",
		LesserHost: certificationTarget{
			BaseURL:      "https://lab.lesser.host?q=<bad>`\n",
			InstanceSlug: "simulacrum\n`slug`",
		},
		RequestedRelease: certificationRequested{
			LesserVersion: "v1.2.6<script>",
		},
		Checks: []certificationCheck{{
			ID:     "hosted_update_completed",
			Status: "fail",
			Detail: "line one\nline two",
		}},
		Jobs: []certificationJob{{
			Kind:         "lesser",
			JobID:        "job-1",
			Status:       "error",
			Step:         "failed",
			RunURL:       "https://example.com/builds/1",
			ErrorCode:    "boom",
			ErrorMessage: "bad `state`",
		}},
		OverallStatus: statusFail,
	}

	rendered := renderMarkdownSummary(report)
	if strings.Contains(rendered, "<script>") || strings.Contains(rendered, "\nline two") {
		t.Fatalf("expected markdown to escape unsafe content, got %q", rendered)
	}
	if !strings.Contains(rendered, "&lt;bad&gt;'") {
		t.Fatalf("expected escaped base url, got %q", rendered)
	}
	if !strings.Contains(rendered, "bad 'state'") {
		t.Fatalf("expected sanitized error message, got %q", rendered)
	}
}
