package provisionworker

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

const (
	managedLesserBodyCompatibilityContractSchemaVersion = 1
	minimumSupportedManagedLesserBodyReleaseVersion     = "v0.2.3"
)

type ManagedLesserBodyCompatibilityContract struct {
	SchemaVersion         int                                       `json:"schema_version"`
	ReleaseName           string                                    `json:"release_name"`
	MinimumReleaseVersion string                                    `json:"minimum_release_version"`
	ReleaseManifestAsset  string                                    `json:"release_manifest_asset"`
	ReleaseManifestSchema int                                       `json:"release_manifest_schema"`
	Checksums             ManagedReleaseChecksumsContract            `json:"checksums"`
	DeployManifest        ManagedLesserBodyDeployManifestContract    `json:"deploy_manifest"`
	Deploy                ManagedLesserBodyDeployContract            `json:"deploy"`
	LambdaZip             ManagedLesserBodyPathContract              `json:"lambda_zip"`
	DeployScript          ManagedLesserBodyPathContract              `json:"deploy_script"`
	SupportedStages       []string                                  `json:"supported_stages"`
	DeployTemplates       ManagedLesserBodyDeployTemplatesContract   `json:"deploy_templates"`
}

type ManagedReleaseChecksumsContract struct {
	Path      string `json:"path"`
	Algorithm string `json:"algorithm"`
}

type ManagedLesserBodyDeployManifestContract struct {
	Path          string `json:"path"`
	SchemaVersion int    `json:"schema_version"`
}

type ManagedLesserBodyDeployContract struct {
	SchemaVersion          int    `json:"schema_version"`
	ManifestPath           string `json:"manifest_path"`
	TemplateSelection      string `json:"template_selection"`
	SourceCheckoutRequired bool   `json:"source_checkout_required"`
	NPMInstallRequired     bool   `json:"npm_install_required"`
}

type ManagedLesserBodyPathContract struct {
	Path string `json:"path"`
}

type ManagedLesserBodyDeployTemplatesContract struct {
	Format string `json:"format"`
}

func CurrentManagedLesserBodyCompatibilityContract() ManagedLesserBodyCompatibilityContract {
	return ManagedLesserBodyCompatibilityContract{
		SchemaVersion:         managedLesserBodyCompatibilityContractSchemaVersion,
		ReleaseName:           "lesser-body",
		MinimumReleaseVersion: minimumSupportedManagedLesserBodyReleaseVersion,
		ReleaseManifestAsset:  managedLesserBodyReleaseManifestAsset,
		ReleaseManifestSchema: requiredLesserBodyReleaseManifestSchema,
		Checksums: ManagedReleaseChecksumsContract{
			Path:      requiredLesserBodyChecksumsPath,
			Algorithm: requiredLesserBodyChecksumsAlgorithm,
		},
		DeployManifest: ManagedLesserBodyDeployManifestContract{
			Path:          requiredLesserBodyDeployManifestPath,
			SchemaVersion: requiredLesserBodyDeployManifestSchema,
		},
		Deploy: ManagedLesserBodyDeployContract{
			SchemaVersion:          requiredLesserBodyDeploySchema,
			ManifestPath:           requiredLesserBodyDeployManifestPath,
			TemplateSelection:      requiredLesserBodyTemplateSelection,
			SourceCheckoutRequired: false,
			NPMInstallRequired:     false,
		},
		LambdaZip: ManagedLesserBodyPathContract{
			Path: requiredLesserBodyLambdaZipPath,
		},
		DeployScript: ManagedLesserBodyPathContract{
			Path: requiredLesserBodyDeployScriptPath,
		},
		SupportedStages: []string{managedStageDev, managedStageStaging, managedStageLive},
		DeployTemplates: ManagedLesserBodyDeployTemplatesContract{
			Format: requiredLesserBodyTemplateFormat,
		},
	}
}

func ValidateManagedLesserBodyReleaseVersionSupported(version string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("lesser-body version is required")
	}

	current, err := parseManagedReleaseSemver(version)
	if err != nil {
		return fmt.Errorf("lesser-body %w", err)
	}
	minimum, err := parseManagedReleaseSemver(minimumSupportedManagedLesserBodyReleaseVersion)
	if err != nil {
		return err
	}
	if compareManagedReleaseSemver(current, minimum) < 0 {
		return fmt.Errorf("managed lesser-body releases before %s are not supported by this lesser-host build", minimumSupportedManagedLesserBodyReleaseVersion)
	}
	return nil
}

func ValidateManagedLesserBodyReleaseCompatibility(ctx context.Context, client *http.Client, owner string, repo string, version string, stage string) error {
	if err := ValidateManagedLesserBodyReleaseVersionSupported(version); err != nil {
		return err
	}

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
		return err
	}
	parsed, err := parseManagedLesserBodyReleaseManifest(raw)
	if err != nil {
		return err
	}
	if validateErr := validateManagedLesserBodyReleaseManifest(parsed, version, stage); validateErr != nil {
		return validateErr
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
		return err
	}
	checksums, parseErr := parseManagedReleaseChecksums(checksumsRaw)
	if parseErr != nil {
		return parseErr
	}
	return validateManagedReleaseChecksumEntries(checksums, buildManagedLesserBodyChecksumRequirements(parsed, stage))
}
