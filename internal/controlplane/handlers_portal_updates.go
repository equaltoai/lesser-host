package controlplane

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/provisioning"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type createUpdateJobRequest struct {
	LesserVersion     string `json:"lesser_version,omitempty"`
	LesserBodyVersion string `json:"lesser_body_version,omitempty"`
	RotateInstanceKey bool   `json:"rotate_instance_key,omitempty"`
	BodyOnly          bool   `json:"body_only,omitempty"`
	MCPOnly           bool   `json:"mcp_only,omitempty"`
}

type updateJobResponse struct {
	ID           string `json:"id"`
	InstanceSlug string `json:"instance_slug"`
	Kind         string `json:"kind,omitempty"`
	Status       string `json:"status"`
	Step         string `json:"step,omitempty"`
	Note         string `json:"note,omitempty"`

	RunID  string `json:"run_id,omitempty"`
	RunURL string `json:"run_url,omitempty"`

	ActivePhase string `json:"active_phase,omitempty"`
	FailedPhase string `json:"failed_phase,omitempty"`

	DeployStatus string `json:"deploy_status,omitempty"`
	DeployRunID  string `json:"deploy_run_id,omitempty"`
	DeployRunURL string `json:"deploy_run_url,omitempty"`
	DeployError  string `json:"deploy_error,omitempty"`

	BodyStatus string `json:"body_status,omitempty"`
	BodyRunID  string `json:"body_run_id,omitempty"`
	BodyRunURL string `json:"body_run_url,omitempty"`
	BodyError  string `json:"body_error,omitempty"`

	MCPStatus string `json:"mcp_status,omitempty"`
	MCPRunID  string `json:"mcp_run_id,omitempty"`
	MCPRunURL string `json:"mcp_run_url,omitempty"`
	MCPError  string `json:"mcp_error,omitempty"`

	AccountID       string `json:"account_id,omitempty"`
	AccountRoleName string `json:"account_role_name,omitempty"`
	Region          string `json:"region,omitempty"`
	BaseDomain      string `json:"base_domain,omitempty"`

	LesserVersion     string `json:"lesser_version,omitempty"`
	LesserBodyVersion string `json:"lesser_body_version,omitempty"`
	BodyOnly          bool   `json:"body_only,omitempty"`
	MCPOnly           bool   `json:"mcp_only,omitempty"`

	LesserHostBaseURL              string `json:"lesser_host_base_url,omitempty"`
	LesserHostAttestationsURL      string `json:"lesser_host_attestations_url,omitempty"`
	LesserHostInstanceKeySecretARN string `json:"lesser_host_instance_key_secret_arn,omitempty"`
	TranslationEnabled             bool   `json:"translation_enabled"`

	TipEnabled         bool   `json:"tip_enabled"`
	TipChainID         int64  `json:"tip_chain_id,omitempty"`
	TipContractAddress string `json:"tip_contract_address,omitempty"`

	AIEnabled                 bool `json:"ai_enabled"`
	AIModerationEnabled       bool `json:"ai_moderation_enabled"`
	AINsfwDetectionEnabled    bool `json:"ai_nsfw_detection_enabled"`
	AISpamDetectionEnabled    bool `json:"ai_spam_detection_enabled"`
	AIPiiDetectionEnabled     bool `json:"ai_pii_detection_enabled"`
	AIContentDetectionEnabled bool `json:"ai_content_detection_enabled"`

	RotateInstanceKey    bool   `json:"rotate_instance_key,omitempty"`
	RotatedInstanceKeyID string `json:"rotated_instance_key_id,omitempty"`

	VerifyTranslationOK  *bool  `json:"verify_translation_ok,omitempty"`
	VerifyTrustOK        *bool  `json:"verify_trust_ok,omitempty"`
	VerifyTipsOK         *bool  `json:"verify_tips_ok,omitempty"`
	VerifyAIOK           *bool  `json:"verify_ai_ok,omitempty"`
	VerifyTranslationErr string `json:"verify_translation_err,omitempty"`
	VerifyTrustErr       string `json:"verify_trust_err,omitempty"`
	VerifyTipsErr        string `json:"verify_tips_err,omitempty"`
	VerifyAIErr          string `json:"verify_ai_err,omitempty"`

	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	RequestID    string `json:"request_id,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type listUpdateJobsResponse struct {
	Jobs  []updateJobResponse `json:"jobs"`
	Count int                 `json:"count"`
}

const (
	updateJobPhaseStatusPending = "pending"
	updateJobPhaseStatusSkipped = "skipped"

	updateJobKindLesser = "lesser"
	updateJobKindBody   = "lesser-body"
	updateJobKindMCP    = "mcp"
)

func updateJobResponseFromModel(j *models.UpdateJob) updateJobResponse {
	if j == nil {
		return updateJobResponse{}
	}
	return updateJobResponse{
		ID:                             strings.TrimSpace(j.ID),
		InstanceSlug:                   strings.TrimSpace(j.InstanceSlug),
		Kind:                           updateJobKind(j),
		Status:                         strings.TrimSpace(j.Status),
		Step:                           strings.TrimSpace(j.Step),
		Note:                           strings.TrimSpace(j.Note),
		RunID:                          strings.TrimSpace(j.RunID),
		RunURL:                         strings.TrimSpace(j.RunURL),
		ActivePhase:                    strings.TrimSpace(j.ActivePhase),
		FailedPhase:                    strings.TrimSpace(j.FailedPhase),
		DeployStatus:                   strings.TrimSpace(j.DeployStatus),
		DeployRunID:                    strings.TrimSpace(j.DeployRunID),
		DeployRunURL:                   strings.TrimSpace(j.DeployRunURL),
		DeployError:                    strings.TrimSpace(j.DeployError),
		BodyStatus:                     strings.TrimSpace(j.BodyStatus),
		BodyRunID:                      strings.TrimSpace(j.BodyRunID),
		BodyRunURL:                     strings.TrimSpace(j.BodyRunURL),
		BodyError:                      strings.TrimSpace(j.BodyError),
		MCPStatus:                      strings.TrimSpace(j.MCPStatus),
		MCPRunID:                       strings.TrimSpace(j.MCPRunID),
		MCPRunURL:                      strings.TrimSpace(j.MCPRunURL),
		MCPError:                       strings.TrimSpace(j.MCPError),
		AccountID:                      strings.TrimSpace(j.AccountID),
		AccountRoleName:                strings.TrimSpace(j.AccountRoleName),
		Region:                         strings.TrimSpace(j.Region),
		BaseDomain:                     strings.TrimSpace(j.BaseDomain),
		LesserVersion:                  strings.TrimSpace(j.LesserVersion),
		LesserBodyVersion:              strings.TrimSpace(j.LesserBodyVersion),
		BodyOnly:                       j.BodyOnly,
		MCPOnly:                        j.MCPOnly,
		LesserHostBaseURL:              strings.TrimSpace(j.LesserHostBaseURL),
		LesserHostAttestationsURL:      strings.TrimSpace(j.LesserHostAttestationsURL),
		LesserHostInstanceKeySecretARN: strings.TrimSpace(j.LesserHostInstanceKeySecretARN),
		TranslationEnabled:             j.TranslationEnabled,
		TipEnabled:                     j.TipEnabled,
		TipChainID:                     j.TipChainID,
		TipContractAddress:             strings.TrimSpace(j.TipContractAddress),
		AIEnabled:                      j.AIEnabled,
		AIModerationEnabled:            j.AIModerationEnabled,
		AINsfwDetectionEnabled:         j.AINsfwDetectionEnabled,
		AISpamDetectionEnabled:         j.AISpamDetectionEnabled,
		AIPiiDetectionEnabled:          j.AIPiiDetectionEnabled,
		AIContentDetectionEnabled:      j.AIContentDetectionEnabled,
		RotateInstanceKey:              j.RotateInstanceKey,
		RotatedInstanceKeyID:           strings.TrimSpace(j.RotatedInstanceKeyID),
		VerifyTranslationOK:            j.VerifyTranslationOK,
		VerifyTrustOK:                  j.VerifyTrustOK,
		VerifyTipsOK:                   j.VerifyTipsOK,
		VerifyAIOK:                     j.VerifyAIOK,
		VerifyTranslationErr:           strings.TrimSpace(j.VerifyTranslationErr),
		VerifyTrustErr:                 strings.TrimSpace(j.VerifyTrustErr),
		VerifyTipsErr:                  strings.TrimSpace(j.VerifyTipsErr),
		VerifyAIErr:                    strings.TrimSpace(j.VerifyAIErr),
		ErrorCode:                      strings.TrimSpace(j.ErrorCode),
		ErrorMessage:                   strings.TrimSpace(j.ErrorMessage),
		RequestID:                      strings.TrimSpace(j.RequestID),
		CreatedAt:                      j.CreatedAt,
		UpdatedAt:                      j.UpdatedAt,
	}
}

func updateJobKind(job *models.UpdateJob) string {
	if job == nil {
		return updateJobKindLesser
	}
	switch {
	case job.MCPOnly:
		return updateJobKindMCP
	case job.BodyOnly:
		return updateJobKindBody
	default:
		return updateJobKindLesser
	}
}

func updateJobKindFromRequest(req createUpdateJobRequest) string {
	switch {
	case req.MCPOnly:
		return updateJobKindMCP
	case req.BodyOnly:
		return updateJobKindBody
	default:
		return updateJobKindLesser
	}
}

func describeManagedUpdateRequest(req createUpdateJobRequest, lesserVersion string, lesserBodyVersion string) string {
	switch updateJobKindFromRequest(req) {
	case updateJobKindBody:
		if strings.TrimSpace(lesserBodyVersion) == "" {
			return "lesser-body update"
		}
		return "lesser-body update to " + strings.TrimSpace(lesserBodyVersion)
	case updateJobKindMCP:
		if strings.TrimSpace(lesserBodyVersion) == "" {
			return "MCP update"
		}
		return "MCP update for lesser-body " + strings.TrimSpace(lesserBodyVersion)
	default:
		desc := "Lesser update"
		if strings.TrimSpace(lesserVersion) != "" {
			desc += " to " + strings.TrimSpace(lesserVersion)
		}
		if req.RotateInstanceKey {
			desc += " with instance key rotation"
		}
		return desc
	}
}

func describeManagedUpdateJob(job *models.UpdateJob) string {
	if job == nil {
		return "update"
	}
	switch updateJobKind(job) {
	case updateJobKindBody:
		if strings.TrimSpace(job.LesserBodyVersion) == "" {
			return "lesser-body update"
		}
		return "lesser-body update to " + strings.TrimSpace(job.LesserBodyVersion)
	case updateJobKindMCP:
		if strings.TrimSpace(job.LesserBodyVersion) == "" {
			return "MCP update"
		}
		return "MCP update for lesser-body " + strings.TrimSpace(job.LesserBodyVersion)
	default:
		desc := "Lesser update"
		if strings.TrimSpace(job.LesserVersion) != "" {
			desc += " to " + strings.TrimSpace(job.LesserVersion)
		}
		if job.RotateInstanceKey {
			desc += " with instance key rotation"
		}
		return desc
	}
}

func sameManagedUpdateRequest(job *models.UpdateJob, req createUpdateJobRequest, lesserVersion string, lesserBodyVersion string) bool {
	if job == nil {
		return false
	}
	if updateJobKind(job) != updateJobKindFromRequest(req) {
		return false
	}
	switch updateJobKind(job) {
	case updateJobKindBody:
		return strings.EqualFold(strings.TrimSpace(job.LesserBodyVersion), strings.TrimSpace(lesserBodyVersion))
	case updateJobKindMCP:
		return strings.EqualFold(strings.TrimSpace(job.LesserBodyVersion), strings.TrimSpace(lesserBodyVersion))
	default:
		return strings.EqualFold(strings.TrimSpace(job.LesserVersion), strings.TrimSpace(lesserVersion)) &&
			job.RotateInstanceKey == req.RotateInstanceKey
	}
}

func managedUpdateConflictError(activeJob *models.UpdateJob, req createUpdateJobRequest, lesserVersion string, lesserBodyVersion string) *apptheory.AppError {
	requestDesc := describeManagedUpdateRequest(req, lesserVersion, lesserBodyVersion)
	activeDesc := describeManagedUpdateJob(activeJob)
	jobID := ""
	if activeJob != nil {
		jobID = strings.TrimSpace(activeJob.ID)
	}
	message := "cannot start " + requestDesc + " while " + activeDesc + " is already in progress"
	if jobID != "" {
		message += " (job " + jobID + ")"
	}
	return &apptheory.AppError{Code: "app.conflict", Message: message}
}

func parseCreateUpdateJobRequest(ctx *apptheory.Context) (createUpdateJobRequest, error) {
	var req createUpdateJobRequest
	if ctx == nil {
		return req, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if len(ctx.Request.Body) == 0 {
		return req, nil
	}
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return req, err
	}
	return req, nil
}

func (s *Server) enqueueUpdateJobBestEffort(ctx *apptheory.Context, jobID string) {
	if s == nil || s.queues == nil || ctx == nil {
		return
	}
	if strings.TrimSpace(s.cfg.ProvisionQueueURL) == "" {
		return
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return
	}

	_ = s.queues.enqueueProvisionJob(ctx.Context(), provisioning.JobMessage{
		Kind:  "update_job",
		JobID: jobID,
	})
}

func updateJobProcessingLeaseActive(job *models.UpdateJob, now time.Time) bool {
	if job == nil {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return !job.ProcessingLeaseUntil.IsZero() && job.ProcessingLeaseUntil.After(now)
}

func (s *Server) maybeNudgeActiveUpdateJob(ctx *apptheory.Context, job *models.UpdateJob, now time.Time) {
	if s == nil || ctx == nil || job == nil || !updateJobIsActive(job) {
		return
	}
	if updateJobProcessingLeaseActive(job, now) {
		return
	}
	if shouldNudgeAsyncJob(now, job.UpdatedAt) {
		s.enqueueUpdateJobBestEffort(ctx, job.ID)
	}
}

func managedUpdateMarkerForRequest(inst *models.Instance, req createUpdateJobRequest) (string, string) {
	if inst == nil {
		return "", ""
	}
	switch updateJobKindFromRequest(req) {
	case updateJobKindBody:
		return strings.ToLower(strings.TrimSpace(inst.LesserBodyUpdateStatus)), strings.TrimSpace(inst.LesserBodyUpdateJobID)
	case updateJobKindMCP:
		return strings.ToLower(strings.TrimSpace(inst.MCPUpdateStatus)), strings.TrimSpace(inst.MCPUpdateJobID)
	default:
		return strings.ToLower(strings.TrimSpace(inst.LesserUpdateStatus)), strings.TrimSpace(inst.LesserUpdateJobID)
	}
}

func setManagedUpdateInstanceMarker(ub core.UpdateBuilder, job *models.UpdateJob, status string, at time.Time) {
	if job == nil {
		return
	}
	status = strings.TrimSpace(status)
	jobID := strings.TrimSpace(job.ID)
	ub.Set("UpdateStatus", status)
	ub.Set("UpdateJobID", jobID)
	if !at.IsZero() {
		ub.Set("UpdatedAt", at)
	}
	switch updateJobKind(job) {
	case updateJobKindBody:
		ub.Set("LesserBodyUpdateStatus", status)
		ub.Set("LesserBodyUpdateJobID", jobID)
		if !at.IsZero() {
			ub.Set("LesserBodyUpdateAt", at)
		}
	case updateJobKindMCP:
		ub.Set("MCPUpdateStatus", status)
		ub.Set("MCPUpdateJobID", jobID)
		if !at.IsZero() {
			ub.Set("MCPUpdateAt", at)
		}
	default:
		ub.Set("LesserUpdateStatus", status)
		ub.Set("LesserUpdateJobID", jobID)
		if !at.IsZero() {
			ub.Set("LesserUpdateAt", at)
		}
	}
}

func (s *Server) getExistingUpdateJobFromInstanceState(ctx *apptheory.Context, inst *models.Instance, req createUpdateJobRequest) (*models.UpdateJob, bool) {
	if s == nil || s.store == nil || ctx == nil || inst == nil {
		return nil, false
	}

	status, jobID := managedUpdateMarkerForRequest(inst, req)
	if jobID == "" {
		status = strings.ToLower(strings.TrimSpace(inst.UpdateStatus))
		jobID = strings.TrimSpace(inst.UpdateJobID)
	}
	if jobID == "" {
		return nil, false
	}
	if status != models.UpdateJobStatusQueued && status != models.UpdateJobStatusRunning {
		return nil, false
	}

	job, err := s.store.GetUpdateJob(ctx.Context(), jobID)
	if err != nil || job == nil {
		return nil, false
	}
	return job, true
}

func updateJobIsActive(job *models.UpdateJob) bool {
	if job == nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(job.Status))
	return status == models.UpdateJobStatusQueued || status == models.UpdateJobStatusRunning
}

func (s *Server) findActiveUpdateJobsByInstance(ctx context.Context, slug string) ([]*models.UpdateJob, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	items, err := s.store.ListUpdateJobsByInstance(ctx, slug, 20)
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, err
	}
	active := make([]*models.UpdateJob, 0, len(items))
	for _, item := range items {
		if updateJobIsActive(item) {
			active = append(active, item)
		}
	}
	return active, nil
}

func (s *Server) repairStaleInstanceUpdateMarker(ctx context.Context, inst *models.Instance) error {
	if s == nil || s.store == nil || s.store.DB == nil || inst == nil {
		return nil
	}
	status := strings.ToLower(strings.TrimSpace(inst.UpdateStatus))
	if status != models.UpdateJobStatusQueued && status != models.UpdateJobStatusRunning {
		return nil
	}
	jobID := strings.TrimSpace(inst.UpdateJobID)
	if jobID == "" {
		return nil
	}

	updateInst := &models.Instance{Slug: strings.TrimSpace(inst.Slug)}
	_ = updateInst.UpdateKeys()
	return s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(updateInst, func(ub core.UpdateBuilder) error {
			ub.Set("UpdateStatus", models.UpdateJobStatusError)
			ub.Set("UpdatedAt", time.Now().UTC())
			markStaleManagedUpdateMarker(ub, jobID, inst.LesserUpdateJobID, inst.LesserUpdateStatus, "LesserUpdateStatus")
			markStaleManagedUpdateMarker(ub, jobID, inst.LesserBodyUpdateJobID, inst.LesserBodyUpdateStatus, "LesserBodyUpdateStatus")
			markStaleManagedUpdateMarker(ub, jobID, inst.MCPUpdateJobID, inst.MCPUpdateStatus, "MCPUpdateStatus")
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.ConditionExpression(
				"updateJobId = :jobId AND updateStatus = :status",
				map[string]any{
					":jobId":  jobID,
					":status": status,
				},
			),
		)
		return nil
	})
}

func updateInstanceNoActiveUpdateCondition() core.TransactCondition {
	return tabletheory.ConditionExpression(
		"attribute_not_exists(updateStatus) OR (updateStatus <> :queued AND updateStatus <> :running)",
		map[string]any{
			":queued":  models.UpdateJobStatusQueued,
			":running": models.UpdateJobStatusRunning,
		},
	)
}

func markStaleManagedUpdateMarker(
	ub core.UpdateBuilder,
	jobID string,
	markerJobID string,
	markerStatus string,
	statusField string,
) {
	if !strings.EqualFold(strings.TrimSpace(markerJobID), strings.TrimSpace(jobID)) {
		return
	}
	status := strings.ToLower(strings.TrimSpace(markerStatus))
	if status != models.UpdateJobStatusQueued && status != models.UpdateJobStatusRunning {
		return
	}
	ub.Set(statusField, models.UpdateJobStatusError)
}

func validateCreateUpdateJobRequest(req createUpdateJobRequest) *apptheory.AppError {
	if req.BodyOnly && req.MCPOnly {
		return &apptheory.AppError{Code: "app.bad_request", Message: "choose either body_only or mcp_only, not both"}
	}
	if (req.BodyOnly || req.MCPOnly) && req.RotateInstanceKey {
		return &apptheory.AppError{Code: "app.bad_request", Message: "body_only and mcp_only updates cannot rotate the instance key"}
	}
	return nil
}

func (s *Server) findExistingManagedUpdateJob(
	ctx *apptheory.Context,
	inst *models.Instance,
	slug string,
	req createUpdateJobRequest,
	lesserVersion string,
	lesserBodyVersion string,
) (*models.UpdateJob, *apptheory.AppError) {
	now := time.Now().UTC()
	if activeJobs, activeErr := s.findActiveUpdateJobsByInstance(ctx.Context(), slug); activeErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to inspect active update jobs"}
	} else if len(activeJobs) > 0 {
		var sameKindActive *models.UpdateJob
		for _, activeJob := range activeJobs {
			if sameManagedUpdateRequest(activeJob, req, lesserVersion, lesserBodyVersion) {
				s.maybeNudgeActiveUpdateJob(ctx, activeJob, now)
				return activeJob, nil
			}
			if sameKindActive == nil && updateJobKind(activeJob) == updateJobKindFromRequest(req) {
				sameKindActive = activeJob
			}
		}
		if sameKindActive != nil {
			return nil, managedUpdateConflictError(sameKindActive, req, lesserVersion, lesserBodyVersion)
		}
		return nil, managedUpdateConflictError(activeJobs[0], req, lesserVersion, lesserBodyVersion)
	}

	if job, ok := s.getExistingUpdateJobFromInstanceState(ctx, inst, req); ok {
		if sameManagedUpdateRequest(job, req, lesserVersion, lesserBodyVersion) {
			s.maybeNudgeActiveUpdateJob(ctx, job, now)
			return job, nil
		}
		if updateJobIsActive(job) {
			return nil, managedUpdateConflictError(job, req, lesserVersion, lesserBodyVersion)
		}
	}

	repairErr := s.repairStaleInstanceUpdateMarker(ctx.Context(), inst)
	if repairErr != nil && !theoryErrors.IsConditionFailed(repairErr) {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to repair stale update state"}
	}
	return nil, nil
}

func (s *Server) newManagedUpdateJob(
	ctx *apptheory.Context,
	inst *models.Instance,
	req createUpdateJobRequest,
	slug string,
	now time.Time,
	lesserVersion string,
	lesserBodyVersion string,
) (*models.UpdateJob, *apptheory.AppError) {
	id, tokenErr := newToken(16)
	if tokenErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create update job"}
	}

	baseURL := strings.TrimSpace(s.publicBaseURL())
	attestationsURL := strings.TrimSpace(baseURL)

	job := s.buildManagedUpdateJob(ctx, inst, req, id, slug, lesserVersion, lesserBodyVersion, baseURL, attestationsURL, now)
	_ = job.UpdateKeys()
	return job, nil
}

func (s *Server) resolveManagedUpdateCreateRequest(
	ctx *apptheory.Context,
	inst *models.Instance,
) (createUpdateJobRequest, string, string, *apptheory.AppError) {
	req, err := parseCreateUpdateJobRequest(ctx)
	if err != nil {
		if appErr, ok := err.(*apptheory.AppError); ok {
			return createUpdateJobRequest{}, "", "", appErr
		}
		return createUpdateJobRequest{}, "", "", &apptheory.AppError{Code: "app.internal", Message: "failed to parse update request"}
	}
	if appErr := validateCreateUpdateJobRequest(req); appErr != nil {
		return createUpdateJobRequest{}, "", "", appErr
	}
	lesserVersion, lesserBodyVersion, appErr := s.resolveManagedUpdateVersions(ctx.Context(), inst, req)
	if appErr != nil {
		return createUpdateJobRequest{}, "", "", appErr
	}
	return req, lesserVersion, lesserBodyVersion, nil
}

func (s *Server) handleManagedUpdateCreateConflict(
	ctx *apptheory.Context,
	slug string,
	req createUpdateJobRequest,
	lesserVersion string,
	lesserBodyVersion string,
) (*apptheory.Response, error) {
	now := time.Now().UTC()
	activeJobs, activeErr := s.findActiveUpdateJobsByInstance(ctx.Context(), slug)
	if activeErr == nil && len(activeJobs) > 0 {
		for _, activeJob := range activeJobs {
			if sameManagedUpdateRequest(activeJob, req, lesserVersion, lesserBodyVersion) {
				s.maybeNudgeActiveUpdateJob(ctx, activeJob, now)
				return apptheory.JSON(http.StatusOK, updateJobResponseFromModel(activeJob))
			}
		}
		return nil, managedUpdateConflictError(activeJobs[0], req, lesserVersion, lesserBodyVersion)
	}
	return nil, &apptheory.AppError{Code: "app.conflict", Message: "an update is already in progress for this instance"}
}

func (s *Server) handlePortalCreateInstanceUpdateJob(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requirePortalApproved(ctx); appErr != nil {
		return nil, appErr
	}

	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))
	if appErr := requireManagedUpdateInstance(inst); appErr != nil {
		return nil, appErr
	}

	req, lesserVersion, lesserBodyVersion, appErr := s.resolveManagedUpdateCreateRequest(ctx, inst)
	if appErr != nil {
		return nil, appErr
	}
	existingJob, existingErr := s.findExistingManagedUpdateJob(ctx, inst, slug, req, lesserVersion, lesserBodyVersion)
	if existingErr != nil {
		return nil, existingErr
	}
	if existingJob != nil {
		return apptheory.JSON(http.StatusOK, updateJobResponseFromModel(existingJob))
	}

	now := time.Now().UTC()
	job, appErr := s.newManagedUpdateJob(ctx, inst, req, slug, now, lesserVersion, lesserBodyVersion)
	if appErr != nil {
		return nil, appErr
	}

	updateInst := &models.Instance{Slug: slug}
	_ = updateInst.UpdateKeys()

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "portal.instance.update.start",
		Target:    fmt.Sprintf("instance:%s", slug),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Create(job)
		tx.UpdateWithBuilder(updateInst, func(ub core.UpdateBuilder) error {
			setManagedUpdateInstanceMarker(ub, job, models.UpdateJobStatusQueued, now)
			return nil
		}, tabletheory.IfExists(), updateInstanceNoActiveUpdateCondition())
		tx.Put(audit)
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return s.handleManagedUpdateCreateConflict(ctx, slug, req, lesserVersion, lesserBodyVersion)
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create update job"}
	}

	s.enqueueUpdateJobBestEffort(ctx, job.ID)
	return apptheory.JSON(http.StatusAccepted, updateJobResponseFromModel(job))
}

func (s *Server) handlePortalListInstanceUpdateJobs(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	limit := parseLimit(queryFirst(ctx, "limit"), 50, 1, 200)

	items, err := s.store.ListUpdateJobsByInstance(ctx.Context(), strings.TrimSpace(inst.Slug), limit)
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list update jobs"}
	}

	s.nudgeActiveUpdateJobIfNeeded(ctx, inst, items)

	out := make([]updateJobResponse, 0, len(items))
	for _, it := range items {
		out = append(out, updateJobResponseFromModel(it))
	}

	return apptheory.JSON(http.StatusOK, listUpdateJobsResponse{Jobs: out, Count: len(out)})
}

func requireManagedUpdateInstance(inst *models.Instance) *apptheory.AppError {
	if inst == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(inst.HostedAccountID) == "" ||
		strings.TrimSpace(inst.HostedRegion) == "" ||
		strings.TrimSpace(inst.HostedBaseDomain) == "" {
		return &apptheory.AppError{Code: "app.conflict", Message: "instance is not a managed provisioned instance"}
	}
	return nil
}

func (s *Server) resolveManagedLesserUpdateVersion(ctx context.Context, inst *models.Instance, req createUpdateJobRequest) (string, *apptheory.AppError) {
	lesserVersion := strings.TrimSpace(req.LesserVersion)
	if lesserVersion == "" {
		lesserVersion = strings.TrimSpace(inst.LesserVersion)
	}
	if lesserVersion == "" {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "lesser_version is required"}
	}
	return s.resolveManagedReleaseVersion(ctx, lesserVersion, "lesser_version", s.cfg.ManagedLesserGitHubOwner, s.cfg.ManagedLesserGitHubRepo, "failed to resolve latest Lesser release")
}

func (s *Server) resolveManagedBodyUpdateVersion(ctx context.Context, inst *models.Instance, req createUpdateJobRequest) (string, *apptheory.AppError) {
	lesserBodyVersion := strings.TrimSpace(req.LesserBodyVersion)
	if !req.BodyOnly && !req.MCPOnly {
		if lesserBodyVersion != "" {
			return "", &apptheory.AppError{Code: "app.bad_request", Message: "use body_only for lesser-body updates"}
		}
		return "", nil
	}
	if !effectiveBodyEnabled(inst.BodyEnabled) {
		return "", &apptheory.AppError{Code: "app.conflict", Message: "lesser-body updates are disabled for this instance"}
	}
	if req.BodyOnly {
		if lesserBodyVersion == "" {
			lesserBodyVersion = strings.TrimSpace(s.cfg.ManagedLesserBodyDefaultVersion)
		}
		if lesserBodyVersion == "" {
			return "", &apptheory.AppError{Code: "app.bad_request", Message: "lesser_body_version is required for body_only updates when no default lesser-body version is configured"}
		}
	} else if req.MCPOnly && lesserBodyVersion == "" {
		lesserBodyVersion = strings.TrimSpace(inst.LesserBodyVersion)
	}
	if lesserBodyVersion == "" {
		return "", nil
	}
	return s.resolveManagedReleaseVersion(ctx, lesserBodyVersion, "lesser_body_version", s.cfg.ManagedLesserBodyGitHubOwner, s.cfg.ManagedLesserBodyGitHubRepo, "failed to resolve latest lesser-body release")
}

func (s *Server) resolveManagedUpdateVersions(ctx context.Context, inst *models.Instance, req createUpdateJobRequest) (string, string, *apptheory.AppError) {
	lesserVersion, appErr := s.resolveManagedLesserUpdateVersion(ctx, inst, req)
	if appErr != nil {
		return "", "", appErr
	}
	lesserBodyVersion, appErr := s.resolveManagedBodyUpdateVersion(ctx, inst, req)
	if appErr != nil {
		return "", "", appErr
	}
	return lesserVersion, lesserBodyVersion, nil
}

func (s *Server) resolveManagedReleaseVersion(ctx context.Context, version string, field string, owner string, repo string, failureMessage string) (string, *apptheory.AppError) {
	version = strings.TrimSpace(version)
	if appErr := validateManagedReleaseVersion(version, field); appErr != nil {
		return "", appErr
	}
	if !strings.EqualFold(version, "latest") {
		return version, nil
	}
	tag, err := resolveLatestGitHubReleaseTag(ctx, owner, repo)
	if err != nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: failureMessage}
	}
	return tag, nil
}

func (s *Server) buildManagedUpdateJob(
	ctx *apptheory.Context,
	inst *models.Instance,
	req createUpdateJobRequest,
	id string,
	slug string,
	lesserVersion string,
	lesserBodyVersion string,
	baseURL string,
	attestationsURL string,
	now time.Time,
) *models.UpdateJob {
	job := &models.UpdateJob{
		ID:                             id,
		InstanceSlug:                   slug,
		Status:                         models.UpdateJobStatusQueued,
		Step:                           "queued",
		AccountID:                      strings.TrimSpace(inst.HostedAccountID),
		AccountRoleName:                strings.TrimSpace(s.cfg.ManagedInstanceRoleName),
		Region:                         strings.TrimSpace(inst.HostedRegion),
		BaseDomain:                     strings.TrimSpace(inst.HostedBaseDomain),
		LesserVersion:                  strings.TrimSpace(lesserVersion),
		LesserBodyVersion:              strings.TrimSpace(lesserBodyVersion),
		BodyOnly:                       req.BodyOnly,
		MCPOnly:                        req.MCPOnly,
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
		RotateInstanceKey:              req.RotateInstanceKey,
		CreatedAt:                      now,
		ExpiresAt:                      now.Add(30 * 24 * time.Hour),
		RequestID:                      strings.TrimSpace(ctx.RequestID),
	}
	switch {
	case req.MCPOnly:
		job.DeployStatus = updateJobPhaseStatusSkipped
		job.BodyStatus = updateJobPhaseStatusSkipped
		job.MCPStatus = updateJobPhaseStatusPending
	case req.BodyOnly:
		job.DeployStatus = updateJobPhaseStatusSkipped
		job.BodyStatus = updateJobPhaseStatusPending
		job.MCPStatus = updateJobPhaseStatusSkipped
	default:
		job.DeployStatus = updateJobPhaseStatusPending
		job.BodyStatus = updateJobPhaseStatusSkipped
		job.MCPStatus = updateJobPhaseStatusSkipped
	}
	return job
}

func effectiveUpdateTranslationEnabled(inst *models.Instance) bool {
	if inst == nil || inst.TranslationEnabled == nil {
		return true
	}
	return *inst.TranslationEnabled
}

func (s *Server) nudgeActiveUpdateJobIfNeeded(ctx *apptheory.Context, inst *models.Instance, items []*models.UpdateJob) {
	if s == nil || ctx == nil {
		return
	}
	now := time.Now().UTC()
	seenActive := false
	for _, it := range items {
		if it == nil || !updateJobIsActive(it) {
			continue
		}
		seenActive = true
		s.maybeNudgeActiveUpdateJob(ctx, it, now)
	}
	if seenActive {
		return
	}

	status := strings.ToLower(strings.TrimSpace(inst.UpdateStatus))
	if status != models.UpdateJobStatusQueued && status != models.UpdateJobStatusRunning {
		return
	}
	if err := s.repairStaleInstanceUpdateMarker(ctx.Context(), inst); err != nil && !theoryErrors.IsConditionFailed(err) {
		return
	}
}
