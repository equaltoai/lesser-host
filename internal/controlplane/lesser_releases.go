package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

type githubLatestRelease struct {
	TagName string `json:"tag_name"`
}

var managedReleaseTagRE = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

func resolveLatestGitHubReleaseTag(ctx context.Context, owner string, repo string) (string, error) {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if owner == "" || repo == "" {
		return "", fmt.Errorf("owner and repo are required")
	}
	if !isValidGitHubRepoSegment(owner) || !isValidGitHubRepoSegment(repo) {
		return "", fmt.Errorf("invalid github repo")
	}

	u := &url.URL{
		Scheme: "https",
		Host:   "api.github.com",
		Path:   fmt.Sprintf("/repos/%s/%s/releases/latest", owner, repo),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "lesser-host-controlplane")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req) //nolint:gosec // Host is fixed to api.github.com and path segments are validated.
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("github latest release request failed (HTTP %d)", resp.StatusCode)
	}

	var parsed githubLatestRelease
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}

	tag := strings.TrimSpace(parsed.TagName)
	if tag == "" || tag == "null" {
		return "", fmt.Errorf("github latest release tag is empty")
	}
	return tag, nil
}

func isValidGitHubRepoSegment(s string) bool {
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

func validateManagedReleaseVersion(version string, field string) *apptheory.AppError {
	version = strings.TrimSpace(version)
	field = strings.TrimSpace(field)
	if version == "" {
		return nil
	}
	if strings.EqualFold(version, "latest") {
		return nil
	}
	if managedReleaseTagRE.MatchString(version) {
		return nil
	}
	message := "release version must be \"latest\" or a tag like v1.2.3"
	if field != "" {
		message = field + " must be \"latest\" or a tag like v1.2.3"
	}
	return &apptheory.AppError{Code: "app.bad_request", Message: message}
}
