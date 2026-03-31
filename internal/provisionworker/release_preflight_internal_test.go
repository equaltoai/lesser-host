package provisionworker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type rewriteHostTransport struct {
	base *url.URL
	rt   http.RoundTripper
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = t.base.Scheme
	cloned.URL.Host = t.base.Host
	return t.rt.RoundTrip(cloned)
}

func newManagedReleaseTestClient(t *testing.T, handler http.Handler) *http.Client {
	t.Helper()

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	baseURL, err := url.Parse(ts.URL)
	require.NoError(t, err)

	baseClient := ts.Client()
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: rewriteHostTransport{
			base: baseURL,
			rt:   baseClient.Transport,
		},
	}
}

func newHappyManagedLesserReleaseClient(t *testing.T, versions ...string) *http.Client {
	t.Helper()

	if len(versions) == 0 {
		versions = []string{"v1.2.3", "v1.2.6"}
	}

	responses := map[string][]byte{}
	for _, version := range versions {
		version = strings.TrimSpace(version)
		if version == "" {
			continue
		}
		releasePath := fmt.Sprintf("/equaltoai/lesser/releases/download/%s/lesser-release.json", version)
		bundlePath := fmt.Sprintf("/equaltoai/lesser/releases/download/%s/lesser-lambda-bundle.json", version)
		responses[releasePath] = lesserReleaseManifestJSON(t, version)
		responses[bundlePath] = lesserBundleManifestJSON(t)
	}

	return newManagedReleaseTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := responses[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

func newHappyManagedLesserBodyReleaseClient(t *testing.T, stage string, versions ...string) *http.Client {
	t.Helper()

	if len(versions) == 0 {
		versions = []string{"v0.2.3"}
	}
	stage = normalizeManagedLesserStage(stage)
	if stage == "" {
		stage = managedStageDev
	}

	responses := map[string][]byte{}
	for _, version := range versions {
		version = strings.TrimSpace(version)
		if version == "" {
			continue
		}
		releasePath := fmt.Sprintf("/equaltoai/lesser-body/releases/download/%s/lesser-body-release.json", version)
		checksumsPath := fmt.Sprintf("/equaltoai/lesser-body/releases/download/%s/checksums.txt", version)
		templatePath := fmt.Sprintf("/equaltoai/lesser-body/releases/download/%s/lesser-body-managed-%s.template.json", version, stage)
		responses[releasePath] = lesserBodyReleaseManifestJSON(t, version, stage)
		responses[checksumsPath] = lesserBodyChecksumsTXT(stage, true)
		responses[templatePath] = lesserBodyTemplateJSON(t, stage)
	}

	return newManagedReleaseTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := responses[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".json") {
			w.Header().Set("Content-Type", "application/json")
		} else {
			w.Header().Set("Content-Type", "text/plain")
		}
		_, _ = w.Write(body)
	}))
}

func lesserReleaseManifestJSON(t *testing.T, version string) []byte {
	t.Helper()

	raw, err := json.Marshal(map[string]any{
		"schema":  1,
		"name":    "lesser",
		"version": version,
		"git_sha": "abc123",
		"artifacts": map[string]any{
			"receipt_schema_version": 2,
			"deploy_artifacts": map[string]any{
				"schema_version": 1,
				"lambda_bundle": map[string]any{
					"path":                    "lesser-lambda-bundle.tar.gz",
					"manifest_path":           "lesser-lambda-bundle.json",
					"manifest_kind":           "lesser.lambda_bundle_manifest",
					"manifest_schema_version": 1,
				},
			},
		},
	})
	require.NoError(t, err)
	return raw
}

func lesserBundleManifestJSON(t *testing.T) []byte {
	t.Helper()

	raw, err := json.Marshal(map[string]any{
		"kind":           "lesser.lambda_bundle_manifest",
		"schema_version": 1,
		"bundle": map[string]any{
			"path":   "lesser-lambda-bundle.tar.gz",
			"sha256": "bundle-sha",
		},
		"files": []map[string]any{
			{"path": "bin/api.zip", "sha256": "api-sha"},
			{"path": "bin/graphql.zip", "sha256": "graphql-sha"},
		},
	})
	require.NoError(t, err)
	return raw
}

func lesserBodyReleaseManifestJSON(t *testing.T, version string, stage string) []byte {
	t.Helper()

	if strings.TrimSpace(stage) == "" {
		stage = managedStageDev
	}
	templatePath := fmt.Sprintf("lesser-body-managed-%s.template.json", stage)
	raw, err := json.Marshal(map[string]any{
		"schema":  1,
		"name":    "lesser-body",
		"version": version,
		"git_sha": "bodysha",
		"artifacts": map[string]any{
			"checksums": map[string]any{
				"path":      "checksums.txt",
				"algorithm": "sha256",
			},
			"lambda_zip": map[string]any{
				"path":   "lesser-body.zip",
				"sha256": "zip-sha",
			},
			"deploy_manifest": map[string]any{
				"path":   "lesser-body-deploy.json",
				"sha256": "manifest-sha",
				"schema": 1,
			},
			"deploy_templates": map[string]any{
				stage: map[string]any{
					"path":   templatePath,
					"sha256": "template-sha",
					"format": "cloudformation-json",
				},
			},
			"deploy_script": map[string]any{
				"path":   "deploy-lesser-body-from-release.sh",
				"sha256": "script-sha",
			},
		},
		"deploy": map[string]any{
			"schema":                   1,
			"manifest_path":            "lesser-body-deploy.json",
			"template_selection":       "by_stage",
			"source_checkout_required": false,
			"npm_install_required":     false,
		},
	})
	require.NoError(t, err)
	return raw
}

func lesserBodyChecksumsTXT(stage string, includeReleaseChecksum bool) []byte {
	if strings.TrimSpace(stage) == "" {
		stage = managedStageDev
	}
	templatePath := fmt.Sprintf("lesser-body-managed-%s.template.json", stage)
	lines := []string{
		"zip-sha  lesser-body.zip",
		"manifest-sha  lesser-body-deploy.json",
		fmt.Sprintf("template-sha  %s", templatePath),
		"script-sha  deploy-lesser-body-from-release.sh",
	}
	if includeReleaseChecksum {
		lines = append(lines, "release-sha  lesser-body-release.json")
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func lesserBodyTemplateJSON(t *testing.T, stage string) []byte {
	t.Helper()

	stage = normalizeManagedLesserStage(stage)
	if stage == "" {
		stage = managedStageDev
	}
	raw, err := json.Marshal(map[string]any{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Parameters": map[string]any{
			"AppName": map[string]any{
				"Type": "String",
			},
			"BaseDomain": map[string]any{
				"Type": "String",
			},
			"LesserBodyCodeBucketName": map[string]any{
				"Type": "String",
			},
			"LesserBodyCodeObjectKey": map[string]any{
				"Type": "String",
			},
			"LesserStageDomainParamLookupParameter": map[string]any{
				"Type":    "String",
				"Default": fmt.Sprintf("/lesser/%s/domain", stage),
			},
		},
		"Resources": map[string]any{},
	})
	require.NoError(t, err)
	return raw
}

func lesserBodyTemplateJSONWithNonStringDefault(t *testing.T, stage string) []byte {
	t.Helper()

	stage = normalizeManagedLesserStage(stage)
	if stage == "" {
		stage = managedStageDev
	}
	raw, err := json.Marshal(map[string]any{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Parameters": map[string]any{
			"LesserStageDomainParamLookupParameter": map[string]any{
				"Type": "String",
				"Default": map[string]any{
					"Fn::Join": []any{"", []any{"/lesser/", stage, "/domain"}},
				},
			},
		},
		"Resources": map[string]any{},
	})
	require.NoError(t, err)
	return raw
}

func TestPreflightManagedLesserRelease_ValidatesReleaseAndBundleManifest(t *testing.T) {
	t.Parallel()

	const version = "v1.2.6"
	handler := http.NewServeMux()
	handler.HandleFunc("/equaltoai/lesser/releases/download/"+version+"/lesser-release.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserReleaseManifestJSON(t, version))
	})
	handler.HandleFunc("/equaltoai/lesser/releases/download/"+version+"/lesser-lambda-bundle.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBundleManifestJSON(t))
	})

	srv := &Server{
		cfg: config.Config{
			ManagedLesserGitHubOwner: "equaltoai",
			ManagedLesserGitHubRepo:  "lesser",
		},
		releaseHTTPClient: newManagedReleaseTestClient(t, handler),
	}

	require.NoError(t, srv.preflightManagedLesserRelease(context.Background(), version))
}

func TestPreflightManagedLesserBodyRelease_ValidatesReleaseManifestAndChecksums(t *testing.T) {
	t.Parallel()

	const version = "v0.2.3"
	const stage = managedStageDev
	handler := http.NewServeMux()
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-release.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyReleaseManifestJSON(t, version, stage))
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(lesserBodyChecksumsTXT(stage, true))
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-managed-"+stage+".template.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyTemplateJSON(t, stage))
	})

	srv := &Server{
		cfg: config.Config{
			Stage:                        "lab",
			ManagedLesserBodyGitHubOwner: "equaltoai",
			ManagedLesserBodyGitHubRepo:  "lesser-body",
		},
		releaseHTTPClient: newManagedReleaseTestClient(t, handler),
	}

	require.NoError(t, srv.preflightManagedLesserBodyRelease(context.Background(), version, stage))
}

func TestPreflightManagedLesserBodyRelease_RejectsNonStringTemplateDefaults(t *testing.T) {
	t.Parallel()

	const version = "v0.2.3"
	const stage = managedStageDev
	handler := http.NewServeMux()
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-release.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyReleaseManifestJSON(t, version, stage))
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(lesserBodyChecksumsTXT(stage, true))
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-managed-"+stage+".template.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyTemplateJSONWithNonStringDefault(t, stage))
	})

	srv := &Server{
		cfg: config.Config{
			Stage:                        "lab",
			ManagedLesserBodyGitHubOwner: "equaltoai",
			ManagedLesserBodyGitHubRepo:  "lesser-body",
		},
		releaseHTTPClient: newManagedReleaseTestClient(t, handler),
	}

	err := srv.preflightManagedLesserBodyRelease(context.Background(), version, stage)
	require.ErrorContains(t, err, "non-string Default")
	require.ErrorContains(t, err, "CloudFormation requires every Default member to be a string")
	require.ErrorContains(t, err, "lesser-body-managed-dev.template.json")
}

func TestAdvanceUpdateDeployReleasePreflightFailureFailsBeforeRunnerStarts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		step      string
		wantCode  string
		advanceFn func(*Server, context.Context, *models.UpdateJob, string, time.Time) (time.Duration, bool, error)
	}{
		{
			name:      "deploy",
			step:      updateStepDeployStart,
			wantCode:  "deploy_release_preflight_failed",
			advanceFn: (*Server).advanceUpdateDeployStart,
		},
		{
			name:      "mcp",
			step:      updateStepDeployMcpStart,
			wantCode:  "mcp_release_preflight_failed",
			advanceFn: (*Server).advanceUpdateDeployMcpStart,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st, db := newBranchTestStore()
			mockBranchInstanceLookup(t, db, managedUpdateRunnerInstance(), nil)

			fakeCB := &fakeCodebuild{
				startOut: &codebuild.StartBuildOutput{
					Build: &cbtypes.Build{Id: aws.String("run-should-not-start")},
				},
			}
			srv := &Server{
				cfg: config.Config{
					ManagedLesserGitHubOwner: "equaltoai",
					ManagedLesserGitHubRepo:  "lesser",
				},
				store:             st,
				releaseHTTPClient: newManagedReleaseTestClient(t, http.NotFoundHandler()),
				cb:                fakeCB,
			}

			job := managedUpdateRunnerJob(tc.step)
			delay, done, err := tc.advanceFn(srv, context.Background(), job, "req", time.Unix(1, 0).UTC())
			require.NoError(t, err)
			require.False(t, done)
			require.Zero(t, delay)
			require.Equal(t, models.UpdateJobStatusError, job.Status)
			require.Equal(t, updateStepFailed, job.Step)
			require.Equal(t, tc.wantCode, job.ErrorCode)
			require.Contains(t, job.ErrorMessage, "Lesser release preflight failed")
			require.Empty(t, job.RunID)
			require.Empty(t, fakeCB.startInputs)
		})
	}
}

func TestAdvanceUpdateBodyReleasePreflightFailureFailsBeforeRunnerStarts(t *testing.T) {
	t.Parallel()

	st, db := newBranchTestStore()
	mockBranchInstanceLookup(t, db, managedUpdateRunnerInstance(), nil)

	fakeCB := &fakeCodebuild{
		startOut: &codebuild.StartBuildOutput{
			Build: &cbtypes.Build{Id: aws.String("run-should-not-start")},
		},
	}
	const version = "v0.2.3"
	handler := http.NewServeMux()
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-release.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyReleaseManifestJSON(t, version, managedStageDev))
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(lesserBodyChecksumsTXT(managedStageDev, false))
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-managed-"+managedStageDev+".template.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyTemplateJSON(t, managedStageDev))
	})

	srv := &Server{
		cfg: config.Config{
			Stage:                        "lab",
			ManagedLesserBodyGitHubOwner: "equaltoai",
			ManagedLesserBodyGitHubRepo:  "lesser-body",
		},
		store:             st,
		releaseHTTPClient: newManagedReleaseTestClient(t, handler),
		cb:                fakeCB,
	}

	job := managedUpdateRunnerJob(updateStepBodyDeployStart)
	job.LesserBodyVersion = version
	delay, done, err := srv.advanceUpdateBodyDeployStart(context.Background(), job, "req", time.Unix(1, 0).UTC())
	require.NoError(t, err)
	require.False(t, done)
	require.Zero(t, delay)
	require.Equal(t, models.UpdateJobStatusError, job.Status)
	require.Equal(t, updateStepFailed, job.Step)
	require.Equal(t, "body_release_preflight_failed", job.ErrorCode)
	require.Equal(t, updatePhaseBody, job.FailedPhase)
	require.Equal(t, updatePhaseStatusFailed, job.BodyStatus)
	require.Contains(t, job.ErrorMessage, "lesser-body release preflight failed")
	require.Contains(t, job.BodyError, "checksum entry missing for lesser-body-release.json")
	require.Empty(t, job.RunID)
	require.Empty(t, fakeCB.startInputs)
}

func TestValidateManagedLesserLambdaBundleManifest_RequiresFileInventory(t *testing.T) {
	t.Parallel()

	err := validateManagedLesserLambdaBundleManifest(&managedLesserLambdaBundleManifest{
		Kind:          requiredLesserBundleManifestKind,
		SchemaVersion: requiredLesserBundleManifestSchema,
		Bundle: struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		}{
			Path:   requiredLesserBundleArchivePath,
			SHA256: "bundle-sha",
		},
	})
	require.ErrorContains(t, err, "lambda bundle file inventory is empty")
}
