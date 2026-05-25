package controlplane

import (
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// InstanceStackResponse is the customer-readable stack-state contract per
// Project 39 provisioning walk Change 5.1. Shape matches the existing M1.6
// StackCard client TypeScript types.
type instanceStackResponse struct {
	Slug         string             `json:"slug"`
	Lesser       instanceStackLesser `json:"lesser"`
	Body         instanceStackBody   `json:"body"`
	MCP          instanceStackMCP    `json:"mcp"`
	DriftSummary string             `json:"drift_summary,omitempty"`
}

type instanceStackLesser struct {
	CurrentVersion string `json:"current_version,omitempty"`
	TargetVersion  string `json:"target_version,omitempty"`
	DeployedAt     string `json:"deployed_at,omitempty"`
	SourceJobID    string `json:"source_job_id,omitempty"`
	Drift          string `json:"drift,omitempty"`
}

type instanceStackBody struct {
	Enabled        bool   `json:"enabled"`
	CurrentVersion string `json:"current_version,omitempty"`
	TargetVersion  string `json:"target_version,omitempty"`
	DeployedAt     string `json:"deployed_at,omitempty"`
	SourceJobID    string `json:"source_job_id,omitempty"`
	Drift          string `json:"drift,omitempty"`
}

type instanceStackMCP struct {
	WiredAt                 string `json:"wired_at,omitempty"`
	WiredAgainstBodyVersion string `json:"wired_against_body_version,omitempty"`
	CurrentBodyVersion      string `json:"current_body_version,omitempty"`
	Drift                   string `json:"drift,omitempty"`
}

// stackDrift values match the StackDrift TypeScript union:
//
//	"ok"         — current matches target
//	"stale"      — current is older than target
//	"wire-stale" — MCP wired against an older body version than deployed
//	"unknown"    — no target / no telemetry yet
const (
	stackDriftOK        = "ok"
	stackDriftStale     = "stale"
	stackDriftWireStale = "wire-stale"
	stackDriftUnknown   = "unknown"
)

// categorizedUpdateJobs holds the latest update job per component kind.
type categorizedUpdateJobs struct {
	lesser *models.UpdateJob
	body   *models.UpdateJob
	mcp    *models.UpdateJob
}

// handlePortalGetInstanceStack returns the customer-readable stack state for
// an instance. Ownership is enforced via requireInstanceAccess before any
// stack-state read. The handler delegates row construction to per-component
// helpers to keep cognitive complexity within the rubric threshold.
func (s *Server) handlePortalGetInstanceStack(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))

	jobs := categorizeLatestUpdateJobs(ctx, s, slug)
	provJob := loadProvisionJobFallback(ctx, s, inst)

	bodyEnabled := effectiveBodyEnabled(inst.BodyEnabled)

	lesser := buildStackLesser(jobs.lesser, provJob)
	body := buildStackBody(bodyEnabled, jobs.body, provJob)
	mcp := buildStackMCP(bodyEnabled, jobs.mcp, body.CurrentVersion, provJob)

	summary := deriveDriftSummary(lesser.Drift, body.Drift, mcp.Drift, bodyEnabled)

	return apptheory.JSON(http.StatusOK, instanceStackResponse{
		Slug:         slug,
		Lesser:       lesser,
		Body:         body,
		MCP:          mcp,
		DriftSummary: summary,
	})
}

// categorizeLatestUpdateJobs queries recent update jobs via GSI1 and returns
// the first (most-recently-created) for each component kind.
func categorizeLatestUpdateJobs(ctx *apptheory.Context, s *Server, slug string) categorizedUpdateJobs {
	items, _ := s.store.ListUpdateJobsByInstance(ctx.Context(), slug, 20)
	var out categorizedUpdateJobs
	for _, item := range items {
		if item == nil {
			continue
		}
		switch updateJobKind(item) {
		case updateJobKindBody:
			if out.body == nil {
				out.body = item
			}
		case updateJobKindMCP:
			if out.mcp == nil {
				out.mcp = item
			}
		default:
			if out.lesser == nil {
				out.lesser = item
			}
		}
	}
	return out
}

// loadProvisionJobFallback loads the initial provisioning job when available.
func loadProvisionJobFallback(ctx *apptheory.Context, s *Server, inst *models.Instance) *models.ProvisionJob {
	if inst == nil {
		return nil
	}
	provisionJobID := strings.TrimSpace(inst.ProvisionJobID)
	if provisionJobID == "" {
		return nil
	}
	job, _ := s.store.GetProvisionJob(ctx.Context(), provisionJobID)
	return job
}

// buildStackLesser constructs the Lesser stack row from update or provision info.
func buildStackLesser(job *models.UpdateJob, provJob *models.ProvisionJob) instanceStackLesser {
	if job != nil {
		return instanceStackLesser{
			CurrentVersion: trimOrEmpty(job.LesserVersion),
			TargetVersion:  trimOrEmpty(job.LesserVersion),
			DeployedAt:     formatStackTime(job.UpdatedAt),
			SourceJobID:    strings.TrimSpace(job.ID),
			Drift:          computeLesserDrift(job),
		}
	}
	if provJob != nil {
		return instanceStackLesser{
			CurrentVersion: trimOrEmpty(provJob.LesserVersion),
			DeployedAt:     formatStackTime(provJob.UpdatedAt),
			SourceJobID:    strings.TrimSpace(provJob.ID),
			Drift:          stackDriftUnknown,
		}
	}
	return instanceStackLesser{Drift: stackDriftUnknown}
}

// buildStackBody constructs the Body stack row.
func buildStackBody(enabled bool, job *models.UpdateJob, provJob *models.ProvisionJob) instanceStackBody {
	body := instanceStackBody{Enabled: enabled}
	if !enabled {
		body.Drift = stackDriftUnknown
		return body
	}
	if job != nil {
		body.CurrentVersion = trimOrEmpty(job.LesserBodyVersion)
		body.TargetVersion = trimOrEmpty(job.LesserBodyVersion)
		body.DeployedAt = formatStackTime(job.UpdatedAt)
		body.SourceJobID = strings.TrimSpace(job.ID)
		body.Drift = computeBodyDrift(job)
		return body
	}
	body.Drift = stackDriftUnknown
	if provJob != nil && !provJob.BodyProvisionedAt.IsZero() {
		body.DeployedAt = formatStackTime(provJob.BodyProvisionedAt)
	}
	return body
}

// buildStackMCP constructs the MCP wiring row.
func buildStackMCP(bodyEnabled bool, job *models.UpdateJob, currentBodyVersion string, provJob *models.ProvisionJob) instanceStackMCP {
	if job != nil {
		return instanceStackMCP{
			WiredAt:                 formatStackTime(job.UpdatedAt),
			WiredAgainstBodyVersion: trimOrEmpty(job.LesserBodyVersion),
			CurrentBodyVersion:      trimOrEmpty(currentBodyVersion),
			Drift:                   computeMCPDrift(job, currentBodyVersion),
		}
	}
	mcp := instanceStackMCP{Drift: stackDriftUnknown}
	if bodyEnabled && provJob != nil && !provJob.McpWiredAt.IsZero() {
		mcp.WiredAt = formatStackTime(provJob.McpWiredAt)
	}
	return mcp
}

// computeLesserDrift returns the drift status for the Lesser component.
func computeLesserDrift(job *models.UpdateJob) string {
	if job == nil {
		return stackDriftUnknown
	}
	if strings.ToLower(strings.TrimSpace(job.Status)) != models.UpdateJobStatusOK {
		return stackDriftUnknown
	}
	return stackDriftOK
}

// computeBodyDrift returns the drift status for the Body component.
func computeBodyDrift(job *models.UpdateJob) string {
	if job == nil {
		return stackDriftUnknown
	}
	if strings.ToLower(strings.TrimSpace(job.Status)) != models.UpdateJobStatusOK {
		return stackDriftUnknown
	}
	return stackDriftOK
}

// computeMCPDrift returns the drift status for the MCP wiring. Returns
// "wire-stale" when MCP was wired against a body version that differs from
// the currently deployed body version (meaning a re-wire is needed).
func computeMCPDrift(job *models.UpdateJob, currentBodyVersion string) string {
	if job == nil {
		return stackDriftUnknown
	}
	if strings.ToLower(strings.TrimSpace(job.Status)) != models.UpdateJobStatusOK {
		return stackDriftUnknown
	}
	wiredAgainst := strings.TrimSpace(job.LesserBodyVersion)
	currentBody := strings.TrimSpace(currentBodyVersion)
	if wiredAgainst != "" && currentBody != "" && wiredAgainst != currentBody {
		return stackDriftWireStale
	}
	return stackDriftOK
}

// deriveDriftSummary produces a single-line summary for the UI.
func deriveDriftSummary(lesserDrift, bodyDrift, mcpDrift string, bodyEnabled bool) string {
	stale, wireStale, unknowns := tallyDriftFlags(lesserDrift, bodyDrift, mcpDrift)

	if wireStale && stale {
		return "stale + MCP wire-stale"
	}
	if wireStale {
		return "MCP wire-stale"
	}
	if stale {
		return "stale components"
	}
	if unknowns >= 1 || !bodyEnabled {
		return "partial telemetry"
	}
	return "up to date"
}

// tallyDriftFlags counts stale, wire-stale, and unknown drift flags.
func tallyDriftFlags(lesserDrift, bodyDrift, mcpDrift string) (stale, wireStale bool, unknowns int) {
	for _, d := range []string{lesserDrift, bodyDrift} {
		switch strings.TrimSpace(d) {
		case stackDriftStale:
			stale = true
		case stackDriftUnknown:
			unknowns++
		}
	}
	switch strings.TrimSpace(mcpDrift) {
	case stackDriftWireStale:
		wireStale = true
	case stackDriftStale:
		stale = true
	case stackDriftUnknown:
		unknowns++
	}
	return
}

// trimOrEmpty returns the trimmed string or "" if it's empty.
func trimOrEmpty(s string) string {
	return strings.TrimSpace(s)
}

// formatStackTime formats a time value for JSON output. Returns "" for zero times.
func formatStackTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
