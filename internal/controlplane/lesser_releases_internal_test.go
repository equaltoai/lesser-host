package controlplane

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestResolveLatestGitHubReleaseTag_MissingOwnerOrRepo(t *testing.T) {
	_, err := resolveLatestGitHubReleaseTag(context.Background(), "", "repo")
	require.Error(t, err)
}

func TestResolveLatestGitHubReleaseTag_InvalidRequestURLErrors(t *testing.T) {
	_, err := resolveLatestGitHubReleaseTag(context.Background(), "own\ner", "repo")
	require.Error(t, err)
}

func TestResolveLatestGitHubReleaseTag_HTTPErrorReturnsError(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })

	var gotReq *http.Request
	http.DefaultTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotReq = r
		return nil, errors.New("boom")
	})

	_, err := resolveLatestGitHubReleaseTag(context.Background(), "owner", "repo")
	require.Error(t, err)
	require.NotNil(t, gotReq)
}

func TestResolveLatestGitHubReleaseTag_Non2xxReturnsError(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })

	var gotReq *http.Request
	http.DefaultTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotReq = r
		return newHTTPResponse(500, `{"tag_name":"v1.2.3"}`), nil
	})

	_, err := resolveLatestGitHubReleaseTag(context.Background(), "owner", "repo")
	require.Error(t, err)
	require.NotNil(t, gotReq)
}

func TestResolveLatestGitHubReleaseTag_InvalidJSONReturnsError(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })

	http.DefaultTransport = roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return newHTTPResponse(200, `{`), nil
	})

	_, err := resolveLatestGitHubReleaseTag(context.Background(), "owner", "repo")
	require.Error(t, err)
}

func TestResolveLatestGitHubReleaseTag_EmptyTagReturnsError(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })

	http.DefaultTransport = roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
		return newHTTPResponse(200, `{"tag_name":"null"}`), nil
	})

	_, err := resolveLatestGitHubReleaseTag(context.Background(), "owner", "repo")
	require.Error(t, err)
}

func TestResolveLatestGitHubReleaseTag_SuccessTrimsTagAndSetsHeaders(t *testing.T) {
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })

	var gotReq *http.Request
	http.DefaultTransport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotReq = r
		return newHTTPResponse(200, `{"tag_name":" v1.2.3 "}`), nil
	})

	tag, err := resolveLatestGitHubReleaseTag(context.Background(), "owner", "repo")
	require.NoError(t, err)
	require.Equal(t, "v1.2.3", tag)

	require.NotNil(t, gotReq)
	require.Equal(t, http.MethodGet, gotReq.Method)
	require.Equal(t, "application/vnd.github+json", gotReq.Header.Get("Accept"))
	require.Equal(t, "lesser-host-controlplane", gotReq.Header.Get("User-Agent"))
	require.NotNil(t, gotReq.URL)
	require.True(t, strings.Contains(gotReq.URL.Path, "/repos/owner/repo/releases/latest"))
}

func TestValidateManagedReleaseVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		wantErr bool
	}{
		{name: "empty", version: "", wantErr: false},
		{name: "latest", version: "latest", wantErr: false},
		{name: "plain semver tag", version: "v1.2.3", wantErr: false},
		{name: "prerelease tag", version: "v1.2.3-rc.1", wantErr: false},
		{name: "build metadata tag", version: "v1.2.3+build.5", wantErr: false},
		{name: "leading typo", version: "v.1.2.3", wantErr: true},
		{name: "missing v", version: "1.2.3", wantErr: true},
		{name: "short tag", version: "v1.2", wantErr: true},
		{name: "spaces", version: "v1.2.3 beta", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appErr := validateManagedReleaseVersion(tt.version, "lesser_version")
			if tt.wantErr {
				require.NotNil(t, appErr)
				require.Equal(t, "app.bad_request", appErr.Code)
				return
			}
			require.Nil(t, appErr)
		})
	}
}
