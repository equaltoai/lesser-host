package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	readinessSchemaVersion     = 1
	defaultProjectOrg          = "equaltoai"
	defaultProjectNumber       = 17
	certificationStatusPass    = "pass"
	certificationStatusFail    = "fail"
	readinessStatusCertified   = "certified"
	readinessStatusBlocked     = "blocked"
	rolloutReadinessReady      = "ready"
	rolloutReadinessBlocked    = "blocked"
	readinessLabelCertified    = "managed-release-certified"
	readinessLabelBlocked      = "managed-release-blocked"
	readinessCommentMarker     = "<!-- managed-release-readiness -->"
	defaultGitHubAPIBaseURL    = "https://api.github.com"
	defaultCertificationReport = "gov-infra/evidence/managed-release-certification/managed-release-certification.json"
	defaultReadinessOutDir     = "gov-infra/evidence/managed-release-certification"
	defaultLesserBodyEvidence  = "managed-release-certification-lesser-body.json"
	readinessStatusNotRequired = "not_required"
)

type cliConfig struct {
	ReportPath    string
	OutDir        string
	ProjectOrg    string
	ProjectNumber int
	IssueTargets  string
	GitHubToken   string
	GitHubAPIBase string
}

type readinessReport struct {
	SchemaVersion                 int                    `json:"schema_version"`
	GeneratedAt                   string                 `json:"generated_at"`
	Project                       readinessProject       `json:"project"`
	LesserHost                    certificationTarget    `json:"lesser_host"`
	RequestedRelease              certificationRequested `json:"requested_release"`
	SourceReportPath              string                 `json:"source_report_path"`
	LesserBodyEvidencePath        string                 `json:"lesser_body_evidence_path,omitempty"`
	LesserBodyCertificationStatus string                 `json:"lesser_body_certification_status,omitempty"`
	CertificationStatus           string                 `json:"certification_status"`
	RolloutReadiness              string                 `json:"rollout_readiness"`
	BlockingChecks                []string               `json:"blocking_checks,omitempty"`
	IssueTargets                  []readinessIssueTarget `json:"issue_targets,omitempty"`
}

type readinessProject struct {
	Org    string `json:"org"`
	Number int    `json:"number"`
}

type readinessIssueTarget struct {
	RepoFullName string `json:"repo_full_name"`
	IssueNumber  int    `json:"issue_number"`
	AppliedLabel string `json:"applied_label,omitempty"`
	RemovedLabel string `json:"removed_label,omitempty"`
	CommentURL   string `json:"comment_url,omitempty"`
}

type certificationReport struct {
	SchemaVersion    int                    `json:"schema_version"`
	GeneratedAt      string                 `json:"generated_at"`
	LesserHost       certificationTarget    `json:"lesser_host"`
	RequestedRelease certificationRequested `json:"requested_release"`
	Checks           []certificationCheck   `json:"checks"`
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

type certificationJob struct {
	Kind             string `json:"kind"`
	JobID            string `json:"job_id"`
	Status           string `json:"status"`
	Step             string `json:"step"`
	RequestedVersion string `json:"requested_version,omitempty"`
}

type issueTarget struct {
	RepoFullName string
	IssueNumber  int
}

type githubIssueComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	URL  string `json:"html_url"`
}

type githubLabelRequest struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

type githubAddLabelsRequest struct {
	Labels []string `json:"labels"`
}

type githubIssueCommentRequest struct {
	Body string `json:"body"`
}

type githubClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type githubResponse struct {
	StatusCode int
	Body       []byte
}

func main() {
	cfg, err := parseCLI(os.Args[1:])
	if err != nil {
		failf("%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report, err := runReadiness(ctx, cfg, &http.Client{Timeout: 15 * time.Second})
	if err != nil {
		failf("%v", err)
	}
	if err := writeReadinessOutputs(cfg.OutDir, report); err != nil {
		failf("write readiness outputs: %v", err)
	}
}

func parseCLI(args []string) (cliConfig, error) {
	fs := flag.NewFlagSet("managed-release-readiness", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	cfg := cliConfig{}
	fs.StringVar(&cfg.ReportPath, "report", defaultCertificationReport, "path to managed-release-certification.json")
	fs.StringVar(&cfg.OutDir, "out-dir", defaultReadinessOutDir, "output directory for readiness evidence")
	fs.StringVar(&cfg.ProjectOrg, "project-org", defaultProjectOrg, "GitHub org that owns the rollout project")
	fs.IntVar(&cfg.ProjectNumber, "project-number", defaultProjectNumber, "GitHub project number that tracks rollout readiness")
	fs.StringVar(&cfg.IssueTargets, "issue-targets", "", "comma-separated issue targets like equaltoai/lesser-host#96")
	fs.StringVar(&cfg.GitHubAPIBase, "github-api-base", defaultGitHubAPIBaseURL, "GitHub API base URL")

	if err := fs.Parse(args); err != nil {
		return cliConfig{}, err
	}

	cfg.ReportPath = strings.TrimSpace(cfg.ReportPath)
	cfg.OutDir = strings.TrimSpace(cfg.OutDir)
	cfg.ProjectOrg = strings.TrimSpace(cfg.ProjectOrg)
	cfg.IssueTargets = strings.TrimSpace(cfg.IssueTargets)
	cfg.GitHubToken = strings.TrimSpace(os.Getenv("GH_TOKEN"))
	cfg.GitHubAPIBase = strings.TrimSpace(cfg.GitHubAPIBase)

	if cfg.ReportPath == "" {
		return cliConfig{}, errors.New("--report is required")
	}
	if cfg.OutDir == "" {
		return cliConfig{}, errors.New("--out-dir is required")
	}
	if cfg.ProjectOrg == "" {
		return cliConfig{}, errors.New("--project-org is required")
	}
	if cfg.ProjectNumber <= 0 {
		return cliConfig{}, errors.New("--project-number must be positive")
	}
	if cfg.GitHubAPIBase == "" {
		return cliConfig{}, errors.New("--github-api-base is required")
	}
	parsedAPIBase, err := url.Parse(cfg.GitHubAPIBase)
	if err != nil {
		return cliConfig{}, fmt.Errorf("--github-api-base is invalid: %w", err)
	}
	if parsedAPIBase.Scheme != "https" && parsedAPIBase.Scheme != "http" {
		return cliConfig{}, errors.New("--github-api-base must use http or https")
	}
	if parsedAPIBase.Host == "" {
		return cliConfig{}, errors.New("--github-api-base must include a host")
	}
	return cfg, nil
}

func runReadiness(ctx context.Context, cfg cliConfig, httpClient *http.Client) (*readinessReport, error) {
	certification, err := loadCertificationReport(cfg.ReportPath)
	if err != nil {
		return nil, err
	}

	lesserBodyEvidence, lesserBodyEvidencePath, bodyEvidenceErr := loadLesserBodyCertificationReport(cfg.ReportPath, certification)
	report, err := buildReadinessReport(certification, lesserBodyEvidence, lesserBodyEvidencePath, bodyEvidenceErr, cfg)
	if err != nil {
		return nil, err
	}

	targets, err := parseIssueTargets(cfg.IssueTargets)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 || cfg.GitHubToken == "" {
		return report, nil
	}

	gh := &githubClient{
		baseURL: strings.TrimRight(cfg.GitHubAPIBase, "/"),
		token:   cfg.GitHubToken,
		client:  httpClient,
	}
	if gh.client == nil {
		gh.client = &http.Client{Timeout: 15 * time.Second}
	}

	syncedTargets, err := syncIssueTargets(ctx, gh, report, targets)
	if err != nil {
		return nil, err
	}
	report.IssueTargets = syncedTargets
	return report, nil
}

func loadCertificationReport(path string) (*certificationReport, error) {
	cleanedPath := filepath.Clean(strings.TrimSpace(path))
	if cleanedPath == "." || cleanedPath == "" {
		return nil, errors.New("certification report path is required")
	}
	raw, err := os.ReadFile(cleanedPath) //nolint:gosec // The certification report path is an operator-provided local evidence file.
	if err != nil {
		return nil, err
	}
	var parsed certificationReport
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func loadLesserBodyCertificationReport(certificationReportPath string, certification *certificationReport) (*lesserBodyCertificationReport, string, error) {
	if certification == nil || !certification.RequestedRelease.RunLesserBody {
		return nil, "", nil
	}

	cleanedPath := filepath.Clean(strings.TrimSpace(certificationReportPath))
	bodyPath := filepath.Join(filepath.Dir(cleanedPath), defaultLesserBodyEvidence)
	raw, err := os.ReadFile(bodyPath) //nolint:gosec // The readiness workflow reads the sibling evidence file from the operator-selected certification output directory.
	if err != nil {
		return nil, bodyPath, err
	}
	var parsed lesserBodyCertificationReport
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, bodyPath, err
	}
	return &parsed, bodyPath, nil
}

func buildReadinessReport(certification *certificationReport, lesserBodyEvidence *lesserBodyCertificationReport, lesserBodyEvidencePath string, lesserBodyEvidenceErr error, cfg cliConfig) (*readinessReport, error) {
	if certification == nil {
		return nil, errors.New("certification report is required")
	}

	status := readinessStatusCertified
	readiness := rolloutReadinessReady
	blockingChecks := failedCertificationChecks(certification.Checks)
	lesserBodyStatus, lesserBodyBlockingChecks := evaluateLesserBodyEvidence(certification, lesserBodyEvidence, lesserBodyEvidenceErr)
	blockingChecks = appendUniqueChecks(blockingChecks, lesserBodyBlockingChecks...)
	if strings.TrimSpace(certification.OverallStatus) != certificationStatusPass || len(blockingChecks) > 0 {
		status = readinessStatusBlocked
		readiness = rolloutReadinessBlocked
	}

	return &readinessReport{
		SchemaVersion:                 readinessSchemaVersion,
		GeneratedAt:                   time.Now().UTC().Format(time.RFC3339),
		Project:                       readinessProject{Org: cfg.ProjectOrg, Number: cfg.ProjectNumber},
		LesserHost:                    certification.LesserHost,
		RequestedRelease:              certification.RequestedRelease,
		SourceReportPath:              cfg.ReportPath,
		LesserBodyEvidencePath:        lesserBodyEvidencePath,
		LesserBodyCertificationStatus: lesserBodyStatus,
		CertificationStatus:           status,
		RolloutReadiness:              readiness,
		BlockingChecks:                blockingChecks,
	}, nil
}

func failedCertificationChecks(checks []certificationCheck) []string {
	var blocking []string
	for _, check := range checks {
		if strings.TrimSpace(check.Status) == certificationStatusFail {
			blocking = append(blocking, strings.TrimSpace(check.ID))
		}
	}
	return blocking
}

func evaluateLesserBodyEvidence(certification *certificationReport, lesserBodyEvidence *lesserBodyCertificationReport, lesserBodyEvidenceErr error) (string, []string) {
	if certification == nil || !certification.RequestedRelease.RunLesserBody {
		return readinessStatusNotRequired, nil
	}
	if lesserBodyEvidenceErr != nil {
		return readinessStatusBlocked, []string{"lesser_body_certification_evidence_present"}
	}
	if lesserBodyEvidence == nil {
		return readinessStatusBlocked, []string{"lesser_body_certification_evidence_present"}
	}
	if strings.TrimSpace(lesserBodyEvidence.RequestedRelease.LesserBodyVersion) != strings.TrimSpace(certification.RequestedRelease.LesserBodyVersion) ||
		strings.TrimSpace(lesserBodyEvidence.LesserHost.InstanceSlug) != strings.TrimSpace(certification.LesserHost.InstanceSlug) {
		return readinessStatusBlocked, []string{"lesser_body_certification_evidence_matches_requested_release"}
	}
	if strings.TrimSpace(lesserBodyEvidence.Job.Kind) != "lesser-body" {
		return readinessStatusBlocked, []string{"lesser_body_certification_evidence_present"}
	}
	if strings.TrimSpace(lesserBodyEvidence.OverallStatus) != certificationStatusPass {
		failedChecks := failedCertificationChecks(lesserBodyEvidence.Checks)
		if len(failedChecks) == 0 {
			failedChecks = []string{"lesser_body_certification_status"}
		}
		return readinessStatusBlocked, failedChecks
	}
	return readinessStatusCertified, nil
}

func appendUniqueChecks(existing []string, values ...string) []string {
	seen := make(map[string]struct{}, len(existing))
	out := append([]string{}, existing...)
	for _, value := range existing {
		seen[strings.TrimSpace(value)] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func parseIssueTargets(raw string) ([]issueTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	targets := make([]issueTarget, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		hashIndex := strings.LastIndex(part, "#")
		if hashIndex <= 0 || hashIndex == len(part)-1 {
			return nil, fmt.Errorf("invalid issue target %q", part)
		}
		number, err := strconv.Atoi(strings.TrimSpace(part[hashIndex+1:]))
		if err != nil || number <= 0 {
			return nil, fmt.Errorf("invalid issue target %q", part)
		}
		repo := strings.TrimSpace(part[:hashIndex])
		repoParts := strings.Split(repo, "/")
		if len(repoParts) != 2 || repoParts[0] == "" || repoParts[1] == "" {
			return nil, fmt.Errorf("invalid issue target %q", part)
		}
		targets = append(targets, issueTarget{RepoFullName: repo, IssueNumber: number})
	}
	return targets, nil
}

func syncIssueTargets(ctx context.Context, gh *githubClient, report *readinessReport, targets []issueTarget) ([]readinessIssueTarget, error) {
	appliedLabel, removedLabel := readinessLabels(report)
	commentBody := renderIssueComment(report)

	out := make([]readinessIssueTarget, 0, len(targets))
	for _, target := range targets {
		if err := gh.ensureLabel(ctx, target.RepoFullName, appliedLabel); err != nil {
			return nil, err
		}
		if err := gh.ensureLabel(ctx, target.RepoFullName, removedLabel); err != nil {
			return nil, err
		}
		if err := gh.addIssueLabel(ctx, target.RepoFullName, target.IssueNumber, appliedLabel); err != nil {
			return nil, err
		}
		if err := gh.removeIssueLabel(ctx, target.RepoFullName, target.IssueNumber, removedLabel); err != nil {
			return nil, err
		}
		commentURL, err := gh.upsertIssueComment(ctx, target.RepoFullName, target.IssueNumber, commentBody)
		if err != nil {
			return nil, err
		}
		out = append(out, readinessIssueTarget{
			RepoFullName: target.RepoFullName,
			IssueNumber:  target.IssueNumber,
			AppliedLabel: appliedLabel,
			RemovedLabel: removedLabel,
			CommentURL:   commentURL,
		})
	}
	return out, nil
}

func readinessLabels(report *readinessReport) (string, string) {
	if report != nil && report.CertificationStatus == readinessStatusCertified {
		return readinessLabelCertified, readinessLabelBlocked
	}
	return readinessLabelBlocked, readinessLabelCertified
}

func renderIssueComment(report *readinessReport) string {
	var b strings.Builder
	b.WriteString(readinessCommentMarker + "\n")
	b.WriteString("## Managed Release Readiness\n\n")
	b.WriteString("- Project: `" + report.Project.Org + "` `#" + strconv.Itoa(report.Project.Number) + "`\n")
	b.WriteString("- Certification status: `" + report.CertificationStatus + "`\n")
	b.WriteString("- Rollout readiness: `" + report.RolloutReadiness + "`\n")
	b.WriteString("- Lesser version: `" + strings.TrimSpace(report.RequestedRelease.LesserVersion) + "`\n")
	b.WriteString("- lesser-body certification: `" + strings.TrimSpace(report.LesserBodyCertificationStatus) + "`\n")
	if strings.TrimSpace(report.RequestedRelease.LesserBodyVersion) != "" {
		b.WriteString("- lesser-body version: `" + strings.TrimSpace(report.RequestedRelease.LesserBodyVersion) + "`\n")
	}
	b.WriteString("- Instance: `" + strings.TrimSpace(report.LesserHost.InstanceSlug) + "`\n")
	if len(report.BlockingChecks) == 0 {
		b.WriteString("- Blocking checks: none\n")
	} else {
		b.WriteString("- Blocking checks: `" + strings.Join(report.BlockingChecks, "`, `") + "`\n")
	}
	if strings.TrimSpace(report.LesserBodyEvidencePath) != "" {
		b.WriteString("- Evidence: `managed-release-certification.json`, `managed-release-certification-lesser-body.json`, `managed-release-readiness.json`\n")
	} else {
		b.WriteString("- Evidence: `managed-release-certification.json`, `managed-release-readiness.json`\n")
	}
	return b.String()
}

func writeReadinessOutputs(outDir string, report *readinessReport) error {
	if report == nil {
		return errors.New("report is required")
	}
	cleanedOutDir := filepath.Clean(strings.TrimSpace(outDir))
	if cleanedOutDir == "." || cleanedOutDir == "" {
		return errors.New("output directory is required")
	}
	if err := os.MkdirAll(cleanedOutDir, 0o755); err != nil { //nolint:gosec // Evidence output is an operator-selected local directory.
		return err
	}

	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	jsonBytes = append(jsonBytes, '\n')
	if err := os.WriteFile(filepath.Join(cleanedOutDir, "managed-release-readiness.json"), jsonBytes, 0o600); err != nil { //nolint:gosec // Evidence files are written only to the operator-selected local output directory.
		return err
	}
	if err := os.WriteFile(filepath.Join(cleanedOutDir, "managed-release-readiness.md"), []byte(renderReadinessMarkdown(report)), 0o600); err != nil { //nolint:gosec // Evidence files are written only to the operator-selected local output directory.
		return err
	}
	return nil
}

func renderReadinessMarkdown(report *readinessReport) string {
	var b strings.Builder
	b.WriteString("# Managed release readiness\n\n")
	b.WriteString("- Project: `" + report.Project.Org + "` `#" + strconv.Itoa(report.Project.Number) + "`\n")
	b.WriteString("- Certification status: `" + report.CertificationStatus + "`\n")
	b.WriteString("- Rollout readiness: `" + report.RolloutReadiness + "`\n")
	b.WriteString("- Lesser version: `" + strings.TrimSpace(report.RequestedRelease.LesserVersion) + "`\n")
	b.WriteString("- lesser-body certification: `" + strings.TrimSpace(report.LesserBodyCertificationStatus) + "`\n")
	if strings.TrimSpace(report.RequestedRelease.LesserBodyVersion) != "" {
		b.WriteString("- lesser-body version: `" + strings.TrimSpace(report.RequestedRelease.LesserBodyVersion) + "`\n")
	}
	if len(report.BlockingChecks) == 0 {
		b.WriteString("- Blocking checks: none\n")
	} else {
		b.WriteString("- Blocking checks: `" + strings.Join(report.BlockingChecks, "`, `") + "`\n")
	}
	if len(report.IssueTargets) > 0 {
		b.WriteString("\n## Synced issues\n\n")
		for _, target := range report.IssueTargets {
			b.WriteString("- `" + target.RepoFullName + "#" + strconv.Itoa(target.IssueNumber) + "` label=`" + target.AppliedLabel + "`")
			if strings.TrimSpace(target.CommentURL) != "" {
				b.WriteString(" comment=`" + target.CommentURL + "`")
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (g *githubClient) ensureLabel(ctx context.Context, repo string, name string) error {
	path := "/repos/" + repo + "/labels/" + url.PathEscape(name)
	resp, err := g.doJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("github label lookup failed for %s: HTTP %d: %s", name, resp.StatusCode, strings.TrimSpace(string(resp.Body)))
	}

	payload := githubLabelRequest{
		Name:        name,
		Color:       labelColor(name),
		Description: labelDescription(name),
	}
	resp, err = g.doJSON(ctx, http.MethodPost, "/repos/"+repo+"/labels", payload)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github label create failed for %s: HTTP %d: %s", name, resp.StatusCode, strings.TrimSpace(string(resp.Body)))
	}
	return nil
}

func (g *githubClient) addIssueLabel(ctx context.Context, repo string, issueNumber int, label string) error {
	resp, err := g.doJSON(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/labels", repo, issueNumber), githubAddLabelsRequest{
		Labels: []string{label},
	})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github add label failed for %s#%d: HTTP %d: %s", repo, issueNumber, resp.StatusCode, strings.TrimSpace(string(resp.Body)))
	}
	return nil
}

func (g *githubClient) removeIssueLabel(ctx context.Context, repo string, issueNumber int, label string) error {
	resp, err := g.doJSON(ctx, http.MethodDelete, fmt.Sprintf("/repos/%s/issues/%d/labels/%s", repo, issueNumber, url.PathEscape(label)), nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("github remove label failed for %s#%d: HTTP %d: %s", repo, issueNumber, resp.StatusCode, strings.TrimSpace(string(resp.Body)))
}

func (g *githubClient) upsertIssueComment(ctx context.Context, repo string, issueNumber int, body string) (string, error) {
	comments, err := g.listIssueComments(ctx, repo, issueNumber)
	if err != nil {
		return "", err
	}
	for _, comment := range comments {
		if strings.Contains(comment.Body, readinessCommentMarker) {
			return g.updateIssueComment(ctx, repo, comment.ID, body)
		}
	}
	return g.createIssueComment(ctx, repo, issueNumber, body)
}

func (g *githubClient) listIssueComments(ctx context.Context, repo string, issueNumber int) ([]githubIssueComment, error) {
	resp, err := g.doJSON(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/issues/%d/comments?per_page=100", repo, issueNumber), nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github list comments failed for %s#%d: HTTP %d: %s", repo, issueNumber, resp.StatusCode, strings.TrimSpace(string(resp.Body)))
	}
	var comments []githubIssueComment
	if err := json.Unmarshal(resp.Body, &comments); err != nil {
		return nil, err
	}
	return comments, nil
}

func (g *githubClient) createIssueComment(ctx context.Context, repo string, issueNumber int, body string) (string, error) {
	resp, err := g.doJSON(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, issueNumber), githubIssueCommentRequest{Body: body})
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github create comment failed for %s#%d: HTTP %d: %s", repo, issueNumber, resp.StatusCode, strings.TrimSpace(string(resp.Body)))
	}
	var comment githubIssueComment
	if err := json.Unmarshal(resp.Body, &comment); err != nil {
		return "", err
	}
	return strings.TrimSpace(comment.URL), nil
}

func (g *githubClient) updateIssueComment(ctx context.Context, repo string, commentID int64, body string) (string, error) {
	resp, err := g.doJSON(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/issues/comments/%d", repo, commentID), githubIssueCommentRequest{Body: body})
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github update comment failed for %s comment %d: HTTP %d: %s", repo, commentID, resp.StatusCode, strings.TrimSpace(string(resp.Body)))
	}
	var comment githubIssueComment
	if err := json.Unmarshal(resp.Body, &comment); err != nil {
		return "", err
	}
	return strings.TrimSpace(comment.URL), nil
}

func (g *githubClient) doJSON(ctx context.Context, method string, path string, payload any) (*githubResponse, error) {
	var bodyReader io.Reader
	if payload != nil {
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("User-Agent", "lesser-host-managed-release-readiness")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := g.client.Do(req) //nolint:gosec // Host is provided by CLI config for GitHub API use and defaults to api.github.com.
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &githubResponse{
		StatusCode: resp.StatusCode,
		Body:       raw,
	}, nil
}

func labelColor(name string) string {
	if name == readinessLabelCertified {
		return "2da44e"
	}
	return "d1242f"
}

func labelDescription(name string) string {
	if name == readinessLabelCertified {
		return "Managed release certification passed for project 17 readiness"
	}
	return "Managed release certification is blocked for project 17 readiness"
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
