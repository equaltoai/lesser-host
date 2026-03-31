package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testProjectOrg              = "equaltoai"
	testProjectNumber           = 17
	testReadinessRepo           = "equaltoai/lesser-host"
	testReadinessIssue          = 96
	testReadinessTarget         = "equaltoai/lesser-host#96"
	testBaseURL                 = "https://lab.lesser.host"
	testInstanceSlug            = "simulacrum"
	testLesserVersion           = "v1.2.6"
	testLesserBodyVersion       = "v0.2.3"
	testCertificationReport     = "managed-release-certification.json"
	testBodyCertificationReport = "managed-release-certification-lesser-body.json"
	testLabelsPath              = "/repos/equaltoai/lesser-host/labels"
	testIssueLabelsPath         = "/repos/equaltoai/lesser-host/issues/96/labels"
	testIssueCommentsPath       = "/repos/equaltoai/lesser-host/issues/96/comments"
)

func TestBuildReadinessReport_Certified(t *testing.T) {
	t.Parallel()

	report, err := buildReadinessReport(&certificationReport{
		LesserHost: certificationTarget{
			BaseURL:      testBaseURL,
			InstanceSlug: testInstanceSlug,
		},
		RequestedRelease: certificationRequested{
			LesserVersion: testLesserVersion,
		},
		Checks: []certificationCheck{
			{ID: "compatibility_contract_valid", Status: certificationStatusPass},
			{ID: "hosted_update_completed", Status: certificationStatusPass},
		},
		OverallStatus: certificationStatusPass,
	}, nil, "", nil, cliConfig{ProjectOrg: testProjectOrg, ProjectNumber: testProjectNumber, ReportPath: testCertificationReport})
	if err != nil {
		t.Fatalf("buildReadinessReport: %v", err)
	}
	if report.CertificationStatus != readinessStatusCertified || report.RolloutReadiness != rolloutReadinessReady {
		t.Fatalf("expected certified readiness, got %#v", report)
	}
	if len(report.BlockingChecks) != 0 {
		t.Fatalf("expected no blocking checks, got %#v", report.BlockingChecks)
	}
	if report.LesserBodyCertificationStatus != readinessStatusNotRequired {
		t.Fatalf("expected not_required body status, got %#v", report)
	}
}

func TestBuildReadinessReport_Blocked(t *testing.T) {
	t.Parallel()

	report, err := buildReadinessReport(&certificationReport{
		Checks: []certificationCheck{
			{ID: "compatibility_contract_valid", Status: certificationStatusFail},
			{ID: "hosted_update_completed", Status: certificationStatusPass},
		},
		OverallStatus: certificationStatusFail,
	}, nil, "", nil, cliConfig{ProjectOrg: testProjectOrg, ProjectNumber: testProjectNumber, ReportPath: testCertificationReport})
	if err != nil {
		t.Fatalf("buildReadinessReport: %v", err)
	}
	if report.CertificationStatus != readinessStatusBlocked || report.RolloutReadiness != rolloutReadinessBlocked {
		t.Fatalf("expected blocked readiness, got %#v", report)
	}
	if len(report.BlockingChecks) != 1 || report.BlockingChecks[0] != "compatibility_contract_valid" {
		t.Fatalf("expected blocking check, got %#v", report.BlockingChecks)
	}
}

func TestBuildReadinessReport_BlocksWhenBodyEvidenceMissing(t *testing.T) {
	t.Parallel()

	report, err := buildReadinessReport(&certificationReport{
		LesserHost: certificationTarget{
			BaseURL:      testBaseURL,
			InstanceSlug: testInstanceSlug,
		},
		RequestedRelease: certificationRequested{
			LesserVersion:     testLesserVersion,
			LesserBodyVersion: testLesserBodyVersion,
			RunLesser:         true,
			RunLesserBody:     true,
		},
		Checks: []certificationCheck{
			{ID: "compatibility_contract_valid", Status: certificationStatusPass},
		},
		OverallStatus: certificationStatusPass,
	}, nil, filepath.Join(t.TempDir(), testBodyCertificationReport), os.ErrNotExist, cliConfig{ProjectOrg: testProjectOrg, ProjectNumber: testProjectNumber, ReportPath: testCertificationReport})
	if err != nil {
		t.Fatalf("buildReadinessReport: %v", err)
	}
	if report.CertificationStatus != readinessStatusBlocked || report.LesserBodyCertificationStatus != readinessStatusBlocked {
		t.Fatalf("expected blocked readiness, got %#v", report)
	}
	if len(report.BlockingChecks) != 1 || report.BlockingChecks[0] != "lesser_body_certification_evidence_present" {
		t.Fatalf("expected body evidence blocking check, got %#v", report.BlockingChecks)
	}
}

func TestBuildReadinessReport_BlocksWhenBodyEvidenceFails(t *testing.T) {
	t.Parallel()

	bodyReport := &lesserBodyCertificationReport{
		SchemaVersion: 1,
		LesserHost: certificationTarget{
			BaseURL:      testBaseURL,
			InstanceSlug: testInstanceSlug,
		},
		RequestedRelease: certificationRequested{
			LesserVersion:     testLesserVersion,
			LesserBodyVersion: testLesserBodyVersion,
			RunLesser:         true,
			RunLesserBody:     true,
		},
		Checks: []certificationCheck{
			{ID: "lesser_body_template_changeset_valid", Status: certificationStatusPass},
			{ID: "lesser_body_completed", Status: certificationStatusFail},
		},
		Job: certificationJob{
			Kind:                     "lesser-body",
			JobID:                    "job-update-1",
			Status:                   "error",
			Step:                     "failed",
			TemplatePath:             "lesser-body-managed-dev.template.json",
			TemplateCertificationKey: "managed/updates/simulacrum/job-update-1/body-template-certification.json",
		},
		OverallStatus: certificationStatusFail,
	}

	report, err := buildReadinessReport(&certificationReport{
		LesserHost: certificationTarget{
			BaseURL:      testBaseURL,
			InstanceSlug: testInstanceSlug,
		},
		RequestedRelease: certificationRequested{
			LesserVersion:     testLesserVersion,
			LesserBodyVersion: testLesserBodyVersion,
			RunLesser:         true,
			RunLesserBody:     true,
		},
		Checks: []certificationCheck{
			{ID: "compatibility_contract_valid", Status: certificationStatusPass},
		},
		OverallStatus: certificationStatusPass,
	}, bodyReport, filepath.Join(t.TempDir(), testBodyCertificationReport), nil, cliConfig{ProjectOrg: testProjectOrg, ProjectNumber: testProjectNumber, ReportPath: testCertificationReport})
	if err != nil {
		t.Fatalf("buildReadinessReport: %v", err)
	}
	if report.LesserBodyCertificationStatus != readinessStatusBlocked {
		t.Fatalf("expected blocked body status, got %#v", report)
	}
	if len(report.BlockingChecks) != 1 || report.BlockingChecks[0] != "lesser_body_completed" {
		t.Fatalf("expected body completion blocking check, got %#v", report.BlockingChecks)
	}
}

func TestBuildReadinessReport_BlocksWhenBodyTemplateEvidenceMissing(t *testing.T) {
	t.Parallel()

	bodyReport := &lesserBodyCertificationReport{
		SchemaVersion: 1,
		LesserHost: certificationTarget{
			BaseURL:      testBaseURL,
			InstanceSlug: testInstanceSlug,
		},
		RequestedRelease: certificationRequested{
			LesserVersion:     testLesserVersion,
			LesserBodyVersion: testLesserBodyVersion,
			RunLesser:         true,
			RunLesserBody:     true,
		},
		Checks: []certificationCheck{
			{ID: "lesser_body_completed", Status: certificationStatusPass},
		},
		Job: certificationJob{
			Kind:   "lesser-body",
			JobID:  "job-update-1",
			Status: "ok",
			Step:   "done",
		},
		OverallStatus: certificationStatusPass,
	}

	report, err := buildReadinessReport(&certificationReport{
		LesserHost: certificationTarget{
			BaseURL:      testBaseURL,
			InstanceSlug: testInstanceSlug,
		},
		RequestedRelease: certificationRequested{
			LesserVersion:     testLesserVersion,
			LesserBodyVersion: testLesserBodyVersion,
			RunLesser:         true,
			RunLesserBody:     true,
		},
		Checks: []certificationCheck{
			{ID: "compatibility_contract_valid", Status: certificationStatusPass},
		},
		OverallStatus: certificationStatusPass,
	}, bodyReport, filepath.Join(t.TempDir(), testBodyCertificationReport), nil, cliConfig{ProjectOrg: testProjectOrg, ProjectNumber: testProjectNumber, ReportPath: testCertificationReport})
	if err != nil {
		t.Fatalf("buildReadinessReport: %v", err)
	}
	if report.LesserBodyCertificationStatus != readinessStatusBlocked {
		t.Fatalf("expected blocked body status, got %#v", report)
	}
	if len(report.BlockingChecks) != 1 || report.BlockingChecks[0] != "lesser_body_template_path_defined" {
		t.Fatalf("expected body template evidence blocking check, got %#v", report.BlockingChecks)
	}
}

func TestParseCLI_Success(t *testing.T) {
	t.Setenv("GH_TOKEN", "token")
	cfg, err := parseCLI([]string{
		"--report", testCertificationReport,
		"--out-dir", "out",
		"--project-org", testProjectOrg,
		"--project-number", "17",
		"--issue-targets", "equaltoai/lesser-host#96",
		"--github-api-base", "https://api.github.com",
	})
	if err != nil {
		t.Fatalf("parseCLI: %v", err)
	}
	if cfg.GitHubToken != "token" {
		t.Fatalf("expected GH_TOKEN to be captured, got %q", cfg.GitHubToken)
	}
	if cfg.ProjectOrg != testProjectOrg || cfg.ProjectNumber != testProjectNumber {
		t.Fatalf("unexpected parsed config: %#v", cfg)
	}
}

func TestParseCLI_InvalidGitHubAPIBase(t *testing.T) {
	t.Parallel()

	if _, err := parseCLI([]string{"--github-api-base", "ftp://api.github.com"}); err == nil {
		t.Fatal("expected invalid API base error")
	}
}

func TestLoadCertificationReport_InvalidJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, testCertificationReport)
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
	if _, err := loadCertificationReport(path); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestParseIssueTargets(t *testing.T) {
	t.Parallel()

	targets, err := parseIssueTargets("equaltoai/lesser-host#96, equaltoai/lesser#658")
	if err != nil {
		t.Fatalf("parseIssueTargets: %v", err)
	}
	if len(targets) != 2 || targets[0].RepoFullName != testReadinessRepo || targets[1].IssueNumber != 658 {
		t.Fatalf("unexpected targets: %#v", targets)
	}

	if _, err := parseIssueTargets("not-a-target"); err == nil {
		t.Fatal("expected invalid target error")
	}
}

func TestRenderIssueComment_IncludesBlockingChecksAndBodyVersion(t *testing.T) {
	t.Parallel()

	comment := renderIssueComment(&readinessReport{
		Project:                       readinessProject{Org: testProjectOrg, Number: testProjectNumber},
		LesserHost:                    certificationTarget{InstanceSlug: testInstanceSlug},
		RequestedRelease:              certificationRequested{LesserVersion: testLesserVersion, LesserBodyVersion: testLesserBodyVersion},
		LesserBodyCertificationStatus: readinessStatusBlocked,
		LesserBodyEvidencePath:        filepath.Join(t.TempDir(), testBodyCertificationReport),
		LesserBodyTemplatePath:        "lesser-body-managed-dev.template.json",
		LesserBodyTemplateEvidenceKey: "managed/updates/simulacrum/job-update-1/body-template-certification.json",
		CertificationStatus:           readinessStatusBlocked,
		RolloutReadiness:              rolloutReadinessBlocked,
		BlockingChecks:                []string{"compatibility_contract_valid", "hosted_update_completed"},
	})
	if !strings.Contains(comment, "lesser-body version: `"+testLesserBodyVersion+"`") {
		t.Fatalf("expected lesser-body version in comment, got %q", comment)
	}
	if !strings.Contains(comment, "lesser-body certification: `blocked`") {
		t.Fatalf("expected lesser-body certification status in comment, got %q", comment)
	}
	if !strings.Contains(comment, "lesser-body template: `lesser-body-managed-dev.template.json`") {
		t.Fatalf("expected lesser-body template path in comment, got %q", comment)
	}
	if !strings.Contains(comment, "lesser-body template evidence: `managed/updates/simulacrum/job-update-1/body-template-certification.json`") {
		t.Fatalf("expected lesser-body template evidence key in comment, got %q", comment)
	}
	if !strings.Contains(comment, "Blocking checks: `compatibility_contract_valid`, `hosted_update_completed`") {
		t.Fatalf("expected blocking checks in comment, got %q", comment)
	}
}

func TestWriteReadinessOutputs_WritesJSONAndMarkdown(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	report := &readinessReport{
		SchemaVersion:                 readinessSchemaVersion,
		GeneratedAt:                   "2026-03-30T00:00:00Z",
		Project:                       readinessProject{Org: testProjectOrg, Number: testProjectNumber},
		LesserHost:                    certificationTarget{BaseURL: testBaseURL, InstanceSlug: testInstanceSlug},
		RequestedRelease:              certificationRequested{LesserVersion: testLesserVersion},
		SourceReportPath:              testCertificationReport,
		LesserBodyCertificationStatus: readinessStatusNotRequired,
		CertificationStatus:           readinessStatusCertified,
		RolloutReadiness:              rolloutReadinessReady,
		IssueTargets: []readinessIssueTarget{{
			RepoFullName: testReadinessRepo,
			IssueNumber:  testReadinessIssue,
			AppliedLabel: readinessLabelCertified,
			CommentURL:   "https://github.com/equaltoai/lesser-host/issues/96#issuecomment-41",
		}},
	}

	if err := writeReadinessOutputs(outDir, report); err != nil {
		t.Fatalf("writeReadinessOutputs: %v", err)
	}

	markdownPath := filepath.Join(outDir, "managed-release-readiness.md")
	jsonPath := filepath.Join(outDir, "managed-release-readiness.json")
	markdownBytes, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	if !strings.Contains(string(markdownBytes), "Synced issues") {
		t.Fatalf("expected synced issues section, got %q", string(markdownBytes))
	}
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("expected readiness json: %v", err)
	}
}

func TestRunReadiness_WritesOutputsWithoutGitHubSync(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	reportPath := writeTestCertificationReport(t, outDir, certificationStatusPass, certificationStatusPass)

	report, err := runReadiness(context.Background(), cliConfig{
		ReportPath:    reportPath,
		OutDir:        outDir,
		ProjectOrg:    testProjectOrg,
		ProjectNumber: testProjectNumber,
	}, &http.Client{})
	if err != nil {
		t.Fatalf("runReadiness: %v", err)
	}
	if len(report.IssueTargets) != 0 {
		t.Fatalf("expected no synced targets, got %#v", report.IssueTargets)
	}

	if err := writeReadinessOutputs(outDir, report); err != nil {
		t.Fatalf("writeReadinessOutputs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "managed-release-readiness.json")); err != nil {
		t.Fatalf("expected readiness json: %v", err)
	}
}

func TestRunReadiness_SyncsIssueLabelsAndComments(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                string
		certificationStatus string
		scenario            readinessSyncScenario
		expectedLabel       string
		expectedCommentText string
		expectCommentCreate bool
	}{
		{
			name:                "blocked release updates existing marker",
			certificationStatus: certificationStatusFail,
			scenario: readinessSyncScenario{
				MissingLabel:    readinessLabelBlocked,
				ExistingLabel:   readinessLabelCertified,
				RemovedLabel:    readinessLabelCertified,
				RemoveStatus:    http.StatusNoContent,
				ExistingComment: true,
			},
			expectedLabel:       readinessLabelBlocked,
			expectedCommentText: "Rollout readiness: `blocked`",
			expectCommentCreate: false,
		},
		{
			name:                "certified release creates fresh marker",
			certificationStatus: certificationStatusPass,
			scenario: readinessSyncScenario{
				MissingLabel:    readinessLabelCertified,
				ExistingLabel:   readinessLabelBlocked,
				RemovedLabel:    readinessLabelBlocked,
				RemoveStatus:    http.StatusNotFound,
				ExistingComment: false,
			},
			expectedLabel:       readinessLabelCertified,
			expectedCommentText: "Rollout readiness: `ready`",
			expectCommentCreate: true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			report, recorder := runReadinessSyncScenario(t, tc.certificationStatus, tc.scenario)
			assertReadinessSyncOutcome(t, report, recorder, tc.expectedLabel, tc.scenario.RemovedLabel, tc.expectedCommentText, tc.expectCommentCreate)
		})
	}
}

type readinessSyncScenario struct {
	MissingLabel    string
	ExistingLabel   string
	RemovedLabel    string
	RemoveStatus    int
	ExistingComment bool
}

type readinessSyncRecorder struct {
	createdLabels []string
	addedLabels   []string
	deletedLabels []string
	createdBodies []string
	updatedBodies []string
}

func assertReadinessSyncOutcome(t *testing.T, report *readinessReport, recorder *readinessSyncRecorder, expectedLabel string, removedLabel string, expectedCommentText string, expectCommentCreate bool) {
	t.Helper()

	if len(report.IssueTargets) != 1 || report.IssueTargets[0].AppliedLabel != expectedLabel {
		t.Fatalf("unexpected issue targets: %#v", report.IssueTargets)
	}
	if len(recorder.createdLabels) != 1 || recorder.createdLabels[0] != expectedLabel {
		t.Fatalf("expected created label %q, got %#v", expectedLabel, recorder.createdLabels)
	}
	if len(recorder.addedLabels) != 1 || recorder.addedLabels[0] != expectedLabel {
		t.Fatalf("expected added label %q, got %#v", expectedLabel, recorder.addedLabels)
	}
	if len(recorder.deletedLabels) != 1 || recorder.deletedLabels[0] != removedLabel {
		t.Fatalf("expected removed label %q, got %#v", removedLabel, recorder.deletedLabels)
	}

	recordedBodies := recorder.updatedBodies
	if expectCommentCreate {
		recordedBodies = recorder.createdBodies
	}
	if len(recordedBodies) != 1 || !strings.Contains(recordedBodies[0], expectedCommentText) {
		t.Fatalf("expected comment evidence %q, got %#v", expectedCommentText, recordedBodies)
	}
}

func runReadinessSyncScenario(t *testing.T, certificationStatus string, scenario readinessSyncScenario) (*readinessReport, *readinessSyncRecorder) {
	t.Helper()

	outDir := t.TempDir()
	reportPath := writeTestCertificationReport(t, outDir, certificationStatus, certificationStatus)
	recorder := &readinessSyncRecorder{}
	server := newReadinessSyncServer(t, scenario, recorder)
	defer server.Close()

	report, err := runReadiness(context.Background(), cliConfig{
		ReportPath:    reportPath,
		OutDir:        outDir,
		ProjectOrg:    testProjectOrg,
		ProjectNumber: testProjectNumber,
		IssueTargets:  testReadinessTarget,
		GitHubToken:   "token",
		GitHubAPIBase: server.URL,
	}, server.Client())
	if err != nil {
		t.Fatalf("runReadiness: %v", err)
	}
	return report, recorder
}

func newReadinessSyncServer(t *testing.T, scenario readinessSyncScenario, recorder *readinessSyncRecorder) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case handleSyncLabelLookup(w, r, scenario):
			return
		case handleSyncLabelMutation(w, r, scenario, recorder):
			return
		case handleSyncCommentList(w, r, scenario):
			return
		case handleSyncCommentMutation(w, r, recorder):
			return
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
}

func handleSyncLabelLookup(w http.ResponseWriter, r *http.Request, scenario readinessSyncScenario) bool {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == testLabelsPath+"/"+scenario.MissingLabel:
		http.NotFound(w, r)
		return true
	case r.Method == http.MethodGet && r.URL.Path == testLabelsPath+"/"+scenario.ExistingLabel:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"` + scenario.ExistingLabel + `"}`))
		return true
	default:
		return false
	}
}

func handleSyncLabelMutation(w http.ResponseWriter, r *http.Request, scenario readinessSyncScenario, recorder *readinessSyncRecorder) bool {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == testLabelsPath:
		var payload githubLabelRequest
		_ = json.NewDecoder(r.Body).Decode(&payload)
		recorder.createdLabels = append(recorder.createdLabels, payload.Name)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"name":"` + payload.Name + `"}`))
		return true
	case r.Method == http.MethodPost && r.URL.Path == testIssueLabelsPath:
		var payload githubAddLabelsRequest
		_ = json.NewDecoder(r.Body).Decode(&payload)
		recorder.addedLabels = append(recorder.addedLabels, payload.Labels...)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
		return true
	case r.Method == http.MethodDelete && r.URL.Path == testIssueLabelsPath+"/"+scenario.RemovedLabel:
		recorder.deletedLabels = append(recorder.deletedLabels, scenario.RemovedLabel)
		w.WriteHeader(scenario.RemoveStatus)
		return true
	default:
		return false
	}
}

func handleSyncCommentList(w http.ResponseWriter, r *http.Request, scenario readinessSyncScenario) bool {
	if r.Method != http.MethodGet || r.URL.Path != testIssueCommentsPath {
		return false
	}
	w.WriteHeader(http.StatusOK)
	if scenario.ExistingComment {
		_, _ = w.Write([]byte(`[{"id":41,"body":"<!-- managed-release-readiness -->\nold body","html_url":"https://github.com/equaltoai/lesser-host/issues/96#issuecomment-41"}]`))
		return true
	}
	_, _ = w.Write([]byte(`[]`))
	return true
}

func handleSyncCommentMutation(w http.ResponseWriter, r *http.Request, recorder *readinessSyncRecorder) bool {
	switch {
	case r.Method == http.MethodPatch && r.URL.Path == "/repos/equaltoai/lesser-host/issues/comments/41":
		var payload githubIssueCommentRequest
		_ = json.NewDecoder(r.Body).Decode(&payload)
		recorder.updatedBodies = append(recorder.updatedBodies, payload.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/equaltoai/lesser-host/issues/96#issuecomment-41"}`))
		return true
	case r.Method == http.MethodPost && r.URL.Path == testIssueCommentsPath:
		var payload githubIssueCommentRequest
		_ = json.NewDecoder(r.Body).Decode(&payload)
		recorder.createdBodies = append(recorder.createdBodies, payload.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/equaltoai/lesser-host/issues/96#issuecomment-42"}`))
		return true
	default:
		return false
	}
}

func writeTestCertificationReport(t *testing.T, dir string, overallStatus string, checkStatus string) string {
	t.Helper()

	path := filepath.Join(dir, testCertificationReport)
	raw, err := json.Marshal(certificationReport{
		SchemaVersion: 1,
		GeneratedAt:   "2026-03-30T00:00:00Z",
		LesserHost: certificationTarget{
			BaseURL:      testBaseURL,
			InstanceSlug: testInstanceSlug,
		},
		RequestedRelease: certificationRequested{
			LesserVersion:     testLesserVersion,
			LesserBodyVersion: testLesserBodyVersion,
			RunLesser:         true,
			RunLesserBody:     true,
			RunMCP:            true,
		},
		Checks: []certificationCheck{{
			ID:     "compatibility_contract_valid",
			Status: checkStatus,
		}},
		OverallStatus: overallStatus,
	})
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	writeErr := os.WriteFile(path, raw, 0o600)
	if writeErr != nil {
		t.Fatalf("write certification report: %v", writeErr)
	}

	bodyPath := filepath.Join(dir, testBodyCertificationReport)
	bodyRaw, err := json.Marshal(lesserBodyCertificationReport{
		SchemaVersion: 1,
		GeneratedAt:   "2026-03-30T00:00:00Z",
		LesserHost: certificationTarget{
			BaseURL:      testBaseURL,
			InstanceSlug: testInstanceSlug,
		},
		RequestedRelease: certificationRequested{
			LesserVersion:     testLesserVersion,
			LesserBodyVersion: testLesserBodyVersion,
			RunLesser:         true,
			RunLesserBody:     true,
			RunMCP:            true,
		},
		Checks: []certificationCheck{{
			ID:     "lesser_body_template_changeset_valid",
			Status: checkStatus,
		}, {
			ID:     "lesser_body_completed",
			Status: checkStatus,
		}},
		Job: certificationJob{
			Kind:                     "lesser-body",
			JobID:                    "job-update-1",
			Status:                   map[string]string{certificationStatusPass: "ok", certificationStatusFail: "error"}[checkStatus],
			Step:                     "done",
			RequestedVersion:         testLesserBodyVersion,
			TemplatePath:             "lesser-body-managed-dev.template.json",
			TemplateCertificationKey: "managed/updates/simulacrum/job-update-1/body-template-certification.json",
		},
		OverallStatus: overallStatus,
	})
	if err != nil {
		t.Fatalf("marshal body report: %v", err)
	}
	writeErr = os.WriteFile(bodyPath, bodyRaw, 0o600)
	if writeErr != nil {
		t.Fatalf("write body certification report: %v", writeErr)
	}
	return path
}
