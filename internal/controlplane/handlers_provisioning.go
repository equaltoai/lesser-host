package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	"github.com/theory-cloud/tabletheory/v3"
	"github.com/theory-cloud/tabletheory/v3/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/provisioning"
	"github.com/equaltoai/lesser-host/internal/provisionworker"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const asyncJobNudgeStaleAfter = 2 * time.Minute

func shouldNudgeAsyncJob(now time.Time, updatedAt time.Time) bool {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if updatedAt.IsZero() {
		return true
	}
	return now.Sub(updatedAt) > asyncJobNudgeStaleAfter
}

type startInstanceProvisionRequest struct {
	LesserVersion      string    `json:"lesser_version,omitempty"`
	Region             string    `json:"region,omitempty"`
	AdminUsername      string    `json:"admin_username,omitempty"`
	AdminWalletType    string    `json:"admin_wallet_type,omitempty"`
	AdminWalletAddress string    `json:"admin_wallet_address,omitempty"`
	AdminWalletChainID int       `json:"admin_wallet_chain_id,omitempty"`
	ConsentChallengeID string    `json:"consent_challenge_id,omitempty"`
	ConsentMessage     string    `json:"consent_message,omitempty"`
	ConsentSignature   string    `json:"consent_signature,omitempty"`
	ConsentExpiresAt   time.Time `json:"-"`
}

func provisionConsentMessageExpiresAt(message string) (time.Time, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return time.Time{}, nil
	}
	var payload struct {
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(message), &payload); err != nil {
		return time.Time{}, fmt.Errorf("invalid consent_message")
	}
	if payload.ExpiresAt.IsZero() {
		return time.Time{}, fmt.Errorf("consent_message expires_at is required")
	}
	return payload.ExpiresAt.UTC(), nil
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
	BodyEnabled       bool      `json:"body_enabled"`
	BodyProvisionedAt time.Time `json:"body_provisioned_at,omitempty"`
	McpWiredAt        time.Time `json:"mcp_wired_at,omitempty"`
	McpURL            string    `json:"mcp_url,omitempty"`
	AdminUsername     string    `json:"admin_username,omitempty"`

	ConsentMessageHash string `json:"consent_message_hash,omitempty"`

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
		BodyEnabled:        j.BodyEnabled,
		BodyProvisionedAt:  j.BodyProvisionedAt,
		McpWiredAt:         j.McpWiredAt,
		AdminUsername:      strings.TrimSpace(j.AdminUsername),
		ConsentMessageHash: strings.TrimSpace(j.ConsentMessageHash),
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
		return req, newAppTheoryError("app.internal", "internal error")
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

func normalizeAdminWalletType(walletType string) (string, *apptheory.AppTheoryError) {
	walletType = strings.ToLower(strings.TrimSpace(walletType))
	if walletType == "" {
		walletType = walletTypeEthereum
	}
	if walletType != walletTypeEthereum {
		return "", newAppTheoryError("app.bad_request", "invalid admin_wallet_type")
	}
	return walletType, nil
}

func normalizeAdminWalletAddress(addr string) (string, *apptheory.AppTheoryError) {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if addr == "" {
		return "", newAppTheoryError("app.bad_request", "admin_wallet_address is required")
	}
	if !common.IsHexAddress(addr) {
		return "", newAppTheoryError("app.bad_request", "invalid admin_wallet_address")
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

	now := time.Now().UTC()
	if shouldNudgeAsyncJob(now, job.UpdatedAt) && !job.HasActiveLease(now) {
		s.enqueueProvisionJobBestEffort(ctx, jobID)
	}
	return job, true
}

func (s *Server) encryptConsentForJob(consentMessage string, consentSignature string) (string, *apptheory.AppTheoryError) {
	encKey, encErr := provisionworker.ConsentEncryptionKeyHex(s.cfg.ManagedProvisionConsentEncryptionKeyHex)
	if encErr != nil {
		return "", newAppTheoryError("app.internal", "consent encryption key is not configured")
	}
	packed, packErr := provisionworker.PackConsent(consentMessage, consentSignature)
	if packErr != nil {
		return "", newAppTheoryError("app.internal", "failed to pack consent")
	}
	encrypted, encErr2 := provisionworker.EncryptConsent(string(packed), encKey)
	if encErr2 != nil {
		return "", newAppTheoryError("app.internal", "failed to protect consent")
	}
	return encrypted, nil
}

// processConsentForJob handles consent expiration parsing, hash computation,
// and encryption. Returns empty artifacts (no error) when no consent is
// supplied. Fails closed when consent is supplied but the encryption key is
// missing or encryption fails (CSR-017).
func (s *Server) processConsentForJob(consentMessage, consentSignature string, reqExpiresAt time.Time) (consentMsgHash string, consentEncrypted string, consentExpiresAt time.Time, appErr *apptheory.AppTheoryError) {
	// Use a trimmed copy only for presence/validation decisions.
	// Raw bytes are preserved for hash and encryption so signed
	// consent round-trips exactly (leading/trailing whitespace
	// and newlines are part of the signed payload).
	trimmedMessage := strings.TrimSpace(consentMessage)
	if trimmedMessage == "" {
		return "", "", reqExpiresAt, nil
	}

	consentExpiresAt = reqExpiresAt
	if consentExpiresAt.IsZero() {
		parsedExpiresAt, parseErr := provisionConsentMessageExpiresAt(consentMessage)
		if parseErr != nil {
			return "", "", time.Time{}, newAppTheoryError("app.bad_request", parseErr.Error())
		}
		consentExpiresAt = parsedExpiresAt
	}

	consentMsgHash = sha256Hex(consentMessage)
	consentEncrypted, appErr = s.encryptConsentForJob(consentMessage, consentSignature)
	if appErr != nil {
		return "", "", time.Time{}, appErr
	}
	return consentMsgHash, consentEncrypted, consentExpiresAt, nil
}

func (s *Server) buildManagedProvisionJob(slug string, req startInstanceProvisionRequest, requestID string, now time.Time) (*models.ProvisionJob, string, string, *apptheory.AppTheoryError) {
	if s == nil {
		return nil, "", "", newAppTheoryError("app.internal", "internal error")
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
		return nil, "", "", newAppTheoryError("app.internal", "failed to create provisioning job")
	}

	baseDomain := managedProvisionBaseDomain(slug, s.cfg.ManagedParentDomain)

	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = strings.TrimSpace(s.cfg.ManagedDefaultRegion)
	}

	lesserVersion := strings.TrimSpace(req.LesserVersion)
	if lesserVersion == "" {
		lesserVersion = strings.TrimSpace(s.cfg.ManagedLesserDefaultVersion)
	}
	if appErr := validateManagedReleaseVersion(lesserVersion, "lesser_version"); appErr != nil {
		return nil, "", "", appErr
	}
	if err := provisionworker.ValidateManagedLesserReleaseVersionSupported(lesserVersion); err != nil {
		return nil, "", "", newAppTheoryError("app.bad_request", err.Error())
	}

	accountEmail := strings.TrimSpace(expandManagedAccountEmailTemplate(s.cfg.ManagedAccountEmailTemplate, slug))

	// CSR-017: process consent expiration, hash, and encryption atomically.
	// Fails closed when consent is supplied but encryption key is missing or
	// encryption fails.
	consentMsgHash, consentEncrypted, consentExpiresAt, consentAppErr := s.processConsentForJob(
		req.ConsentMessage, req.ConsentSignature, req.ConsentExpiresAt,
	)
	if consentAppErr != nil {
		return nil, "", "", consentAppErr
	}

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
		ConsentMessageHash: consentMsgHash,
		ConsentEncrypted:   consentEncrypted,
		ConsentExpiresAt:   consentExpiresAt,
		ParentHostedZoneID: strings.TrimSpace(s.cfg.ManagedParentHostedZoneID),
		BaseDomain:         baseDomain,
		CreatedAt:          now,
		ExpiresAt:          now.Add(30 * 24 * time.Hour),
		RequestID:          strings.TrimSpace(requestID),
	}
	_ = job.UpdateKeys()

	return job, baseDomain, region, nil
}

func hydrateManagedProvisionJobFromInstance(job *models.ProvisionJob, inst *models.Instance) {
	if job == nil || inst == nil {
		return
	}
	if accountID := strings.TrimSpace(inst.HostedAccountID); accountID != "" && strings.TrimSpace(job.AccountID) == "" {
		job.AccountID = accountID
	}
	if region := strings.TrimSpace(inst.HostedRegion); region != "" {
		job.Region = region
	}
	if baseDomain := strings.TrimSpace(inst.HostedBaseDomain); baseDomain != "" && strings.TrimSpace(job.BaseDomain) == "" {
		job.BaseDomain = baseDomain
	}
	if zoneID := strings.TrimSpace(inst.HostedZoneID); zoneID != "" && strings.TrimSpace(job.ChildHostedZoneID) == "" {
		job.ChildHostedZoneID = zoneID
	}
	_ = job.UpdateKeys()
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

func (s *Server) createManagedProvisionJobTx(ctx *apptheory.Context, job *models.ProvisionJob, slug string, baseDomain string, region string, actor string, auditAction string, requestID string, now time.Time) *apptheory.AppTheoryError {
	if s == nil || s.store == nil || s.store.DB == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if ctx == nil || job == nil {
		return newAppTheoryError("app.internal", "internal error")
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
		return newAppTheoryError("app.internal", "failed to start provisioning")
	}

	return nil
}

func (s *Server) handleStartInstanceProvisioning(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	slug := strings.ToLower(strings.TrimSpace(ctx.Param("slug")))
	if slug == "" {
		return nil, newAppTheoryError("app.bad_request", "slug is required")
	}
	if !instanceSlugRE.MatchString(slug) {
		return nil, newAppTheoryError("app.bad_request", "invalid slug")
	}

	inst, err := s.getInstance(ctx, slug)
	if theoryErrors.IsNotFound(err) {
		return nil, newAppTheoryError("app.not_found", "instance not found")
	}
	if err != nil || inst == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	if job, ok := s.getExistingProvisionJobAndNudge(ctx, inst); ok {
		return apptheory.JSON(http.StatusOK, s.provisionJobResponseWithDerivedFields(job))
	}

	req, err := parseStartInstanceProvisionRequest(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	job, _, _, appErr := s.buildManagedProvisionJob(slug, req, ctx.RequestID, now)
	if appErr != nil {
		return nil, appErr
	}
	job.SoulEnabled = effectiveSoulEnabled(inst.SoulEnabled)
	job.BodyEnabled = effectiveBodyEnabled(inst.BodyEnabled)
	hydrateManagedProvisionJobFromInstance(job, inst)
	baseDomain := strings.TrimSpace(job.BaseDomain)
	region := strings.TrimSpace(job.Region)

	if appErr := s.createManagedProvisionJobTx(ctx, job, slug, baseDomain, region, ctx.AuthIdentity, "instance.provision.start", ctx.RequestID, now); appErr != nil {
		return nil, appErr
	}

	s.enqueueProvisionJobBestEffort(ctx, job.ID)
	return apptheory.JSON(http.StatusAccepted, s.provisionJobResponseWithDerivedFields(job))
}

func (s *Server) handleGetInstanceProvisioning(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	slug := strings.ToLower(strings.TrimSpace(ctx.Param("slug")))
	if slug == "" {
		return nil, newAppTheoryError("app.bad_request", "slug is required")
	}

	job, jobID, appErr := s.loadInstanceProvisioningJob(ctx, slug)
	if appErr != nil {
		return nil, appErr
	}

	if status := strings.ToLower(strings.TrimSpace(job.Status)); status == models.ProvisionJobStatusQueued || status == models.ProvisionJobStatusRunning {
		now := time.Now().UTC()
		if shouldNudgeAsyncJob(now, job.UpdatedAt) && !job.HasActiveLease(now) {
			s.enqueueProvisionJobBestEffort(ctx, jobID)
		}
	}

	return apptheory.JSON(http.StatusOK, s.provisionJobResponseWithDerivedFields(job))
}

func (s *Server) loadInstanceProvisioningJob(ctx *apptheory.Context, slug string) (*models.ProvisionJob, string, *apptheory.AppTheoryError) {
	inst, err := s.getInstance(ctx, slug)
	if theoryErrors.IsNotFound(err) {
		return nil, "", newAppTheoryError("app.not_found", "instance not found")
	}
	if err != nil || inst == nil {
		return nil, "", newAppTheoryError("app.internal", "internal error")
	}

	jobID := strings.TrimSpace(inst.ProvisionJobID)
	if jobID == "" {
		return nil, "", newAppTheoryError("app.not_found", "no provisioning job")
	}

	job, err := s.store.GetProvisionJob(ctx.Context(), jobID)
	if theoryErrors.IsNotFound(err) {
		return nil, "", newAppTheoryError("app.not_found", "provisioning job not found")
	}
	if err != nil || job == nil {
		return nil, "", newAppTheoryError("app.internal", "internal error")
	}
	return job, jobID, nil
}
