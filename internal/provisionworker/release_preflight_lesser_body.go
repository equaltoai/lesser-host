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
	managedLesserBodyReleaseManifestAsset                   = "lesser-body-release.json"
	requiredLesserBodyReleaseManifestSchema                 = 1
	supportedLesserBodyReleaseManifestSchemaAuxiliaryAssets = 2
	requiredLesserBodyChecksumsPath                         = "checksums.txt"
	requiredLesserBodyChecksumsAlgorithm                    = "sha256"
	requiredLesserBodyDeploySchema                          = 1
	supportedLesserBodyDeploySchemaAuxiliaryAssets          = 2
	requiredLesserBodyDeployManifestPath                    = "lesser-body-deploy.json"
	requiredLesserBodyDeployManifestSchema                  = 1
	supportedLesserBodyDeployManifestSchemaAuxiliaryAssets  = 2
	requiredLesserBodyTemplateSelection                     = "by_stage"
	requiredLesserBodyTemplateFormat                        = "cloudformation-json"
	requiredLesserBodyLambdaZipPath                         = "lesser-body.zip"
	requiredLesserBodyDeployScriptPath                      = "deploy-lesser-body-from-release.sh"
	managedLesserBodyCapabilityAuxiliaryAssetsV1            = "managed_auxiliary_assets_v1"
)

type managedLesserBodyAuxiliaryAssetTemplateReference struct {
	Stage           string `json:"stage"`
	Template        string `json:"template"`
	LogicalID       string `json:"logical_id"`
	PropertyPath    string `json:"property_path"`
	BucketParameter string `json:"bucket_parameter"`
	KeyParameter    string `json:"key_parameter"`
}

type managedLesserBodyAuxiliaryAssetSource struct {
	Kind          string `json:"kind"`
	SourceHash    string `json:"source_hash"`
	ConstructPath string `json:"construct_path"`
}

type managedLesserBodyAuxiliaryAsset struct {
	ID                 string                                             `json:"id"`
	Path               string                                             `json:"path"`
	SHA256             string                                             `json:"sha256"`
	Bytes              int64                                              `json:"bytes"`
	Required           bool                                               `json:"required"`
	S3Key              string                                             `json:"s3_key"`
	TemplateParameter  string                                             `json:"template_parameter"`
	ContentType        string                                             `json:"content_type"`
	Source             managedLesserBodyAuxiliaryAssetSource              `json:"source"`
	TemplateReferences []managedLesserBodyAuxiliaryAssetTemplateReference `json:"template_references"`
}

type managedLesserBodyDeployTemplateMeta struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Format string `json:"format"`
}

type managedLesserBodyDeployManifest struct {
	Schema               int                                            `json:"schema"`
	RequiredCapabilities []string                                       `json:"required_capabilities"`
	AssetPrefixDefault   string                                         `json:"asset_prefix_default"`
	Templates            map[string]managedLesserBodyDeployTemplateMeta `json:"templates"`
	AuxiliaryAssets      []managedLesserBodyAuxiliaryAsset              `json:"auxiliary_assets"`
}

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
		AuxiliaryAssets []managedLesserBodyAuxiliaryAsset `json:"auxiliary_assets"`
	} `json:"artifacts"`
	Deploy struct {
		Schema                 int      `json:"schema"`
		ManifestPath           string   `json:"manifest_path"`
		TemplateSelection      string   `json:"template_selection"`
		SourceCheckoutRequired *bool    `json:"source_checkout_required"`
		NPMInstallRequired     *bool    `json:"npm_install_required"`
		RequiredCapabilities   []string `json:"required_capabilities"`
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

func parseManagedLesserBodyDeployManifest(raw []byte) (*managedLesserBodyDeployManifest, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil, fmt.Errorf("deploy manifest is empty")
	}

	var parsed managedLesserBodyDeployManifest
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
	if !isSupportedManagedLesserBodyReleaseManifestSchema(parsed.Schema) {
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
	if err := validateManagedLesserBodyReleaseChecksumMetadata(parsed); err != nil {
		return err
	}
	if err := validateManagedLesserBodyReleaseLambdaMetadata(parsed); err != nil {
		return err
	}
	if err := validateManagedLesserBodyReleaseDeployManifestMetadata(parsed); err != nil {
		return err
	}
	if err := validateManagedLesserBodyReleaseScriptMetadata(parsed); err != nil {
		return err
	}
	if err := validateManagedLesserBodyReleaseTemplateMetadata(parsed, stage); err != nil {
		return err
	}
	return validateManagedLesserBodyReleaseAuxiliaryMetadata(parsed, stage)
}

func validateManagedLesserBodyReleaseChecksumMetadata(parsed *managedLesserBodyReleaseManifest) error {
	if strings.TrimSpace(parsed.Artifacts.Checksums.Path) != requiredLesserBodyChecksumsPath {
		return fmt.Errorf("unexpected checksums path %q", strings.TrimSpace(parsed.Artifacts.Checksums.Path))
	}
	if strings.TrimSpace(parsed.Artifacts.Checksums.Algorithm) != requiredLesserBodyChecksumsAlgorithm {
		return fmt.Errorf("unexpected checksums algorithm %q", strings.TrimSpace(parsed.Artifacts.Checksums.Algorithm))
	}
	return nil
}

func validateManagedLesserBodyReleaseLambdaMetadata(parsed *managedLesserBodyReleaseManifest) error {
	if strings.TrimSpace(parsed.Artifacts.LambdaZip.Path) != requiredLesserBodyLambdaZipPath {
		return fmt.Errorf("unexpected lambda zip path %q", strings.TrimSpace(parsed.Artifacts.LambdaZip.Path))
	}
	if strings.TrimSpace(parsed.Artifacts.LambdaZip.SHA256) == "" {
		return fmt.Errorf("lambda zip checksum is missing")
	}
	return nil
}

func validateManagedLesserBodyReleaseDeployManifestMetadata(parsed *managedLesserBodyReleaseManifest) error {
	if strings.TrimSpace(parsed.Artifacts.DeployManifest.Path) != requiredLesserBodyDeployManifestPath {
		return fmt.Errorf("unexpected deploy manifest path %q", strings.TrimSpace(parsed.Artifacts.DeployManifest.Path))
	}
	if !isSupportedManagedLesserBodyDeployManifestSchemaForRelease(parsed.Schema, parsed.Artifacts.DeployManifest.Schema) {
		return fmt.Errorf("unsupported deploy manifest schema %d", parsed.Artifacts.DeployManifest.Schema)
	}
	if strings.TrimSpace(parsed.Artifacts.DeployManifest.SHA256) == "" {
		return fmt.Errorf("deploy manifest checksum is missing")
	}
	return nil
}

func validateManagedLesserBodyReleaseScriptMetadata(parsed *managedLesserBodyReleaseManifest) error {
	if strings.TrimSpace(parsed.Artifacts.DeployScript.Path) != requiredLesserBodyDeployScriptPath {
		return fmt.Errorf("unexpected deploy script path %q", strings.TrimSpace(parsed.Artifacts.DeployScript.Path))
	}
	if strings.TrimSpace(parsed.Artifacts.DeployScript.SHA256) == "" {
		return fmt.Errorf("deploy script checksum is missing")
	}
	return nil
}

func validateManagedLesserBodyReleaseTemplateMetadata(parsed *managedLesserBodyReleaseManifest, stage string) error {
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

func validateManagedLesserBodyReleaseAuxiliaryMetadata(parsed *managedLesserBodyReleaseManifest, stage string) error {
	if parsed.Schema == supportedLesserBodyReleaseManifestSchemaAuxiliaryAssets {
		templatePath := fmt.Sprintf("lesser-body-managed-%s.template.json", stage)
		if err := validateManagedLesserBodyAuxiliaryAssets(parsed.Artifacts.AuxiliaryAssets, stage, templatePath); err != nil {
			return err
		}
	}
	return nil
}

func validateManagedLesserBodyReleaseDeployMetadata(parsed *managedLesserBodyReleaseManifest) error {
	if !isSupportedManagedLesserBodyDeploySchemaForRelease(parsed.Schema, parsed.Deploy.Schema) {
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
	if parsed.Deploy.Schema == supportedLesserBodyDeploySchemaAuxiliaryAssets {
		return validateManagedLesserBodyCapabilities(parsed.Deploy.RequiredCapabilities, "release deploy required_capabilities", true)
	}
	if len(parsed.Deploy.RequiredCapabilities) > 0 {
		return validateManagedLesserBodyCapabilities(parsed.Deploy.RequiredCapabilities, "release deploy required_capabilities", false)
	}
	return nil
}

func isSupportedManagedLesserBodyReleaseManifestSchema(schema int) bool {
	return schema == requiredLesserBodyReleaseManifestSchema || schema == supportedLesserBodyReleaseManifestSchemaAuxiliaryAssets
}

func isSupportedManagedLesserBodyDeployManifestSchemaForRelease(releaseSchema int, deployManifestSchema int) bool {
	switch releaseSchema {
	case requiredLesserBodyReleaseManifestSchema:
		return deployManifestSchema == requiredLesserBodyDeployManifestSchema
	case supportedLesserBodyReleaseManifestSchemaAuxiliaryAssets:
		return deployManifestSchema == supportedLesserBodyDeployManifestSchemaAuxiliaryAssets
	default:
		return false
	}
}

func isSupportedManagedLesserBodyDeploySchemaForRelease(releaseSchema int, deploySchema int) bool {
	switch releaseSchema {
	case requiredLesserBodyReleaseManifestSchema:
		return deploySchema == requiredLesserBodyDeploySchema
	case supportedLesserBodyReleaseManifestSchemaAuxiliaryAssets:
		return deploySchema == supportedLesserBodyDeploySchemaAuxiliaryAssets
	default:
		return false
	}
}

func validateManagedLesserBodyCapabilities(caps []string, label string, requireAuxiliaryAssets bool) error {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "required_capabilities"
	}
	seenAuxiliaryAssets := false
	seen := map[string]struct{}{}
	for _, cap := range caps {
		cap = strings.TrimSpace(cap)
		if cap == "" {
			return fmt.Errorf("%s contains an empty capability", label)
		}
		if _, ok := seen[cap]; ok {
			return fmt.Errorf("%s contains duplicate capability %q", label, cap)
		}
		seen[cap] = struct{}{}
		switch cap {
		case managedLesserBodyCapabilityAuxiliaryAssetsV1:
			seenAuxiliaryAssets = true
		default:
			return fmt.Errorf("unsupported lesser-body required capability %q", cap)
		}
	}
	if requireAuxiliaryAssets && !seenAuxiliaryAssets {
		return fmt.Errorf("%s must include %q for schema 2 managed auxiliary assets", label, managedLesserBodyCapabilityAuxiliaryAssetsV1)
	}
	return nil
}

func validateManagedLesserBodyDeployManifest(parsed *managedLesserBodyDeployManifest, release *managedLesserBodyReleaseManifest, stage string) error {
	if parsed == nil {
		return fmt.Errorf("deploy manifest is required")
	}
	stage = normalizeManagedLesserStage(stage)
	if stage == "" {
		stage = managedStageDev
	}
	if parsed.Schema != supportedLesserBodyDeployManifestSchemaAuxiliaryAssets {
		return fmt.Errorf("unsupported deploy manifest schema %d", parsed.Schema)
	}
	if err := validateManagedLesserBodyCapabilities(parsed.RequiredCapabilities, "deploy manifest required_capabilities", true); err != nil {
		return err
	}
	if strings.TrimSpace(parsed.AssetPrefixDefault) != "" {
		if err := validateManagedReleaseAssetPath(parsed.AssetPrefixDefault, "deploy manifest asset_prefix_default"); err != nil {
			return err
		}
	}
	if err := validateManagedLesserBodyDeployManifestTemplate(parsed, release, stage); err != nil {
		return err
	}
	if err := validateManagedLesserBodyAuxiliaryAssets(parsed.AuxiliaryAssets, stage, fmt.Sprintf("lesser-body-managed-%s.template.json", stage)); err != nil {
		return err
	}
	if err := validateManagedLesserBodyAuxiliaryAssetManifestAgreement(parsed.AuxiliaryAssets, release); err != nil {
		return err
	}
	return nil
}

func validateManagedLesserBodyDeployManifestTemplate(parsed *managedLesserBodyDeployManifest, release *managedLesserBodyReleaseManifest, stage string) error {
	templatePath := fmt.Sprintf("lesser-body-managed-%s.template.json", stage)
	templateMeta, ok := parsed.Templates[stage]
	if !ok {
		return fmt.Errorf("deploy manifest is missing template metadata for stage %s", stage)
	}
	if strings.TrimSpace(templateMeta.Path) != templatePath {
		return fmt.Errorf("unexpected deploy manifest template path for stage %s: %q", stage, strings.TrimSpace(templateMeta.Path))
	}
	if strings.TrimSpace(templateMeta.SHA256) == "" {
		return fmt.Errorf("deploy manifest template checksum is missing for stage %s", stage)
	}
	if strings.TrimSpace(templateMeta.Format) != requiredLesserBodyTemplateFormat {
		return fmt.Errorf("unexpected deploy manifest template format for stage %s: %q", stage, strings.TrimSpace(templateMeta.Format))
	}
	if release != nil {
		releaseTemplate := release.Artifacts.DeployTemplates[stage]
		if strings.TrimSpace(releaseTemplate.Path) != "" && strings.TrimSpace(releaseTemplate.Path) != strings.TrimSpace(templateMeta.Path) {
			return fmt.Errorf("deploy manifest template path for stage %s does not match release manifest", stage)
		}
		if strings.TrimSpace(releaseTemplate.SHA256) != "" && strings.TrimSpace(releaseTemplate.SHA256) != strings.TrimSpace(templateMeta.SHA256) {
			return fmt.Errorf("deploy manifest template checksum for stage %s does not match release manifest", stage)
		}
	}
	return nil
}

func validateManagedLesserBodyAuxiliaryAssets(assets []managedLesserBodyAuxiliaryAsset, stage string, templatePath string) error {
	seen := newManagedLesserBodyAuxiliaryAssetUniqueness(stage, templatePath)
	for i, asset := range assets {
		if err := validateManagedLesserBodyAuxiliaryAsset(asset, i, stage, templatePath, seen); err != nil {
			return err
		}
	}
	return nil
}

type managedLesserBodyAuxiliaryAssetUniqueness struct {
	ids                map[string]struct{}
	paths              map[string]struct{}
	s3Keys             map[string]struct{}
	templateParameters map[string]struct{}
	reservedPaths      map[string]struct{}
}

func newManagedLesserBodyAuxiliaryAssetUniqueness(stage string, templatePath string) *managedLesserBodyAuxiliaryAssetUniqueness {
	return &managedLesserBodyAuxiliaryAssetUniqueness{
		ids:                map[string]struct{}{},
		paths:              map[string]struct{}{},
		s3Keys:             map[string]struct{}{},
		templateParameters: map[string]struct{}{},
		reservedPaths:      managedLesserBodyReservedReleaseAssetPaths(stage, templatePath),
	}
}

func managedLesserBodyReservedReleaseAssetPaths(stage string, templatePath string) map[string]struct{} {
	stage = normalizeManagedLesserStage(stage)
	if stage == "" {
		stage = managedStageDev
	}
	templatePath = strings.TrimSpace(templatePath)
	if templatePath == "" {
		templatePath = fmt.Sprintf("lesser-body-managed-%s.template.json", stage)
	}
	paths := []string{
		managedLesserBodyReleaseManifestAsset,
		requiredLesserBodyChecksumsPath,
		requiredLesserBodyDeployManifestPath,
		requiredLesserBodyLambdaZipPath,
		requiredLesserBodyDeployScriptPath,
		templatePath,
	}
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path != "" {
			out[path] = struct{}{}
		}
	}
	return out
}

func (seen *managedLesserBodyAuxiliaryAssetUniqueness) rejectReservedReleaseAssetPath(value string, label string) error {
	if seen == nil || len(seen.reservedPaths) == 0 {
		return nil
	}
	value = strings.TrimSpace(value)
	if _, ok := seen.reservedPaths[value]; ok {
		return fmt.Errorf("%s %q is reserved for a verified lesser-body release artifact", strings.TrimSpace(label), value)
	}
	return nil
}

func validateManagedLesserBodyAuxiliaryAsset(
	asset managedLesserBodyAuxiliaryAsset,
	index int,
	stage string,
	templatePath string,
	seen *managedLesserBodyAuxiliaryAssetUniqueness,
) error {
	label := fmt.Sprintf("auxiliary asset %d", index)
	if err := validateManagedLesserBodyAuxiliaryAssetIdentity(asset, label, seen); err != nil {
		return err
	}
	if err := validateManagedLesserBodyAuxiliaryAssetObjectMetadata(asset, label, seen); err != nil {
		return err
	}
	if asset.Required && len(asset.TemplateReferences) == 0 {
		return fmt.Errorf("%s template_references are required", label)
	}
	return validateManagedLesserBodyTemplateReferences(asset, stage, templatePath, label)
}

func validateManagedLesserBodyAuxiliaryAssetIdentity(asset managedLesserBodyAuxiliaryAsset, label string, seen *managedLesserBodyAuxiliaryAssetUniqueness) error {
	if err := validateManagedLesserBodyUniqueRequiredValue(strings.TrimSpace(asset.ID), label+" id", seen.ids); err != nil {
		return err
	}
	if err := validateManagedReleaseAssetPath(asset.Path, label+" path"); err != nil {
		return err
	}
	if err := seen.rejectReservedReleaseAssetPath(asset.Path, label+" path"); err != nil {
		return err
	}
	return validateManagedLesserBodyUniqueRequiredValue(strings.TrimSpace(asset.Path), label+" path", seen.paths)
}

func validateManagedLesserBodyAuxiliaryAssetObjectMetadata(asset managedLesserBodyAuxiliaryAsset, label string, seen *managedLesserBodyAuxiliaryAssetUniqueness) error {
	if strings.TrimSpace(asset.SHA256) == "" {
		return fmt.Errorf("%s checksum is required", label)
	}
	if asset.Bytes <= 0 {
		return fmt.Errorf("%s bytes must be positive", label)
	}
	if err := validateManagedReleaseAssetPath(asset.S3Key, label+" s3_key"); err != nil {
		return err
	}
	if err := seen.rejectReservedReleaseAssetPath(asset.S3Key, label+" s3_key"); err != nil {
		return err
	}
	if err := validateManagedLesserBodyUniqueRequiredValue(strings.TrimSpace(asset.S3Key), label+" s3_key", seen.s3Keys); err != nil {
		return err
	}
	if err := validateManagedLesserBodyTemplateParameterName(asset.TemplateParameter, label+" template_parameter"); err != nil {
		return err
	}
	return validateManagedLesserBodyUniqueRequiredValue(strings.TrimSpace(asset.TemplateParameter), label+" template_parameter", seen.templateParameters)
}

func validateManagedLesserBodyUniqueRequiredValue(value string, label string, seen map[string]struct{}) error {
	if value == "" {
		return fmt.Errorf("%s is required", label)
	}
	if _, ok := seen[value]; ok {
		return fmt.Errorf("%s %q is duplicated", label, value)
	}
	seen[value] = struct{}{}
	return nil
}

func validateManagedLesserBodyAuxiliaryAssetManifestAgreement(deployAssets []managedLesserBodyAuxiliaryAsset, release *managedLesserBodyReleaseManifest) error {
	if release == nil {
		return nil
	}
	releaseAssets := release.Artifacts.AuxiliaryAssets
	if len(releaseAssets) == 0 && len(deployAssets) == 0 {
		return nil
	}

	releaseByPath := map[string]managedLesserBodyAuxiliaryAsset{}
	for _, asset := range releaseAssets {
		path := strings.TrimSpace(asset.Path)
		if path == "" {
			continue
		}
		releaseByPath[path] = asset
	}
	deployByPath := map[string]managedLesserBodyAuxiliaryAsset{}
	for _, asset := range deployAssets {
		path := strings.TrimSpace(asset.Path)
		if path == "" {
			continue
		}
		deployByPath[path] = asset
		releaseAsset, ok := releaseByPath[path]
		if !ok {
			return fmt.Errorf("release manifest is missing auxiliary asset %s declared in deploy manifest", path)
		}
		if err := validateManagedLesserBodyAuxiliaryAssetMetadataAgreement(path, releaseAsset, asset); err != nil {
			return err
		}
	}
	for path := range releaseByPath {
		if _, ok := deployByPath[path]; !ok {
			return fmt.Errorf("deploy manifest is missing auxiliary asset %s declared in release manifest", path)
		}
	}
	return nil
}

func validateManagedLesserBodyAuxiliaryAssetMetadataAgreement(path string, releaseAsset managedLesserBodyAuxiliaryAsset, deployAsset managedLesserBodyAuxiliaryAsset) error {
	if strings.TrimSpace(releaseAsset.ID) != strings.TrimSpace(deployAsset.ID) {
		return fmt.Errorf("auxiliary asset %s id does not match release manifest", path)
	}
	if strings.TrimSpace(releaseAsset.SHA256) != strings.TrimSpace(deployAsset.SHA256) {
		return fmt.Errorf("auxiliary asset %s checksum does not match release manifest", path)
	}
	if releaseAsset.Bytes != deployAsset.Bytes {
		return fmt.Errorf("auxiliary asset %s byte size does not match release manifest", path)
	}
	if releaseAsset.Required != deployAsset.Required {
		return fmt.Errorf("auxiliary asset %s required flag does not match release manifest", path)
	}
	if strings.TrimSpace(releaseAsset.S3Key) != strings.TrimSpace(deployAsset.S3Key) {
		return fmt.Errorf("auxiliary asset %s s3_key does not match release manifest", path)
	}
	if strings.TrimSpace(releaseAsset.TemplateParameter) != strings.TrimSpace(deployAsset.TemplateParameter) {
		return fmt.Errorf("auxiliary asset %s template_parameter does not match release manifest", path)
	}
	if strings.TrimSpace(releaseAsset.ContentType) != strings.TrimSpace(deployAsset.ContentType) {
		return fmt.Errorf("auxiliary asset %s content_type does not match release manifest", path)
	}
	return nil
}

func validateManagedLesserBodyTemplateParameterName(value string, label string) error {
	value = strings.TrimSpace(value)
	label = strings.TrimSpace(label)
	if label == "" {
		label = "template parameter"
	}
	if value == "" {
		return fmt.Errorf("%s is required", label)
	}
	for i, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return fmt.Errorf("%s must be a CloudFormation parameter identifier", label)
		}
	}
	return nil
}

func validateManagedLesserBodyTemplateReferences(asset managedLesserBodyAuxiliaryAsset, stage string, templatePath string, label string) error {
	stage = normalizeManagedLesserStage(stage)
	if stage == "" {
		stage = managedStageDev
	}
	hasSelectedStageReference := false
	for _, ref := range asset.TemplateReferences {
		refStage := normalizeManagedLesserStage(ref.Stage)
		refTemplate := strings.TrimSpace(ref.Template)
		if refStage == stage && refTemplate == templatePath {
			hasSelectedStageReference = true
			if strings.TrimSpace(ref.LogicalID) == "" {
				return fmt.Errorf("%s template reference logical_id is required", label)
			}
			if strings.TrimSpace(ref.PropertyPath) == "" {
				return fmt.Errorf("%s template reference property_path is required", label)
			}
			if strings.TrimSpace(ref.BucketParameter) == "" {
				return fmt.Errorf("%s template reference bucket_parameter is required", label)
			}
			if strings.TrimSpace(ref.KeyParameter) != strings.TrimSpace(asset.TemplateParameter) {
				return fmt.Errorf("%s template reference key_parameter must match template_parameter", label)
			}
		}
	}
	if asset.Required && !hasSelectedStageReference {
		return fmt.Errorf("%s is required but has no template reference for %s", label, templatePath)
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

func validateManagedLesserBodyTemplateAuxiliaryReferences(raw []byte, templatePath string, stage string, assets []managedLesserBodyAuxiliaryAsset) error {
	raw = []byte(strings.TrimSpace(string(raw)))
	templatePath = strings.TrimSpace(templatePath)
	if len(raw) == 0 {
		return managedTemplatePathErrorf(templatePath, "is empty")
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return managedTemplatePathErrorf(templatePath, "is not valid JSON: %v", err)
	}
	parameters, _, err := managedTemplateParameters(parsed, templatePath)
	if err != nil {
		return err
	}

	declaredParams := map[string]managedLesserBodyAuxiliaryAsset{}
	for _, asset := range assets {
		if !managedLesserBodyAuxiliaryAssetAppliesToTemplate(asset, stage, templatePath) {
			continue
		}
		paramName := strings.TrimSpace(asset.TemplateParameter)
		if paramName == "" {
			continue
		}
		declaredParams[paramName] = asset
		if _, ok := parameters[paramName]; !ok {
			return managedTemplateParameterErrorf(templatePath, paramName, "is required by auxiliary asset %s but is not declared", strings.TrimSpace(asset.ID))
		}
		for _, ref := range asset.TemplateReferences {
			if normalizeManagedLesserStage(ref.Stage) != normalizeManagedLesserStage(stage) || strings.TrimSpace(ref.Template) != templatePath {
				continue
			}
			if err := validateManagedLesserBodyTemplateReference(parsed, templatePath, asset, ref); err != nil {
				return err
			}
		}
	}
	return validateManagedLesserBodyTemplateDeclaredAuxiliaryRefs(parsed, templatePath, declaredParams)
}

func managedLesserBodyAuxiliaryAssetAppliesToTemplate(asset managedLesserBodyAuxiliaryAsset, stage string, templatePath string) bool {
	if len(asset.TemplateReferences) == 0 {
		return asset.Required
	}
	stage = normalizeManagedLesserStage(stage)
	for _, ref := range asset.TemplateReferences {
		if normalizeManagedLesserStage(ref.Stage) == stage && strings.TrimSpace(ref.Template) == strings.TrimSpace(templatePath) {
			return true
		}
	}
	return false
}

func validateManagedLesserBodyTemplateReference(parsed map[string]any, templatePath string, asset managedLesserBodyAuxiliaryAsset, ref managedLesserBodyAuxiliaryAssetTemplateReference) error {
	resources, ok := parsed["Resources"].(map[string]any)
	if !ok {
		return managedTemplatePathErrorf(templatePath, "Resources must be an object when auxiliary assets are declared")
	}
	resource, ok := resources[strings.TrimSpace(ref.LogicalID)].(map[string]any)
	if !ok {
		return managedTemplatePathErrorf(templatePath, "is missing auxiliary asset %s logical resource %s", strings.TrimSpace(asset.ID), strings.TrimSpace(ref.LogicalID))
	}
	rawValue, ok := managedTemplateValueAtPath(resource, strings.TrimSpace(ref.PropertyPath))
	if !ok {
		return managedTemplatePathErrorf(templatePath, "is missing auxiliary asset %s property %s", strings.TrimSpace(asset.ID), strings.TrimSpace(ref.PropertyPath))
	}
	if got := managedTemplateRef(rawValue); got != strings.TrimSpace(ref.KeyParameter) {
		return managedTemplatePathErrorf(templatePath, "property %s for auxiliary asset %s must Ref %s", strings.TrimSpace(ref.PropertyPath), strings.TrimSpace(asset.ID), strings.TrimSpace(ref.KeyParameter))
	}
	if strings.TrimSpace(ref.BucketParameter) != "" {
		if got := managedTemplateRef(managedTemplateValueAtSiblingPath(resource, strings.TrimSpace(ref.PropertyPath), "S3Bucket")); got != strings.TrimSpace(ref.BucketParameter) {
			return managedTemplatePathErrorf(templatePath, "bucket property for auxiliary asset %s must Ref %s", strings.TrimSpace(asset.ID), strings.TrimSpace(ref.BucketParameter))
		}
	}
	return nil
}

func managedTemplateValueAtSiblingPath(root map[string]any, propertyPath string, sibling string) any {
	propertyPath = strings.TrimSpace(propertyPath)
	sibling = strings.TrimSpace(sibling)
	if propertyPath == "" || sibling == "" {
		return nil
	}
	lastDot := strings.LastIndex(propertyPath, ".")
	if lastDot < 0 {
		return root[sibling]
	}
	value, _ := managedTemplateValueAtPath(root, propertyPath[:lastDot+1]+sibling)
	return value
}

func validateManagedLesserBodyTemplateDeclaredAuxiliaryRefs(parsed map[string]any, templatePath string, declaredParams map[string]managedLesserBodyAuxiliaryAsset) error {
	resources, ok := parsed["Resources"].(map[string]any)
	if !ok {
		return nil
	}
	for logicalID, rawResource := range resources {
		resource, ok := rawResource.(map[string]any)
		if !ok || strings.TrimSpace(fmt.Sprint(resource["Type"])) != "AWS::Lambda::Function" {
			continue
		}
		codeRaw, ok := managedTemplateValueAtPath(resource, "Properties.Code")
		if !ok {
			continue
		}
		code, ok := codeRaw.(map[string]any)
		if !ok {
			continue
		}
		bucketValue, hasBucket := code["S3Bucket"]
		keyValue, hasKey := code["S3Key"]
		if !hasBucket && !hasKey {
			continue
		}
		bucketRef := managedTemplateRef(bucketValue)
		keyRef := managedTemplateRef(keyValue)
		if bucketRef != "LesserBodyCodeBucketName" {
			return managedTemplatePathErrorf(
				templatePath,
				"lambda %s Code.S3Bucket must Ref LesserBodyCodeBucketName; literal, Fn::Sub, and CDK bootstrap buckets are not allowed",
				strings.TrimSpace(logicalID),
			)
		}
		if keyRef == "" {
			return managedTemplatePathErrorf(
				templatePath,
				"lambda %s Code.S3Key must Ref LesserBodyCodeObjectKey or a declared auxiliary asset parameter",
				strings.TrimSpace(logicalID),
			)
		}
		if keyRef == "LesserBodyCodeObjectKey" {
			continue
		}
		if _, ok := declaredParams[keyRef]; ok {
			continue
		}
		return managedTemplatePathErrorf(templatePath, "references auxiliary code key parameter %s from %s without a declared auxiliary asset", keyRef, strings.TrimSpace(logicalID))
	}
	return nil
}

func managedTemplateValueAtPath(root map[string]any, propertyPath string) (any, bool) {
	current := any(root)
	for _, part := range strings.Split(propertyPath, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = obj[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func managedTemplateRef(value any) string {
	obj, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	ref, _ := obj["Ref"].(string)
	return strings.TrimSpace(ref)
}

func buildManagedLesserBodyChecksumRequirements(parsed *managedLesserBodyReleaseManifest, stage string, deployManifests ...*managedLesserBodyDeployManifest) map[string]string {
	required := map[string]string{
		managedLesserBodyReleaseManifestAsset:                      "",
		requiredLesserBodyDeployManifestPath:                       strings.TrimSpace(parsed.Artifacts.DeployManifest.SHA256),
		requiredLesserBodyLambdaZipPath:                            strings.TrimSpace(parsed.Artifacts.LambdaZip.SHA256),
		requiredLesserBodyDeployScriptPath:                         strings.TrimSpace(parsed.Artifacts.DeployScript.SHA256),
		fmt.Sprintf("lesser-body-managed-%s.template.json", stage): strings.TrimSpace(parsed.Artifacts.DeployTemplates[stage].SHA256),
	}
	for _, asset := range parsed.Artifacts.AuxiliaryAssets {
		if strings.TrimSpace(asset.Path) != "" {
			required[strings.TrimSpace(asset.Path)] = strings.TrimSpace(asset.SHA256)
		}
	}
	for _, deployManifest := range deployManifests {
		if deployManifest == nil {
			continue
		}
		for _, asset := range deployManifest.AuxiliaryAssets {
			if strings.TrimSpace(asset.Path) != "" {
				required[strings.TrimSpace(asset.Path)] = strings.TrimSpace(asset.SHA256)
			}
		}
	}
	return required
}

func loadManagedLesserBodySchema2DeployManifest(
	ctx context.Context,
	client *http.Client,
	owner string,
	repo string,
	version string,
	releaseManifest *managedLesserBodyReleaseManifest,
	stage string,
) (*managedLesserBodyDeployManifest, error) {
	if releaseManifest == nil || releaseManifest.Artifacts.DeployManifest.Schema != supportedLesserBodyDeployManifestSchemaAuxiliaryAssets {
		return nil, nil
	}
	deployRaw, err := fetchManagedGitHubReleaseAsset(
		ctx,
		client,
		owner,
		repo,
		version,
		strings.TrimSpace(releaseManifest.Artifacts.DeployManifest.Path),
	)
	if err != nil {
		return nil, err
	}
	deployManifest, err := parseManagedLesserBodyDeployManifest(deployRaw)
	if err != nil {
		return nil, err
	}
	if err := validateManagedLesserBodyDeployManifest(deployManifest, releaseManifest, stage); err != nil {
		return nil, err
	}
	return deployManifest, nil
}

func loadManagedLesserBodyReleaseManifestForPreflight(
	ctx context.Context,
	client *http.Client,
	owner string,
	repo string,
	version string,
	stage string,
) (*managedLesserBodyReleaseManifest, error) {
	raw, err := fetchManagedGitHubReleaseAsset(
		ctx,
		client,
		owner,
		repo,
		version,
		managedLesserBodyReleaseManifestAsset,
	)
	if err != nil {
		return nil, err
	}
	parsed, err := parseManagedLesserBodyReleaseManifest(raw)
	if err != nil {
		return nil, err
	}
	if err := validateManagedLesserBodyReleaseManifest(parsed, version, stage); err != nil {
		return nil, err
	}
	return parsed, nil
}

func validateManagedLesserBodyReleaseChecksumsForPreflight(
	ctx context.Context,
	client *http.Client,
	owner string,
	repo string,
	version string,
	stage string,
	releaseManifest *managedLesserBodyReleaseManifest,
	deployManifest *managedLesserBodyDeployManifest,
) error {
	checksumsRaw, err := fetchManagedGitHubReleaseAsset(
		ctx,
		client,
		owner,
		repo,
		version,
		requiredLesserBodyChecksumsPath,
	)
	if err != nil {
		return err
	}
	checksums, err := parseManagedReleaseChecksums(checksumsRaw)
	if err != nil {
		return err
	}
	return validateManagedReleaseChecksumEntries(checksums, buildManagedLesserBodyChecksumRequirements(releaseManifest, stage, deployManifest))
}

func validateManagedLesserBodyTemplateAssetForPreflight(
	ctx context.Context,
	client *http.Client,
	owner string,
	repo string,
	version string,
	stage string,
	releaseManifest *managedLesserBodyReleaseManifest,
	deployManifest *managedLesserBodyDeployManifest,
) (string, error) {
	templatePath := strings.TrimSpace(releaseManifest.Artifacts.DeployTemplates[stage].Path)
	templateRaw, err := fetchManagedGitHubReleaseAsset(ctx, client, owner, repo, version, templatePath)
	if err != nil {
		return templatePath, err
	}
	if err := validateManagedLesserBodyTemplateJSON(templateRaw, templatePath); err != nil {
		return templatePath, err
	}
	if deployManifest != nil {
		if err := validateManagedLesserBodyTemplateAuxiliaryReferences(templateRaw, templatePath, stage, deployManifest.AuxiliaryAssets); err != nil {
			return templatePath, err
		}
	}
	if err := validateManagedLesserBodyLargeTemplateScriptSupport(ctx, client, owner, repo, version, releaseManifest, templatePath, templateRaw); err != nil {
		return templatePath, err
	}
	return templatePath, nil
}

func validateManagedLesserBodyLargeTemplateScriptSupport(
	ctx context.Context,
	client *http.Client,
	owner string,
	repo string,
	version string,
	releaseManifest *managedLesserBodyReleaseManifest,
	templatePath string,
	templateRaw []byte,
) error {
	if len(templateRaw) <= 51200 {
		return nil
	}
	scriptPath := strings.TrimSpace(releaseManifest.Artifacts.DeployScript.Path)
	scriptRaw, err := fetchManagedGitHubReleaseAsset(ctx, client, owner, repo, version, scriptPath)
	if err != nil {
		return err
	}
	if !strings.Contains(string(scriptRaw), "--s3-bucket") {
		return managedTemplatePathErrorf(templatePath, "exceeds 51200 bytes but %s does not support --s3-bucket", scriptPath)
	}
	return nil
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

	parsed, err := loadManagedLesserBodyReleaseManifestForPreflight(ctx, client, owner, repo, version, stage)
	if err != nil {
		return "", err
	}
	deployManifest, err := loadManagedLesserBodySchema2DeployManifest(ctx, client, owner, repo, version, parsed, stage)
	if err != nil {
		return "", err
	}
	if err := validateManagedLesserBodyReleaseChecksumsForPreflight(ctx, client, owner, repo, version, stage, parsed, deployManifest); err != nil {
		return "", err
	}
	return validateManagedLesserBodyTemplateAssetForPreflight(ctx, client, owner, repo, version, stage, parsed, deployManifest)
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
