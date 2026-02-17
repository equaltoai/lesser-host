package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type githubLatestRelease struct {
	TagName string `json:"tag_name"`
}

func resolveLatestGitHubReleaseTag(ctx context.Context, owner string, repo string) (string, error) {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	if owner == "" || repo == "" {
		return "", fmt.Errorf("owner and repo are required")
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "lesser-host-controlplane")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
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
