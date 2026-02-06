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

	"github.com/equaltoai/lesser-host/internal/provisioning"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type startInstanceProvisionRequest struct {
	LesserVersion string `json:"lesser_version,omitempty"`
	Region        string `json:"region,omitempty"`
}

type provisionJobResponse struct {
	ID           string `json:"id"`
	InstanceSlug string `json:"instance_slug"`
	Status       string `json:"status"`
	Step         string `json:"step,omitempty"`
	Note         string `json:"note,omitempty"`

	Mode          string `json:"mode,omitempty"`
	Plan          string `json:"plan,omitempty"`
	Region        string `json:"region,omitempty"`
	Stage         string `json:"stage,omitempty"`
	LesserVersion string `json:"lesser_version,omitempty"`

	AccountRequestID string `json:"account_request_id,omitempty"`
	AccountID        string `json:"account_id,omitempty"`

	ParentHostedZoneID string   `json:"parent_hosted_zone_id,omitempty"`
	BaseDomain         string   `json:"base_domain,omitempty"`
	ChildHostedZoneID  string   `json:"child_hosted_zone_id,omitempty"`
	ChildNameServers   []string `json:"child_name_servers,omitempty"`

	RunID string `json:"run_id,omitempty"`

	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func provisionJobResponseFromModel(j *models.ProvisionJob) provisionJobResponse {
	if j == nil {
		return provisionJobResponse{}
	}
	return provisionJobResponse{
		ID:                 strings.TrimSpace(j.ID),
		InstanceSlug:       strings.TrimSpace(j.InstanceSlug),
		Status:             strings.TrimSpace(j.Status),
		Step:               strings.TrimSpace(j.Step),
		Note:               strings.TrimSpace(j.Note),
		Mode:               strings.TrimSpace(j.Mode),
		Plan:               strings.TrimSpace(j.Plan),
		Region:             strings.TrimSpace(j.Region),
		Stage:              strings.TrimSpace(j.Stage),
		LesserVersion:      strings.TrimSpace(j.LesserVersion),
		AccountRequestID:   strings.TrimSpace(j.AccountRequestID),
		AccountID:          strings.TrimSpace(j.AccountID),
		ParentHostedZoneID: strings.TrimSpace(j.ParentHostedZoneID),
		BaseDomain:         strings.TrimSpace(j.BaseDomain),
		ChildHostedZoneID:  strings.TrimSpace(j.ChildHostedZoneID),
		ChildNameServers:   append([]string(nil), j.ChildNameServers...),
		RunID:              strings.TrimSpace(j.RunID),
		ErrorCode:          strings.TrimSpace(j.ErrorCode),
		ErrorMessage:       strings.TrimSpace(j.ErrorMessage),
		CreatedAt:          j.CreatedAt,
		UpdatedAt:          j.UpdatedAt,
	}
}

func (s *Server) handleStartInstanceProvisioning(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	slug := strings.ToLower(strings.TrimSpace(ctx.Param("slug")))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}
	if !instanceSlugRE.MatchString(slug) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid slug"}
	}

	inst, err := s.getInstance(ctx, slug)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "instance not found"}
	}
	if err != nil || inst == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	// Idempotency: if a job is already queued/running, return it.
	existingStatus := strings.ToLower(strings.TrimSpace(inst.ProvisionStatus))
	existingJobID := strings.TrimSpace(inst.ProvisionJobID)
	if (existingStatus == models.ProvisionJobStatusQueued || existingStatus == models.ProvisionJobStatusRunning) && existingJobID != "" {
		if job, jerr := s.store.GetProvisionJob(ctx.Context(), existingJobID); jerr == nil && job != nil {
			// Best-effort: allow admins to "nudge" stalled jobs by re-enqueuing the existing idempotent job.
			if s.queues != nil && strings.TrimSpace(s.cfg.ProvisionQueueURL) != "" {
				_ = s.queues.enqueueProvisionJob(ctx.Context(), provisioning.JobMessage{
					Kind:  "provision_job",
					JobID: existingJobID,
				})
			}
			return apptheory.JSON(http.StatusOK, provisionJobResponseFromModel(job))
		}
	}

	var req startInstanceProvisionRequest
	if len(ctx.Request.Body) > 0 {
		if err := parseJSON(ctx, &req); err != nil {
			return nil, err
		}
	}

	id, err := newToken(16)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create provisioning job"}
	}

	now := time.Now().UTC()
	parentDomain := strings.TrimSpace(s.cfg.ManagedParentDomain)
	if parentDomain == "" {
		parentDomain = defaultManagedParentDomain
	}
	baseDomain := fmt.Sprintf("%s.%s", slug, strings.TrimPrefix(parentDomain, "."))

	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = strings.TrimSpace(s.cfg.ManagedDefaultRegion)
	}

	lesserVersion := strings.TrimSpace(req.LesserVersion)
	if lesserVersion == "" {
		lesserVersion = strings.TrimSpace(s.cfg.ManagedLesserDefaultVersion)
	}

	job := &models.ProvisionJob{
		ID:                 id,
		InstanceSlug:       slug,
		Status:             models.ProvisionJobStatusQueued,
		Step:               "queued",
		Mode:               "managed",
		Region:             region,
		LesserVersion:      lesserVersion,
		ParentHostedZoneID: strings.TrimSpace(s.cfg.ManagedParentHostedZoneID),
		BaseDomain:         baseDomain,
		CreatedAt:          now,
		ExpiresAt:          now.Add(30 * 24 * time.Hour),
		RequestID:          strings.TrimSpace(ctx.RequestID),
	}
	_ = job.UpdateKeys()

	updateInst := &models.Instance{Slug: slug}
	_ = updateInst.UpdateKeys()

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "instance.provision.start",
		Target:    fmt.Sprintf("instance:%s", slug),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Create(job)
		tx.UpdateWithBuilder(updateInst, func(ub core.UpdateBuilder) error {
			ub.Set("ProvisionStatus", models.ProvisionJobStatusQueued)
			ub.Set("ProvisionJobID", id)
			ub.Set("HostedBaseDomain", baseDomain)
			if region != "" {
				ub.Set("HostedRegion", region)
			}
			return nil
		}, tabletheory.IfExists())
		tx.Put(audit)
		return nil
	}); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to start provisioning"}
	}

	// Best-effort: enqueue provisioning work if configured.
	if s.queues != nil && strings.TrimSpace(s.cfg.ProvisionQueueURL) != "" {
		_ = s.queues.enqueueProvisionJob(ctx.Context(), provisioning.JobMessage{
			Kind:  "provision_job",
			JobID: id,
		})
	}

	return apptheory.JSON(http.StatusAccepted, provisionJobResponseFromModel(job))
}

func (s *Server) handleGetInstanceProvisioning(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	slug := strings.ToLower(strings.TrimSpace(ctx.Param("slug")))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}

	inst, err := s.getInstance(ctx, slug)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "instance not found"}
	}
	if err != nil || inst == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	jobID := strings.TrimSpace(inst.ProvisionJobID)
	if jobID == "" {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "no provisioning job"}
	}

	job, err := s.store.GetProvisionJob(ctx.Context(), jobID)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "provisioning job not found"}
	}
	if err != nil || job == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	return apptheory.JSON(http.StatusOK, provisionJobResponseFromModel(job))
}
