package controlplane

import (
	"fmt"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

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

	// Owner enrichment: look up the User profile for the instance owner.
	// Best-effort with graceful fallback — owner may not have a User profile
	// (operator-created instances assigned to an owner who hasn't logged in
	// through the portal yet). Only handle, role, and avatar hash are surfaced;
	// account-level membership and internal fields are never leaked.
	enrichOwnerIdentity(ctx, s, inst, &resp)

	items, err := s.store.ListUpdateJobsByInstance(ctx.Context(), strings.TrimSpace(inst.Slug), 20)
	if err != nil {
		return resp
	}

	cats := categorizeDetailUpdateJobs(items)

	applyDerivedManagedUpdateSummary(&resp, cats.latestLesser, updateJobKindLesser)
	applyDerivedManagedUpdateSummary(&resp, cats.latestBody, updateJobKindBody)
	applyDerivedManagedUpdateSummary(&resp, cats.latestMCP, updateJobKindMCP)

	// Per-component drift flags and summary.  Drift uses the latest
	// successful (ok-status) job per kind, matching the /stack endpoint
	// contract.  The status summary above uses the latest job overall so
	// operators see the real current state (running, error, etc.).
	enrichDerivedDrift(&resp, cats.latestOkLesser, cats.latestOkBody, cats.latestOkMCP)

	return resp
}

// enrichOwnerIdentity reads the User profile for the instance owner and populates
// OwnerHandle, OwnerRole, and OwnerAvatarHash on the response. The lookup is
// best-effort: if the user record is not found or an error occurs, the fields
// remain empty (honest null/empty semantics).
func enrichOwnerIdentity(ctx *apptheory.Context, s *Server, inst *models.Instance, resp *instanceResponse) {
	if ctx == nil || s == nil || s.store == nil || s.store.DB == nil || inst == nil || resp == nil {
		return
	}
	ownerUsername := strings.TrimSpace(inst.Owner)
	if ownerUsername == "" {
		return
	}

	var user models.User
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.User{}).
		Where("PK", "=", fmt.Sprintf(models.KeyPatternUser, ownerUsername)).
		Where("SK", "=", models.SKProfile).
		First(&user)
	if theoryErrors.IsNotFound(err) || err != nil {
		return
	}

	resp.OwnerHandle = strings.TrimSpace(user.DisplayName)
	if resp.OwnerHandle == "" {
		resp.OwnerHandle = strings.TrimSpace(user.Username)
	}
	resp.OwnerRole = strings.TrimSpace(user.Role)
	// OwnerAvatarHash is intentionally empty — no avatar storage exists.
}

// enrichDerivedDrift computes per-component drift flags and a summary string
// from the latest update job per component kind. The drift values mirror the
// dedicated stack endpoint so the Instance Overview can consume them inline.
func enrichDerivedDrift(resp *instanceResponse, latestLesser *models.UpdateJob, latestBody *models.UpdateJob, latestMCP *models.UpdateJob) {
	if resp == nil {
		return
	}
	resp.LesserDrift = computeDriftForKind(latestLesser)
	resp.LesserBodyDrift = computeDriftForKind(latestBody)
	resp.MCPDrift = computeMCPDriftForDetail(latestMCP, latestBody)
	resp.DriftSummary = deriveDriftSummary(resp.LesserDrift, resp.LesserBodyDrift, resp.MCPDrift, resp.BodyEnabled)
}

// computeDriftForKind returns a drift label for a single component kind.
// Only ok-status jobs are treated as successfully deployed; all others
// (including queued, running, error, and nil) produce "unknown".
func computeDriftForKind(job *models.UpdateJob) string {
	if job == nil {
		return stackDriftUnknown
	}
	if strings.ToLower(strings.TrimSpace(job.Status)) != models.UpdateJobStatusOK {
		return stackDriftUnknown
	}
	return stackDriftOK
}

// computeMCPDriftForDetail returns drift for the MCP wiring in the instance
// detail context. Returns "wire-stale" when MCP was wired against a body version
// that differs from the currently deployed body version.
func computeMCPDriftForDetail(mcpJob *models.UpdateJob, bodyJob *models.UpdateJob) string {
	if mcpJob == nil {
		return stackDriftUnknown
	}
	if strings.ToLower(strings.TrimSpace(mcpJob.Status)) != models.UpdateJobStatusOK {
		return stackDriftUnknown
	}
	currentBodyVersion := ""
	if bodyJob != nil && strings.ToLower(strings.TrimSpace(bodyJob.Status)) == models.UpdateJobStatusOK {
		currentBodyVersion = strings.TrimSpace(bodyJob.LesserBodyVersion)
	}
	wiredAgainst := strings.TrimSpace(mcpJob.LesserBodyVersion)
	if wiredAgainst != "" && currentBodyVersion != "" && wiredAgainst != currentBodyVersion {
		return stackDriftWireStale
	}
	return stackDriftOK
}

// categorizedDetailUpdateJobs holds the latest job per component kind,
// separated into two tiers: the latest overall (any status, for managed
// update status fields) and the latest successful (ok) job (for drift,
// matching the /stack endpoint contract).
type categorizedDetailUpdateJobs struct {
	latestLesser   *models.UpdateJob
	latestBody     *models.UpdateJob
	latestMCP      *models.UpdateJob
	latestOkLesser *models.UpdateJob
	latestOkBody   *models.UpdateJob
	latestOkMCP    *models.UpdateJob
}

// categorizeDetailUpdateJobs extracts the latest overall and latest
// successful job per component kind from a list of update jobs (newest
// first).  The caller receives both tiers so status summary can report
// the real current state while drift uses the ok-tier only.
func categorizeDetailUpdateJobs(items []*models.UpdateJob) categorizedDetailUpdateJobs {
	var out categorizedDetailUpdateJobs
	for _, item := range items {
		if item == nil {
			continue
		}
		isOK := strings.ToLower(strings.TrimSpace(item.Status)) == models.UpdateJobStatusOK
		switch updateJobKind(item) {
		case updateJobKindBody:
			out.setBody(item, isOK)
		case updateJobKindMCP:
			out.setMCP(item, isOK)
		default:
			out.setLesser(item, isOK)
		}
	}
	return out
}

func (c *categorizedDetailUpdateJobs) setLesser(item *models.UpdateJob, isOK bool) {
	c.setIfNil(&c.latestLesser, item)
	if isOK {
		c.setIfNil(&c.latestOkLesser, item)
	}
}

func (c *categorizedDetailUpdateJobs) setBody(item *models.UpdateJob, isOK bool) {
	c.setIfNil(&c.latestBody, item)
	if isOK {
		c.setIfNil(&c.latestOkBody, item)
	}
}

func (c *categorizedDetailUpdateJobs) setMCP(item *models.UpdateJob, isOK bool) {
	c.setIfNil(&c.latestMCP, item)
	if isOK {
		c.setIfNil(&c.latestOkMCP, item)
	}
}

func (c *categorizedDetailUpdateJobs) setIfNil(dst **models.UpdateJob, item *models.UpdateJob) {
	if *dst == nil {
		*dst = item
	}
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
