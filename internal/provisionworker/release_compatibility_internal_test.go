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

type releaseRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f releaseRoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCurrentManagedLesserCompatibilityContract_MatchesPublishedJSON(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "spec", "lesser-managed-compatibility.json"))
	require.NoError(t, err)

	var published ManagedLesserCompatibilityContract
	require.NoError(t, json.Unmarshal(raw, &published))
	require.Equal(t, CurrentManagedLesserCompatibilityContract(), published)
}

func TestValidateManagedLesserReleaseVersionSupported(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateManagedLesserReleaseVersionSupported("v1.2.6"))
	require.NoError(t, ValidateManagedLesserReleaseVersionSupported("v1.2.7"))
	require.NoError(t, ValidateManagedLesserReleaseVersionSupported("v1.3.0-rc.1"))
	require.ErrorContains(t, ValidateManagedLesserReleaseVersionSupported("v1.2.5"), "before v1.2.6 are not supported")
	require.ErrorContains(t, ValidateManagedLesserReleaseVersionSupported("latest"), "must be a concrete semver tag like v1.2.6")
}

func TestValidateManagedLesserReleaseCompatibility_RejectsUnsupportedVersionsBeforeFetch(t *testing.T) {
	t.Parallel()

	called := false
	client := &http.Client{Transport: releaseRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	})}

	err := ValidateManagedLesserReleaseCompatibility(context.Background(), client, "equaltoai", "lesser", "v1.2.5")
	require.ErrorContains(t, err, "before v1.2.6 are not supported")
	require.False(t, called, "expected compatibility check to fail before any network request")
}
