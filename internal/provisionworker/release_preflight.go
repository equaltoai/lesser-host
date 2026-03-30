package provisionworker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	managedLesserReleaseManifestAsset = "lesser-release.json"

	requiredLesserReleaseManifestSchema     = 1
	requiredLesserReleaseReceiptSchemaMin   = 2
	requiredLesserDeployArtifactsSchema     = 1
	requiredLesserBundleManifestSchema      = 1
	requiredLesserBundleArchivePath         = "lesser-lambda-bundle.tar.gz"
	requiredLesserBundleManifestPath        = "lesser-lambda-bundle.json"
	requiredLesserBundleManifestKind        = "lesser.lambda_bundle_manifest"
	managedReleasePreflightRequestUserAgent = "lesser-host-provisionworker"
)

type managedLesserReleaseManifest struct {
	Schema    int    `json:"schema"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	GitSHA    string `json:"git_sha"`
	Artifacts struct {
		ReceiptSchemaVersion int `json:"receipt_schema_version"`
		DeployArtifacts      struct {
			SchemaVersion int `json:"schema_version"`
			LambdaBundle  struct {
				Path                  string `json:"path"`
				ManifestPath          string `json:"manifest_path"`
				ManifestKind          string `json:"manifest_kind"`
				ManifestSchemaVersion int    `json:"manifest_schema_version"`
			} `json:"lambda_bundle"`
		} `json:"deploy_artifacts"`
	} `json:"artifacts"`
}

type managedLesserLambdaBundleManifest struct {
	Kind          string `json:"kind"`
	SchemaVersion int    `json:"schema_version"`
	Bundle        struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"bundle"`
	Files []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

func validateManagedGitHubRepoSegment(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

func managedReleasePreflightHTTPClient(s *Server) *http.Client {
	if s != nil && s.releaseHTTPClient != nil {
		return s.releaseHTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func managedGitHubReleaseAssetURL(owner string, repo string, tag string, asset string) (string, error) {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	tag = strings.TrimSpace(tag)
	asset = strings.TrimSpace(asset)
	if owner == "" || repo == "" || tag == "" || asset == "" {
		return "", fmt.Errorf("owner, repo, tag, and asset are required")
	}
	if !validateManagedGitHubRepoSegment(owner) || !validateManagedGitHubRepoSegment(repo) {
		return "", fmt.Errorf("invalid github repo")
	}
	if strings.Contains(tag, "/") || strings.Contains(tag, "\n") || strings.Contains(tag, "\r") {
		return "", fmt.Errorf("invalid github release tag")
	}
	if strings.Contains(asset, "/") || strings.Contains(asset, "\n") || strings.Contains(asset, "\r") {
		return "", fmt.Errorf("invalid github release asset")
	}

	u := &url.URL{
		Scheme: "https",
		Host:   "github.com",
		Path:   fmt.Sprintf("/%s/%s/releases/download/%s/%s", owner, repo, tag, asset),
	}
	return u.String(), nil
}

func fetchManagedGitHubReleaseAsset(ctx context.Context, client *http.Client, owner string, repo string, tag string, asset string) ([]byte, error) {
	u, err := managedGitHubReleaseAssetURL(owner, repo, tag, asset)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", managedReleasePreflightRequestUserAgent)
	resp, err := client.Do(req) //nolint:gosec // Host is fixed to github.com and validated path segments are interpolated.
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("github release asset request failed (HTTP %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("github release asset is empty")
	}
	return body, nil
}

func parseManagedLesserReleaseManifest(raw []byte) (*managedLesserReleaseManifest, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil, fmt.Errorf("release manifest is empty")
	}

	var parsed managedLesserReleaseManifest
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func validateManagedLesserReleaseManifest(parsed *managedLesserReleaseManifest, expectedTag string) error {
	if parsed == nil {
		return fmt.Errorf("release manifest is required")
	}
	expectedTag = strings.TrimSpace(expectedTag)
	if parsed.Schema != requiredLesserReleaseManifestSchema {
		return fmt.Errorf("unsupported release manifest schema %d", parsed.Schema)
	}
	if strings.TrimSpace(parsed.Name) != "lesser" {
		return fmt.Errorf("unexpected release manifest name %q", strings.TrimSpace(parsed.Name))
	}
	if expectedTag != "" && strings.TrimSpace(parsed.Version) != expectedTag {
		return fmt.Errorf("release manifest version mismatch: got %q, want %q", strings.TrimSpace(parsed.Version), expectedTag)
	}
	if strings.TrimSpace(parsed.GitSHA) == "" {
		return fmt.Errorf("release manifest git_sha is missing")
	}
	if parsed.Artifacts.ReceiptSchemaVersion < requiredLesserReleaseReceiptSchemaMin {
		return fmt.Errorf("unsupported receipt schema version %d", parsed.Artifacts.ReceiptSchemaVersion)
	}
	if parsed.Artifacts.DeployArtifacts.SchemaVersion != requiredLesserDeployArtifactsSchema {
		return fmt.Errorf("unsupported deploy_artifacts schema version %d", parsed.Artifacts.DeployArtifacts.SchemaVersion)
	}
	bundle := parsed.Artifacts.DeployArtifacts.LambdaBundle
	if strings.TrimSpace(bundle.Path) != requiredLesserBundleArchivePath {
		return fmt.Errorf("unexpected lambda bundle path %q", strings.TrimSpace(bundle.Path))
	}
	if strings.TrimSpace(bundle.ManifestPath) != requiredLesserBundleManifestPath {
		return fmt.Errorf("unexpected lambda bundle manifest path %q", strings.TrimSpace(bundle.ManifestPath))
	}
	if strings.TrimSpace(bundle.ManifestKind) != requiredLesserBundleManifestKind {
		return fmt.Errorf("unexpected lambda bundle manifest kind %q", strings.TrimSpace(bundle.ManifestKind))
	}
	if bundle.ManifestSchemaVersion != requiredLesserBundleManifestSchema {
		return fmt.Errorf("unsupported lambda bundle manifest schema version %d", bundle.ManifestSchemaVersion)
	}
	return nil
}

func parseManagedLesserLambdaBundleManifest(raw []byte) (*managedLesserLambdaBundleManifest, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil, fmt.Errorf("lambda bundle manifest is empty")
	}

	var parsed managedLesserLambdaBundleManifest
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func validateManagedLesserLambdaBundleManifest(parsed *managedLesserLambdaBundleManifest) error {
	if parsed == nil {
		return fmt.Errorf("lambda bundle manifest is required")
	}
	if strings.TrimSpace(parsed.Kind) != requiredLesserBundleManifestKind {
		return fmt.Errorf("unexpected lambda bundle manifest kind %q", strings.TrimSpace(parsed.Kind))
	}
	if parsed.SchemaVersion != requiredLesserBundleManifestSchema {
		return fmt.Errorf("unsupported lambda bundle manifest schema version %d", parsed.SchemaVersion)
	}
	if strings.TrimSpace(parsed.Bundle.Path) != requiredLesserBundleArchivePath {
		return fmt.Errorf("unexpected lambda bundle archive path %q", strings.TrimSpace(parsed.Bundle.Path))
	}
	if strings.TrimSpace(parsed.Bundle.SHA256) == "" {
		return fmt.Errorf("lambda bundle checksum is missing")
	}
	if len(parsed.Files) == 0 {
		return fmt.Errorf("lambda bundle file inventory is empty")
	}
	for _, item := range parsed.Files {
		if strings.TrimSpace(item.Path) == "" {
			return fmt.Errorf("lambda bundle file path is missing")
		}
		if strings.TrimSpace(item.SHA256) == "" {
			return fmt.Errorf("lambda bundle checksum is missing for %q", strings.TrimSpace(item.Path))
		}
	}
	return nil
}

func (s *Server) preflightManagedLesserRelease(ctx context.Context, version string) error {
	if s == nil {
		return fmt.Errorf("server is nil")
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("lesser version is required")
	}

	raw, err := fetchManagedGitHubReleaseAsset(
		ctx,
		managedReleasePreflightHTTPClient(s),
		strings.TrimSpace(s.cfg.ManagedLesserGitHubOwner),
		strings.TrimSpace(s.cfg.ManagedLesserGitHubRepo),
		version,
		managedLesserReleaseManifestAsset,
	)
	if err != nil {
		return err
	}
	parsed, err := parseManagedLesserReleaseManifest(raw)
	if err != nil {
		return err
	}
	if manifestErr := validateManagedLesserReleaseManifest(parsed, version); manifestErr != nil {
		return manifestErr
	}

	bundleRaw, err := fetchManagedGitHubReleaseAsset(
		ctx,
		managedReleasePreflightHTTPClient(s),
		strings.TrimSpace(s.cfg.ManagedLesserGitHubOwner),
		strings.TrimSpace(s.cfg.ManagedLesserGitHubRepo),
		version,
		requiredLesserBundleManifestPath,
	)
	if err != nil {
		return err
	}
	bundleManifest, err := parseManagedLesserLambdaBundleManifest(bundleRaw)
	if err != nil {
		return err
	}
	return validateManagedLesserLambdaBundleManifest(bundleManifest)
}
