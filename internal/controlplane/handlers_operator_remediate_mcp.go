package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/provisioning"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// remediateMCPDriftResponse is the response for POST /api/v1/operators/instances/remediate-mcp-drift.
type remediateMCPDriftResponse struct {
	CreatedJobIDs []string `json:"created_job_ids"`
	Created       int      `json:"created"`
	Skipped       int      `json:"skipped"`
}

// wireStaleEntry captures a wire-stale instance identified during fleet drift
// computation for remediation.
type wireStaleEntry struct {
	slug           string
	currentBodyVer string
}

// handleOperatorRemediateMCPDrift creates MCP-only UpdateJobs for instances
// with wire-stale MCP wiring. Operator JWT required. Idempotent: skips slugs
// that already have an active MCP-only UpdateJob.
func (s *Server) handleOperatorRemediateMCPDrift(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	instances, appErr := s.listActiveInstances(ctx)
	if appErr != nil {
		return nil, appErr
	}

	// Compute fleet drift to identify wire-stale instances.
	lesserTarget := strings.TrimSpace(s.cfg.ManagedLesserDefaultVersion)
	bodyTarget := strings.TrimSpace(s.cfg.ManagedLesserBodyDefaultVersion)
	driftResp := computeFleetDrift(instances, lesserTarget, bodyTarget)

	// Collect wire-stale slugs with their current body versions.
	var wireStale []wireStaleEntry
	for _, entry := range driftResp.Instances {
		if entry.MCP.Drift == stackDriftWireStale {
			wireStale = append(wireStale, wireStaleEntry{
				slug:           entry.Slug,
				currentBodyVer: entry.Body.Current,
			})
		}
	}

	if len(wireStale) == 0 {
		return apptheory.JSON(http.StatusOK, remediateMCPDriftResponse{
			CreatedJobIDs: []string{},
			Created:       0,
			Skipped:       0,
		})
	}

	// Check for existing active MCP-only jobs (idempotency).
	activeJobs, listErr := s.store.ListActiveUpdateJobs(ctx.Context(), 500)
	if listErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list active update jobs"}
	}

	activeMCPSlugs := map[string]bool{}
	for _, job := range activeJobs {
		if job != nil && job.MCPOnly {
			activeMCPSlugs[strings.ToLower(strings.TrimSpace(job.InstanceSlug))] = true
		}
	}

	// Resolve instance details and create jobs.
	now := time.Now().UTC()
	actor := strings.TrimSpace(ctx.AuthIdentity)
	createdJobIDs, skipped := s.remediateWireStaleInstances(ctx, wireStale, activeMCPSlugs, bodyTarget, now)

	// Emit operator audit event.
	s.emitRemediationAudit(ctx, len(wireStale), now, actor)

	return apptheory.JSON(http.StatusOK, remediateMCPDriftResponse{
		CreatedJobIDs: createdJobIDs,
		Created:       len(createdJobIDs),
		Skipped:       skipped,
	})
}

// remediateWireStaleInstances creates MCP-only UpdateJobs for wire-stale slugs,
// skipping those that already have an active MCP-only job. Returns created
// job IDs and the count of skipped slugs.
func (s *Server) remediateWireStaleInstances(
	ctx *apptheory.Context,
	wireStale []wireStaleEntry,
	activeMCPSlugs map[string]bool,
	bodyTarget string,
	now time.Time,
) ([]string, int) {
	var createdJobIDs []string
	var skipped int

	for _, entry := range wireStale {
		if activeMCPSlugs[strings.ToLower(entry.slug)] {
			skipped++
			continue
		}

		inst, instErr := s.getInstance(ctx, entry.slug)
		if instErr != nil || inst == nil {
			skipped++
			continue
		}

		job, jobErr := s.createRemediationMCPJob(ctx, inst, entry.currentBodyVer, bodyTarget, now)
		if jobErr != nil {
			skipped++
			continue
		}

		if err := s.store.DB.WithContext(ctx.Context()).Model(job).Create(); err != nil {
			skipped++
			continue
		}

		_ = s.queues.enqueueProvisionJob(ctx.Context(), provisioning.JobMessage{
			Kind:  "update_job",
			JobID: job.ID,
		})

		createdJobIDs = append(createdJobIDs, strings.TrimSpace(job.ID))
	}

	return createdJobIDs, skipped
}

// createRemediationMCPJob builds an MCP-only UpdateJob for fleet remediation.
// It sets LesserBodyVersion to the body target (config default) so the wire-mcp
// deploy runner wires MCP against the target body version.
func (s *Server) createRemediationMCPJob(
	ctx *apptheory.Context,
	inst *models.Instance,
	currentBodyVer string,
	bodyTarget string,
	now time.Time,
) (*models.UpdateJob, error) {
	id, tokenErr := newToken(16)
	if tokenErr != nil {
		return nil, tokenErr
	}

	// Use target as the LesserBodyVersion for the MCP wiring step.
	lesserBodyVersion := strings.TrimSpace(bodyTarget)
	if lesserBodyVersion == "" {
		lesserBodyVersion = strings.TrimSpace(currentBodyVer)
	}
	if lesserBodyVersion == "" {
		return nil, fmt.Errorf("no body version available for MCP wiring")
	}

	baseURL := strings.TrimSpace(s.publicBaseURL())
	attestationsURL := strings.TrimSpace(baseURL)

	job := &models.UpdateJob{
		ID:                             id,
		InstanceSlug:                   strings.TrimSpace(inst.Slug),
		Status:                         models.UpdateJobStatusQueued,
		Step:                           "queued",
		AccountID:                      strings.TrimSpace(inst.HostedAccountID),
		AccountRoleName:                strings.TrimSpace(s.cfg.ManagedInstanceRoleName),
		Region:                         strings.TrimSpace(inst.HostedRegion),
		BaseDomain:                     strings.TrimSpace(inst.HostedBaseDomain),
		LesserVersion:                  strings.TrimSpace(inst.LesserVersion),
		LesserBodyVersion:              lesserBodyVersion,
		MCPOnly:                        true,
		LesserHostBaseURL:              baseURL,
		LesserHostAttestationsURL:      attestationsURL,
		LesserHostInstanceKeySecretARN: strings.TrimSpace(inst.LesserHostInstanceKeySecretARN),
		TranslationEnabled:             effectiveUpdateTranslationEnabled(inst),
		TipEnabled:                     effectiveTipEnabled(inst.TipEnabled),
		TipChainID:                     effectiveTipChainID(inst.TipChainID),
		TipContractAddress:             strings.TrimSpace(inst.TipContractAddress),
		AIEnabled:                      effectiveLesserAIEnabled(inst.LesserAIEnabled),
		AIModerationEnabled:            effectiveLesserAIModerationEnabled(inst.LesserAIModerationEnabled),
		AINsfwDetectionEnabled:         effectiveLesserAINsfwDetectionEnabled(inst.LesserAINsfwDetectionEnabled),
		AISpamDetectionEnabled:         effectiveLesserAISpamDetectionEnabled(inst.LesserAISpamDetectionEnabled),
		AIPiiDetectionEnabled:          effectiveLesserAIPiiDetectionEnabled(inst.LesserAIPiiDetectionEnabled),
		AIContentDetectionEnabled:      effectiveLesserAIContentDetectionEnabled(inst.LesserAIContentDetectionEnabled),
		DeployStatus:                   updateJobPhaseStatusSkipped,
		BodyStatus:                     updateJobPhaseStatusSkipped,
		MCPStatus:                      updateJobPhaseStatusPending,
		CreatedAt:                      now,
		ExpiresAt:                      now.Add(30 * 24 * time.Hour),
		RequestID:                      strings.TrimSpace(ctx.RequestID),
	}
	_ = job.UpdateKeys()
	return job, nil
}

// emitRemediationAudit writes an operator audit event for MCP remediation.
func (s *Server) emitRemediationAudit(
	ctx *apptheory.Context,
	numWireStale int,
	now time.Time,
	actor string,
) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return
	}

	audit := &models.AuditLogEntry{
		Actor:     actor,
		Action:    "operator.fleet.remediate_mcp_drift",
		Target:    fmt.Sprintf("fleet:mcp-remediation:%d-slugs", numWireStale),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	s.tryWriteAuditLog(ctx, audit)
}
