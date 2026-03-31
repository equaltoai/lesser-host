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

	"github.com/equaltoai/lesser-host/internal/provisionworker"
)

const certificationSchemaVersion = 1
const lesserBodyCertificationSchemaVersion = 1

const (
	certificationJSONFileName               = "managed-release-certification.json"
	certificationMarkdownFileName           = "managed-release-certification.md"
	lesserBodyCertificationJSONFileName     = "managed-release-certification-lesser-body.json"
	lesserBodyCertificationMarkdownFileName = "managed-release-certification-lesser-body.md"
	lesserBodyTemplateVerificationMode      = "cloudformation_deploy_no_execute_changeset"
)

const (
	statusPass    = "pass"
	statusFail    = "fail"
	statusSkipped = "skipped"

	updateJobStatusQueued  = "queued"
	updateJobStatusRunning = "running"
	updateJobStatusOK      = "ok"
	updateJobStatusError   = "error"

	updateJobKindLesser = "lesser"
	updateJobKindBody   = "lesser-body"
	updateJobKindMCP    = "mcp"
)

type cliConfig struct {
	BaseURL               string
	Token                 string
	InstanceSlug          string
	LesserVersion         string
	LesserBodyVersion     string
	LesserGitHubOwner     string
	LesserGitHubRepo      string
	LesserBodyGitHubOwner string
	LesserBodyGitHubRepo  string
	ManagedStage          string
	RequireLesserBody     bool
	RequireMCP            bool
	PollInterval          time.Duration
	Timeout               time.Duration
	OutDir                string
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

type lesserBodyCertificationReport struct {
	SchemaVersion    int                    `json:"schema_version"`
	GeneratedAt      string                 `json:"generated_at"`
	LesserHost       certificationTarget    `json:"lesser_host"`
	RequestedRelease certificationRequested `json:"requested_release"`
	Checks           []certificationCheck   `json:"checks"`
	Job              certificationJob       `json:"job"`
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
	Kind                     string `json:"kind"`
	JobID                    string `json:"job_id"`
	Status                   string `json:"status"`
	Step                     string `json:"step"`
	FailedPhase              string `json:"failed_phase,omitempty"`
	Note                     string `json:"note,omitempty"`
	ErrorCode                string `json:"error_code,omitempty"`
	ErrorMessage             string `json:"error_message,omitempty"`
	RunURL                   string `json:"run_url,omitempty"`
	DeployRunURL             string `json:"deploy_run_url,omitempty"`
	BodyRunURL               string `json:"body_run_url,omitempty"`
	MCPRunURL                string `json:"mcp_run_url,omitempty"`
	ReceiptKey               string `json:"receipt_key,omitempty"`
	RequestedVersion         string `json:"requested_version,omitempty"`
	TemplatePath             string `json:"template_path,omitempty"`
	TemplateCertificationKey string `json:"template_certification_key,omitempty"`
	TemplateVerificationMode string `json:"template_verification_mode,omitempty"`
}

type updateJobResponse struct {
	ID                string `json:"id"`
	Kind              string `json:"kind"`
	Status            string `json:"status"`
	Step              string `json:"step"`
	Note              string `json:"note"`
	ActivePhase       string `json:"active_phase"`
	FailedPhase       string `json:"failed_phase"`
	ErrorCode         string `json:"error_code"`
	ErrorMessage      string `json:"error_message"`
	RunURL            string `json:"run_url"`
	DeployStatus      string `json:"deploy_status"`
	DeployRunURL      string `json:"deploy_run_url"`
	DeployError       string `json:"deploy_error"`
	BodyStatus        string `json:"body_status"`
	BodyRunURL        string `json:"body_run_url"`
	BodyError         string `json:"body_error"`
	MCPStatus         string `json:"mcp_status"`
	MCPRunURL         string `json:"mcp_run_url"`
	MCPError          string `json:"mcp_error"`
	LesserVersion     string `json:"lesser_version"`
	LesserBodyVersion string `json:"lesser_body_version"`
}

type listUpdateJobsResponse struct {
	Jobs []updateJobResponse `json:"jobs"`
}

type createUpdateJobRequest struct {
	LesserVersion       string `json:"lesser_version,omitempty"`
	LesserBodyVersion   string `json:"lesser_body_version,omitempty"`
	BodyOnly            bool   `json:"body_only,omitempty"`
	MCPOnly             bool   `json:"mcp_only,omitempty"`
	BodyTemplateCertify bool   `json:"body_template_certify,omitempty"`
}

type certificationClient struct {
	baseURL string
	token   string
	client  *http.Client
}

var validateManagedLesserCompatibility = provisionworker.ValidateManagedLesserReleaseCompatibility
var validateManagedLesserBodyCompatibility = provisionworker.ValidateManagedLesserBodyReleaseCompatibility
var validateManagedLesserBodyTemplatePreflight = provisionworker.ValidateManagedLesserBodyReleaseTemplatePreflight

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

	if report.OverallStatus != statusPass {
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
	fs.StringVar(&cfg.LesserBodyVersion, "lesser-body-version", "", "optional lesser-body release tag to require for follow-on deploy certification")
	fs.StringVar(&cfg.LesserGitHubOwner, "lesser-github-owner", "equaltoai", "GitHub owner for Lesser release compatibility checks")
	fs.StringVar(&cfg.LesserGitHubRepo, "lesser-github-repo", "lesser", "GitHub repo for Lesser release compatibility checks")
	fs.StringVar(&cfg.LesserBodyGitHubOwner, "lesser-body-github-owner", "equaltoai", "GitHub owner for lesser-body release compatibility checks")
	fs.StringVar(&cfg.LesserBodyGitHubRepo, "lesser-body-github-repo", "lesser-body", "GitHub repo for lesser-body release compatibility checks")
	fs.StringVar(&cfg.ManagedStage, "managed-stage", "dev", "managed instance stage to use for stage-scoped lesser-body compatibility checks")
	fs.BoolVar(&cfg.RequireLesserBody, "require-lesser-body", false, "require lesser-body follow-on deploy to succeed in the certification run")
	fs.BoolVar(&cfg.RequireMCP, "require-mcp", false, "require MCP follow-on wiring to succeed in the certification run")
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
	cfg.LesserBodyVersion = strings.TrimSpace(cfg.LesserBodyVersion)
	cfg.LesserGitHubOwner = strings.TrimSpace(cfg.LesserGitHubOwner)
	cfg.LesserGitHubRepo = strings.TrimSpace(cfg.LesserGitHubRepo)
	cfg.LesserBodyGitHubOwner = strings.TrimSpace(cfg.LesserBodyGitHubOwner)
	cfg.LesserBodyGitHubRepo = strings.TrimSpace(cfg.LesserBodyGitHubRepo)
	cfg.ManagedStage = strings.TrimSpace(cfg.ManagedStage)
	cfg.OutDir = strings.TrimSpace(cfg.OutDir)

	if err := validateRequiredCLIFields(cfg); err != nil {
		return cliConfig{}, err
	}

	return validateParsedCLIConfig(cfg)
}

func validateRequiredCLIFields(cfg cliConfig) error {
	parsedBaseURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return fmt.Errorf("--base-url is invalid: %w", err)
	}
	if parsedBaseURL.Host == "" {
		return errors.New("--base-url must include a host")
	}
	if parsedBaseURL.Scheme != "https" && parsedBaseURL.Scheme != "http" {
		return errors.New("--base-url must use http or https")
	}
	return nil
}

func validateParsedCLIConfig(cfg cliConfig) (cliConfig, error) {
	switch {
	case cfg.LesserGitHubOwner == "":
		return cliConfig{}, errors.New("--lesser-github-owner is required")
	case cfg.LesserGitHubRepo == "":
		return cliConfig{}, errors.New("--lesser-github-repo is required")
	case cfg.LesserBodyGitHubOwner == "":
		return cliConfig{}, errors.New("--lesser-body-github-owner is required")
	case cfg.LesserBodyGitHubRepo == "":
		return cliConfig{}, errors.New("--lesser-body-github-repo is required")
	case cfg.ManagedStage == "":
		return cliConfig{}, errors.New("--managed-stage is required")
	default:
		return cfg, nil
	}
}

func runCertification(ctx context.Context, cfg cliConfig, httpClient *http.Client) (*certificationReport, error) {
	client := newCertificationClient(cfg, httpClient)
	report := newCertificationReport(cfg)

	lesserBodyTemplatePath, ok := runCompatibilityChecks(ctx, cfg, report)
	if !ok {
		return report, nil
	}

	allowFollowOns, ok := runLesserUpdate(ctx, client, cfg, report, lesserBodyTemplatePath)
	if !ok {
		return report, nil
	}

	if cfg.RequireLesserBody {
		appendLesserBodyEvidenceAndChecks(ctx, client, cfg, report, lesserBodyTemplatePath, allowFollowOns)
	}

	if cfg.RequireMCP {
		appendMCPEvidenceAndChecks(ctx, client, cfg, report, allowFollowOns)
	}

	report.OverallStatus = overallStatus(report.Checks)
	return report, nil
}

func newCertificationClient(cfg cliConfig, httpClient *http.Client) *certificationClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &certificationClient{
		baseURL: cfg.BaseURL,
		token:   cfg.Token,
		client:  httpClient,
	}
}

func newCertificationReport(cfg cliConfig) *certificationReport {
	return &certificationReport{
		SchemaVersion: certificationSchemaVersion,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		LesserHost: certificationTarget{
			BaseURL:      cfg.BaseURL,
			InstanceSlug: cfg.InstanceSlug,
		},
		RequestedRelease: certificationRequested{
			LesserVersion:     cfg.LesserVersion,
			LesserBodyVersion: cfg.LesserBodyVersion,
			RunLesser:         true,
			RunLesserBody:     cfg.RequireLesserBody,
			RunMCP:            cfg.RequireMCP,
		},
	}
}

func runCompatibilityChecks(ctx context.Context, cfg cliConfig, report *certificationReport) (string, bool) {
	if err := validateManagedLesserCompatibility(ctx, &http.Client{Timeout: 30 * time.Second}, cfg.LesserGitHubOwner, cfg.LesserGitHubRepo, cfg.LesserVersion); err != nil {
		report.Checks = append(report.Checks, certificationCheck{
			ID:     "compatibility_contract_valid",
			Status: statusFail,
			Detail: err.Error(),
		})
		report.OverallStatus = statusFail
		return "", false
	}
	report.Checks = append(report.Checks, certificationCheck{
		ID:     "compatibility_contract_valid",
		Status: statusPass,
		Detail: "requested release matches the published lesser-host managed compatibility contract",
	})

	if !cfg.RequireLesserBody && !cfg.RequireMCP {
		return "", true
	}

	if strings.TrimSpace(cfg.LesserBodyVersion) == "" {
		report.Checks = append(report.Checks, certificationCheck{
			ID:     "lesser_body_version_selected",
			Status: statusFail,
			Detail: "--lesser-body-version is required when lesser-body or MCP certification is requested",
		})
		report.OverallStatus = statusFail
		return "", false
	}

	report.Checks = append(report.Checks, certificationCheck{
		ID:     "lesser_body_version_selected",
		Status: statusPass,
		Detail: "requested lesser-body release " + cfg.LesserBodyVersion + " will be validated for managed certification",
	})

	if err := validateManagedLesserBodyCompatibility(
		ctx,
		&http.Client{Timeout: 30 * time.Second},
		cfg.LesserBodyGitHubOwner,
		cfg.LesserBodyGitHubRepo,
		cfg.LesserBodyVersion,
		cfg.ManagedStage,
	); err != nil {
		report.Checks = append(report.Checks, certificationCheck{
			ID:     "lesser_body_compatibility_contract_valid",
			Status: statusFail,
			Detail: err.Error(),
		})
		report.OverallStatus = statusFail
		return "", false
	}

	report.Checks = append(report.Checks, certificationCheck{
		ID:     "lesser_body_compatibility_contract_valid",
		Status: statusPass,
		Detail: "requested lesser-body release matches the published lesser-host managed compatibility contract",
	})

	templatePath, err := validateManagedLesserBodyTemplatePreflight(
		ctx,
		&http.Client{Timeout: 30 * time.Second},
		cfg.LesserBodyGitHubOwner,
		cfg.LesserBodyGitHubRepo,
		cfg.LesserBodyVersion,
		cfg.ManagedStage,
	)
	if err != nil {
		report.Checks = append(report.Checks, certificationCheck{
			ID:     "lesser_body_template_preflight_valid",
			Status: statusFail,
			Detail: err.Error(),
		})
		report.OverallStatus = statusFail
		return "", false
	}

	report.Checks = append(report.Checks, certificationCheck{
		ID:     "lesser_body_template_preflight_valid",
		Status: statusPass,
		Detail: "published template " + templatePath + " passed lesser-host managed body template preflight",
	})

	return templatePath, true
}

func runLesserUpdate(ctx context.Context, client *certificationClient, cfg cliConfig, report *certificationReport, lesserBodyTemplatePath string) (bool, bool) {
	startedJob, err := client.createLesserUpdate(ctx, cfg.InstanceSlug, cfg.LesserVersion)
	if err != nil {
		report.Checks = append(report.Checks, certificationCheck{
			ID:     "hosted_update_started",
			Status: statusFail,
			Detail: err.Error(),
		})
		report.OverallStatus = statusFail
		return false, false
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
		return false, false
	}

	lesserEvidence := certificationJobFromResponse(finalJob, cfg.InstanceSlug)
	report.Jobs = append(report.Jobs, lesserEvidence)
	coreCfg := cfg
	coreCfg.RequireLesserBody = false
	coreCfg.RequireMCP = false
	report.Checks = append(report.Checks, certificationChecksForManagedUpdate(finalJob, []certificationJob{lesserEvidence}, coreCfg, lesserBodyTemplatePath)...)

	allowFollowOns := strings.EqualFold(strings.TrimSpace(finalJob.Status), updateJobStatusOK)
	return allowFollowOns, true
}

func appendLesserBodyEvidenceAndChecks(
	ctx context.Context,
	client *certificationClient,
	cfg cliConfig,
	report *certificationReport,
	lesserBodyTemplatePath string,
	allowFollowOns bool,
) {
	templatePath := strings.TrimSpace(firstNonEmpty(lesserBodyTemplatePath, expectedLesserBodyTemplatePath(cfg.ManagedStage)))

	if !allowFollowOns {
		report.Checks = append(report.Checks,
			certificationCheck{
				ID:     "lesser_body_template_changeset_valid",
				Status: statusFail,
				Detail: "lesser-body deploy was blocked by the failed Lesser update",
			},
			certificationCheck{
				ID:     "lesser_body_completed",
				Status: statusFail,
				Detail: "lesser-body deploy was blocked by the failed Lesser update",
			},
			certificationCheck{
				ID:     "lesser_body_runner_visibility_present",
				Status: statusFail,
				Detail: "lesser-body deploy was not started",
			},
			certificationCheck{
				ID:     "lesser_body_receipt_key_defined",
				Status: statusFail,
				Detail: "lesser-body receipt key could not be derived from the managed update job",
			},
		)
		return
	}

	startedBodyJob, err := client.createLesserBodyUpdate(ctx, cfg.InstanceSlug, cfg.LesserBodyVersion, true)
	if err != nil {
		report.Checks = append(report.Checks,
			certificationCheck{ID: "lesser_body_template_changeset_valid", Status: statusFail, Detail: err.Error()},
			certificationCheck{ID: "lesser_body_completed", Status: statusFail, Detail: err.Error()},
			certificationCheck{ID: "lesser_body_runner_visibility_present", Status: statusFail, Detail: err.Error()},
			certificationCheck{ID: "lesser_body_receipt_key_defined", Status: statusFail, Detail: err.Error()},
		)
		return
	}

	finalBodyJob, err := client.waitForJob(ctx, cfg.InstanceSlug, startedBodyJob.ID, cfg.PollInterval)
	if err != nil {
		bodyEvidence := certificationJobFromResponse(startedBodyJob, cfg.InstanceSlug)
		bodyEvidence.TemplatePath = templatePath
		bodyEvidence.TemplateCertificationKey = deriveBodyTemplateCertificationKey(cfg.InstanceSlug, startedBodyJob.ID)
		bodyEvidence.TemplateVerificationMode = lesserBodyTemplateVerificationMode
		report.Jobs = append(report.Jobs, bodyEvidence)
		report.Checks = append(report.Checks,
			certificationCheck{ID: "lesser_body_template_changeset_valid", Status: statusFail, Detail: err.Error()},
			certificationCheck{ID: "lesser_body_completed", Status: statusFail, Detail: err.Error()},
			certificationCheck{ID: "lesser_body_runner_visibility_present", Status: statusFail, Detail: err.Error()},
			certificationCheck{ID: "lesser_body_receipt_key_defined", Status: statusFail, Detail: err.Error()},
		)
		return
	}

	bodyEvidence := certificationJobFromResponse(finalBodyJob, cfg.InstanceSlug)
	bodyEvidence.TemplatePath = templatePath
	bodyEvidence.TemplateCertificationKey = deriveBodyTemplateCertificationKey(cfg.InstanceSlug, finalBodyJob.ID)
	bodyEvidence.TemplateVerificationMode = lesserBodyTemplateVerificationMode
	report.Jobs = append(report.Jobs, bodyEvidence)

	report.Checks = append(report.Checks,
		certificationCheck{
			ID:     "lesser_body_template_changeset_valid",
			Status: checkStatusForBodyTemplateCertification(bodyEvidence),
			Detail: templateCertificationDetail(bodyEvidence, lesserBodyTemplatePath),
		},
		certificationCheck{
			ID:     "lesser_body_completed",
			Status: checkStatusForPhase(bodyEvidence),
			Detail: phaseCompletionDetail(updateJobKindBody, bodyEvidence),
		},
		certificationCheck{
			ID:     "lesser_body_runner_visibility_present",
			Status: checkStatusForValue(firstNonEmpty(bodyEvidence.BodyRunURL, bodyEvidence.RunURL)),
			Detail: valueDetail(firstNonEmpty(bodyEvidence.BodyRunURL, bodyEvidence.RunURL), "lesser-body run link was not preserved in the managed update evidence"),
		},
		certificationCheck{
			ID:     "lesser_body_receipt_key_defined",
			Status: checkStatusForReceiptKey(bodyEvidence),
			Detail: receiptDetail(bodyEvidence, "lesser-body"),
		},
	)
}

func appendMCPEvidenceAndChecks(
	ctx context.Context,
	client *certificationClient,
	cfg cliConfig,
	report *certificationReport,
	allowFollowOns bool,
) {
	if !allowFollowOns {
		report.Checks = append(report.Checks,
			certificationCheck{
				ID:     "mcp_wiring_completed",
				Status: statusFail,
				Detail: "MCP wiring was blocked by the failed Lesser update",
			},
			certificationCheck{
				ID:     "mcp_receipt_key_defined",
				Status: statusFail,
				Detail: "MCP receipt key could not be derived from the managed update job",
			},
		)
		return
	}

	startedMCPJob, err := client.createMCPUpdate(ctx, cfg.InstanceSlug, cfg.LesserBodyVersion)
	if err != nil {
		report.Checks = append(report.Checks,
			certificationCheck{ID: "mcp_wiring_completed", Status: statusFail, Detail: err.Error()},
			certificationCheck{ID: "mcp_receipt_key_defined", Status: statusFail, Detail: err.Error()},
		)
		return
	}

	finalMCPJob, err := client.waitForJob(ctx, cfg.InstanceSlug, startedMCPJob.ID, cfg.PollInterval)
	if err != nil {
		mcpEvidence := certificationJobFromResponse(startedMCPJob, cfg.InstanceSlug)
		report.Jobs = append(report.Jobs, mcpEvidence)
		report.Checks = append(report.Checks,
			certificationCheck{ID: "mcp_wiring_completed", Status: statusFail, Detail: err.Error()},
			certificationCheck{ID: "mcp_receipt_key_defined", Status: statusFail, Detail: err.Error()},
		)
		return
	}

	mcpEvidence := certificationJobFromResponse(finalMCPJob, cfg.InstanceSlug)
	report.Jobs = append(report.Jobs, mcpEvidence)

	report.Checks = append(report.Checks,
		certificationCheck{
			ID:     "mcp_wiring_completed",
			Status: checkStatusForPhase(mcpEvidence),
			Detail: phaseCompletionDetail(updateJobKindMCP, mcpEvidence),
		},
		certificationCheck{
			ID:     "mcp_receipt_key_defined",
			Status: checkStatusForReceiptKey(mcpEvidence),
			Detail: receiptDetail(mcpEvidence, "MCP"),
		},
	)
}

func certificationChecksForManagedUpdate(job updateJobResponse, evidence []certificationJob, cfg cliConfig, lesserBodyTemplatePath string) []certificationCheck {
	lesserEvidence := findCertificationJob(evidence, updateJobKindLesser)
	checks := []certificationCheck{{
		ID:     "receipt_key_defined",
		Status: checkStatusForReceiptKey(lesserEvidence),
		Detail: receiptDetail(lesserEvidence, "Lesser"),
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

	if strings.EqualFold(strings.TrimSpace(job.Status), updateJobStatusOK) {
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
	} else {
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
	}

	if cfg.RequireLesserBody {
		bodyEvidence := findCertificationJob(evidence, updateJobKindBody)
		checks = append(checks,
			certificationCheck{
				ID:     "lesser_body_template_changeset_valid",
				Status: checkStatusForBodyTemplateCertification(bodyEvidence),
				Detail: templateCertificationDetail(bodyEvidence, lesserBodyTemplatePath),
			},
			certificationCheck{
				ID:     "lesser_body_completed",
				Status: checkStatusForPhase(bodyEvidence),
				Detail: phaseCompletionDetail(updateJobKindBody, bodyEvidence),
			},
			certificationCheck{
				ID:     "lesser_body_runner_visibility_present",
				Status: checkStatusForValue(bodyEvidence.BodyRunURL),
				Detail: valueDetail(bodyEvidence.BodyRunURL, "lesser-body run link was not preserved in the managed update evidence"),
			},
			certificationCheck{
				ID:     "lesser_body_receipt_key_defined",
				Status: checkStatusForReceiptKey(bodyEvidence),
				Detail: receiptDetail(bodyEvidence, "lesser-body"),
			},
		)
	}

	if cfg.RequireMCP {
		mcpEvidence := findCertificationJob(evidence, updateJobKindMCP)
		checks = append(checks,
			certificationCheck{
				ID:     "mcp_wiring_completed",
				Status: checkStatusForPhase(mcpEvidence),
				Detail: phaseCompletionDetail(updateJobKindMCP, mcpEvidence),
			},
			certificationCheck{
				ID:     "mcp_receipt_key_defined",
				Status: checkStatusForReceiptKey(mcpEvidence),
				Detail: receiptDetail(mcpEvidence, "MCP"),
			},
		)
	}
	return checks
}

func certificationJobFromResponse(job updateJobResponse, slug string) certificationJob {
	version := strings.TrimSpace(job.LesserVersion)
	if version == "" {
		version = strings.TrimSpace(job.LesserBodyVersion)
	}
	return certificationJob{
		Kind:             firstNonEmpty(strings.TrimSpace(job.Kind), updateJobKindLesser),
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
		ReceiptKey:       deriveReceiptKey(firstNonEmpty(strings.TrimSpace(job.Kind), updateJobKindLesser), slug, job.ID),
		RequestedVersion: version,
	}
}

func findCertificationJob(jobs []certificationJob, kind string) certificationJob {
	for _, job := range jobs {
		if strings.TrimSpace(job.Kind) == kind {
			return job
		}
	}
	return certificationJob{Kind: kind}
}

func checkStatusForPhase(job certificationJob) string {
	if strings.TrimSpace(job.JobID) == "" {
		return statusFail
	}
	if strings.TrimSpace(job.Status) == updateJobStatusOK {
		return statusPass
	}
	return statusFail
}

func checkStatusForReceiptKey(job certificationJob) string {
	if strings.TrimSpace(job.ReceiptKey) == "" {
		return statusFail
	}
	return statusPass
}

func checkStatusForValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return statusFail
	}
	return statusPass
}

func checkStatusForBodyTemplateCertification(job certificationJob) string {
	if strings.TrimSpace(job.JobID) == "" {
		return statusFail
	}
	if strings.TrimSpace(job.TemplatePath) == "" {
		return statusFail
	}
	if strings.TrimSpace(job.TemplateCertificationKey) == "" {
		return statusFail
	}
	if strings.TrimSpace(job.TemplateVerificationMode) == "" {
		return statusFail
	}
	if bodyTemplateCertificationFailed(job) {
		return statusFail
	}
	status := strings.TrimSpace(job.Status)
	if status == updateJobStatusOK {
		return statusPass
	}
	if status == updateJobStatusError && strings.TrimSpace(job.ErrorMessage) != "" {
		return statusPass
	}
	return statusFail
}

func bodyTemplateCertificationFailed(job certificationJob) bool {
	errorMessage := strings.TrimSpace(job.ErrorMessage)
	if errorMessage == "" {
		return false
	}
	return strings.Contains(errorMessage, lesserBodyTemplateVerificationMode)
}

func receiptDetail(job certificationJob, label string) string {
	if strings.TrimSpace(job.ReceiptKey) != "" {
		return job.ReceiptKey
	}
	return label + " receipt key could not be derived from the managed update job"
}

func valueDetail(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func phaseCompletionDetail(kind string, job certificationJob) string {
	name := kind
	switch kind {
	case updateJobKindBody:
		name = "lesser-body"
	case updateJobKindMCP:
		name = "MCP"
	case updateJobKindLesser:
		name = "Lesser"
	}
	if strings.TrimSpace(job.JobID) == "" {
		return name + " phase evidence was not present in the managed update response"
	}
	if strings.TrimSpace(job.Status) == updateJobStatusOK {
		return name + " managed phase completed successfully"
	}
	if strings.TrimSpace(job.ErrorMessage) != "" {
		return job.ErrorMessage
	}
	return fmt.Sprintf("%s phase ended with status=%s step=%s", name, strings.TrimSpace(job.Status), strings.TrimSpace(job.Step))
}

func templateCertificationDetail(job certificationJob, templatePath string) string {
	templatePath = firstNonEmpty(job.TemplatePath, templatePath)
	if strings.TrimSpace(job.JobID) == "" {
		if templatePath == "" {
			return "lesser-body template certification evidence was not present in the managed update response"
		}
		return "lesser-body template certification evidence was not present for " + templatePath
	}
	if strings.TrimSpace(job.Status) != updateJobStatusOK && strings.TrimSpace(job.ErrorMessage) == "" {
		return fmt.Sprintf("lesser-body template certification evidence for %s did not preserve a terminal outcome", templatePath)
	}
	if !bodyTemplateCertificationFailed(job) {
		if strings.TrimSpace(job.Status) == updateJobStatusOK {
			return fmt.Sprintf(
				"published template %s passed %s and is recorded at %s",
				templatePath,
				firstNonEmpty(job.TemplateVerificationMode, lesserBodyTemplateVerificationMode),
				job.TemplateCertificationKey,
			)
		}
		return fmt.Sprintf(
			"published template %s passed %s and is recorded at %s before the later lesser-body phase outcome",
			templatePath,
			firstNonEmpty(job.TemplateVerificationMode, lesserBodyTemplateVerificationMode),
			job.TemplateCertificationKey,
		)
	}
	if strings.TrimSpace(job.ErrorMessage) != "" {
		return job.ErrorMessage
	}
	return fmt.Sprintf("lesser-body template certification ended with status=%s step=%s", strings.TrimSpace(job.Status), strings.TrimSpace(job.Step))
}

func deriveReceiptKey(kind string, slug string, jobID string) string {
	slug = strings.TrimSpace(slug)
	jobID = strings.TrimSpace(jobID)
	if slug == "" || jobID == "" {
		return ""
	}
	switch strings.TrimSpace(kind) {
	case updateJobKindBody:
		return fmt.Sprintf("managed/updates/%s/%s/body-state.json", slug, jobID)
	case updateJobKindMCP:
		return fmt.Sprintf("managed/updates/%s/%s/mcp-state.json", slug, jobID)
	default:
		return fmt.Sprintf("managed/updates/%s/%s/state.json", slug, jobID)
	}
}

func deriveBodyTemplateCertificationKey(slug string, jobID string) string {
	slug = strings.TrimSpace(slug)
	jobID = strings.TrimSpace(jobID)
	if slug == "" || jobID == "" {
		return ""
	}
	return fmt.Sprintf("managed/updates/%s/%s/body-template-certification.json", slug, jobID)
}

func expectedLesserBodyTemplatePath(stage string) string {
	stage = strings.TrimSpace(stage)
	if stage == "" {
		stage = "dev"
	}
	return fmt.Sprintf("lesser-body-managed-%s.template.json", stage)
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

	jsonPath := filepath.Join(cleanedOutDir, certificationJSONFileName)
	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	jsonBytes = append(jsonBytes, '\n')
	writeErr := os.WriteFile(jsonPath, jsonBytes, 0o600) //nolint:gosec // Evidence files are written only to the operator-selected local certification output directory.
	if writeErr != nil {
		return writeErr
	}

	markdownPath := filepath.Join(cleanedOutDir, certificationMarkdownFileName)
	writeErr = os.WriteFile(markdownPath, []byte(renderMarkdownSummary(report)), 0o600) //nolint:gosec // Evidence files are written only to the operator-selected local certification output directory.
	if writeErr != nil {
		return writeErr
	}

	bodyReport := buildLesserBodyCertificationReport(report)
	if bodyReport == nil {
		removeErr := removeIfExists(filepath.Join(cleanedOutDir, lesserBodyCertificationJSONFileName))
		if removeErr != nil {
			return removeErr
		}
		return removeIfExists(filepath.Join(cleanedOutDir, lesserBodyCertificationMarkdownFileName))
	}

	bodyJSONBytes, err := json.MarshalIndent(bodyReport, "", "  ")
	if err != nil {
		return err
	}
	bodyJSONBytes = append(bodyJSONBytes, '\n')
	writeErr = os.WriteFile(filepath.Join(cleanedOutDir, lesserBodyCertificationJSONFileName), bodyJSONBytes, 0o600) //nolint:gosec // Evidence files are written only to the operator-selected local certification output directory.
	if writeErr != nil {
		return writeErr
	}
	writeErr = os.WriteFile(filepath.Join(cleanedOutDir, lesserBodyCertificationMarkdownFileName), []byte(renderLesserBodyMarkdownSummary(bodyReport)), 0o600) //nolint:gosec // Evidence files are written only to the operator-selected local certification output directory.
	if writeErr != nil {
		return writeErr
	}
	return nil
}

func buildLesserBodyCertificationReport(report *certificationReport) *lesserBodyCertificationReport {
	if report == nil || !report.RequestedRelease.RunLesserBody {
		return nil
	}

	bodyChecks := make([]certificationCheck, 0, len(report.Checks))
	for _, check := range report.Checks {
		if strings.HasPrefix(strings.TrimSpace(check.ID), "lesser_body_") {
			bodyChecks = append(bodyChecks, check)
		}
	}
	if len(bodyChecks) == 0 {
		bodyChecks = append(bodyChecks, certificationCheck{
			ID:     "lesser_body_evidence_present",
			Status: statusFail,
			Detail: "body-enabled certification did not emit lesser-body checks",
		})
	}

	bodyJob := findCertificationJob(report.Jobs, updateJobKindBody)
	if strings.TrimSpace(bodyJob.JobID) == "" {
		bodyJob = certificationJob{
			Kind:             updateJobKindBody,
			Status:           updateJobStatusError,
			Step:             "evidence.missing",
			ErrorCode:        "lesser_body_evidence_missing",
			ErrorMessage:     "lesser-body phase evidence was not present in the managed certification report",
			RequestedVersion: strings.TrimSpace(report.RequestedRelease.LesserBodyVersion),
		}
		if failedCheck := firstFailingCheck(bodyChecks); failedCheck != nil {
			bodyJob.Step = "preflight.failed"
			bodyJob.ErrorCode = "lesser_body_certification_failed"
			bodyJob.ErrorMessage = strings.TrimSpace(failedCheck.Detail)
		}
	}

	return &lesserBodyCertificationReport{
		SchemaVersion:    lesserBodyCertificationSchemaVersion,
		GeneratedAt:      report.GeneratedAt,
		LesserHost:       report.LesserHost,
		RequestedRelease: report.RequestedRelease,
		Checks:           bodyChecks,
		Job:              bodyJob,
		OverallStatus:    overallStatus(bodyChecks),
	}
}

func firstFailingCheck(checks []certificationCheck) *certificationCheck {
	for i := range checks {
		if strings.TrimSpace(checks[i].Status) == statusFail {
			return &checks[i]
		}
	}
	return nil
}

func renderMarkdownSummary(report *certificationReport) string {
	var b strings.Builder
	writeMarkdownHeader(&b, report)
	writeMarkdownChecks(&b, report.Checks)
	writeMarkdownJobs(&b, report.Jobs)
	return b.String()
}

func renderLesserBodyMarkdownSummary(report *lesserBodyCertificationReport) string {
	var b strings.Builder
	b.WriteString("# lesser-body managed certification\n\n")
	writeMarkdownBullet(&b, "Generated at", report.GeneratedAt)
	writeMarkdownBullet(&b, "Base URL", report.LesserHost.BaseURL)
	writeMarkdownBullet(&b, "Instance slug", report.LesserHost.InstanceSlug)
	writeMarkdownBullet(&b, "Lesser version", report.RequestedRelease.LesserVersion)
	writeMarkdownBullet(&b, "lesser-body version", report.RequestedRelease.LesserBodyVersion)
	writeMarkdownBullet(&b, "Overall status", report.OverallStatus)
	b.WriteString("\n## Checks\n\n")
	for _, check := range report.Checks {
		b.WriteString("- `" + safeMarkdownText(check.ID) + "`: `" + safeMarkdownText(check.Status) + "`")
		if strings.TrimSpace(check.Detail) != "" {
			b.WriteString(" - " + safeMarkdownText(check.Detail))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n## Job\n\n")
	writeMarkdownJob(&b, report.Job)
	return b.String()
}

func writeMarkdownHeader(b *strings.Builder, report *certificationReport) {
	b.WriteString("# Managed release certification\n\n")
	writeMarkdownBullet(b, "Generated at", report.GeneratedAt)
	writeMarkdownBullet(b, "Base URL", report.LesserHost.BaseURL)
	writeMarkdownBullet(b, "Instance slug", report.LesserHost.InstanceSlug)
	writeMarkdownBullet(b, "Lesser version", report.RequestedRelease.LesserVersion)
	if strings.TrimSpace(report.RequestedRelease.LesserBodyVersion) != "" {
		writeMarkdownBullet(b, "lesser-body version", report.RequestedRelease.LesserBodyVersion)
	}
	writeMarkdownBullet(b, "Require lesser-body", fmt.Sprintf("%t", report.RequestedRelease.RunLesserBody))
	writeMarkdownBullet(b, "Require MCP", fmt.Sprintf("%t", report.RequestedRelease.RunMCP))
	writeMarkdownBullet(b, "Overall status", report.OverallStatus)
	b.WriteString("\n")
}

func writeMarkdownChecks(b *strings.Builder, checks []certificationCheck) {
	b.WriteString("## Checks\n\n")
	for _, check := range checks {
		b.WriteString("- `" + safeMarkdownText(check.ID) + "`: `" + safeMarkdownText(check.Status) + "`")
		if strings.TrimSpace(check.Detail) != "" {
			b.WriteString(" - " + safeMarkdownText(check.Detail))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func writeMarkdownJobs(b *strings.Builder, jobs []certificationJob) {
	b.WriteString("## Jobs\n\n")
	for _, job := range jobs {
		writeMarkdownJob(b, job)
	}
}

func writeMarkdownJob(b *strings.Builder, job certificationJob) {
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

	writeMarkdownField(b, "run_url", job.RunURL)
	writeMarkdownDistinctField(b, "deploy_run_url", job.DeployRunURL, job.RunURL)
	writeMarkdownDistinctField(b, "body_run_url", job.BodyRunURL, job.RunURL)
	writeMarkdownDistinctField(b, "mcp_run_url", job.MCPRunURL, job.RunURL)
	writeMarkdownField(b, "template_path", job.TemplatePath)
	writeMarkdownField(b, "template_certification_key", job.TemplateCertificationKey)
	writeMarkdownField(b, "template_verification_mode", job.TemplateVerificationMode)
	writeMarkdownField(b, "note", job.Note)
	if job.ErrorCode != "" || job.ErrorMessage != "" {
		writeMarkdownField(b, "failure", strings.TrimSpace(job.ErrorCode)+" "+strings.TrimSpace(job.ErrorMessage))
	}
}

func writeMarkdownBullet(b *strings.Builder, label string, value string) {
	b.WriteString("- " + label + ": `" + safeMarkdownText(value) + "`\n")
}

func writeMarkdownField(b *strings.Builder, label string, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	b.WriteString("  " + label + ": " + safeMarkdownText(value) + "\n")
}

func writeMarkdownDistinctField(b *strings.Builder, label string, value string, primary string) {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) == strings.TrimSpace(primary) {
		return
	}
	writeMarkdownField(b, label, value)
}

func safeMarkdownText(value string) string {
	escaped := html.EscapeString(strings.TrimSpace(value))
	escaped = strings.ReplaceAll(escaped, "`", "'")
	escaped = strings.ReplaceAll(escaped, "\r", " ")
	escaped = strings.ReplaceAll(escaped, "\n", " ")
	return escaped
}

func removeIfExists(path string) error {
	err := os.Remove(path) //nolint:gosec // Evidence files are removed only from the operator-selected local output directory.
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (c *certificationClient) createLesserUpdate(ctx context.Context, slug string, lesserVersion string) (updateJobResponse, error) {
	return c.createUpdate(ctx, slug, createUpdateJobRequest{
		LesserVersion: strings.TrimSpace(lesserVersion),
	})
}

func (c *certificationClient) createLesserBodyUpdate(ctx context.Context, slug string, lesserBodyVersion string, templateCertify bool) (updateJobResponse, error) {
	return c.createUpdate(ctx, slug, createUpdateJobRequest{
		LesserBodyVersion:   strings.TrimSpace(lesserBodyVersion),
		BodyOnly:            true,
		BodyTemplateCertify: templateCertify,
	})
}

func (c *certificationClient) createMCPUpdate(ctx context.Context, slug string, lesserBodyVersion string) (updateJobResponse, error) {
	return c.createUpdate(ctx, slug, createUpdateJobRequest{
		LesserBodyVersion: strings.TrimSpace(lesserBodyVersion),
		MCPOnly:           true,
	})
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
			if status == updateJobStatusOK || status == updateJobStatusError {
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
