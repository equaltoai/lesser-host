package provisionworker

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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
