package provisionworker

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCurrentManagedLesserBodyCompatibilityContract_MatchesPublishedJSON(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "spec", "lesser-body-managed-compatibility.json"))
	require.NoError(t, err)

	var published ManagedLesserBodyCompatibilityContract
	require.NoError(t, json.Unmarshal(raw, &published))
	require.Equal(t, CurrentManagedLesserBodyCompatibilityContract(), published)
}

func TestValidateManagedLesserBodyReleaseVersionSupported(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateManagedLesserBodyReleaseVersionSupported("v0.2.3"))
	require.NoError(t, ValidateManagedLesserBodyReleaseVersionSupported("v0.2.4"))
	require.NoError(t, ValidateManagedLesserBodyReleaseVersionSupported("v0.3.0-rc.1"))
	require.ErrorContains(t, ValidateManagedLesserBodyReleaseVersionSupported("v0.2.2"), "before v0.2.3 are not supported")
	require.ErrorContains(t, ValidateManagedLesserBodyReleaseVersionSupported("latest"), "must be a concrete semver tag like v1.2.6")
}

func TestValidateManagedLesserBodyReleaseCompatibility_RejectsUnsupportedVersionsBeforeFetch(t *testing.T) {
	t.Parallel()

	called := false
	client := &http.Client{Transport: releaseRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	})}

	err := ValidateManagedLesserBodyReleaseCompatibility(context.Background(), client, "equaltoai", "lesser-body", "v0.2.2", managedStageDev)
	require.ErrorContains(t, err, "before v0.2.3 are not supported")
	require.False(t, called, "expected compatibility check to fail before any network request")
}

func TestValidateManagedLesserBodyReleaseTemplatePreflight_RejectsNonStringTemplateDefaults(t *testing.T) {
	t.Parallel()

	const version = "v0.2.3"
	client := newManagedReleaseTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/equaltoai/lesser-body/releases/download/" + version + "/lesser-body-release.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(lesserBodyReleaseManifestJSON(t, version, managedStageDev))
		case "/equaltoai/lesser-body/releases/download/" + version + "/checksums.txt":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(lesserBodyChecksumsTXT(managedStageDev, true))
		case "/equaltoai/lesser-body/releases/download/" + version + "/lesser-body-managed-" + managedStageDev + ".template.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(lesserBodyTemplateJSONWithNonStringDefault(t, managedStageDev))
		default:
			http.NotFound(w, r)
		}
	}))

	_, err := ValidateManagedLesserBodyReleaseTemplatePreflight(context.Background(), client, "equaltoai", "lesser-body", version, managedStageDev)
	require.ErrorContains(t, err, "non-string Default")
	require.ErrorContains(t, err, "lesser-body-managed-dev.template.json")
}

func TestValidateManagedLesserBodyReleaseTemplatePreflight_RejectsLargeTemplatesWithoutS3BucketHelper(t *testing.T) {
	t.Parallel()

	const version = "v0.2.3"
	largeDescription := strings.Repeat("a", 52000)
	templateRaw, err := json.Marshal(map[string]any{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Description":              largeDescription,
		"Resources":                map[string]any{},
	})
	require.NoError(t, err)
	require.Greater(t, len(templateRaw), 51200)

	client := newManagedReleaseTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/equaltoai/lesser-body/releases/download/" + version + "/lesser-body-release.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(lesserBodyReleaseManifestJSON(t, version, managedStageDev))
		case "/equaltoai/lesser-body/releases/download/" + version + "/checksums.txt":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(lesserBodyChecksumsTXT(managedStageDev, true))
		case "/equaltoai/lesser-body/releases/download/" + version + "/lesser-body-managed-" + managedStageDev + ".template.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(templateRaw)
		case "/equaltoai/lesser-body/releases/download/" + version + "/deploy-lesser-body-from-release.sh":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("#!/usr/bin/env bash\nset -euo pipefail\n# missing s3 bucket flag\n"))
		default:
			http.NotFound(w, r)
		}
	}))

	_, err = ValidateManagedLesserBodyReleaseTemplatePreflight(context.Background(), client, "equaltoai", "lesser-body", version, managedStageDev)
	require.ErrorContains(t, err, "--s3-bucket")
	require.ErrorContains(t, err, "lesser-body-managed-dev.template.json")
}

func TestValidateManagedLesserBodyReleaseTemplatePreflight_AllowsLargeTemplatesWithS3BucketHelper(t *testing.T) {
	t.Parallel()

	const version = "v0.2.3"
	largeDescription := strings.Repeat("a", 52000)
	templateRaw, err := json.Marshal(map[string]any{
		"AWSTemplateFormatVersion": "2010-09-09",
		"Description":              largeDescription,
		"Resources":                map[string]any{},
	})
	require.NoError(t, err)
	require.Greater(t, len(templateRaw), 51200)

	client := newManagedReleaseTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/equaltoai/lesser-body/releases/download/" + version + "/lesser-body-release.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(lesserBodyReleaseManifestJSON(t, version, managedStageDev))
		case "/equaltoai/lesser-body/releases/download/" + version + "/checksums.txt":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(lesserBodyChecksumsTXT(managedStageDev, true))
		case "/equaltoai/lesser-body/releases/download/" + version + "/lesser-body-managed-" + managedStageDev + ".template.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(templateRaw)
		case "/equaltoai/lesser-body/releases/download/" + version + "/deploy-lesser-body-from-release.sh":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("#!/usr/bin/env bash\nset -euo pipefail\nargs=(--s3-bucket \"$1\")\n"))
		default:
			http.NotFound(w, r)
		}
	}))

	templatePath, err := ValidateManagedLesserBodyReleaseTemplatePreflight(context.Background(), client, "equaltoai", "lesser-body", version, managedStageDev)
	require.NoError(t, err)
	require.Equal(t, "lesser-body-managed-dev.template.json", templatePath)
}
