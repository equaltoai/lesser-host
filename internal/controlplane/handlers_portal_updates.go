package controlplane

import (
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
	RotateInstanceKey bool   `json:"rotate_instance_key,omitempty"`
}

type updateJobResponse struct {
	ID           string `json:"id"`
	InstanceSlug string `json:"instance_slug"`
	Status       string `json:"status"`
	Step         string `json:"step,omitempty"`
	Note         string `json:"note,omitempty"`

	RunID  string `json:"run_id,omitempty"`
	RunURL string `json:"run_url,omitempty"`

	AccountID       string `json:"account_id,omitempty"`
	AccountRoleName string `json:"account_role_name,omitempty"`
	Region          string `json:"region,omitempty"`
	BaseDomain      string `json:"base_domain,omitempty"`

	LesserVersion string `json:"lesser_version,omitempty"`

	LesserHostBaseURL              string `json:"lesser_host_base_url,omitempty"`
	LesserHostAttestationsURL      string `json:"lesser_host_attestations_url,omitempty"`
	LesserHostInstanceKeySecretARN string `json:"lesser_host_instance_key_secret_arn,omitempty"`
	TranslationEnabled             bool   `json:"translation_enabled"`
	RotateInstanceKey              bool   `json:"rotate_instance_key,omitempty"`
	RotatedInstanceKeyID           string `json:"rotated_instance_key_id,omitempty"`

	VerifyTranslationOK  *bool  `json:"verify_translation_ok,omitempty"`
	VerifyTrustOK        *bool  `json:"verify_trust_ok,omitempty"`
	VerifyTranslationErr string `json:"verify_translation_err,omitempty"`
	VerifyTrustErr       string `json:"verify_trust_err,omitempty"`

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

func updateJobResponseFromModel(j *models.UpdateJob) updateJobResponse {
	if j == nil {
		return updateJobResponse{}
	}
	return updateJobResponse{
		ID:                             strings.TrimSpace(j.ID),
		InstanceSlug:                   strings.TrimSpace(j.InstanceSlug),
		Status:                         strings.TrimSpace(j.Status),
		Step:                           strings.TrimSpace(j.Step),
		Note:                           strings.TrimSpace(j.Note),
		RunID:                          strings.TrimSpace(j.RunID),
		RunURL:                         strings.TrimSpace(j.RunURL),
		AccountID:                      strings.TrimSpace(j.AccountID),
		AccountRoleName:                strings.TrimSpace(j.AccountRoleName),
		Region:                         strings.TrimSpace(j.Region),
		BaseDomain:                     strings.TrimSpace(j.BaseDomain),
		LesserVersion:                  strings.TrimSpace(j.LesserVersion),
		LesserHostBaseURL:              strings.TrimSpace(j.LesserHostBaseURL),
		LesserHostAttestationsURL:      strings.TrimSpace(j.LesserHostAttestationsURL),
		LesserHostInstanceKeySecretARN: strings.TrimSpace(j.LesserHostInstanceKeySecretARN),
		TranslationEnabled:             j.TranslationEnabled,
		RotateInstanceKey:              j.RotateInstanceKey,
		RotatedInstanceKeyID:           strings.TrimSpace(j.RotatedInstanceKeyID),
		VerifyTranslationOK:            j.VerifyTranslationOK,
		VerifyTrustOK:                  j.VerifyTrustOK,
		VerifyTranslationErr:           strings.TrimSpace(j.VerifyTranslationErr),
		VerifyTrustErr:                 strings.TrimSpace(j.VerifyTrustErr),
		ErrorCode:                      strings.TrimSpace(j.ErrorCode),
		ErrorMessage:                   strings.TrimSpace(j.ErrorMessage),
		RequestID:                      strings.TrimSpace(j.RequestID),
		CreatedAt:                      j.CreatedAt,
		UpdatedAt:                      j.UpdatedAt,
	}
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

func (s *Server) getExistingUpdateJobAndNudge(ctx *apptheory.Context, inst *models.Instance) (*models.UpdateJob, bool) {
	if s == nil || s.store == nil || ctx == nil || inst == nil {
		return nil, false
	}

	status := strings.ToLower(strings.TrimSpace(inst.UpdateStatus))
	jobID := strings.TrimSpace(inst.UpdateJobID)
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

	s.enqueueUpdateJobBestEffort(ctx, jobID)
	return job, true
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
	if job, ok := s.getExistingUpdateJobAndNudge(ctx, inst); ok {
		return apptheory.JSON(http.StatusOK, updateJobResponseFromModel(job))
	}

	if strings.TrimSpace(inst.HostedAccountID) == "" ||
		strings.TrimSpace(inst.HostedRegion) == "" ||
		strings.TrimSpace(inst.HostedBaseDomain) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "instance is not a managed provisioned instance"}
	}

	req, err := parseCreateUpdateJobRequest(ctx)
	if err != nil {
		return nil, err
	}

	lesserVersion := strings.TrimSpace(req.LesserVersion)
	if lesserVersion == "" {
		lesserVersion = strings.TrimSpace(inst.LesserVersion)
	}
	if lesserVersion == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "lesser_version is required"}
	}

	if strings.EqualFold(lesserVersion, "latest") {
		tag, err := resolveLatestGitHubReleaseTag(ctx.Context(), s.cfg.ManagedLesserGitHubOwner, s.cfg.ManagedLesserGitHubRepo)
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to resolve latest Lesser release"}
		}
		lesserVersion = tag
	}

	translationEnabled := true
	if inst.TranslationEnabled != nil {
		translationEnabled = *inst.TranslationEnabled
	}

	now := time.Now().UTC()
	id, tokenErr := newToken(16)
	if tokenErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create update job"}
	}

	baseURL := strings.TrimSpace(s.publicBaseURL())
	attestationsURL := strings.TrimSpace(baseURL)

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
		LesserHostBaseURL:              baseURL,
		LesserHostAttestationsURL:      attestationsURL,
		LesserHostInstanceKeySecretARN: strings.TrimSpace(inst.LesserHostInstanceKeySecretARN),
		TranslationEnabled:             translationEnabled,
		RotateInstanceKey:              req.RotateInstanceKey,
		CreatedAt:                      now,
		ExpiresAt:                      now.Add(30 * 24 * time.Hour),
		RequestID:                      strings.TrimSpace(ctx.RequestID),
	}
	_ = job.UpdateKeys()

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
			ub.Set("UpdateStatus", models.UpdateJobStatusQueued)
			ub.Set("UpdateJobID", strings.TrimSpace(job.ID))
			return nil
		}, tabletheory.IfExists())
		tx.Put(audit)
		return nil
	}); err != nil {
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

	out := make([]updateJobResponse, 0, len(items))
	for _, it := range items {
		out = append(out, updateJobResponseFromModel(it))
	}

	return apptheory.JSON(http.StatusOK, listUpdateJobsResponse{Jobs: out, Count: len(out)})
}
