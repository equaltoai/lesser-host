package controlplane

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"

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
// with wire-stale MCP wiring, using evidence-based drift detection (from
// Blocker 2). Operator JWT required. Idempotent: skips slugs that already
// have an active MCP-only UpdateJob.
//
// Remediation wires MCP against the currently deployed body version (from
// drift evidence), not the fleet config target, so MCP is consistent with
// what's actually running on each instance.
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

	// Compute evidence-based drift to identify wire-stale instances.
	evidence := gatherFleetEvidence(ctx, s, instances)
	lesserTarget := strings.TrimSpace(s.cfg.ManagedLesserDefaultVersion)
	bodyTarget := strings.TrimSpace(s.cfg.ManagedLesserBodyDefaultVersion)
	driftResp := computeFleetDrift(evidence, lesserTarget, bodyTarget)

	// Collect wire-stale slugs with their current deployed body versions.
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

	// Check for existing active MCP-only jobs (idempotency via GSI2 UPDATE_ACTIVE).
	activeJobs, listErr := s.store.ListActiveUpdateJobs(ctx.Context(), 500)
	if listErr != nil {
		return nil, newAppTheoryError("app.internal", "failed to list active update jobs")
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

	// Remediation uses the drift entry's current_body (deployed body version),
	// not the fleet config target. bodyTarget is only a fallback when the
	// entry's current body version is empty.
	createdJobIDs, remediatedSlugs, skipped := s.remediateWireStaleInstances(
		ctx, wireStale, activeMCPSlugs, bodyTarget, now,
	)

	// Emit operator audit event AFTER remediation so we can include the
	// affected slug list and created job IDs.
	if auditErr := s.emitRemediationAudit(ctx, remediatedSlugs, createdJobIDs, now, actor); auditErr != nil {
		return nil, newAppTheoryError("app.internal", "failed to persist remediation audit log")
	}

	return apptheory.JSON(http.StatusOK, remediateMCPDriftResponse{
		CreatedJobIDs: createdJobIDs,
		Created:       len(createdJobIDs),
		Skipped:       skipped,
	})
}

// remediateWireStaleInstances creates MCP-only UpdateJobs for wire-stale slugs,
// skipping those that already have an active MCP-only job. Returns created
// job IDs, the list of successfully remediated slugs, and the count of skipped slugs.
func (s *Server) remediateWireStaleInstances(
	ctx *apptheory.Context,
	wireStale []wireStaleEntry,
	activeMCPSlugs map[string]bool,
	bodyTarget string,
	now time.Time,
) ([]string, []string, int) {
	var createdJobIDs []string
	var remediatedSlugs []string
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

		// Use the drift entry's current_body (deployed body version) as the
		// primary version source for MCP wiring. Fall back to bodyTarget only
		// if currentBodyVer is empty.
		wireVersion := strings.TrimSpace(entry.currentBodyVer)
		if wireVersion == "" {
			wireVersion = strings.TrimSpace(bodyTarget)
		}

		job, jobErr := s.createRemediationMCPJob(ctx, inst, wireVersion, now)
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
		remediatedSlugs = append(remediatedSlugs, entry.slug)
	}

	return createdJobIDs, remediatedSlugs, skipped
}

// createRemediationMCPJob builds an MCP-only UpdateJob for fleet remediation.
// wireBodyVersion is the body version to wire MCP against — this should be
// the currently deployed body version (from drift evidence), not the config
// target, so MCP is wired against what's actually running on the instance.
func (s *Server) createRemediationMCPJob(
	ctx *apptheory.Context,
	inst *models.Instance,
	wireBodyVersion string,
	now time.Time,
) (*models.UpdateJob, error) {
	id, tokenErr := newToken(16)
	if tokenErr != nil {
		return nil, tokenErr
	}

	lesserBodyVersion := strings.TrimSpace(wireBodyVersion)
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

// emitRemediationAudit writes an operator audit event for MCP remediation after
// remediation finishes. The key-bearing Target is bounded and deterministic;
// the full affected slug list and created job IDs are carried in Details.
func (s *Server) emitRemediationAudit(
	ctx *apptheory.Context,
	slugs []string,
	jobIDs []string,
	now time.Time,
	actor string,
) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil
	}

	joinedSlugs := strings.Join(slugs, ",")
	sum := sha256.Sum256([]byte(joinedSlugs))
	hash := fmt.Sprintf("%x", sum)[:16]
	target := fmt.Sprintf("fleet:mcp-remediation:count=%d:hash=%s", len(slugs), hash)
	details := fmt.Sprintf("slugs=%s;jobs=%s", joinedSlugs, strings.Join(jobIDs, ","))

	requestID := ""
	if ctx != nil {
		requestID = strings.TrimSpace(ctx.RequestID)
	}

	audit := &models.AuditLogEntry{
		Actor:     actor,
		Action:    "operator.fleet.remediate_mcp_drift",
		Target:    target,
		Details:   details,
		RequestID: requestID,
		CreatedAt: now,
	}
	if strings.TrimSpace(audit.Actor) == "" && ctx != nil {
		audit.Actor = strings.TrimSpace(ctx.AuthIdentity)
	}
	if strings.TrimSpace(audit.RequestID) == "" && ctx != nil {
		audit.RequestID = strings.TrimSpace(ctx.RequestID)
	}
	if ctx != nil {
		applyAuditSourceProvenance(ctx, audit)
		if s.tryWriteAuditLogWithContext(ctx.Context(), audit) {
			return nil
		}
		return fmt.Errorf("remediation audit log was not persisted")
	}
	if s.tryWriteAuditLogWithContext(context.Background(), audit) {
		return nil
	}
	return fmt.Errorf("remediation audit log was not persisted")
}
