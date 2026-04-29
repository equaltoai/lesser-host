package provisionworker

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func verifyTrustAuthEndpoint(ctx context.Context, client *http.Client, baseURL string, instanceKey string) (bool, string) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	instanceKey = strings.TrimSpace(instanceKey)
	if baseURL == "" {
		return false, "lesser host base url is missing"
	}
	if instanceKey == "" {
		return false, "instance key is missing"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/trust/verify", nil)
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+instanceKey)

	if client == nil {
		client = ssrfProtectedHTTPClient(nil)
	}
	resp, err := client.Do(req) //nolint:gosec // SSRF mitigated by ssrfProtectedHTTPClient (verify path) or caller-provided transport in tests.
	if err != nil {
		return false, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, ""
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, fmt.Sprintf("unauthorized (HTTP %d)", resp.StatusCode)
	}
	return false, fmt.Sprintf("unexpected status (HTTP %d)", resp.StatusCode)
}

func (s *Server) verifyUpdateTrustAuth(ctx context.Context, client *http.Client, job *models.UpdateJob) (bool, string) {
	if s == nil || job == nil {
		return false, updateVerifyInternalError
	}
	key, err := s.resolveInstanceKeyPlaintext(ctx, job)
	if err != nil {
		return false, err.Error()
	}
	baseURL := strings.TrimSpace(job.LesserHostBaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(s.publicBaseURL())
	}
	return verifyTrustAuthEndpoint(ctx, client, baseURL, key)
}
