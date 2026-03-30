package provisionworker

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const (
	managedLesserCompatibilityContractSchemaVersion = 1
	minimumSupportedManagedLesserReleaseVersion     = "v1.2.6"
)

var managedReleaseSemverCoreRE = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+].*)?$`)

type ManagedLesserCompatibilityContract struct {
	SchemaVersion         int                               `json:"schema_version"`
	ReleaseName           string                            `json:"release_name"`
	MinimumReleaseVersion string                            `json:"minimum_release_version"`
	ReleaseManifestAsset  string                            `json:"release_manifest_asset"`
	ReleaseManifestSchema int                               `json:"release_manifest_schema"`
	MinimumReceiptSchema  int                               `json:"minimum_receipt_schema_version"`
	DeployArtifactsSchema int                               `json:"deploy_artifacts_schema_version"`
	LambdaBundle          ManagedLesserLambdaBundleContract `json:"lambda_bundle"`
}

type ManagedLesserLambdaBundleContract struct {
	Path                  string `json:"path"`
	ManifestPath          string `json:"manifest_path"`
	ManifestKind          string `json:"manifest_kind"`
	ManifestSchemaVersion int    `json:"manifest_schema_version"`
}

type managedReleaseSemver struct {
	major int
	minor int
	patch int
}

func CurrentManagedLesserCompatibilityContract() ManagedLesserCompatibilityContract {
	return ManagedLesserCompatibilityContract{
		SchemaVersion:         managedLesserCompatibilityContractSchemaVersion,
		ReleaseName:           "lesser",
		MinimumReleaseVersion: minimumSupportedManagedLesserReleaseVersion,
		ReleaseManifestAsset:  managedLesserReleaseManifestAsset,
		ReleaseManifestSchema: requiredLesserReleaseManifestSchema,
		MinimumReceiptSchema:  requiredLesserReleaseReceiptSchemaMin,
		DeployArtifactsSchema: requiredLesserDeployArtifactsSchema,
		LambdaBundle: ManagedLesserLambdaBundleContract{
			Path:                  requiredLesserBundleArchivePath,
			ManifestPath:          requiredLesserBundleManifestPath,
			ManifestKind:          requiredLesserBundleManifestKind,
			ManifestSchemaVersion: requiredLesserBundleManifestSchema,
		},
	}
}

func ValidateManagedLesserReleaseVersionSupported(version string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("lesser version is required")
	}

	current, err := parseManagedReleaseSemver(version)
	if err != nil {
		return err
	}
	minimum, err := parseManagedReleaseSemver(minimumSupportedManagedLesserReleaseVersion)
	if err != nil {
		return err
	}
	if compareManagedReleaseSemver(current, minimum) < 0 {
		return fmt.Errorf("managed Lesser releases before %s are not supported by this lesser-host build", minimumSupportedManagedLesserReleaseVersion)
	}
	return nil
}

func ValidateManagedLesserReleaseCompatibility(ctx context.Context, client *http.Client, owner string, repo string, version string) error {
	if err := ValidateManagedLesserReleaseVersionSupported(version); err != nil {
		return err
	}

	raw, err := fetchManagedGitHubReleaseAsset(
		ctx,
		client,
		owner,
		repo,
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
		client,
		owner,
		repo,
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

func parseManagedReleaseSemver(version string) (managedReleaseSemver, error) {
	version = strings.TrimSpace(version)
	matches := managedReleaseSemverCoreRE.FindStringSubmatch(version)
	if len(matches) != 4 {
		return managedReleaseSemver{}, fmt.Errorf("release version %q must be a concrete semver tag like v1.2.6", version)
	}

	major, err := strconv.Atoi(matches[1])
	if err != nil {
		return managedReleaseSemver{}, err
	}
	minor, err := strconv.Atoi(matches[2])
	if err != nil {
		return managedReleaseSemver{}, err
	}
	patch, err := strconv.Atoi(matches[3])
	if err != nil {
		return managedReleaseSemver{}, err
	}
	return managedReleaseSemver{major: major, minor: minor, patch: patch}, nil
}

func compareManagedReleaseSemver(left managedReleaseSemver, right managedReleaseSemver) int {
	switch {
	case left.major != right.major:
		return compareManagedReleaseInt(left.major, right.major)
	case left.minor != right.minor:
		return compareManagedReleaseInt(left.minor, right.minor)
	default:
		return compareManagedReleaseInt(left.patch, right.patch)
	}
}

func compareManagedReleaseInt(left int, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
