package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	portalUpdatesPath = "/api/v1/portal/instances/simulacrum/updates"
	jobUpdateID       = "job-update-1"
)

var managedLesserCompatibilityValidatorMu sync.Mutex
var managedLesserBodyCompatibilityValidatorMu sync.Mutex

func useStubManagedLesserCompatibilityValidator(t *testing.T, fn func(context.Context, *http.Client, string, string, string) error) {
	t.Helper()

	managedLesserCompatibilityValidatorMu.Lock()
	previous := validateManagedLesserCompatibility
	validateManagedLesserCompatibility = fn
	t.Cleanup(func() {
		validateManagedLesserCompatibility = previous
		managedLesserCompatibilityValidatorMu.Unlock()
	})
}

func useStubManagedLesserBodyCompatibilityValidator(t *testing.T, fn func(context.Context, *http.Client, string, string, string, string) error) {
	t.Helper()

	managedLesserBodyCompatibilityValidatorMu.Lock()
	previous := validateManagedLesserBodyCompatibility
	validateManagedLesserBodyCompatibility = fn
	t.Cleanup(func() {
		validateManagedLesserBodyCompatibility = previous
		managedLesserBodyCompatibilityValidatorMu.Unlock()
	})
}

func requireCheckStatus(t *testing.T, report *certificationReport, checkID string, want string) {
	t.Helper()

	for _, check := range report.Checks {
		if check.ID == checkID {
			if check.Status != want {
				t.Fatalf("expected %s to be %s, got %#v", checkID, want, check)
			}
			return
		}
	}
	t.Fatalf("missing check %q in %#v", checkID, report.Checks)
}

func newManagedCertificationServer(t *testing.T, postResponse string, getResponses []string, requestBody *createUpdateJobRequest) *httptest.Server {
	t.Helper()

	var (
		mu        sync.Mutex
		listCalls int
	)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == portalUpdatesPath:
			if requestBody != nil {
				if err := json.NewDecoder(r.Body).Decode(requestBody); err != nil {
					t.Fatalf("decode request body: %v", err)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(postResponse))
		case r.Method == http.MethodGet && r.URL.Path == portalUpdatesPath:
			mu.Lock()
			listCalls++
			call := listCalls
			mu.Unlock()

			index := call - 1
			if index >= len(getResponses) {
				index = len(getResponses) - 1
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(getResponses[index]))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestRunCertification_LesserUpdatePasses(t *testing.T) {
	t.Parallel()
	useStubManagedLesserCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string) error { return nil })
	useStubManagedLesserBodyCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string, string) error { return nil })

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

func TestRunCertification_FullManagedFlowPasses(t *testing.T) {
	t.Parallel()
	useStubManagedLesserCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string) error { return nil })
	useStubManagedLesserBodyCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string, string) error { return nil })

	var requestBody createUpdateJobRequest
	server := newManagedCertificationServer(
		t,
		`{"id":"job-update-1","kind":"lesser","status":"queued","step":"queued","lesser_version":"v1.2.6","lesser_body_version":"v0.2.3"}`,
		[]string{
			`{"jobs":[{"id":"job-update-1","kind":"lesser","status":"running","step":"deploy.wait","active_phase":"deploy","deploy_status":"running","body_status":"pending","mcp_status":"pending","lesser_version":"v1.2.6","lesser_body_version":"v0.2.3"}]}`,
			`{"jobs":[{"id":"job-update-1","kind":"lesser","status":"ok","step":"done","note":"updated","run_url":"https://example.com/builds/update","deploy_status":"succeeded","deploy_run_url":"https://example.com/builds/deploy","body_status":"succeeded","body_run_url":"https://example.com/builds/body","mcp_status":"succeeded","mcp_run_url":"https://example.com/builds/mcp","lesser_version":"v1.2.6","lesser_body_version":"v0.2.3"}]}`,
		},
		&requestBody,
	)
	defer server.Close()

	cfg := cliConfig{
		BaseURL:           server.URL,
		Token:             "token",
		InstanceSlug:      "simulacrum",
		LesserVersion:     "v1.2.6",
		LesserBodyVersion: "v0.2.3",
		RequireLesserBody: true,
		RequireMCP:        true,
		ManagedStage:      "dev",
		PollInterval:      5 * time.Millisecond,
		Timeout:           2 * time.Second,
		OutDir:            t.TempDir(),
	}

	report, err := runCertification(context.Background(), cfg, server.Client())
	if err != nil {
		t.Fatalf("runCertification: %v", err)
	}
	if report.OverallStatus != statusPass {
		t.Fatalf("expected pass report, got %#v", report)
	}
	requireCheckStatus(t, report, "compatibility_contract_valid", statusPass)
	if requestBody.LesserVersion != "v1.2.6" || requestBody.LesserBodyVersion != "v0.2.3" {
		t.Fatalf("unexpected update request: %#v", requestBody)
	}
	if len(report.Jobs) != 3 {
		t.Fatalf("expected three phase jobs, got %#v", report.Jobs)
	}
	if report.Jobs[0].JobID != jobUpdateID || report.Jobs[1].JobID != jobUpdateID || report.Jobs[2].JobID != jobUpdateID {
		t.Fatalf("expected shared managed update id across phase evidence, got %#v", report.Jobs)
	}

	for _, expectedID := range []string{"lesser_body_version_selected", "lesser_body_compatibility_contract_valid", "lesser_body_completed", "lesser_body_runner_visibility_present", "lesser_body_receipt_key_defined", "mcp_wiring_completed", "mcp_receipt_key_defined"} {
		requireCheckStatus(t, report, expectedID, statusPass)
	}
}

func TestRunCertification_LesserUpdateFailurePreservesRetryVisibility(t *testing.T) {
	t.Parallel()
	useStubManagedLesserCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string) error { return nil })
	useStubManagedLesserBodyCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string, string) error { return nil })

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

func TestRunCertification_RequiredFollowOnPhasesFailWhenSkipped(t *testing.T) {
	t.Parallel()
	useStubManagedLesserCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string) error { return nil })
	useStubManagedLesserBodyCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string, string) error { return nil })

	server := newManagedCertificationServer(
		t,
		`{"id":"job-update-2","kind":"lesser","status":"queued","step":"queued","lesser_version":"v1.2.6","lesser_body_version":"v0.2.3"}`,
		[]string{
			`{"jobs":[{"id":"job-update-2","kind":"lesser","status":"ok","step":"done","note":"updated","run_url":"https://example.com/builds/update","deploy_status":"succeeded","deploy_run_url":"https://example.com/builds/deploy","body_status":"skipped","mcp_status":"skipped","lesser_version":"v1.2.6","lesser_body_version":"v0.2.3"}]}`,
		},
		nil,
	)
	defer server.Close()

	cfg := cliConfig{
		BaseURL:           server.URL,
		Token:             "token",
		InstanceSlug:      "simulacrum",
		LesserVersion:     "v1.2.6",
		LesserBodyVersion: "v0.2.3",
		RequireLesserBody: true,
		RequireMCP:        true,
		ManagedStage:      "dev",
		PollInterval:      5 * time.Millisecond,
		Timeout:           2 * time.Second,
		OutDir:            t.TempDir(),
	}

	report, err := runCertification(context.Background(), cfg, server.Client())
	if err != nil {
		t.Fatalf("runCertification: %v", err)
	}
	if report.OverallStatus != statusFail {
		t.Fatalf("expected fail report, got %#v", report)
	}

	for _, expectedID := range []string{"lesser_body_completed", "lesser_body_runner_visibility_present", "mcp_wiring_completed"} {
		requireCheckStatus(t, report, expectedID, statusFail)
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

func validCLIArgs() []string {
	return []string{
		"--base-url", "https://lab.lesser.host",
		"--token", "token",
		"--instance-slug", "simulacrum",
		"--lesser-version", "v1.2.6",
	}
}

func TestParseCLI_RejectsInvalidArgs(t *testing.T) {
	t.Parallel()

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
		{
			name:    "missing github owner",
			args:    []string{"--base-url", "https://lab.lesser.host", "--token", "token", "--instance-slug", "simulacrum", "--lesser-version", "v1.2.6", "--lesser-github-owner", "   "},
			wantErr: "--lesser-github-owner is required",
		},
		{
			name:    "missing github repo",
			args:    []string{"--base-url", "https://lab.lesser.host", "--token", "token", "--instance-slug", "simulacrum", "--lesser-version", "v1.2.6", "--lesser-github-repo", "   "},
			wantErr: "--lesser-github-repo is required",
		},
		{
			name:    "missing lesser-body github owner",
			args:    []string{"--base-url", "https://lab.lesser.host", "--token", "token", "--instance-slug", "simulacrum", "--lesser-version", "v1.2.6", "--lesser-body-github-owner", "   "},
			wantErr: "--lesser-body-github-owner is required",
		},
		{
			name:    "missing lesser-body github repo",
			args:    []string{"--base-url", "https://lab.lesser.host", "--token", "token", "--instance-slug", "simulacrum", "--lesser-version", "v1.2.6", "--lesser-body-github-repo", "   "},
			wantErr: "--lesser-body-github-repo is required",
		},
		{
			name:    "missing managed stage",
			args:    []string{"--base-url", "https://lab.lesser.host", "--token", "token", "--instance-slug", "simulacrum", "--lesser-version", "v1.2.6", "--managed-stage", "   "},
			wantErr: "--managed-stage is required",
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
}

func TestParseCLI_ParsesValidArgsAndDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := parseCLI(validCLIArgs())
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
	if cfg.LesserGitHubOwner != "equaltoai" || cfg.LesserGitHubRepo != "lesser" {
		t.Fatalf("unexpected Lesser GitHub defaults: %#v", cfg)
	}
	if cfg.RequireLesserBody || cfg.RequireMCP {
		t.Fatalf("expected optional follow-on requirements to default false, got %#v", cfg)
	}

	cfg, err = parseCLI(append(validCLIArgs(),
		"--lesser-body-version", "v0.2.3",
		"--require-lesser-body",
		"--require-mcp",
		"--managed-stage", "live",
	))
	if err != nil {
		t.Fatalf("parseCLI optional args: %v", err)
	}
	if cfg.LesserBodyVersion != "v0.2.3" || !cfg.RequireLesserBody || !cfg.RequireMCP || cfg.ManagedStage != "live" {
		t.Fatalf("expected follow-on args to parse, got %#v", cfg)
	}
}

func TestRunCertification_CreateFailureProducesFailReport(t *testing.T) {
	t.Parallel()
	useStubManagedLesserCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string) error { return nil })

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
	requireCheckStatus(t, report, "compatibility_contract_valid", statusPass)
	requireCheckStatus(t, report, "hosted_update_started", statusFail)
}

func TestRunCertification_CompatibilityFailureBlocksManagedUpdate(t *testing.T) {
	t.Parallel()

	useStubManagedLesserCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string) error {
		return errors.New("managed Lesser releases before v1.2.6 are not supported by this lesser-host build")
	})
	useStubManagedLesserBodyCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string, string) error { return nil })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("did not expect hosted update request after compatibility failure: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	cfg := cliConfig{
		BaseURL:       server.URL,
		Token:         "token",
		InstanceSlug:  "simulacrum",
		LesserVersion: "v1.2.5",
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
	requireCheckStatus(t, report, "compatibility_contract_valid", statusFail)
	if len(report.Jobs) != 0 {
		t.Fatalf("expected no job evidence when compatibility fails early, got %#v", report.Jobs)
	}
}

func TestRunCertification_BodyVerificationRequiresExplicitVersion(t *testing.T) {
	t.Parallel()

	useStubManagedLesserCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string) error { return nil })
	useStubManagedLesserBodyCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string, string) error {
		t.Fatal("did not expect lesser-body compatibility validation without an explicit version")
		return nil
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("did not expect hosted update request after missing lesser-body version: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	cfg := cliConfig{
		BaseURL:           server.URL,
		Token:             "token",
		InstanceSlug:      "simulacrum",
		LesserVersion:     "v1.2.6",
		RequireLesserBody: true,
		PollInterval:      5 * time.Millisecond,
		Timeout:           2 * time.Second,
		OutDir:            t.TempDir(),
	}

	report, err := runCertification(context.Background(), cfg, server.Client())
	if err != nil {
		t.Fatalf("runCertification: %v", err)
	}
	requireCheckStatus(t, report, "lesser_body_version_selected", statusFail)
	if report.OverallStatus != statusFail {
		t.Fatalf("expected fail report, got %#v", report)
	}
	require.Empty(t, report.Jobs)
}

func TestRunCertification_BodyCompatibilityFailureBlocksManagedUpdate(t *testing.T) {
	t.Parallel()

	useStubManagedLesserCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string) error { return nil })
	useStubManagedLesserBodyCompatibilityValidator(t, func(context.Context, *http.Client, string, string, string, string) error {
		return errors.New("managed lesser-body releases before v0.2.3 are not supported by this lesser-host build")
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("did not expect hosted update request after lesser-body compatibility failure: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	cfg := cliConfig{
		BaseURL:           server.URL,
		Token:             "token",
		InstanceSlug:      "simulacrum",
		LesserVersion:     "v1.2.6",
		LesserBodyVersion: "v0.2.2",
		RequireLesserBody: true,
		ManagedStage:      "dev",
		PollInterval:      5 * time.Millisecond,
		Timeout:           2 * time.Second,
		OutDir:            t.TempDir(),
	}

	report, err := runCertification(context.Background(), cfg, server.Client())
	if err != nil {
		t.Fatalf("runCertification: %v", err)
	}
	requireCheckStatus(t, report, "lesser_body_version_selected", statusPass)
	requireCheckStatus(t, report, "lesser_body_compatibility_contract_valid", statusFail)
	if report.OverallStatus != statusFail {
		t.Fatalf("expected fail report, got %#v", report)
	}
	require.Empty(t, report.Jobs)
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
