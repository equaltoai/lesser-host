package provisionworker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	managedLesserBodyReleaseManifestAsset   = "lesser-body-release.json"
	requiredLesserBodyReleaseManifestSchema = 1
	requiredLesserBodyChecksumsPath         = "checksums.txt"
	requiredLesserBodyChecksumsAlgorithm    = "sha256"
	requiredLesserBodyDeploySchema          = 1
	requiredLesserBodyDeployManifestPath    = "lesser-body-deploy.json"
	requiredLesserBodyDeployManifestSchema  = 1
	requiredLesserBodyTemplateSelection     = "by_stage"
	requiredLesserBodyTemplateFormat        = "cloudformation-json"
	requiredLesserBodyLambdaZipPath         = "lesser-body.zip"
	requiredLesserBodyDeployScriptPath      = "deploy-lesser-body-from-release.sh"
)

type managedLesserBodyReleaseManifest struct {
	Schema    int    `json:"schema"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	GitSHA    string `json:"git_sha"`
	Artifacts struct {
		Checksums struct {
			Path      string `json:"path"`
			Algorithm string `json:"algorithm"`
		} `json:"checksums"`
		LambdaZip struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"lambda_zip"`
		DeployManifest struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Schema int    `json:"schema"`
		} `json:"deploy_manifest"`
		DeployTemplates map[string]struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Format string `json:"format"`
		} `json:"deploy_templates"`
		DeployScript struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"deploy_script"`
	} `json:"artifacts"`
	Deploy struct {
		Schema                 int    `json:"schema"`
		ManifestPath           string `json:"manifest_path"`
		TemplateSelection      string `json:"template_selection"`
		SourceCheckoutRequired *bool  `json:"source_checkout_required"`
		NPMInstallRequired     *bool  `json:"npm_install_required"`
	} `json:"deploy"`
}

func parseManagedLesserBodyReleaseManifest(raw []byte) (*managedLesserBodyReleaseManifest, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil, fmt.Errorf("release manifest is empty")
	}

	var parsed managedLesserBodyReleaseManifest
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseManagedReleaseChecksums(raw []byte) (map[string]string, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil, fmt.Errorf("checksums manifest is empty")
	}

	out := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid checksum manifest entry %q", line)
		}
		path := strings.TrimSpace(parts[len(parts)-1])
		if path == "" {
			return nil, fmt.Errorf("checksum manifest entry is missing a path")
		}
		out[path] = strings.TrimSpace(parts[0])
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("checksums manifest is empty")
	}
	return out, nil
}

func validateManagedReleaseChecksumEntries(checksums map[string]string, required map[string]string) error {
	if len(required) == 0 {
		return nil
	}
	if len(checksums) == 0 {
		return fmt.Errorf("checksums manifest is empty")
	}
	for path, expectedSHA := range required {
		actualSHA, ok := checksums[strings.TrimSpace(path)]
		if !ok {
			return fmt.Errorf("checksum entry missing for %s", strings.TrimSpace(path))
		}
		expectedSHA = strings.TrimSpace(expectedSHA)
		if expectedSHA != "" && actualSHA != expectedSHA {
			return fmt.Errorf("checksum mismatch for %s", strings.TrimSpace(path))
		}
	}
	return nil
}

func validateManagedLesserBodyReleaseManifest(parsed *managedLesserBodyReleaseManifest, expectedTag string, stage string) error {
	if parsed == nil {
		return fmt.Errorf("release manifest is required")
	}
	expectedTag = strings.TrimSpace(expectedTag)
	stage = normalizeManagedLesserStage(stage)
	if stage == "" {
		stage = managedStageDev
	}

	if err := validateManagedLesserBodyReleaseIdentity(parsed, expectedTag); err != nil {
		return err
	}
	if err := validateManagedLesserBodyReleaseAssetMetadata(parsed, stage); err != nil {
		return err
	}
	return validateManagedLesserBodyReleaseDeployMetadata(parsed)
}

func validateManagedLesserBodyReleaseIdentity(parsed *managedLesserBodyReleaseManifest, expectedTag string) error {
	if parsed.Schema != requiredLesserBodyReleaseManifestSchema {
		return fmt.Errorf("unsupported release manifest schema %d", parsed.Schema)
	}
	if strings.TrimSpace(parsed.Name) != "lesser-body" {
		return fmt.Errorf("unexpected release manifest name %q", strings.TrimSpace(parsed.Name))
	}
	if expectedTag != "" && strings.TrimSpace(parsed.Version) != expectedTag {
		return fmt.Errorf("release manifest version mismatch: got %q, want %q", strings.TrimSpace(parsed.Version), expectedTag)
	}
	if strings.TrimSpace(parsed.GitSHA) == "" {
		return fmt.Errorf("release manifest git_sha is missing")
	}
	return nil
}

func validateManagedLesserBodyReleaseAssetMetadata(parsed *managedLesserBodyReleaseManifest, stage string) error {
	if strings.TrimSpace(parsed.Artifacts.Checksums.Path) != requiredLesserBodyChecksumsPath {
		return fmt.Errorf("unexpected checksums path %q", strings.TrimSpace(parsed.Artifacts.Checksums.Path))
	}
	if strings.TrimSpace(parsed.Artifacts.Checksums.Algorithm) != requiredLesserBodyChecksumsAlgorithm {
		return fmt.Errorf("unexpected checksums algorithm %q", strings.TrimSpace(parsed.Artifacts.Checksums.Algorithm))
	}
	if strings.TrimSpace(parsed.Artifacts.LambdaZip.Path) != requiredLesserBodyLambdaZipPath {
		return fmt.Errorf("unexpected lambda zip path %q", strings.TrimSpace(parsed.Artifacts.LambdaZip.Path))
	}
	if strings.TrimSpace(parsed.Artifacts.LambdaZip.SHA256) == "" {
		return fmt.Errorf("lambda zip checksum is missing")
	}
	if strings.TrimSpace(parsed.Artifacts.DeployManifest.Path) != requiredLesserBodyDeployManifestPath {
		return fmt.Errorf("unexpected deploy manifest path %q", strings.TrimSpace(parsed.Artifacts.DeployManifest.Path))
	}
	if parsed.Artifacts.DeployManifest.Schema != requiredLesserBodyDeployManifestSchema {
		return fmt.Errorf("unsupported deploy manifest schema %d", parsed.Artifacts.DeployManifest.Schema)
	}
	if strings.TrimSpace(parsed.Artifacts.DeployManifest.SHA256) == "" {
		return fmt.Errorf("deploy manifest checksum is missing")
	}
	if strings.TrimSpace(parsed.Artifacts.DeployScript.Path) != requiredLesserBodyDeployScriptPath {
		return fmt.Errorf("unexpected deploy script path %q", strings.TrimSpace(parsed.Artifacts.DeployScript.Path))
	}
	if strings.TrimSpace(parsed.Artifacts.DeployScript.SHA256) == "" {
		return fmt.Errorf("deploy script checksum is missing")
	}

	templatePath := fmt.Sprintf("lesser-body-managed-%s.template.json", stage)
	templateMeta, ok := parsed.Artifacts.DeployTemplates[stage]
	if !ok {
		return fmt.Errorf("release manifest is missing template metadata for stage %s", stage)
	}
	if strings.TrimSpace(templateMeta.Path) != templatePath {
		return fmt.Errorf("unexpected template path for stage %s: %q", stage, strings.TrimSpace(templateMeta.Path))
	}
	if strings.TrimSpace(templateMeta.SHA256) == "" {
		return fmt.Errorf("template checksum is missing for stage %s", stage)
	}
	if strings.TrimSpace(templateMeta.Format) != requiredLesserBodyTemplateFormat {
		return fmt.Errorf("unexpected template format for stage %s: %q", stage, strings.TrimSpace(templateMeta.Format))
	}
	return nil
}

func validateManagedLesserBodyReleaseDeployMetadata(parsed *managedLesserBodyReleaseManifest) error {
	if parsed.Deploy.Schema != requiredLesserBodyDeploySchema {
		return fmt.Errorf("unsupported deploy schema %d", parsed.Deploy.Schema)
	}
	if strings.TrimSpace(parsed.Deploy.ManifestPath) != requiredLesserBodyDeployManifestPath {
		return fmt.Errorf("unexpected deploy manifest path %q", strings.TrimSpace(parsed.Deploy.ManifestPath))
	}
	if strings.TrimSpace(parsed.Deploy.TemplateSelection) != requiredLesserBodyTemplateSelection {
		return fmt.Errorf("unexpected deploy template selection %q", strings.TrimSpace(parsed.Deploy.TemplateSelection))
	}
	if parsed.Deploy.SourceCheckoutRequired == nil || *parsed.Deploy.SourceCheckoutRequired {
		return fmt.Errorf("release unexpectedly requires a source checkout")
	}
	if parsed.Deploy.NPMInstallRequired == nil || *parsed.Deploy.NPMInstallRequired {
		return fmt.Errorf("release unexpectedly requires npm install")
	}
	return nil
}

func validateManagedLesserBodyTemplateJSON(raw []byte, templatePath string) error {
	raw = []byte(strings.TrimSpace(string(raw)))
	templatePath = strings.TrimSpace(templatePath)
	if len(raw) == 0 {
		return managedTemplatePathErrorf(templatePath, "is empty")
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return managedTemplatePathErrorf(templatePath, "is not valid JSON: %v", err)
	}

	parameters, ok, err := managedTemplateParameters(parsed, templatePath)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	for name, rawParam := range parameters {
		param, ok := rawParam.(map[string]any)
		if !ok {
			return managedTemplateParameterErrorf(templatePath, name, "must be an object")
		}
		if err := validateManagedTemplateParameterDefault(templatePath, name, param); err != nil {
			return err
		}
	}
	return nil
}

func managedTemplateParameters(parsed map[string]any, templatePath string) (map[string]any, bool, error) {
	parametersRaw, ok := parsed["Parameters"]
	if !ok || parametersRaw == nil {
		return nil, false, nil
	}
	parameters, ok := parametersRaw.(map[string]any)
	if !ok {
		return nil, false, managedTemplatePathErrorf(templatePath, "Parameters must be an object")
	}
	return parameters, true, nil
}

func validateManagedTemplateParameterDefault(templatePath string, name string, param map[string]any) error {
	defaultValue, ok := param["Default"]
	if !ok || defaultValue == nil {
		return nil
	}
	if _, ok := defaultValue.(string); ok {
		return nil
	}
	return managedTemplateParameterErrorf(
		templatePath,
		name,
		"has non-string Default (%s); CloudFormation requires every Default member to be a string",
		managedTemplateValueType(defaultValue),
	)
}

func managedTemplateValueType(value any) string {
	switch value.(type) {
	case bool:
		return "boolean"
	case float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

func managedTemplatePathErrorf(templatePath string, format string, args ...any) error {
	templatePath = strings.TrimSpace(templatePath)
	if templatePath == "" {
		return fmt.Errorf("managed template "+format, args...)
	}
	return fmt.Errorf("managed template %s "+format, append([]any{templatePath}, args...)...)
}

func managedTemplateParameterErrorf(templatePath string, name string, format string, args ...any) error {
	templatePath = strings.TrimSpace(templatePath)
	name = strings.TrimSpace(name)
	if templatePath == "" {
		return fmt.Errorf("managed template parameter %s "+format, append([]any{name}, args...)...)
	}
	return fmt.Errorf("managed template %s parameter %s "+format, append([]any{templatePath, name}, args...)...)
}

func buildManagedLesserBodyChecksumRequirements(parsed *managedLesserBodyReleaseManifest, stage string) map[string]string {
	required := map[string]string{
		managedLesserBodyReleaseManifestAsset:                      "",
		requiredLesserBodyDeployManifestPath:                       strings.TrimSpace(parsed.Artifacts.DeployManifest.SHA256),
		requiredLesserBodyLambdaZipPath:                            strings.TrimSpace(parsed.Artifacts.LambdaZip.SHA256),
		requiredLesserBodyDeployScriptPath:                         strings.TrimSpace(parsed.Artifacts.DeployScript.SHA256),
		fmt.Sprintf("lesser-body-managed-%s.template.json", stage): strings.TrimSpace(parsed.Artifacts.DeployTemplates[stage].SHA256),
	}
	return required
}

func validateManagedLesserBodyReleaseTemplatePreflight(
	ctx context.Context,
	client *http.Client,
	owner string,
	repo string,
	version string,
	stage string,
) (string, error) {
	version = strings.TrimSpace(version)
	stage = normalizeManagedLesserStage(stage)
	if stage == "" {
		stage = managedStageDev
	}

	raw, err := fetchManagedGitHubReleaseAsset(
		ctx,
		client,
		owner,
		repo,
		version,
		managedLesserBodyReleaseManifestAsset,
	)
	if err != nil {
		return "", err
	}
	parsed, err := parseManagedLesserBodyReleaseManifest(raw)
	if err != nil {
		return "", err
	}
	if validateErr := validateManagedLesserBodyReleaseManifest(parsed, version, stage); validateErr != nil {
		return "", validateErr
	}

	checksumsRaw, err := fetchManagedGitHubReleaseAsset(
		ctx,
		client,
		owner,
		repo,
		version,
		requiredLesserBodyChecksumsPath,
	)
	if err != nil {
		return "", err
	}
	checksums, parseErr := parseManagedReleaseChecksums(checksumsRaw)
	if parseErr != nil {
		return "", parseErr
	}
	if checksumErr := validateManagedReleaseChecksumEntries(checksums, buildManagedLesserBodyChecksumRequirements(parsed, stage)); checksumErr != nil {
		return "", checksumErr
	}

	templatePath := strings.TrimSpace(parsed.Artifacts.DeployTemplates[stage].Path)
	templateRaw, err := fetchManagedGitHubReleaseAsset(
		ctx,
		client,
		owner,
		repo,
		version,
		templatePath,
	)
	if err != nil {
		return templatePath, err
	}
	if err := validateManagedLesserBodyTemplateJSON(templateRaw, templatePath); err != nil {
		return templatePath, err
	}
	return templatePath, nil
}

func ValidateManagedLesserBodyReleaseTemplatePreflight(
	ctx context.Context,
	client *http.Client,
	owner string,
	repo string,
	version string,
	stage string,
) (string, error) {
	if err := ValidateManagedLesserBodyReleaseVersionSupported(version); err != nil {
		return "", err
	}
	return validateManagedLesserBodyReleaseTemplatePreflight(ctx, client, owner, repo, version, stage)
}

func (s *Server) preflightManagedLesserBodyRelease(ctx context.Context, version string, stage string) error {
	if s == nil {
		return fmt.Errorf("server is nil")
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("lesser-body version is required")
	}
	owner := strings.TrimSpace(s.cfg.ManagedLesserBodyGitHubOwner)
	repo := strings.TrimSpace(s.cfg.ManagedLesserBodyGitHubRepo)
	if owner == "" || repo == "" {
		return fmt.Errorf("lesser-body github release source is not configured")
	}
	if err := ValidateManagedLesserBodyReleaseCompatibility(
		ctx,
		managedReleasePreflightHTTPClient(s),
		owner,
		repo,
		version,
		stage,
	); err != nil {
		return err
	}
	_, err := ValidateManagedLesserBodyReleaseTemplatePreflight(
		ctx,
		managedReleasePreflightHTTPClient(s),
		owner,
		repo,
		version,
		stage,
	)
	return err
}
