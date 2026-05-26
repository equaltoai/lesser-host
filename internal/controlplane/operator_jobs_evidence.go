package controlplane

import (
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// instanceJobsEvidence holds the latest successful update and provision job
// evidence for a single instance. Used by operator fleet endpoints to source
// version data from authoritative UpdateJob + ProvisionJob records rather than
// relying on Instance fields alone.
type instanceJobsEvidence struct {
	latestLesserUpdate *models.UpdateJob
	latestBodyUpdate   *models.UpdateJob
	latestMCPUpdate    *models.UpdateJob
	provisionJob       *models.ProvisionJob
	instance           *models.Instance
}

// gatherInstanceJobsEvidence queries update jobs (via GSI1) and the initial
// provision job for a single instance. Store.ListUpdateJobsByInstance returns
// newest-first rows, so the first successful (status=ok) update job per kind is
// the authoritative latest deployment evidence for operator fleet endpoints.
func gatherInstanceJobsEvidence(ctx *apptheory.Context, s *Server, inst *models.Instance) *instanceJobsEvidence {
	if inst == nil {
		return nil
	}

	ev := &instanceJobsEvidence{instance: inst}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))
	if slug == "" {
		return ev
	}

	// Query update job history and keep the first successful row per kind.
	// ListUpdateJobsByInstance orders newest-first; overwriting while iterating
	// would incorrectly select the oldest successful job in the returned page.
	items, _ := s.store.ListUpdateJobsByInstance(ctx.Context(), slug, 20)
	for _, item := range items {
		if item == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(item.Status)) != models.UpdateJobStatusOK {
			continue
		}
		switch updateJobKind(item) {
		case updateJobKindBody:
			if ev.latestBodyUpdate == nil {
				ev.latestBodyUpdate = item
			}
		case updateJobKindMCP:
			if ev.latestMCPUpdate == nil {
				ev.latestMCPUpdate = item
			}
		default:
			if ev.latestLesserUpdate == nil {
				ev.latestLesserUpdate = item
			}
		}
	}

	provJob := loadProvisionJobFallback(ctx, s, inst)
	ev.provisionJob = provJob

	return ev
}

// gatherFleetEvidence gathers jobs evidence for a list of active instances.
func gatherFleetEvidence(ctx *apptheory.Context, s *Server, instances []*models.Instance) []*instanceJobsEvidence {
	evidence := make([]*instanceJobsEvidence, 0, len(instances))
	for _, inst := range instances {
		ev := gatherInstanceJobsEvidence(ctx, s, inst)
		if ev != nil {
			evidence = append(evidence, ev)
		}
	}
	return evidence
}

// lesserVersionFromEvidence returns the best-available lesser version from
// evidence: latest successful lesser update → provision job → instance state.
func lesserVersionFromEvidence(ev *instanceJobsEvidence) string {
	if ev == nil || ev.instance == nil {
		return ""
	}
	if ev.latestLesserUpdate != nil {
		if v := strings.TrimSpace(ev.latestLesserUpdate.LesserVersion); v != "" {
			return v
		}
	}
	if ev.provisionJob != nil {
		if v := strings.TrimSpace(ev.provisionJob.LesserVersion); v != "" {
			return v
		}
	}
	return strings.TrimSpace(ev.instance.LesserVersion)
}

// lesserDeployedAtFromEvidence returns the best-available deployment timestamp
// for the lesser component: latest successful lesser update → provision job →
// instance update time.
func lesserDeployedAtFromEvidence(ev *instanceJobsEvidence) string {
	if ev == nil {
		return ""
	}
	if ev.latestLesserUpdate != nil {
		return formatStackTime(ev.latestLesserUpdate.UpdatedAt)
	}
	if ev.provisionJob != nil {
		return formatStackTime(ev.provisionJob.UpdatedAt)
	}
	if ev.instance != nil {
		return formatStackTime(ev.instance.UpdatedAt)
	}
	return ""
}

// bodyVersionFromEvidence returns the best-available body version from
// evidence: latest successful body update → instance state.
func bodyVersionFromEvidence(ev *instanceJobsEvidence) string {
	if ev == nil || ev.instance == nil {
		return ""
	}
	if ev.latestBodyUpdate != nil {
		if v := strings.TrimSpace(ev.latestBodyUpdate.LesserBodyVersion); v != "" {
			return v
		}
	}
	return strings.TrimSpace(ev.instance.LesserBodyVersion)
}

// bodyDeployedAtFromEvidence returns the best-available deployment timestamp
// for the body component: latest successful body update → provision job →
// instance body update time.
func bodyDeployedAtFromEvidence(ev *instanceJobsEvidence) string {
	if ev == nil {
		return ""
	}
	if ev.latestBodyUpdate != nil {
		return formatStackTime(ev.latestBodyUpdate.UpdatedAt)
	}
	if ev.provisionJob != nil && !ev.provisionJob.BodyProvisionedAt.IsZero() {
		return formatStackTime(ev.provisionJob.BodyProvisionedAt)
	}
	if ev.instance != nil {
		return formatStackTime(ev.instance.LesserBodyUpdateAt)
	}
	return ""
}

// mcpWiredAgainstFromEvidence returns the body version that MCP was last wired
// against from the latest successful MCP-only update job. It intentionally does
// not infer the version from Instance or ProvisionJob state: those records do
// not preserve the body version MCP was wired against after later body updates.
func mcpWiredAgainstFromEvidence(ev *instanceJobsEvidence) string {
	if ev == nil {
		return ""
	}
	if ev.latestMCPUpdate != nil {
		if v := strings.TrimSpace(ev.latestMCPUpdate.LesserBodyVersion); v != "" {
			return v
		}
	}
	return ""
}

// mcpCurrentBodyFromEvidence returns the currently deployed body version
// that MCP should be wired against: same as bodyVersionFromEvidence.
func mcpCurrentBodyFromEvidence(ev *instanceJobsEvidence) string {
	return bodyVersionFromEvidence(ev)
}

// mcpWiredAtFromEvidence returns the ISO8601 timestamp of the last MCP wiring.
func mcpWiredAtFromEvidence(ev *instanceJobsEvidence) string {
	if ev == nil {
		return ""
	}
	if ev.latestMCPUpdate != nil {
		return formatStackTime(ev.latestMCPUpdate.UpdatedAt)
	}
	if ev.provisionJob != nil && !ev.provisionJob.McpWiredAt.IsZero() {
		return formatStackTime(ev.provisionJob.McpWiredAt)
	}
	if ev.instance != nil {
		return formatStackTime(ev.instance.McpWiredAt)
	}
	return ""
}
