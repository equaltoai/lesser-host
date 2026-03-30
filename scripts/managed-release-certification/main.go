package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const certificationSchemaVersion = 1

const (
	statusPass    = "pass"
	statusFail    = "fail"
	statusSkipped = "skipped"
)

type cliConfig struct {
	BaseURL       string
	Token         string
	InstanceSlug  string
	LesserVersion string
	PollInterval  time.Duration
	Timeout       time.Duration
	OutDir        string
}

type certificationReport struct {
	SchemaVersion    int                    `json:"schema_version"`
	GeneratedAt      string                 `json:"generated_at"`
	LesserHost       certificationTarget    `json:"lesser_host"`
	RequestedRelease certificationRequested `json:"requested_release"`
	Checks           []certificationCheck   `json:"checks"`
	Jobs             []certificationJob     `json:"jobs"`
	OverallStatus    string                 `json:"overall_status"`
}

type certificationTarget struct {
	BaseURL      string `json:"base_url"`
	InstanceSlug string `json:"instance_slug"`
}

type certificationRequested struct {
	LesserVersion     string `json:"lesser_version"`
	LesserBodyVersion string `json:"lesser_body_version,omitempty"`
	RunLesser         bool   `json:"run_lesser"`
	RunLesserBody     bool   `json:"run_lesser_body"`
	RunMCP            bool   `json:"run_mcp"`
}

type certificationCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type certificationJob struct {
	Kind             string `json:"kind"`
	JobID            string `json:"job_id"`
	Status           string `json:"status"`
	Step             string `json:"step"`
	FailedPhase      string `json:"failed_phase,omitempty"`
	Note             string `json:"note,omitempty"`
	ErrorCode        string `json:"error_code,omitempty"`
	ErrorMessage     string `json:"error_message,omitempty"`
	RunURL           string `json:"run_url,omitempty"`
	DeployRunURL     string `json:"deploy_run_url,omitempty"`
	BodyRunURL       string `json:"body_run_url,omitempty"`
	MCPRunURL        string `json:"mcp_run_url,omitempty"`
	ReceiptKey       string `json:"receipt_key,omitempty"`
	RequestedVersion string `json:"requested_version,omitempty"`
}

type updateJobResponse struct {
	ID                string `json:"id"`
	Kind              string `json:"kind"`
	Status            string `json:"status"`
	Step              string `json:"step"`
	Note              string `json:"note"`
	FailedPhase       string `json:"failed_phase"`
	ErrorCode         string `json:"error_code"`
	ErrorMessage      string `json:"error_message"`
	RunURL            string `json:"run_url"`
	DeployRunURL      string `json:"deploy_run_url"`
	BodyRunURL        string `json:"body_run_url"`
	MCPRunURL         string `json:"mcp_run_url"`
	LesserVersion     string `json:"lesser_version"`
	LesserBodyVersion string `json:"lesser_body_version"`
}

type listUpdateJobsResponse struct {
	Jobs []updateJobResponse `json:"jobs"`
}

type createUpdateJobRequest struct {
	LesserVersion string `json:"lesser_version,omitempty"`
}

type certificationClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func main() {
	cfg, err := parseCLI(os.Args[1:])
	if err != nil {
		failf("%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	report, err := runCertification(ctx, cfg, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		failf("%v", err)
	}

	if err := writeCertificationOutputs(cfg.OutDir, report); err != nil {
		failf("write certification outputs: %v", err)
	}

	if report.OverallStatus != "pass" {
		failf("managed release certification failed")
	}
}

func parseCLI(args []string) (cliConfig, error) {
	fs := flag.NewFlagSet("managed-release-certification", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var cfg cliConfig
	fs.StringVar(&cfg.BaseURL, "base-url", "", "lesser-host base URL (for example https://lab.lesser.host)")
	fs.StringVar(&cfg.Token, "token", "", "bearer token with access to the managed instance")
	fs.StringVar(&cfg.InstanceSlug, "instance-slug", "", "managed instance slug to update")
	fs.StringVar(&cfg.LesserVersion, "lesser-version", "", "published Lesser release tag to certify")
	fs.DurationVar(&cfg.PollInterval, "poll-interval", 10*time.Second, "poll interval for update status")
	fs.DurationVar(&cfg.Timeout, "timeout", 30*time.Minute, "overall certification timeout")
	fs.StringVar(&cfg.OutDir, "out-dir", "gov-infra/evidence/managed-release-certification", "output directory for certification evidence")

	if err := fs.Parse(args); err != nil {
		return cliConfig{}, err
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return cliConfig{}, errors.New("--base-url is required")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return cliConfig{}, errors.New("--token is required")
	}
	if strings.TrimSpace(cfg.InstanceSlug) == "" {
		return cliConfig{}, errors.New("--instance-slug is required")
	}
	if strings.TrimSpace(cfg.LesserVersion) == "" {
		return cliConfig{}, errors.New("--lesser-version is required")
	}
	if cfg.PollInterval <= 0 {
		return cliConfig{}, errors.New("--poll-interval must be positive")
	}
	if cfg.Timeout <= 0 {
		return cliConfig{}, errors.New("--timeout must be positive")
	}
	if strings.TrimSpace(cfg.OutDir) == "" {
		return cliConfig{}, errors.New("--out-dir is required")
	}

	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.Token = strings.TrimSpace(cfg.Token)
	cfg.InstanceSlug = strings.TrimSpace(cfg.InstanceSlug)
	cfg.LesserVersion = strings.TrimSpace(cfg.LesserVersion)
	cfg.OutDir = strings.TrimSpace(cfg.OutDir)

	parsedBaseURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return cliConfig{}, fmt.Errorf("--base-url is invalid: %w", err)
	}
	if parsedBaseURL.Host == "" {
		return cliConfig{}, errors.New("--base-url must include a host")
	}
	if parsedBaseURL.Scheme != "https" && parsedBaseURL.Scheme != "http" {
		return cliConfig{}, errors.New("--base-url must use http or https")
	}
	return cfg, nil
}

func runCertification(ctx context.Context, cfg cliConfig, httpClient *http.Client) (*certificationReport, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	client := &certificationClient{
		baseURL: cfg.BaseURL,
		token:   cfg.Token,
		client:  httpClient,
	}

	report := &certificationReport{
		SchemaVersion: certificationSchemaVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		LesserHost: certificationTarget{
			BaseURL:      cfg.BaseURL,
			InstanceSlug: cfg.InstanceSlug,
		},
		RequestedRelease: certificationRequested{
			LesserVersion: cfg.LesserVersion,
			RunLesser:     true,
			RunLesserBody: false,
			RunMCP:        false,
		},
	}

	startedJob, err := client.createLesserUpdate(ctx, cfg.InstanceSlug, cfg.LesserVersion)
	if err != nil {
		report.Checks = append(report.Checks, certificationCheck{
			ID:     "hosted_update_started",
			Status: statusFail,
			Detail: err.Error(),
		})
		report.OverallStatus = statusFail
		return report, nil
	}
	report.Checks = append(report.Checks, certificationCheck{
		ID:     "hosted_update_started",
		Status: statusPass,
		Detail: "lesser-host accepted the managed Lesser update request",
	})

	finalJob, err := client.waitForJob(ctx, cfg.InstanceSlug, startedJob.ID, cfg.PollInterval)
	if err != nil {
		report.Checks = append(report.Checks, certificationCheck{
			ID:     "hosted_update_completed",
			Status: statusFail,
			Detail: err.Error(),
		})
		report.Jobs = append(report.Jobs, certificationJobFromResponse(startedJob, cfg.InstanceSlug))
		report.OverallStatus = statusFail
		return report, nil
	}

	jobEvidence := certificationJobFromResponse(finalJob, cfg.InstanceSlug)
	report.Jobs = append(report.Jobs, jobEvidence)
	report.Checks = append(report.Checks, certificationChecksForLesserJob(finalJob, jobEvidence)...)
	report.OverallStatus = overallStatus(report.Checks)
	return report, nil
}

func certificationChecksForLesserJob(job updateJobResponse, evidence certificationJob) []certificationCheck {
	checks := []certificationCheck{{
		ID:     "receipt_key_defined",
		Status: statusPass,
		Detail: evidence.ReceiptKey,
	}}

	if hasRunnerVisibility(job) {
		checks = append(checks, certificationCheck{
			ID:     "runner_visibility_present",
			Status: statusPass,
			Detail: firstNonEmpty(job.RunURL, job.DeployRunURL, job.BodyRunURL, job.MCPRunURL),
		})
	} else {
		checks = append(checks, certificationCheck{
			ID:     "runner_visibility_present",
			Status: statusFail,
			Detail: "update completed without any runner deep link",
		})
	}

	if strings.EqualFold(strings.TrimSpace(job.Status), "ok") {
		checks = append(checks, certificationCheck{
			ID:     "hosted_update_completed",
			Status: statusPass,
			Detail: "managed Lesser update completed successfully",
		})
		checks = append(checks, certificationCheck{
			ID:     "retry_visibility_present",
			Status: statusSkipped,
			Detail: "retry visibility is only required for failed certification runs",
		})
		return checks
	}

	checks = append(checks, certificationCheck{
		ID:     "hosted_update_completed",
		Status: statusFail,
		Detail: fmt.Sprintf("managed Lesser update ended with status=%s step=%s", strings.TrimSpace(job.Status), strings.TrimSpace(job.Step)),
	})

	if hasFailureVisibility(job) {
		checks = append(checks, certificationCheck{
			ID:     "retry_visibility_present",
			Status: statusPass,
			Detail: "failed update preserved failure code, message, phase, and runner visibility",
		})
	} else {
		checks = append(checks, certificationCheck{
			ID:     "retry_visibility_present",
			Status: statusFail,
			Detail: "failed update did not preserve complete retry visibility fields",
		})
	}
	return checks
}

func certificationJobFromResponse(job updateJobResponse, slug string) certificationJob {
	version := strings.TrimSpace(job.LesserVersion)
	if version == "" {
		version = strings.TrimSpace(job.LesserBodyVersion)
	}
	return certificationJob{
		Kind:             firstNonEmpty(strings.TrimSpace(job.Kind), "lesser"),
		JobID:            strings.TrimSpace(job.ID),
		Status:           strings.TrimSpace(job.Status),
		Step:             strings.TrimSpace(job.Step),
		FailedPhase:      strings.TrimSpace(job.FailedPhase),
		Note:             strings.TrimSpace(job.Note),
		ErrorCode:        strings.TrimSpace(job.ErrorCode),
		ErrorMessage:     strings.TrimSpace(job.ErrorMessage),
		RunURL:           strings.TrimSpace(job.RunURL),
		DeployRunURL:     strings.TrimSpace(job.DeployRunURL),
		BodyRunURL:       strings.TrimSpace(job.BodyRunURL),
		MCPRunURL:        strings.TrimSpace(job.MCPRunURL),
		ReceiptKey:       deriveReceiptKey(firstNonEmpty(strings.TrimSpace(job.Kind), "lesser"), slug, job.ID),
		RequestedVersion: version,
	}
}

func deriveReceiptKey(kind string, slug string, jobID string) string {
	slug = strings.TrimSpace(slug)
	jobID = strings.TrimSpace(jobID)
	if slug == "" || jobID == "" {
		return ""
	}
	switch strings.TrimSpace(kind) {
	case "lesser-body":
		return fmt.Sprintf("managed/updates/%s/%s/body-state.json", slug, jobID)
	case "mcp":
		return fmt.Sprintf("managed/updates/%s/%s/mcp-state.json", slug, jobID)
	default:
		return fmt.Sprintf("managed/updates/%s/%s/state.json", slug, jobID)
	}
}

func overallStatus(checks []certificationCheck) string {
	for _, check := range checks {
		if check.Status == statusFail {
			return statusFail
		}
	}
	return statusPass
}

func hasRunnerVisibility(job updateJobResponse) bool {
	return firstNonEmpty(job.RunURL, job.DeployRunURL, job.BodyRunURL, job.MCPRunURL) != ""
}

func hasFailureVisibility(job updateJobResponse) bool {
	return strings.TrimSpace(job.ErrorCode) != "" &&
		strings.TrimSpace(job.ErrorMessage) != "" &&
		strings.TrimSpace(job.FailedPhase) != "" &&
		hasRunnerVisibility(job)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func writeCertificationOutputs(outDir string, report *certificationReport) error {
	if report == nil {
		return errors.New("report is required")
	}
	cleanedOutDir := filepath.Clean(strings.TrimSpace(outDir))
	if cleanedOutDir == "." || cleanedOutDir == "" {
		return errors.New("output directory is required")
	}
	if err := os.MkdirAll(cleanedOutDir, 0o755); err != nil { //nolint:gosec // Evidence output is an operator-provided local filesystem path for this certification CLI.
		return err
	}

	jsonPath := filepath.Join(cleanedOutDir, "managed-release-certification.json")
	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	jsonBytes = append(jsonBytes, '\n')
	if err := os.WriteFile(jsonPath, jsonBytes, 0o600); err != nil { //nolint:gosec // Evidence files are written only to the operator-selected local certification output directory.
		return err
	}

	markdownPath := filepath.Join(cleanedOutDir, "managed-release-certification.md")
	if err := os.WriteFile(markdownPath, []byte(renderMarkdownSummary(report)), 0o600); err != nil { //nolint:gosec // Evidence files are written only to the operator-selected local certification output directory.
		return err
	}
	return nil
}

func renderMarkdownSummary(report *certificationReport) string {
	var b strings.Builder
	b.WriteString("# Managed release certification\n\n")
	b.WriteString("- Generated at: `" + safeMarkdownText(report.GeneratedAt) + "`\n")
	b.WriteString("- Base URL: `" + safeMarkdownText(report.LesserHost.BaseURL) + "`\n")
	b.WriteString("- Instance slug: `" + safeMarkdownText(report.LesserHost.InstanceSlug) + "`\n")
	b.WriteString("- Lesser version: `" + safeMarkdownText(report.RequestedRelease.LesserVersion) + "`\n")
	b.WriteString("- Overall status: `" + safeMarkdownText(report.OverallStatus) + "`\n\n")

	b.WriteString("## Checks\n\n")
	for _, check := range report.Checks {
		b.WriteString("- `" + safeMarkdownText(check.ID) + "`: `" + safeMarkdownText(check.Status) + "`")
		if strings.TrimSpace(check.Detail) != "" {
			b.WriteString(" - " + safeMarkdownText(check.Detail))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n## Jobs\n\n")
	for _, job := range report.Jobs {
		b.WriteString(
			"- `" + safeMarkdownText(job.Kind) +
				"` `" + safeMarkdownText(job.JobID) +
				"`: status=`" + safeMarkdownText(job.Status) +
				"` step=`" + safeMarkdownText(job.Step) + "`",
		)
		if job.RequestedVersion != "" {
			b.WriteString(" version=`" + safeMarkdownText(job.RequestedVersion) + "`")
		}
		if job.ReceiptKey != "" {
			b.WriteString(" receipt=`" + safeMarkdownText(job.ReceiptKey) + "`")
		}
		b.WriteString("\n")
		if job.RunURL != "" {
			b.WriteString("  run_url: " + safeMarkdownText(job.RunURL) + "\n")
		}
		if job.DeployRunURL != "" && job.DeployRunURL != job.RunURL {
			b.WriteString("  deploy_run_url: " + safeMarkdownText(job.DeployRunURL) + "\n")
		}
		if job.Note != "" {
			b.WriteString("  note: " + safeMarkdownText(job.Note) + "\n")
		}
		if job.ErrorCode != "" || job.ErrorMessage != "" {
			b.WriteString("  failure: " + safeMarkdownText(strings.TrimSpace(job.ErrorCode)) + " " + safeMarkdownText(strings.TrimSpace(job.ErrorMessage)) + "\n")
		}
	}
	return b.String()
}

func safeMarkdownText(value string) string {
	escaped := html.EscapeString(strings.TrimSpace(value))
	escaped = strings.ReplaceAll(escaped, "`", "'")
	escaped = strings.ReplaceAll(escaped, "\r", " ")
	escaped = strings.ReplaceAll(escaped, "\n", " ")
	return escaped
}

func (c *certificationClient) createLesserUpdate(ctx context.Context, slug string, lesserVersion string) (updateJobResponse, error) {
	return c.createUpdate(ctx, slug, createUpdateJobRequest{LesserVersion: strings.TrimSpace(lesserVersion)})
}

func (c *certificationClient) createUpdate(ctx context.Context, slug string, reqBody createUpdateJobRequest) (updateJobResponse, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return updateJobResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/api/v1/portal/instances/"+slug+"/updates"), bytes.NewReader(body))
	if err != nil {
		return updateJobResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req) //nolint:gosec // Target host is an explicitly provided certification endpoint validated by parseCLI.
	if err != nil {
		return updateJobResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return updateJobResponse{}, fmt.Errorf("create update failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed updateJobResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return updateJobResponse{}, err
	}
	if strings.TrimSpace(parsed.ID) == "" {
		return updateJobResponse{}, errors.New("create update response did not include a job id")
	}
	return parsed, nil
}

func (c *certificationClient) waitForJob(ctx context.Context, slug string, jobID string, pollInterval time.Duration) (updateJobResponse, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return updateJobResponse{}, errors.New("job id is required")
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		jobs, err := c.listUpdates(ctx, slug)
		if err != nil {
			return updateJobResponse{}, err
		}
		for _, job := range jobs {
			if strings.TrimSpace(job.ID) != jobID {
				continue
			}
			status := strings.TrimSpace(job.Status)
			if status == "ok" || status == "error" {
				return job, nil
			}
			break
		}

		select {
		case <-ctx.Done():
			return updateJobResponse{}, fmt.Errorf("timed out waiting for job %s: %w", jobID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (c *certificationClient) listUpdates(ctx context.Context, slug string) ([]updateJobResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/api/v1/portal/instances/"+slug+"/updates"), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req) //nolint:gosec // Target host is an explicitly provided certification endpoint validated by parseCLI.
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list updates failed (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed listUpdateJobsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	return parsed.Jobs, nil
}

func (c *certificationClient) endpoint(path string) string {
	return c.baseURL + path
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
