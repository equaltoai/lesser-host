package controlplane

import (
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

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

func (s *Server) portalInstanceDetailResponse(ctx *apptheory.Context, inst *models.Instance) instanceResponse {
	resp := s.portalInstanceResponseFromModel(inst)
	if s == nil || s.store == nil || ctx == nil || inst == nil {
		return resp
	}

	items, err := s.store.ListUpdateJobsByInstance(ctx.Context(), strings.TrimSpace(inst.Slug), 20)
	if err != nil {
		return resp
	}

	var latestLesser *models.UpdateJob
	var latestBody *models.UpdateJob
	var latestMCP *models.UpdateJob
	for _, item := range items {
		if item == nil {
			continue
		}
		switch updateJobKind(item) {
		case updateJobKindBody:
			if latestBody == nil {
				latestBody = item
			}
		case updateJobKindMCP:
			if latestMCP == nil {
				latestMCP = item
			}
		default:
			if latestLesser == nil {
				latestLesser = item
			}
		}
	}

	applyDerivedManagedUpdateSummary(&resp, latestLesser, updateJobKindLesser)
	applyDerivedManagedUpdateSummary(&resp, latestBody, updateJobKindBody)
	applyDerivedManagedUpdateSummary(&resp, latestMCP, updateJobKindMCP)
	return resp
}

func applyDerivedManagedUpdateSummary(resp *instanceResponse, job *models.UpdateJob, kind string) {
	if resp == nil || job == nil {
		return
	}
	at := job.UpdatedAt
	var statusPtr *string
	var jobIDPtr *string
	var atPtr *time.Time
	switch kind {
	case updateJobKindBody:
		statusPtr = &resp.LesserBodyUpdateStatus
		jobIDPtr = &resp.LesserBodyUpdateJobID
		atPtr = &resp.LesserBodyUpdateAt
	case updateJobKindMCP:
		statusPtr = &resp.MCPUpdateStatus
		jobIDPtr = &resp.MCPUpdateJobID
		atPtr = &resp.MCPUpdateAt
	default:
		statusPtr = &resp.LesserUpdateStatus
		jobIDPtr = &resp.LesserUpdateJobID
		atPtr = &resp.LesserUpdateAt
	}
	setManagedUpdateSummaryField(statusPtr, strings.TrimSpace(job.Status))
	setManagedUpdateSummaryField(jobIDPtr, strings.TrimSpace(job.ID))
	setManagedUpdateSummaryTime(atPtr, at)

	if !at.IsZero() && (resp.UpdatedAt.IsZero() || at.After(resp.UpdatedAt)) {
		resp.UpdatedAt = at
	}
}

func setManagedUpdateSummaryField(dst *string, value string) {
	if dst == nil || strings.TrimSpace(*dst) != "" {
		return
	}
	*dst = strings.TrimSpace(value)
}

func setManagedUpdateSummaryTime(dst *time.Time, value time.Time) {
	if dst == nil || !dst.IsZero() || value.IsZero() {
		return
	}
	*dst = value
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
