package provisionworker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/equaltoai/lesser-host/internal/outboundhttp"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type trustAuthVerifyResponse struct {
	Status       string `json:"status"`
	InstanceSlug string `json:"instance_slug"`
}

func verifyTrustAuthEndpoint(ctx context.Context, client *http.Client, baseURL string, instanceKey string, expectedInstanceSlug string) (bool, string) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	instanceKey = strings.TrimSpace(instanceKey)
	expectedInstanceSlug = strings.TrimSpace(expectedInstanceSlug)
	if baseURL == "" {
		return false, "lesser host base url is missing"
	}
	if instanceKey == "" {
		return false, "instance key is missing"
	}
	if expectedInstanceSlug == "" {
		return false, "expected instance slug is missing"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/trust/verify", nil)
	if err != nil {
		return false, err.Error()
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+instanceKey)

	if client == nil {
		client = outboundhttp.NewSSRFProtectedClient(nil)
	}
	resp, err := client.Do(req) //nolint:gosec // SSRF mitigated by outboundhttp.NewSSRFProtectedClient (verify path) or caller-provided transport in tests.
	if err != nil {
		return false, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, fmt.Sprintf("unauthorized (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Sprintf("unexpected status (HTTP %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return false, "failed to read trust verify response"
	}
	var out trustAuthVerifyResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return false, "invalid trust verify response"
	}
	if strings.TrimSpace(out.Status) != "ok" {
		return false, "trust verify response status is not ok"
	}
	if strings.TrimSpace(out.InstanceSlug) != expectedInstanceSlug {
		return false, fmt.Sprintf("expected instance_slug %q, got %q", expectedInstanceSlug, strings.TrimSpace(out.InstanceSlug))
	}
	return true, ""
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
	return verifyTrustAuthEndpoint(ctx, client, baseURL, key, strings.TrimSpace(job.InstanceSlug))
}
