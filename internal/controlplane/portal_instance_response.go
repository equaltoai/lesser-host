package controlplane

import (
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) portalInstanceResponseFromModel(inst *models.Instance) instanceResponse {
	resp := s.instanceResponseWithDerivedFields(inst)

	baseURL := ""
	if s != nil {
		baseURL = strings.TrimSpace(s.publicBaseURL())
	}

	// Guardrail: portal responses should only surface first-party domains.
	if baseURL != "" {
		resp.LesserHostBaseURL = baseURL
		resp.LesserHostAttestationsURL = baseURL
	}

	resp.LesserHostBaseURL = sanitizePortalURL(resp.LesserHostBaseURL, baseURL)
	resp.LesserHostAttestationsURL = sanitizePortalURL(resp.LesserHostAttestationsURL, baseURL)
	return resp
}

func sanitizePortalURL(raw string, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return strings.TrimSpace(fallback)
	}

	lowered := strings.ToLower(raw)
	if strings.Contains(lowered, ".lambda-url.") ||
		strings.Contains(lowered, ".on.aws") ||
		strings.Contains(lowered, "amazonaws.com") {
		return strings.TrimSpace(fallback)
	}

	if strings.HasPrefix(lowered, "http://") {
		return "https://" + strings.TrimPrefix(raw, "http://")
	}

	return raw
}
