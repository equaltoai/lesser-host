package controlplane

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
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

type operatorProvisionJobListItem struct {
	ID           string `json:"id"`
	InstanceSlug string `json:"instance_slug"`
	Status       string `json:"status"`
	Step         string `json:"step,omitempty"`
	Note         string `json:"note,omitempty"`
	RunID        string `json:"run_id,omitempty"`

	Attempts    int64 `json:"attempts"`
	MaxAttempts int64 `json:"max_attempts,omitempty"`

	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	RequestID    string `json:"request_id,omitempty"`

	HasReceipt bool `json:"has_receipt"`
	HasSoulReceipt bool `json:"has_soul_receipt"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type operatorProvisionJobDetail struct {
	operatorProvisionJobListItem

	Mode          string `json:"mode,omitempty"`
	Plan          string `json:"plan,omitempty"`
	Region        string `json:"region,omitempty"`
	Stage         string `json:"stage,omitempty"`
	LesserVersion string `json:"lesser_version,omitempty"`

	AccountRequestID string `json:"account_request_id,omitempty"`
	AccountID        string `json:"account_id,omitempty"`
	AccountEmail     string `json:"account_email,omitempty"`

	ParentHostedZoneID string   `json:"parent_hosted_zone_id,omitempty"`
	BaseDomain         string   `json:"base_domain,omitempty"`
	ChildHostedZoneID  string   `json:"child_hosted_zone_id,omitempty"`
	ChildNameServers   []string `json:"child_name_servers,omitempty"`

	ReceiptJSON string `json:"receipt_json,omitempty"`
	SoulReceiptJSON string `json:"soul_receipt_json,omitempty"`
}

type listOperatorProvisionJobsResponse struct {
	Jobs  []operatorProvisionJobListItem `json:"jobs"`
	Count int                            `json:"count"`
}

type appendProvisionJobNoteRequest struct {
	Note string `json:"note"`
}

type adoptProvisionJobAccountRequest struct {
	AccountID    string `json:"account_id"`
	AccountEmail string `json:"account_email,omitempty"`
	Note         string `json:"note,omitempty"`
}

func operatorProvisionJobListItemFromModel(j *models.ProvisionJob) operatorProvisionJobListItem {
	if j == nil {
		return operatorProvisionJobListItem{}
	}
	receipt := strings.TrimSpace(j.ReceiptJSON)
	soulReceipt := strings.TrimSpace(j.SoulReceiptJSON)
	return operatorProvisionJobListItem{
		ID:           strings.TrimSpace(j.ID),
		InstanceSlug: strings.TrimSpace(j.InstanceSlug),
		Status:       strings.TrimSpace(j.Status),
		Step:         strings.TrimSpace(j.Step),
		Note:         strings.TrimSpace(j.Note),
		RunID:        strings.TrimSpace(j.RunID),
		Attempts:     j.Attempts,
		MaxAttempts:  j.MaxAttempts,
		ErrorCode:    strings.TrimSpace(j.ErrorCode),
		ErrorMessage: strings.TrimSpace(j.ErrorMessage),
		RequestID:    strings.TrimSpace(j.RequestID),
		HasReceipt:   receipt != "",
		HasSoulReceipt: soulReceipt != "",
		CreatedAt:    j.CreatedAt,
		UpdatedAt:    j.UpdatedAt,
	}
}

func operatorProvisionJobDetailFromModel(j *models.ProvisionJob) operatorProvisionJobDetail {
	if j == nil {
		return operatorProvisionJobDetail{}
	}
	base := operatorProvisionJobListItemFromModel(j)
		return operatorProvisionJobDetail{
		operatorProvisionJobListItem: base,
		Mode:                         strings.TrimSpace(j.Mode),
		Plan:                         strings.TrimSpace(j.Plan),
		Region:                       strings.TrimSpace(j.Region),
		Stage:                        strings.TrimSpace(j.Stage),
		LesserVersion:                strings.TrimSpace(j.LesserVersion),
		AccountRequestID:             strings.TrimSpace(j.AccountRequestID),
		AccountID:                    strings.TrimSpace(j.AccountID),
		AccountEmail:                 strings.TrimSpace(j.AccountEmail),
		ParentHostedZoneID:           strings.TrimSpace(j.ParentHostedZoneID),
		BaseDomain:                   strings.TrimSpace(j.BaseDomain),
			ChildHostedZoneID:            strings.TrimSpace(j.ChildHostedZoneID),
			ChildNameServers:             append([]string(nil), j.ChildNameServers...),
			ReceiptJSON:                  strings.TrimSpace(j.ReceiptJSON),
			SoulReceiptJSON:              strings.TrimSpace(j.SoulReceiptJSON),
		}
	}

func queryFirst(ctx *apptheory.Context, key string) string {
	if ctx == nil || key == "" || len(ctx.Request.Query) == 0 {
		return ""
	}
	if v := ctx.Request.Query[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func parseLimit(raw string, def, min, max int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func isAWSAccountID(raw string) bool {
	raw = strings.TrimSpace(raw)
	if len(raw) != 12 {
		return false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseAdoptProvisionJobAccountRequest(ctx *apptheory.Context) (adoptProvisionJobAccountRequest, *apptheory.AppError) {
	var req adoptProvisionJobAccountRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		if appErr, ok := err.(*apptheory.AppError); ok {
			return req, appErr
		}
		return req, &apptheory.AppError{Code: "app.bad_request", Message: "invalid request"}
	}
	req.AccountID = strings.TrimSpace(req.AccountID)
	req.AccountEmail = strings.TrimSpace(req.AccountEmail)
	req.Note = strings.TrimSpace(req.Note)
	if !isAWSAccountID(req.AccountID) {
		return req, &apptheory.AppError{Code: "app.bad_request", Message: "account_id must be a 12-digit AWS account id"}
	}
	return req, nil
}

func validateAdoptableProvisionJob(job *models.ProvisionJob) *apptheory.AppError {
	if job == nil {
		return &apptheory.AppError{Code: "app.not_found", Message: "job not found"}
	}
	status := strings.ToLower(strings.TrimSpace(job.Status))
	if status == models.ProvisionJobStatusOK {
		return &apptheory.AppError{Code: "app.conflict", Message: "job already ok"}
	}
	if status != models.ProvisionJobStatusError {
		return &apptheory.AppError{Code: "app.conflict", Message: "job must be in error state to adopt an account"}
	}
	mode := strings.ToLower(strings.TrimSpace(job.Mode))
	if mode != "" && mode != "managed" {
		return &apptheory.AppError{Code: "app.conflict", Message: "job is not a managed provisioning job"}
	}
	return nil
}

func buildAdoptAccountNote(existingNote string, actor string, accountID string, note string, now time.Time) string {
	noteLine := fmt.Sprintf("%s operator adopt account %s by %s", now.Format(time.RFC3339), accountID, actor)
	if strings.TrimSpace(note) != "" {
		noteLine = noteLine + ": " + strings.TrimSpace(note)
	}
	nextNote := strings.TrimSpace(existingNote)
	if nextNote != "" {
		return nextNote + "\n" + noteLine
	}
	return noteLine
}

func (s *Server) loadProvisionJobForOperator(ctx *apptheory.Context, id string) (*models.ProvisionJob, *apptheory.AppError) {
	job, err := s.store.GetProvisionJob(ctx.Context(), id)
	if theoryErrors.IsNotFound(err) || job == nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "job not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return job, nil
}

func (s *Server) adoptProvisionJobAccountTx(ctx *apptheory.Context, job *models.ProvisionJob, req adoptProvisionJobAccountRequest, note string, now time.Time) *apptheory.AppError {
	actor := strings.TrimSpace(ctx.AuthIdentity)

	jobKey := &models.ProvisionJob{
		ID:           strings.TrimSpace(job.ID),
		InstanceSlug: strings.TrimSpace(job.InstanceSlug),
		CreatedAt:    job.CreatedAt,
		ExpiresAt:    job.ExpiresAt,
	}
	_ = jobKey.UpdateKeys()

	instKey := &models.Instance{Slug: strings.TrimSpace(job.InstanceSlug)}
	_ = instKey.UpdateKeys()

	audit := &models.AuditLogEntry{
		Actor:     actor,
		Action:    "provision_job.adopt_account",
		Target:    fmt.Sprintf("provision_job:%s", strings.TrimSpace(job.ID)),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(jobKey, func(ub core.UpdateBuilder) error {
			ub.Set("Status", models.ProvisionJobStatusQueued)
			ub.Set("Step", "account.move")
			ub.Set("Attempts", int64(0))
			ub.Set("ErrorCode", "")
			ub.Set("ErrorMessage", "")
			ub.Set("AccountID", req.AccountID)
			ub.Set("AccountEmail", req.AccountEmail)
			ub.Set("AccountRequestID", "")
			ub.Set("RequestID", strings.TrimSpace(ctx.RequestID))
			ub.Set("UpdatedAt", now)
			ub.Set("Note", note)
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.Condition("Status", "=", models.ProvisionJobStatusError),
		)

		tx.UpdateWithBuilder(instKey, func(ub core.UpdateBuilder) error {
			ub.Set("ProvisionStatus", models.ProvisionJobStatusQueued)
			ub.Set("ProvisionJobID", strings.TrimSpace(job.ID))
			ub.Set("HostedAccountID", req.AccountID)
			return nil
		}, tabletheory.IfExists())

		tx.Put(audit)
		return nil
	}); err != nil && !theoryErrors.IsConditionFailed(err) {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to adopt account"}
	}
	return nil
}

func (s *Server) handleListOperatorProvisionJobs(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	status := strings.ToLower(strings.TrimSpace(queryFirst(ctx, "status")))
	if status == "all" {
		status = ""
	}
	instanceSlug := strings.ToLower(strings.TrimSpace(queryFirst(ctx, "instance_slug")))
	limit := parseLimit(queryFirst(ctx, "limit"), 50, 1, 200)

	var items []*models.ProvisionJob
	var err error

	if instanceSlug != "" {
		err = s.store.DB.WithContext(ctx.Context()).
			Model(&models.ProvisionJob{}).
			Index("gsi1").
			Where("gsi1PK", "=", fmt.Sprintf("PROVISION_INSTANCE#%s", instanceSlug)).
			Limit(200).
			All(&items)
	} else {
		// Operator-friendly: scan provision jobs (limited) and sort in-memory.
		err = s.store.DB.WithContext(ctx.Context()).
			Model(&models.ProvisionJob{}).
			Where("SK", "=", "JOB").
			Limit(200).
			All(&items)
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list provisioning jobs"}
	}

	filtered := make([]*models.ProvisionJob, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		if status != "" && strings.ToLower(strings.TrimSpace(it.Status)) != status {
			continue
		}
		filtered = append(filtered, it)
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
	})

	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	out := make([]operatorProvisionJobListItem, 0, len(filtered))
	for _, it := range filtered {
		out = append(out, operatorProvisionJobListItemFromModel(it))
	}

	return apptheory.JSON(http.StatusOK, listOperatorProvisionJobsResponse{Jobs: out, Count: len(out)})
}

func (s *Server) handleGetOperatorProvisionJob(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "job id is required"}
	}

	job, err := s.store.GetProvisionJob(ctx.Context(), id)
	if theoryErrors.IsNotFound(err) || job == nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "job not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	return apptheory.JSON(http.StatusOK, operatorProvisionJobDetailFromModel(job))
}

func (s *Server) handleRetryOperatorProvisionJob(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := s.requireProvisionRetryReady(); appErr != nil {
		return nil, appErr
	}

	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "job id is required"}
	}

	job, err := s.store.GetProvisionJob(ctx.Context(), id)
	if theoryErrors.IsNotFound(err) || job == nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "job not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	status := strings.ToLower(strings.TrimSpace(job.Status))
	if status == models.ProvisionJobStatusOK {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "job already ok"}
	}

	now := time.Now().UTC()
	actor := strings.TrimSpace(ctx.AuthIdentity)

	auditAction := "provision_job.requeue"
	if status == models.ProvisionJobStatusError {
		auditAction = "provision_job.retry"

		noteLine := fmt.Sprintf("%s operator retry by %s", now.Format(time.RFC3339), actor)
		nextNote := strings.TrimSpace(job.Note)
		if nextNote != "" {
			nextNote = nextNote + "\n" + noteLine
		} else {
			nextNote = noteLine
		}

		jobKey := &models.ProvisionJob{
			ID:           strings.TrimSpace(job.ID),
			InstanceSlug: strings.TrimSpace(job.InstanceSlug),
			CreatedAt:    job.CreatedAt,
			ExpiresAt:    job.ExpiresAt,
		}
		_ = jobKey.UpdateKeys()

		instKey := &models.Instance{Slug: strings.TrimSpace(job.InstanceSlug)}
		_ = instKey.UpdateKeys()

		audit := &models.AuditLogEntry{
			Actor:     actor,
			Action:    auditAction,
			Target:    fmt.Sprintf("provision_job:%s", strings.TrimSpace(job.ID)),
			RequestID: ctx.RequestID,
			CreatedAt: now,
		}
		_ = audit.UpdateKeys()

		if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
			tx.UpdateWithBuilder(jobKey, func(ub core.UpdateBuilder) error {
				ub.Set("Status", models.ProvisionJobStatusQueued)
				ub.Set("Step", "queued")
				ub.Set("Attempts", int64(0))
				ub.Set("ErrorCode", "")
				ub.Set("ErrorMessage", "")
				ub.Set("RequestID", strings.TrimSpace(ctx.RequestID))
				ub.Set("UpdatedAt", now)
				ub.Set("Note", nextNote)
				return nil
			},
				tabletheory.IfExists(),
				tabletheory.Condition("Status", "=", models.ProvisionJobStatusError),
			)

			tx.UpdateWithBuilder(instKey, func(ub core.UpdateBuilder) error {
				ub.Set("ProvisionStatus", models.ProvisionJobStatusQueued)
				ub.Set("ProvisionJobID", strings.TrimSpace(job.ID))
				return nil
			}, tabletheory.IfExists())

			tx.Put(audit)
			return nil
		}); err != nil && !theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to retry job"}
		}
	} else {
		audit := &models.AuditLogEntry{
			Actor:     actor,
			Action:    auditAction,
			Target:    fmt.Sprintf("provision_job:%s", strings.TrimSpace(job.ID)),
			RequestID: ctx.RequestID,
			CreatedAt: now,
		}
		_ = audit.UpdateKeys()
		_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()
	}

	// Enqueue the idempotent job message.
	_ = s.queues.enqueueProvisionJob(ctx.Context(), provisioning.JobMessage{
		Kind:  "provision_job",
		JobID: strings.TrimSpace(job.ID),
	})

	updated, _ := s.store.GetProvisionJob(ctx.Context(), strings.TrimSpace(job.ID))
	return apptheory.JSON(http.StatusOK, operatorProvisionJobDetailFromModel(updated))
}

func (s *Server) handleAdoptOperatorProvisionJobAccount(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := s.requireProvisionRetryReady(); appErr != nil {
		return nil, appErr
	}

	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "job id is required"}
	}

	req, appErr := parseAdoptProvisionJobAccountRequest(ctx)
	if appErr != nil {
		return nil, appErr
	}

	job, appErr := s.loadProvisionJobForOperator(ctx, id)
	if appErr != nil {
		return nil, appErr
	}
	if appErr := validateAdoptableProvisionJob(job); appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	note := buildAdoptAccountNote(job.Note, strings.TrimSpace(ctx.AuthIdentity), req.AccountID, req.Note, now)
	if appErr := s.adoptProvisionJobAccountTx(ctx, job, req, note, now); appErr != nil {
		return nil, appErr
	}

	_ = s.queues.enqueueProvisionJob(ctx.Context(), provisioning.JobMessage{
		Kind:  "provision_job",
		JobID: strings.TrimSpace(job.ID),
	})

	updated, _ := s.store.GetProvisionJob(ctx.Context(), strings.TrimSpace(job.ID))
	return apptheory.JSON(http.StatusOK, operatorProvisionJobDetailFromModel(updated))
}

func (s *Server) requireProvisionRetryReady() *apptheory.AppError {
	if s == nil || s.store == nil || s.store.DB == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.queues == nil || strings.TrimSpace(s.cfg.ProvisionQueueURL) == "" {
		return &apptheory.AppError{Code: "app.conflict", Message: "provision queue not configured"}
	}
	return nil
}

func (s *Server) handleAppendOperatorProvisionJobNote(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "job id is required"}
	}

	var req appendProvisionJobNoteRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}
	note := strings.TrimSpace(req.Note)
	if note == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "note is required"}
	}

	job, err := s.store.GetProvisionJob(ctx.Context(), id)
	if theoryErrors.IsNotFound(err) || job == nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "job not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	now := time.Now().UTC()
	actor := strings.TrimSpace(ctx.AuthIdentity)
	line := fmt.Sprintf("%s %s: %s", now.Format(time.RFC3339), actor, note)

	nextNote := strings.TrimSpace(job.Note)
	if nextNote != "" {
		nextNote = nextNote + "\n" + line
	} else {
		nextNote = line
	}

	jobKey := &models.ProvisionJob{
		ID:           strings.TrimSpace(job.ID),
		InstanceSlug: strings.TrimSpace(job.InstanceSlug),
		CreatedAt:    job.CreatedAt,
		ExpiresAt:    job.ExpiresAt,
	}
	_ = jobKey.UpdateKeys()

	audit := &models.AuditLogEntry{
		Actor:     actor,
		Action:    "provision_job.note.append",
		Target:    fmt.Sprintf("provision_job:%s", strings.TrimSpace(job.ID)),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(jobKey, func(ub core.UpdateBuilder) error {
			ub.Set("Note", nextNote)
			ub.Set("UpdatedAt", now)
			ub.Set("RequestID", strings.TrimSpace(ctx.RequestID))
			return nil
		}, tabletheory.IfExists())
		tx.Put(audit)
		return nil
	}); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to append note"}
	}

	updated, _ := s.store.GetProvisionJob(ctx.Context(), strings.TrimSpace(job.ID))
	return apptheory.JSON(http.StatusOK, operatorProvisionJobDetailFromModel(updated))
}
