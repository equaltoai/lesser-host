package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/provisioning"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type startInstanceProvisionRequest struct {
	LesserVersion      string `json:"lesser_version,omitempty"`
	Region             string `json:"region,omitempty"`
	AdminUsername      string `json:"admin_username,omitempty"`
	AdminWalletType    string `json:"admin_wallet_type,omitempty"`
	AdminWalletAddress string `json:"admin_wallet_address,omitempty"`
	AdminWalletChainID int    `json:"admin_wallet_chain_id,omitempty"`
	ConsentChallengeID string `json:"consent_challenge_id,omitempty"`
	ConsentMessage     string `json:"consent_message,omitempty"`
	ConsentSignature   string `json:"consent_signature,omitempty"`
}

type provisionJobResponse struct {
	ID           string `json:"id"`
	InstanceSlug string `json:"instance_slug"`
	Status       string `json:"status"`
	Step         string `json:"step,omitempty"`
	Note         string `json:"note,omitempty"`

	Mode              string    `json:"mode,omitempty"`
	Plan              string    `json:"plan,omitempty"`
	Region            string    `json:"region,omitempty"`
	Stage             string    `json:"stage,omitempty"`
	LesserVersion     string    `json:"lesser_version,omitempty"`
	SoulEnabled       bool      `json:"soul_enabled"`
	SoulProvisionedAt time.Time `json:"soul_provisioned_at,omitempty"`
	AdminUsername     string    `json:"admin_username,omitempty"`

	ConsentMessageHash string `json:"consent_message_hash,omitempty"`
	ConsentSignature   string `json:"consent_signature,omitempty"`

	AccountRequestID string `json:"account_request_id,omitempty"`
	AccountID        string `json:"account_id,omitempty"`

	ParentHostedZoneID string   `json:"parent_hosted_zone_id,omitempty"`
	BaseDomain         string   `json:"base_domain,omitempty"`
	ChildHostedZoneID  string   `json:"child_hosted_zone_id,omitempty"`
	ChildNameServers   []string `json:"child_name_servers,omitempty"`

	RunID string `json:"run_id,omitempty"`

	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	RequestID    string `json:"request_id,omitempty"`

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
		SoulEnabled:        j.SoulEnabled,
		SoulProvisionedAt:  j.SoulProvisionedAt,
		AdminUsername:      strings.TrimSpace(j.AdminUsername),
		ConsentMessageHash: strings.TrimSpace(j.ConsentMessageHash),
		ConsentSignature:   strings.TrimSpace(j.ConsentSignature),
		AccountRequestID:   strings.TrimSpace(j.AccountRequestID),
		AccountID:          strings.TrimSpace(j.AccountID),
		ParentHostedZoneID: strings.TrimSpace(j.ParentHostedZoneID),
		BaseDomain:         strings.TrimSpace(j.BaseDomain),
		ChildHostedZoneID:  strings.TrimSpace(j.ChildHostedZoneID),
		ChildNameServers:   append([]string(nil), j.ChildNameServers...),
		RunID:              strings.TrimSpace(j.RunID),
		ErrorCode:          strings.TrimSpace(j.ErrorCode),
		ErrorMessage:       strings.TrimSpace(j.ErrorMessage),
		RequestID:          strings.TrimSpace(j.RequestID),
		CreatedAt:          j.CreatedAt,
		UpdatedAt:          j.UpdatedAt,
	}
}

func parseStartInstanceProvisionRequest(ctx *apptheory.Context) (startInstanceProvisionRequest, error) {
	var req startInstanceProvisionRequest
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

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func normalizeAdminWalletType(walletType string) (string, *apptheory.AppError) {
	walletType = strings.ToLower(strings.TrimSpace(walletType))
	if walletType == "" {
		walletType = walletTypeEthereum
	}
	if walletType != walletTypeEthereum {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid admin_wallet_type"}
	}
	return walletType, nil
}

func normalizeAdminWalletAddress(addr string) (string, *apptheory.AppError) {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if addr == "" {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "admin_wallet_address is required"}
	}
	if !common.IsHexAddress(addr) {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid admin_wallet_address"}
	}
	if reservedErr := validateNotReservedWalletAddress(addr, "admin_wallet_address"); reservedErr != nil {
		return "", reservedErr
	}
	return addr, nil
}

func normalizeAdminWalletChainID(chainID int) int {
	if chainID <= 0 {
		return 1
	}
	return chainID
}

func (s *Server) enqueueProvisionJobBestEffort(ctx *apptheory.Context, jobID string) {
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
		Kind:  "provision_job",
		JobID: jobID,
	})
}

func (s *Server) getExistingProvisionJobAndNudge(ctx *apptheory.Context, inst *models.Instance) (*models.ProvisionJob, bool) {
	if s == nil || s.store == nil || ctx == nil || inst == nil {
		return nil, false
	}

	status := strings.ToLower(strings.TrimSpace(inst.ProvisionStatus))
	jobID := strings.TrimSpace(inst.ProvisionJobID)
	if jobID == "" {
		return nil, false
	}

	if status != models.ProvisionJobStatusQueued && status != models.ProvisionJobStatusRunning {
		return nil, false
	}

	job, err := s.store.GetProvisionJob(ctx.Context(), jobID)
	if err != nil || job == nil {
		return nil, false
	}

	s.enqueueProvisionJobBestEffort(ctx, jobID)
	return job, true
}

func (s *Server) buildManagedProvisionJob(slug string, req startInstanceProvisionRequest, requestID string, now time.Time) (*models.ProvisionJob, string, string, *apptheory.AppError) {
	if s == nil {
		return nil, "", "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	stage := normalizeControlPlaneStage(s.cfg.Stage)

	adminUsername, appErr := normalizeProvisionAdminUsername(slug, req.AdminUsername)
	if appErr != nil {
		return nil, "", "", appErr
	}

	adminWalletType, appErr := normalizeAdminWalletType(req.AdminWalletType)
	if appErr != nil {
		return nil, "", "", appErr
	}

	adminWalletAddr, appErr := normalizeAdminWalletAddress(req.AdminWalletAddress)
	if appErr != nil {
		return nil, "", "", appErr
	}
	adminWalletChainID := normalizeAdminWalletChainID(req.AdminWalletChainID)

	id, err := newToken(16)
	if err != nil {
		return nil, "", "", &apptheory.AppError{Code: "app.internal", Message: "failed to create provisioning job"}
	}

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

	accountEmail := strings.TrimSpace(expandManagedAccountEmailTemplate(s.cfg.ManagedAccountEmailTemplate, slug))

	job := &models.ProvisionJob{
		ID:                 id,
		InstanceSlug:       slug,
		Status:             models.ProvisionJobStatusQueued,
		Step:               "queued",
		Mode:               "managed",
		Region:             region,
		Stage:              stage,
		LesserVersion:      lesserVersion,
		AdminUsername:      adminUsername,
		AdminWalletType:    adminWalletType,
		AdminWalletAddr:    adminWalletAddr,
		AdminWalletChainID: adminWalletChainID,
		AccountEmail:       accountEmail,
		ConsentMessage:     strings.TrimSpace(req.ConsentMessage),
		ConsentSignature:   strings.TrimSpace(req.ConsentSignature),
		ConsentMessageHash: func() string {
			msg := strings.TrimSpace(req.ConsentMessage)
			if msg == "" {
				return ""
			}
			return sha256Hex(msg)
		}(),
		ParentHostedZoneID: strings.TrimSpace(s.cfg.ManagedParentHostedZoneID),
		BaseDomain:         baseDomain,
		CreatedAt:          now,
		ExpiresAt:          now.Add(30 * 24 * time.Hour),
		RequestID:          strings.TrimSpace(requestID),
	}
	_ = job.UpdateKeys()

	return job, baseDomain, region, nil
}

func expandManagedAccountEmailTemplate(tmpl string, slug string) string {
	tmpl = strings.TrimSpace(tmpl)
	if tmpl == "" {
		return ""
	}
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}
	return strings.ReplaceAll(tmpl, "{slug}", slug)
}

func (s *Server) createManagedProvisionJobTx(ctx *apptheory.Context, job *models.ProvisionJob, slug string, baseDomain string, region string, actor string, auditAction string, requestID string, now time.Time) *apptheory.AppError {
	if s == nil || s.store == nil || s.store.DB == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil || job == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	updateInst := &models.Instance{Slug: slug}
	_ = updateInst.UpdateKeys()

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(actor),
		Action:    strings.TrimSpace(auditAction),
		Target:    fmt.Sprintf("instance:%s", slug),
		RequestID: strings.TrimSpace(requestID),
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Create(job)
		tx.UpdateWithBuilder(updateInst, func(ub core.UpdateBuilder) error {
			ub.Set("ProvisionStatus", models.ProvisionJobStatusQueued)
			ub.Set("ProvisionJobID", strings.TrimSpace(job.ID))
			ub.Set("HostedBaseDomain", strings.TrimSpace(baseDomain))
			if strings.TrimSpace(region) != "" {
				ub.Set("HostedRegion", strings.TrimSpace(region))
			}
			return nil
		}, tabletheory.IfExists())
		tx.Put(audit)
		return nil
	}); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to start provisioning"}
	}

	return nil
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

	if job, ok := s.getExistingProvisionJobAndNudge(ctx, inst); ok {
		return apptheory.JSON(http.StatusOK, provisionJobResponseFromModel(job))
	}

	req, err := parseStartInstanceProvisionRequest(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	job, baseDomain, region, appErr := s.buildManagedProvisionJob(slug, req, ctx.RequestID, now)
	if appErr != nil {
		return nil, appErr
	}
	job.SoulEnabled = effectiveSoulEnabled(inst.SoulEnabled)

	if appErr := s.createManagedProvisionJobTx(ctx, job, slug, baseDomain, region, ctx.AuthIdentity, "instance.provision.start", ctx.RequestID, now); appErr != nil {
		return nil, appErr
	}

	s.enqueueProvisionJobBestEffort(ctx, job.ID)
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
