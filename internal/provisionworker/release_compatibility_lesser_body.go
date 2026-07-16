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
	SchemaVersion                   int                                      `json:"schema_version"`
	ReleaseName                     string                                   `json:"release_name"`
	MinimumReleaseVersion           string                                   `json:"minimum_release_version"`
	ReleaseManifestAsset            string                                   `json:"release_manifest_asset"`
	ReleaseManifestSchema           int                                      `json:"release_manifest_schema"`
	SupportedReleaseManifestSchemas []int                                    `json:"supported_release_manifest_schemas"`
	Checksums                       ManagedReleaseChecksumsContract          `json:"checksums"`
	DeployManifest                  ManagedLesserBodyDeployManifestContract  `json:"deploy_manifest"`
	Deploy                          ManagedLesserBodyDeployContract          `json:"deploy"`
	LambdaZip                       ManagedLesserBodyPathContract            `json:"lambda_zip"`
	DeployScript                    ManagedLesserBodyPathContract            `json:"deploy_script"`
	SupportedStages                 []string                                 `json:"supported_stages"`
	DeployTemplates                 ManagedLesserBodyDeployTemplatesContract `json:"deploy_templates"`
	AuxiliaryAssets                 ManagedLesserBodyAuxiliaryAssetsContract `json:"auxiliary_assets"`
	InstancePlane                   ManagedLesserBodyInstancePlaneContract   `json:"instance_plane"`
}

type ManagedReleaseChecksumsContract struct {
	Path      string `json:"path"`
	Algorithm string `json:"algorithm"`
}

type ManagedLesserBodyDeployManifestContract struct {
	Path                    string `json:"path"`
	SchemaVersion           int    `json:"schema_version"`
	SupportedSchemaVersions []int  `json:"supported_schema_versions"`
}

type ManagedLesserBodyDeployContract struct {
	SchemaVersion                 int      `json:"schema_version"`
	SupportedSchemaVersions       []int    `json:"supported_schema_versions"`
	ManifestPath                  string   `json:"manifest_path"`
	TemplateSelection             string   `json:"template_selection"`
	SourceCheckoutRequired        bool     `json:"source_checkout_required"`
	NPMInstallRequired            bool     `json:"npm_install_required"`
	SupportedRequiredCapabilities []string `json:"supported_required_capabilities"`
}

type ManagedLesserBodyPathContract struct {
	Path string `json:"path"`
}

type ManagedLesserBodyDeployTemplatesContract struct {
	Format string `json:"format"`
}

type ManagedLesserBodyAuxiliaryAssetsContract struct {
	RequiredCapability string   `json:"required_capability"`
	S3KeySemantics     string   `json:"s3_key_semantics"`
	TemplateParameter  string   `json:"template_parameter"`
	RequiredFields     []string `json:"required_fields"`
}

type ManagedLesserBodyInstancePlaneContract struct {
	BuildArtifactPath          string   `json:"build_artifact_path"`
	ReleaseAssetRepresentation string   `json:"release_asset_representation"`
	LambdaLogicalID            string   `json:"lambda_logical_id"`
	RequiredTables             []string `json:"required_tables"`
	RequiredSSMParameters      []string `json:"required_ssm_parameters"`
}

func CurrentManagedLesserBodyCompatibilityContract() ManagedLesserBodyCompatibilityContract {
	return ManagedLesserBodyCompatibilityContract{
		SchemaVersion:                   managedLesserBodyCompatibilityContractSchemaVersion,
		ReleaseName:                     "lesser-body",
		MinimumReleaseVersion:           minimumSupportedManagedLesserBodyReleaseVersion,
		ReleaseManifestAsset:            managedLesserBodyReleaseManifestAsset,
		ReleaseManifestSchema:           requiredLesserBodyReleaseManifestSchema,
		SupportedReleaseManifestSchemas: []int{requiredLesserBodyReleaseManifestSchema, supportedLesserBodyReleaseManifestSchemaAuxiliaryAssets},
		Checksums: ManagedReleaseChecksumsContract{
			Path:      requiredLesserBodyChecksumsPath,
			Algorithm: requiredLesserBodyChecksumsAlgorithm,
		},
		DeployManifest: ManagedLesserBodyDeployManifestContract{
			Path:                    requiredLesserBodyDeployManifestPath,
			SchemaVersion:           requiredLesserBodyDeployManifestSchema,
			SupportedSchemaVersions: []int{requiredLesserBodyDeployManifestSchema, supportedLesserBodyDeployManifestSchemaAuxiliaryAssets},
		},
		Deploy: ManagedLesserBodyDeployContract{
			SchemaVersion:                 requiredLesserBodyDeploySchema,
			SupportedSchemaVersions:       []int{requiredLesserBodyDeploySchema, supportedLesserBodyDeploySchemaAuxiliaryAssets},
			ManifestPath:                  requiredLesserBodyDeployManifestPath,
			TemplateSelection:             requiredLesserBodyTemplateSelection,
			SourceCheckoutRequired:        false,
			NPMInstallRequired:            false,
			SupportedRequiredCapabilities: []string{managedLesserBodyCapabilityAuxiliaryAssetsV1},
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
		AuxiliaryAssets: ManagedLesserBodyAuxiliaryAssetsContract{
			RequiredCapability: managedLesserBodyCapabilityAuxiliaryAssetsV1,
			S3KeySemantics:     "prefix-relative",
			TemplateParameter:  "cloudformation-parameter-name",
			RequiredFields:     []string{"id", "path", "sha256", "bytes", "required", "s3_key", "template_parameter", "template_references"},
		},
		InstancePlane: ManagedLesserBodyInstancePlaneContract{
			BuildArtifactPath:          managedLesserBodyInstanceLambdaBuildArtifactPath,
			ReleaseAssetRepresentation: "schema2_auxiliary_asset",
			LambdaLogicalID:            managedLesserBodyInstanceLambdaLogicalID,
			RequiredTables:             managedLesserBodyInstancePlaneTableLogicalIDs(),
			RequiredSSMParameters:      managedLesserBodyInstancePlaneSSMParameterLogicalIDs(),
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
	deployManifest, err := loadManagedLesserBodySchema2DeployManifest(ctx, client, owner, repo, version, parsed, stage)
	if err != nil {
		return err
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
	return validateManagedReleaseChecksumEntries(checksums, buildManagedLesserBodyChecksumRequirements(parsed, stage, deployManifest))
}
