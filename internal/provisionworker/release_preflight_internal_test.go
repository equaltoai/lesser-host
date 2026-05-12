package provisionworker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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
		"git_sha": "0123456789abcdef0123456789abcdef01234567",
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

func lesserBodyReleaseManifestSchema2JSON(t *testing.T, version string, stage string, capability string) []byte {
	t.Helper()

	if strings.TrimSpace(stage) == "" {
		stage = managedStageDev
	}
	templatePath := fmt.Sprintf("lesser-body-managed-%s.template.json", stage)
	if strings.TrimSpace(capability) == "" {
		capability = managedLesserBodyCapabilityAuxiliaryAssetsV1
	}
	raw, err := json.Marshal(map[string]any{
		"schema":  2,
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
				"schema": 2,
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
			"auxiliary_assets": []map[string]any{
				lesserBodyAuxiliaryAssetFixture(stage),
			},
		},
		"deploy": map[string]any{
			"schema":                   2,
			"manifest_path":            "lesser-body-deploy.json",
			"template_selection":       "by_stage",
			"source_checkout_required": false,
			"npm_install_required":     false,
			"required_capabilities":    []string{capability},
		},
	})
	require.NoError(t, err)
	return raw
}

func lesserBodyDeployManifestSchema2JSON(t *testing.T, stage string, capability string, assets []map[string]any) []byte {
	t.Helper()

	if strings.TrimSpace(stage) == "" {
		stage = managedStageDev
	}
	if strings.TrimSpace(capability) == "" {
		capability = managedLesserBodyCapabilityAuxiliaryAssetsV1
	}
	templatePath := fmt.Sprintf("lesser-body-managed-%s.template.json", stage)
	if assets == nil {
		assets = []map[string]any{lesserBodyAuxiliaryAssetFixture(stage)}
	}
	raw, err := json.Marshal(map[string]any{
		"schema":                2,
		"required_capabilities": []string{capability},
		"asset_prefix_default":  "releases/lesser-body/v0.2.3",
		"templates": map[string]any{
			stage: map[string]any{
				"path":   templatePath,
				"sha256": "template-sha",
				"bytes":  1234,
				"format": "cloudformation-json",
			},
		},
		"auxiliary_assets": assets,
	})
	require.NoError(t, err)
	return raw
}

func lesserBodyAuxiliaryAssetFixture(stage string) map[string]any {
	if strings.TrimSpace(stage) == "" {
		stage = managedStageDev
	}
	templatePath := fmt.Sprintf("lesser-body-managed-%s.template.json", stage)
	return map[string]any{
		"id":                 "apptheory-s3-auto-delete-objects-provider",
		"required":           true,
		"path":               "assets/mcp-stream-spill-auto-delete-provider.zip",
		"sha256":             "aux-sha",
		"bytes":              42,
		"content_type":       "application/zip",
		"s3_key":             "assets/mcp-stream-spill-auto-delete-provider.zip",
		"template_parameter": "AppTheoryAutoDeleteObjectsCodeObjectKey",
		"template_references": []map[string]any{{
			"stage":            stage,
			"template":         templatePath,
			"logical_id":       "CustomS3AutoDeleteObjectsCustomResourceProviderHandler9D90184F",
			"property_path":    "Properties.Code.S3Key",
			"bucket_parameter": "LesserBodyCodeBucketName",
			"key_parameter":    "AppTheoryAutoDeleteObjectsCodeObjectKey",
		}},
	}
}

func lesserBodyTemplateJSONWithAuxiliary(t *testing.T, stage string) []byte {
	t.Helper()

	raw, err := json.Marshal(map[string]any{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Parameters": map[string]any{
			"LesserBodyCodeBucketName": map[string]any{
				"Type": "String",
			},
			"LesserBodyCodeObjectKey": map[string]any{
				"Type": "String",
			},
			"AppTheoryAutoDeleteObjectsCodeObjectKey": map[string]any{
				"Type": "String",
			},
		},
		"Resources": map[string]any{
			"McpHandler03E6F2E1": map[string]any{
				"Type": "AWS::Lambda::Function",
				"Properties": map[string]any{
					"Code": map[string]any{
						"S3Bucket": map[string]any{"Ref": "LesserBodyCodeBucketName"},
						"S3Key":    map[string]any{"Ref": "LesserBodyCodeObjectKey"},
					},
				},
			},
			"CustomS3AutoDeleteObjectsCustomResourceProviderHandler9D90184F": map[string]any{
				"Type": "AWS::Lambda::Function",
				"Properties": map[string]any{
					"Code": map[string]any{
						"S3Bucket": map[string]any{"Ref": "LesserBodyCodeBucketName"},
						"S3Key":    map[string]any{"Ref": "AppTheoryAutoDeleteObjectsCodeObjectKey"},
					},
				},
			},
		},
	})
	require.NoError(t, err)
	return raw
}

func lesserBodyTemplateJSONWithRawBootstrapAuxiliary(t *testing.T, stage string) []byte {
	t.Helper()

	raw, err := json.Marshal(map[string]any{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Parameters": map[string]any{
			"LesserBodyCodeBucketName": map[string]any{
				"Type": "String",
			},
			"LesserBodyCodeObjectKey": map[string]any{
				"Type": "String",
			},
		},
		"Resources": map[string]any{
			"McpHandler03E6F2E1": map[string]any{
				"Type": "AWS::Lambda::Function",
				"Properties": map[string]any{
					"Code": map[string]any{
						"S3Bucket": map[string]any{"Ref": "LesserBodyCodeBucketName"},
						"S3Key":    map[string]any{"Ref": "LesserBodyCodeObjectKey"},
					},
				},
			},
			"CustomS3AutoDeleteObjectsCustomResourceProviderHandler9D90184F": map[string]any{
				"Type": "AWS::Lambda::Function",
				"Properties": map[string]any{
					"Code": map[string]any{
						"S3Bucket": map[string]any{"Fn::Sub": "cdk-hnb659fds-assets-${AWS::AccountId}-${AWS::Region}"},
						"S3Key":    "asset.apptheory-s3-auto-delete-provider/handler.zip",
					},
				},
			},
		},
	})
	require.NoError(t, err)
	return raw
}

func lesserBodyChecksumsSchema2TXT(stage string) []byte {
	if strings.TrimSpace(stage) == "" {
		stage = managedStageDev
	}
	templatePath := fmt.Sprintf("lesser-body-managed-%s.template.json", stage)
	lines := []string{
		"zip-sha  lesser-body.zip",
		"manifest-sha  lesser-body-deploy.json",
		fmt.Sprintf("template-sha  %s", templatePath),
		"script-sha  deploy-lesser-body-from-release.sh",
		"release-sha  lesser-body-release.json",
		"aux-sha  assets/mcp-stream-spill-auto-delete-provider.zip",
	}
	return []byte(strings.Join(lines, "\n") + "\n")
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

func TestValidateManagedLesserReleaseManifest_RejectsMutableGitSHA(t *testing.T) {
	t.Parallel()

	manifest, err := parseManagedLesserReleaseManifest(lesserReleaseManifestJSON(t, "v1.2.6"))
	require.NoError(t, err)
	manifest.GitSHA = "main"

	err = validateManagedLesserReleaseManifest(manifest, "v1.2.6")
	require.ErrorContains(t, err, "40-character git commit SHA")
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

func TestPreflightManagedLesserBodyRelease_SupportsSchema2AuxiliaryAssets(t *testing.T) {
	t.Parallel()

	const version = "v0.2.3"
	const stage = managedStageDev
	handler := http.NewServeMux()
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-release.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyReleaseManifestSchema2JSON(t, version, stage, ""))
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-deploy.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyDeployManifestSchema2JSON(t, stage, "", nil))
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(lesserBodyChecksumsSchema2TXT(stage))
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-managed-"+stage+".template.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyTemplateJSONWithAuxiliary(t, stage))
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

func TestPreflightManagedLesserBodyRelease_RejectsUnsupportedSchema2Capability(t *testing.T) {
	t.Parallel()

	const version = "v0.2.3"
	const stage = managedStageDev
	handler := http.NewServeMux()
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-release.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyReleaseManifestSchema2JSON(t, version, stage, "unsupported_capability_v9"))
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
	require.ErrorContains(t, err, "unsupported lesser-body required capability")
}

func TestPreflightManagedLesserBodyRelease_RejectsTemplateReferenceWithoutAuxiliaryDeclaration(t *testing.T) {
	t.Parallel()

	const version = "v0.2.3"
	const stage = managedStageDev
	handler := http.NewServeMux()
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-release.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyReleaseManifestSchema2JSON(t, version, stage, ""))
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-deploy.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyDeployManifestSchema2JSON(t, stage, "", []map[string]any{}))
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(lesserBodyChecksumsSchema2TXT(stage))
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-managed-"+stage+".template.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyTemplateJSONWithAuxiliary(t, stage))
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
	require.ErrorContains(t, err, "deploy manifest is missing auxiliary asset")
}

func TestPreflightManagedLesserBodyRelease_RejectsRawBootstrapLambdaAssetRefs(t *testing.T) {
	t.Parallel()

	const version = "v0.2.3"
	const stage = managedStageDev
	releaseRaw := lesserBodyReleaseManifestSchema2JSON(t, version, stage, "")
	var releaseDoc map[string]any
	require.NoError(t, json.Unmarshal(releaseRaw, &releaseDoc))
	artifacts, ok := releaseDoc["artifacts"].(map[string]any)
	require.True(t, ok)
	artifacts["auxiliary_assets"] = []map[string]any{}
	releaseRaw, err := json.Marshal(releaseDoc)
	require.NoError(t, err)

	handler := http.NewServeMux()
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-release.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(releaseRaw)
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-deploy.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyDeployManifestSchema2JSON(t, stage, "", []map[string]any{}))
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(lesserBodyChecksumsSchema2TXT(stage))
	})
	handler.HandleFunc("/equaltoai/lesser-body/releases/download/"+version+"/lesser-body-managed-"+stage+".template.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(lesserBodyTemplateJSONWithRawBootstrapAuxiliary(t, stage))
	})

	srv := &Server{
		cfg: config.Config{
			Stage:                        "lab",
			ManagedLesserBodyGitHubOwner: "equaltoai",
			ManagedLesserBodyGitHubRepo:  "lesser-body",
		},
		releaseHTTPClient: newManagedReleaseTestClient(t, handler),
	}

	err = srv.preflightManagedLesserBodyRelease(context.Background(), version, stage)
	require.ErrorContains(t, err, "Code.S3Bucket must Ref LesserBodyCodeBucketName")
	require.ErrorContains(t, err, "CDK bootstrap buckets are not allowed")
}

func TestManagedLesserBodySchema2BodyFixtureParsesAuxiliaryUploadPlan(t *testing.T) {
	t.Parallel()

	fixtureDir := filepath.Join("testdata", "lesser-body-app-theory-v1.5.0-multi-asset")
	releaseRaw, err := os.ReadFile(filepath.Join(fixtureDir, "lesser-body-release.json"))
	require.NoError(t, err)
	deployRaw, err := os.ReadFile(filepath.Join(fixtureDir, "lesser-body-deploy.json"))
	require.NoError(t, err)
	templateRaw, err := os.ReadFile(filepath.Join(fixtureDir, "lesser-body-managed-dev.template.json"))
	require.NoError(t, err)
	checksumsRaw, err := os.ReadFile(filepath.Join(fixtureDir, "checksums.txt"))
	require.NoError(t, err)

	releaseManifest, err := parseManagedLesserBodyReleaseManifest(releaseRaw)
	require.NoError(t, err)
	require.Equal(t, supportedLesserBodyReleaseManifestSchemaAuxiliaryAssets, releaseManifest.Schema)

	deployManifest, err := parseManagedLesserBodyDeployManifest(deployRaw)
	require.NoError(t, err)
	require.NoError(t, validateManagedLesserBodyDeployManifest(deployManifest, nil, managedStageDev))
	require.Len(t, deployManifest.AuxiliaryAssets, 1)
	aux := deployManifest.AuxiliaryAssets[0]
	require.Equal(t, "assets/mcp-stream-spill-auto-delete-provider.fixture.txt", aux.Path)
	require.Equal(t, "assets/mcp-stream-spill-auto-delete-provider.fixture.txt", aux.S3Key)
	require.Equal(t, "AppTheoryAutoDeleteObjectsCodeObjectKey", aux.TemplateParameter)

	checksums, err := parseManagedReleaseChecksums(checksumsRaw)
	require.NoError(t, err)
	require.NoError(t, validateManagedReleaseChecksumEntries(checksums, map[string]string{
		aux.Path: aux.SHA256,
	}))
	require.NoError(t, validateManagedLesserBodyTemplateAuxiliaryReferences(templateRaw, "lesser-body-managed-dev.template.json", managedStageDev, deployManifest.AuxiliaryAssets))
}

func TestManagedReleaseAssetPath_AllowsNestedAssetsAndRejectsUnsafePaths(t *testing.T) {
	t.Parallel()

	u, err := managedGitHubReleaseAssetURL("equaltoai", "lesser-body", "v0.2.3", "assets/mcp-stream-spill-auto-delete-provider.zip")
	require.NoError(t, err)
	require.Contains(t, u, "/assets/mcp-stream-spill-auto-delete-provider.zip")

	for _, path := range []string{"../evil.zip", "/evil.zip", "assets//evil.zip", "assets/./evil.zip", "assets\\evil.zip"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			_, err := managedGitHubReleaseAssetURL("equaltoai", "lesser-body", "v0.2.3", path)
			require.Error(t, err)
		})
	}
}

func TestManagedLesserBodySchema2DeployManifestRejectsContractDrift(t *testing.T) {
	t.Parallel()

	releaseManifest, err := parseManagedLesserBodyReleaseManifest(lesserBodyReleaseManifestSchema2JSON(t, "v0.2.3", managedStageDev, ""))
	require.NoError(t, err)

	t.Run("duplicate capability", func(t *testing.T) {
		raw := lesserBodyDeployManifestSchema2JSON(t, managedStageDev, "", nil)
		var doc map[string]any
		require.NoError(t, json.Unmarshal(raw, &doc))
		doc["required_capabilities"] = []string{managedLesserBodyCapabilityAuxiliaryAssetsV1, managedLesserBodyCapabilityAuxiliaryAssetsV1}
		modified, err := json.Marshal(doc)
		require.NoError(t, err)
		deployManifest, err := parseManagedLesserBodyDeployManifest(modified)
		require.NoError(t, err)
		err = validateManagedLesserBodyDeployManifest(deployManifest, releaseManifest, managedStageDev)
		require.ErrorContains(t, err, "duplicate capability")
	})

	t.Run("template checksum mismatch", func(t *testing.T) {
		raw := lesserBodyDeployManifestSchema2JSON(t, managedStageDev, "", nil)
		var doc map[string]any
		require.NoError(t, json.Unmarshal(raw, &doc))
		templates, ok := doc["templates"].(map[string]any)
		require.True(t, ok)
		devTemplate, ok := templates[managedStageDev].(map[string]any)
		require.True(t, ok)
		devTemplate["sha256"] = "different-template-sha"
		modified, err := json.Marshal(doc)
		require.NoError(t, err)
		deployManifest, err := parseManagedLesserBodyDeployManifest(modified)
		require.NoError(t, err)
		err = validateManagedLesserBodyDeployManifest(deployManifest, releaseManifest, managedStageDev)
		require.ErrorContains(t, err, "does not match release manifest")
	})

	t.Run("unsafe auxiliary key", func(t *testing.T) {
		asset := lesserBodyAuxiliaryAssetFixture(managedStageDev)
		asset["s3_key"] = "../escape.zip"
		deployManifest, err := parseManagedLesserBodyDeployManifest(lesserBodyDeployManifestSchema2JSON(t, managedStageDev, "", []map[string]any{asset}))
		require.NoError(t, err)
		err = validateManagedLesserBodyDeployManifest(deployManifest, releaseManifest, managedStageDev)
		require.ErrorContains(t, err, "must not contain empty, current, or parent path segments")
	})
}

func TestManagedLesserBodyTemplateAuxiliaryReferencesRejectWrongParameter(t *testing.T) {
	t.Parallel()

	deployManifest, err := parseManagedLesserBodyDeployManifest(lesserBodyDeployManifestSchema2JSON(t, managedStageDev, "", nil))
	require.NoError(t, err)
	templateRaw := lesserBodyTemplateJSONWithAuxiliary(t, managedStageDev)
	require.NoError(t, validateManagedLesserBodyTemplateAuxiliaryReferences(templateRaw, "lesser-body-managed-dev.template.json", managedStageDev, deployManifest.AuxiliaryAssets))
	err = validateManagedLesserBodyTemplateAuxiliaryReferences(templateRaw, "lesser-body-managed-dev.template.json", managedStageDev, nil)
	require.ErrorContains(t, err, "without a declared auxiliary asset")

	var template map[string]any
	require.NoError(t, json.Unmarshal(templateRaw, &template))
	resources, ok := template["Resources"].(map[string]any)
	require.True(t, ok)
	auxFn, ok := resources["CustomS3AutoDeleteObjectsCustomResourceProviderHandler9D90184F"].(map[string]any)
	require.True(t, ok)
	props, ok := auxFn["Properties"].(map[string]any)
	require.True(t, ok)
	code, ok := props["Code"].(map[string]any)
	require.True(t, ok)
	code["S3Key"] = map[string]any{"Ref": "UndeclaredAuxiliaryObjectKey"}
	modified, err := json.Marshal(template)
	require.NoError(t, err)
	err = validateManagedLesserBodyTemplateAuxiliaryReferences(modified, "lesser-body-managed-dev.template.json", managedStageDev, deployManifest.AuxiliaryAssets)
	require.ErrorContains(t, err, "must Ref AppTheoryAutoDeleteObjectsCodeObjectKey")
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
