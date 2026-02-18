package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

var instanceSlugRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?$`)

const (
	renderPolicyAlways     = "always"
	renderPolicySuspicious = "suspicious"

	overagePolicyAllow = "allow"
	overagePolicyBlock = "block"

	moderationTriggerOnReports      = "on_reports"
	moderationTriggerAlways         = "always"
	moderationTriggerLinksMediaOnly = "links_media_only"
	moderationTriggerVirality       = "virality"

	aiBatchingModeNone      = "none"
	aiBatchingModeInRequest = "in_request"
	aiBatchingModeWorker    = "worker"
	aiBatchingModeHybrid    = "hybrid"
)

type createInstanceRequest struct {
	Slug  string `json:"slug"`
	Owner string `json:"owner,omitempty"`
}

type instanceResponse struct {
	Slug                      string    `json:"slug"`
	Owner                     string    `json:"owner,omitempty"`
	Status                    string    `json:"status"`
	ProvisionStatus           string    `json:"provision_status,omitempty"`
	ProvisionJobID            string    `json:"provision_job_id,omitempty"`
	UpdateStatus              string    `json:"update_status,omitempty"`
	UpdateJobID               string    `json:"update_job_id,omitempty"`
	HostedAccountID           string    `json:"hosted_account_id,omitempty"`
	HostedRegion              string    `json:"hosted_region,omitempty"`
	HostedBaseDomain          string    `json:"hosted_base_domain,omitempty"`
	HostedZoneID              string    `json:"hosted_zone_id,omitempty"`
	LesserVersion             string    `json:"lesser_version,omitempty"`
	LesserHostBaseURL         string    `json:"lesser_host_base_url,omitempty"`
	LesserHostAttestationsURL string    `json:"lesser_host_attestations_url,omitempty"`
	TranslationEnabled        bool      `json:"translation_enabled"`
	TipEnabled                bool      `json:"tip_enabled"`
	TipChainID                int64     `json:"tip_chain_id,omitempty"`
	TipContractAddress        string    `json:"tip_contract_address,omitempty"`
	LesserAIEnabled           bool      `json:"lesser_ai_enabled"`
	LesserAIModerationEnabled bool      `json:"lesser_ai_moderation_enabled"`
	LesserAINsfwEnabled       bool      `json:"lesser_ai_nsfw_detection_enabled"`
	LesserAISpamEnabled       bool      `json:"lesser_ai_spam_detection_enabled"`
	LesserAIPiiEnabled        bool      `json:"lesser_ai_pii_detection_enabled"`
	LesserAIContentEnabled    bool      `json:"lesser_ai_content_detection_enabled"`
	HostedPreviewsEnabled     bool      `json:"hosted_previews_enabled"`
	LinkSafetyEnabled         bool      `json:"link_safety_enabled"`
	RendersEnabled            bool      `json:"renders_enabled"`
	RenderPolicy              string    `json:"render_policy"`
	OveragePolicy             string    `json:"overage_policy"`
	ModerationEnabled         bool      `json:"moderation_enabled"`
	ModerationTrigger         string    `json:"moderation_trigger"`
	ModerationViralityMin     int64     `json:"moderation_virality_min"`
	AIEnabled                 bool      `json:"ai_enabled"`
	AIModelSet                string    `json:"ai_model_set"`
	AIBatchingMode            string    `json:"ai_batching_mode"`
	AIBatchMaxItems           int64     `json:"ai_batch_max_items"`
	AIBatchMaxTotalBytes      int64     `json:"ai_batch_max_total_bytes"`
	AIPricingMultiplierBps    int64     `json:"ai_pricing_multiplier_bps"`
	AIMaxInflightJobs         int64     `json:"ai_max_inflight_jobs"`
	CreatedAt                 time.Time `json:"created_at"`
}

type listInstancesResponse struct {
	Instances []instanceResponse `json:"instances"`
	Count     int                `json:"count"`
}

type createInstanceKeyResponse struct {
	InstanceSlug string `json:"instance_slug"`
	Key          string `json:"key"`
	KeyID        string `json:"key_id"`
}

type updateInstanceConfigRequest struct {
	HostedPreviewsEnabled  *bool   `json:"hosted_previews_enabled,omitempty"`
	LinkSafetyEnabled      *bool   `json:"link_safety_enabled,omitempty"`
	RendersEnabled         *bool   `json:"renders_enabled,omitempty"`
	RenderPolicy           *string `json:"render_policy,omitempty"`  // always|suspicious
	OveragePolicy          *string `json:"overage_policy,omitempty"` // block|allow
	ModerationEnabled      *bool   `json:"moderation_enabled,omitempty"`
	ModerationTrigger      *string `json:"moderation_trigger,omitempty"` // on_reports|always|links_media_only|virality
	ModerationViralityMin  *int64  `json:"moderation_virality_min,omitempty"`
	AIEnabled              *bool   `json:"ai_enabled,omitempty"`
	AIModelSet             *string `json:"ai_model_set,omitempty"`
	AIBatchingMode         *string `json:"ai_batching_mode,omitempty"` // none|in_request|worker|hybrid
	AIBatchMaxItems        *int64  `json:"ai_batch_max_items,omitempty"`
	AIBatchMaxTotalBytes   *int64  `json:"ai_batch_max_total_bytes,omitempty"`
	AIPricingMultiplierBps *int64  `json:"ai_pricing_multiplier_bps,omitempty"`
	AIMaxInflightJobs      *int64  `json:"ai_max_inflight_jobs,omitempty"`
	TranslationEnabled     *bool   `json:"translation_enabled,omitempty"`

	TipEnabled         *bool   `json:"tip_enabled,omitempty"`
	TipChainID         *int64  `json:"tip_chain_id,omitempty"`
	TipContractAddress *string `json:"tip_contract_address,omitempty"`

	LesserAIEnabled                 *bool `json:"lesser_ai_enabled,omitempty"`
	LesserAIModerationEnabled       *bool `json:"lesser_ai_moderation_enabled,omitempty"`
	LesserAINsfwDetectionEnabled    *bool `json:"lesser_ai_nsfw_detection_enabled,omitempty"`
	LesserAISpamDetectionEnabled    *bool `json:"lesser_ai_spam_detection_enabled,omitempty"`
	LesserAIPiiDetectionEnabled     *bool `json:"lesser_ai_pii_detection_enabled,omitempty"`
	LesserAIContentDetectionEnabled *bool `json:"lesser_ai_content_detection_enabled,omitempty"`
}

type setBudgetMonthRequest struct {
	IncludedCredits int64 `json:"included_credits"`
}

type budgetMonthResponse struct {
	InstanceSlug    string    `json:"instance_slug"`
	Month           string    `json:"month"`
	IncludedCredits int64     `json:"included_credits"`
	UsedCredits     int64     `json:"used_credits"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func instanceResponseFromModel(inst *models.Instance) instanceResponse {
	if inst == nil {
		return instanceResponse{}
	}
	return instanceResponse{
		Slug:                      strings.TrimSpace(inst.Slug),
		Owner:                     strings.TrimSpace(inst.Owner),
		Status:                    strings.TrimSpace(inst.Status),
		ProvisionStatus:           strings.TrimSpace(inst.ProvisionStatus),
		ProvisionJobID:            strings.TrimSpace(inst.ProvisionJobID),
		UpdateStatus:              strings.TrimSpace(inst.UpdateStatus),
		UpdateJobID:               strings.TrimSpace(inst.UpdateJobID),
		HostedAccountID:           strings.TrimSpace(inst.HostedAccountID),
		HostedRegion:              strings.TrimSpace(inst.HostedRegion),
		HostedBaseDomain:          strings.TrimSpace(inst.HostedBaseDomain),
		HostedZoneID:              strings.TrimSpace(inst.HostedZoneID),
		LesserVersion:             strings.TrimSpace(inst.LesserVersion),
		LesserHostBaseURL:         strings.TrimSpace(inst.LesserHostBaseURL),
		LesserHostAttestationsURL: strings.TrimSpace(inst.LesserHostAttestationsURL),
		TranslationEnabled:        effectiveTranslationEnabled(inst.TranslationEnabled),
		TipEnabled:                effectiveTipEnabled(inst.TipEnabled),
		TipChainID:                effectiveTipChainID(inst.TipChainID),
		TipContractAddress:        strings.TrimSpace(inst.TipContractAddress),
		LesserAIEnabled:           effectiveLesserAIEnabled(inst.LesserAIEnabled),
		LesserAIModerationEnabled: effectiveLesserAIModerationEnabled(inst.LesserAIModerationEnabled),
		LesserAINsfwEnabled:       effectiveLesserAINsfwDetectionEnabled(inst.LesserAINsfwDetectionEnabled),
		LesserAISpamEnabled:       effectiveLesserAISpamDetectionEnabled(inst.LesserAISpamDetectionEnabled),
		LesserAIPiiEnabled:        effectiveLesserAIPiiDetectionEnabled(inst.LesserAIPiiDetectionEnabled),
		LesserAIContentEnabled:    effectiveLesserAIContentDetectionEnabled(inst.LesserAIContentDetectionEnabled),
		HostedPreviewsEnabled:     effectiveHostedPreviewsEnabled(inst.HostedPreviewsEnabled),
		LinkSafetyEnabled:         effectiveLinkSafetyEnabled(inst.LinkSafetyEnabled),
		RendersEnabled:            effectiveRendersEnabled(inst.RendersEnabled),
		RenderPolicy:              effectiveRenderPolicy(inst.RenderPolicy),
		OveragePolicy:             effectiveOveragePolicy(inst.OveragePolicy),
		ModerationEnabled:         effectiveModerationEnabled(inst.ModerationEnabled),
		ModerationTrigger:         effectiveModerationTrigger(inst.ModerationTrigger),
		ModerationViralityMin:     effectiveModerationViralityMin(inst.ModerationViralityMin),
		AIEnabled:                 effectiveAIEnabled(inst.AIEnabled),
		AIModelSet:                effectiveAIModelSet(inst.AIModelSet),
		AIBatchingMode:            effectiveAIBatchingMode(inst.AIBatchingMode),
		AIBatchMaxItems:           effectiveAIBatchMaxItems(inst.AIBatchMaxItems),
		AIBatchMaxTotalBytes:      effectiveAIBatchMaxTotalBytes(inst.AIBatchMaxTotalBytes),
		AIPricingMultiplierBps:    effectiveAIPricingMultiplierBps(inst.AIPricingMultiplierBps),
		AIMaxInflightJobs:         effectiveAIMaxInflightJobs(inst.AIMaxInflightJobs),
		CreatedAt:                 inst.CreatedAt,
	}
}

func (s *Server) getInstance(ctx *apptheory.Context, slug string) (*models.Instance, error) {
	var inst models.Instance
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.Instance{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE#%s", slug)).
		Where("SK", "=", models.SKMetadata).
		First(&inst)
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

func (s *Server) handleCreateInstance(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}

	var req createInstanceRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(req.Slug))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}
	if !instanceSlugRE.MatchString(slug) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid slug"}
	}

	now := time.Now().UTC()
	hostedPreviewsEnabled := true
	linkSafetyEnabled := true
	rendersEnabled := true
	renderPolicy := renderPolicySuspicious
	overagePolicy := overagePolicyBlock
	moderationEnabled := false
	moderationTrigger := moderationTriggerOnReports
	moderationViralityMin := int64(0)
	aiEnabled := false
	aiModelSet := defaultAIModelSet
	aiBatchingMode := aiBatchingModeNone
	aiBatchMaxItems := int64(8)
	aiBatchMaxTotalBytes := int64(64 * 1024)
	aiPricingMultiplierBps := int64(10000)
	aiMaxInflightJobs := int64(200)
	inst := &models.Instance{
		Slug:                   slug,
		Owner:                  strings.TrimSpace(req.Owner),
		Status:                 models.InstanceStatusActive,
		HostedPreviewsEnabled:  &hostedPreviewsEnabled,
		LinkSafetyEnabled:      &linkSafetyEnabled,
		RendersEnabled:         &rendersEnabled,
		RenderPolicy:           renderPolicy,
		OveragePolicy:          overagePolicy,
		ModerationEnabled:      &moderationEnabled,
		ModerationTrigger:      moderationTrigger,
		ModerationViralityMin:  moderationViralityMin,
		AIEnabled:              &aiEnabled,
		AIModelSet:             aiModelSet,
		AIBatchingMode:         aiBatchingMode,
		AIBatchMaxItems:        aiBatchMaxItems,
		AIBatchMaxTotalBytes:   aiBatchMaxTotalBytes,
		AIPricingMultiplierBps: &aiPricingMultiplierBps,
		AIMaxInflightJobs:      &aiMaxInflightJobs,
		CreatedAt:              now,
	}
	if err := inst.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	parentDomain := strings.TrimSpace(s.cfg.ManagedParentDomain)
	if parentDomain == "" {
		parentDomain = defaultManagedParentDomain
	}
	baseDomain := fmt.Sprintf("%s.%s", slug, strings.TrimPrefix(parentDomain, "."))

	primaryDomain := &models.Domain{
		Domain:             baseDomain,
		DomainRaw:          baseDomain,
		InstanceSlug:       slug,
		Type:               models.DomainTypePrimary,
		Status:             models.DomainStatusVerified,
		VerificationMethod: "managed",
		CreatedAt:          now,
		UpdatedAt:          now,
		VerifiedAt:         now,
	}
	_ = primaryDomain.UpdateKeys()

	auditInstance := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "instance.create",
		Target:    fmt.Sprintf("instance:%s", slug),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = auditInstance.UpdateKeys()

	auditDomain := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "domain.primary.create",
		Target:    fmt.Sprintf("domain:%s", primaryDomain.Domain),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = auditDomain.UpdateKeys()

	tipOp, auditTipOp, err := s.buildAutoTipRegistryOperation(ctx.Context(), primaryDomain.Domain, primaryDomain.DomainRaw, strings.TrimSpace(ctx.AuthIdentity), ctx.RequestID, now)
	if err != nil {
		return nil, err
	}

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Create(inst)
		tx.Create(primaryDomain)
		tx.Put(auditInstance)
		tx.Put(auditDomain)
		if tipOp != nil {
			tx.Create(tipOp)
		}
		if auditTipOp != nil {
			tx.Put(auditTipOp)
		}
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "instance already exists"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create instance"}
	}

	return apptheory.JSON(http.StatusCreated, instanceResponseFromModel(inst))
}

func (s *Server) handleListInstances(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}

	var items []*models.Instance
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.Instance{}).
		Filter("SK", "=", models.SKMetadata).
		Scan(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list instances"}
	}

	out := make([]instanceResponse, 0, len(items))
	for _, inst := range items {
		out = append(out, instanceResponseFromModel(inst))
	}

	return apptheory.JSON(http.StatusOK, listInstancesResponse{
		Instances: out,
		Count:     len(out),
	})
}

func effectiveHostedPreviewsEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func effectiveLinkSafetyEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func effectiveRendersEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func effectiveRenderPolicy(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return renderPolicySuspicious
	}
	return v
}

func effectiveOveragePolicy(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return overagePolicyBlock
	}
	return v
}

func effectiveModerationEnabled(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func effectiveModerationTrigger(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case moderationTriggerOnReports, moderationTriggerAlways, moderationTriggerLinksMediaOnly, moderationTriggerVirality:
		return v
	default:
		return moderationTriggerOnReports
	}
}

func effectiveModerationViralityMin(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

func effectiveAIEnabled(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func effectiveAIModelSet(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return defaultAIModelSet
	}
	return v
}

func effectiveAIBatchingMode(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case aiBatchingModeNone, aiBatchingModeInRequest, aiBatchingModeWorker, aiBatchingModeHybrid:
		return v
	default:
		return aiBatchingModeNone
	}
}

func effectiveAIBatchMaxItems(v int64) int64 {
	if v <= 0 {
		return 8
	}
	return v
}

func effectiveAIBatchMaxTotalBytes(v int64) int64 {
	if v <= 0 {
		return 64 * 1024
	}
	return v
}

func effectiveAIPricingMultiplierBps(v *int64) int64 {
	if v == nil || *v <= 0 {
		return 10000
	}
	return *v
}

func effectiveAIMaxInflightJobs(v *int64) int64 {
	if v == nil || *v <= 0 {
		return 200
	}
	return *v
}

func effectiveTranslationEnabled(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func effectiveTipEnabled(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func effectiveTipChainID(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

func effectiveLesserAIEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func effectiveLesserAIModerationEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func effectiveLesserAINsfwDetectionEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func effectiveLesserAISpamDetectionEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func effectiveLesserAIPiiDetectionEnabled(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func effectiveLesserAIContentDetectionEnabled(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func (s *Server) handleCreateInstanceKey(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(ctx.Param("slug")))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}

	if _, err := s.getInstance(ctx, slug); theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "instance not found"}
	} else if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	secret, err := newToken(32)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create key"}
	}
	plaintext := "lhk_" + secret

	sum := sha256.Sum256([]byte(plaintext))
	keyID := hex.EncodeToString(sum[:])

	now := time.Now().UTC()
	key := &models.InstanceKey{
		ID:           keyID,
		InstanceSlug: slug,
		CreatedAt:    now,
	}
	if err := key.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(key).IfNotExists().Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create key"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "instance_key.create",
		Target:    fmt.Sprintf("instance:%s", slug),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to write audit log"}
	}

	return apptheory.JSON(http.StatusCreated, createInstanceKeyResponse{
		InstanceSlug: slug,
		Key:          plaintext,
		KeyID:        keyID,
	})
}

func (s *Server) handleUpdateInstanceConfig(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(ctx.Param("slug")))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}

	if _, err := s.getInstance(ctx, slug); theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "instance not found"}
	} else if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var req updateInstanceConfigRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	update, fields, err := buildInstanceConfigUpdate(slug, req)
	if err != nil {
		return nil, err
	}
	if updateErr := update.UpdateKeys(); updateErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if dbErr := s.store.DB.WithContext(ctx.Context()).Model(update).IfExists().Update(fields...); dbErr != nil {
		if theoryErrors.IsConditionFailed(dbErr) {
			return nil, &apptheory.AppError{Code: "app.not_found", Message: "instance not found"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to update instance"}
	}

	now := time.Now().UTC()
	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "instance.config.update",
		Target:    fmt.Sprintf("instance:%s", slug),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	if auditErr := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); auditErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to write audit log"}
	}

	inst, err := s.getInstance(ctx, slug)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	return apptheory.JSON(http.StatusOK, instanceResponseFromModel(inst))
}

func buildInstanceConfigUpdate(slug string, req updateInstanceConfigRequest) (*models.Instance, []string, error) {
	update := &models.Instance{Slug: slug}
	fields := make([]string, 0, 12)

	setBoolPtr(&update.HostedPreviewsEnabled, req.HostedPreviewsEnabled, "HostedPreviewsEnabled", &fields)
	setBoolPtr(&update.LinkSafetyEnabled, req.LinkSafetyEnabled, "LinkSafetyEnabled", &fields)
	setBoolPtr(&update.RendersEnabled, req.RendersEnabled, "RendersEnabled", &fields)

	if err := setRenderPolicy(update, req.RenderPolicy, &fields); err != nil {
		return nil, nil, err
	}
	if err := setOveragePolicy(update, req.OveragePolicy, &fields); err != nil {
		return nil, nil, err
	}

	setBoolPtr(&update.ModerationEnabled, req.ModerationEnabled, "ModerationEnabled", &fields)
	if err := setModerationTrigger(update, req.ModerationTrigger, &fields); err != nil {
		return nil, nil, err
	}
	if err := setModerationViralityMin(update, req.ModerationViralityMin, &fields); err != nil {
		return nil, nil, err
	}

	setBoolPtr(&update.AIEnabled, req.AIEnabled, "AIEnabled", &fields)

	if err := setAIModelSet(update, req.AIModelSet, &fields); err != nil {
		return nil, nil, err
	}
	if err := setAIBatchingMode(update, req.AIBatchingMode, &fields); err != nil {
		return nil, nil, err
	}
	if err := setPositiveInt64(&update.AIBatchMaxItems, req.AIBatchMaxItems, "ai_batch_max_items", "AIBatchMaxItems", &fields); err != nil {
		return nil, nil, err
	}
	if err := setPositiveInt64(&update.AIBatchMaxTotalBytes, req.AIBatchMaxTotalBytes, "ai_batch_max_total_bytes", "AIBatchMaxTotalBytes", &fields); err != nil {
		return nil, nil, err
	}
	if err := setAIPricingMultiplierBps(update, req.AIPricingMultiplierBps, &fields); err != nil {
		return nil, nil, err
	}
	if err := setAIMaxInflightJobs(update, req.AIMaxInflightJobs, &fields); err != nil {
		return nil, nil, err
	}

	setBoolPtr(&update.TranslationEnabled, req.TranslationEnabled, "TranslationEnabled", &fields)

	setBoolPtr(&update.TipEnabled, req.TipEnabled, "TipEnabled", &fields)
	if err := setTipChainID(update, req.TipChainID, &fields); err != nil {
		return nil, nil, err
	}
	if err := setTipContractAddress(update, req.TipContractAddress, &fields); err != nil {
		return nil, nil, err
	}

	setBoolPtr(&update.LesserAIEnabled, req.LesserAIEnabled, "LesserAIEnabled", &fields)
	setBoolPtr(&update.LesserAIModerationEnabled, req.LesserAIModerationEnabled, "LesserAIModerationEnabled", &fields)
	setBoolPtr(&update.LesserAINsfwDetectionEnabled, req.LesserAINsfwDetectionEnabled, "LesserAINsfwDetectionEnabled", &fields)
	setBoolPtr(&update.LesserAISpamDetectionEnabled, req.LesserAISpamDetectionEnabled, "LesserAISpamDetectionEnabled", &fields)
	setBoolPtr(&update.LesserAIPiiDetectionEnabled, req.LesserAIPiiDetectionEnabled, "LesserAIPiiDetectionEnabled", &fields)
	setBoolPtr(&update.LesserAIContentDetectionEnabled, req.LesserAIContentDetectionEnabled, "LesserAIContentDetectionEnabled", &fields)

	if len(fields) == 0 {
		return nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "no config fields provided"}
	}
	return update, fields, nil
}

func setTipChainID(update *models.Instance, src *int64, fields *[]string) error {
	if src == nil {
		return nil
	}
	if *src < 0 {
		return &apptheory.AppError{Code: "app.bad_request", Message: "tip_chain_id must be >= 0"}
	}
	update.TipChainID = *src
	*fields = append(*fields, "TipChainID")
	return nil
}

func setTipContractAddress(update *models.Instance, src *string, fields *[]string) error {
	if src == nil {
		return nil
	}
	update.TipContractAddress = strings.TrimSpace(*src)
	*fields = append(*fields, "TipContractAddress")
	return nil
}

func setBoolPtr(dst **bool, src *bool, fieldName string, fields *[]string) {
	if src == nil {
		return
	}
	*dst = src
	*fields = append(*fields, fieldName)
}

func setRenderPolicy(update *models.Instance, src *string, fields *[]string) error {
	if src == nil {
		return nil
	}
	rp := strings.ToLower(strings.TrimSpace(*src))
	if rp != renderPolicyAlways && rp != renderPolicySuspicious {
		return &apptheory.AppError{Code: "app.bad_request", Message: "render_policy must be always or suspicious"}
	}
	update.RenderPolicy = rp
	*fields = append(*fields, "RenderPolicy")
	return nil
}

func setOveragePolicy(update *models.Instance, src *string, fields *[]string) error {
	if src == nil {
		return nil
	}
	op := strings.ToLower(strings.TrimSpace(*src))
	if op != overagePolicyBlock && op != overagePolicyAllow {
		return &apptheory.AppError{Code: "app.bad_request", Message: "overage_policy must be block or allow"}
	}
	update.OveragePolicy = op
	*fields = append(*fields, "OveragePolicy")
	return nil
}

func setModerationTrigger(update *models.Instance, src *string, fields *[]string) error {
	if src == nil {
		return nil
	}
	mode := strings.ToLower(strings.TrimSpace(*src))
	switch mode {
	case moderationTriggerOnReports, moderationTriggerAlways, moderationTriggerLinksMediaOnly, moderationTriggerVirality:
		// ok
	default:
		return &apptheory.AppError{Code: "app.bad_request", Message: "moderation_trigger must be on_reports, always, links_media_only, or virality"}
	}
	update.ModerationTrigger = mode
	*fields = append(*fields, "ModerationTrigger")
	return nil
}

func setModerationViralityMin(update *models.Instance, src *int64, fields *[]string) error {
	if src == nil {
		return nil
	}
	if *src < 0 {
		return &apptheory.AppError{Code: "app.bad_request", Message: "moderation_virality_min must be >= 0"}
	}
	update.ModerationViralityMin = *src
	*fields = append(*fields, "ModerationViralityMin")
	return nil
}

func setAIModelSet(update *models.Instance, src *string, fields *[]string) error {
	if src == nil {
		return nil
	}
	ms := strings.TrimSpace(*src)
	if ms == "" {
		return &apptheory.AppError{Code: "app.bad_request", Message: "ai_model_set cannot be empty"}
	}
	update.AIModelSet = ms
	*fields = append(*fields, "AIModelSet")
	return nil
}

func setAIBatchingMode(update *models.Instance, src *string, fields *[]string) error {
	if src == nil {
		return nil
	}
	mode := strings.ToLower(strings.TrimSpace(*src))
	switch mode {
	case aiBatchingModeNone, aiBatchingModeInRequest, aiBatchingModeWorker, aiBatchingModeHybrid:
		// ok
	default:
		return &apptheory.AppError{Code: "app.bad_request", Message: "ai_batching_mode must be none, in_request, worker, or hybrid"}
	}
	update.AIBatchingMode = mode
	*fields = append(*fields, "AIBatchingMode")
	return nil
}

func setPositiveInt64(dst *int64, src *int64, jsonField string, modelField string, fields *[]string) error {
	if src == nil {
		return nil
	}
	if *src <= 0 {
		return &apptheory.AppError{Code: "app.bad_request", Message: jsonField + " must be > 0"}
	}
	*dst = *src
	*fields = append(*fields, modelField)
	return nil
}

func setAIPricingMultiplierBps(update *models.Instance, src *int64, fields *[]string) error {
	if src == nil {
		return nil
	}
	if *src <= 0 {
		return &apptheory.AppError{Code: "app.bad_request", Message: "ai_pricing_multiplier_bps must be > 0"}
	}
	if *src > 1_000_000 {
		return &apptheory.AppError{Code: "app.bad_request", Message: "ai_pricing_multiplier_bps too large"}
	}
	update.AIPricingMultiplierBps = src
	*fields = append(*fields, "AIPricingMultiplierBps")
	return nil
}

func setAIMaxInflightJobs(update *models.Instance, src *int64, fields *[]string) error {
	if src == nil {
		return nil
	}
	if *src <= 0 {
		return &apptheory.AppError{Code: "app.bad_request", Message: "ai_max_inflight_jobs must be > 0"}
	}
	if *src > 10_000 {
		return &apptheory.AppError{Code: "app.bad_request", Message: "ai_max_inflight_jobs too large"}
	}
	update.AIMaxInflightJobs = src
	*fields = append(*fields, "AIMaxInflightJobs")
	return nil
}
func (s *Server) handleSetInstanceBudgetMonth(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(ctx.Param("slug")))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}
	month := strings.TrimSpace(ctx.Param("month"))
	if month == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month is required"}
	}
	if _, err := time.Parse("2006-01", month); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month must be YYYY-MM"}
	}

	if _, err := s.getInstance(ctx, slug); theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "instance not found"}
	} else if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var req setBudgetMonthRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}
	if req.IncludedCredits < 0 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "included_credits must be >= 0"}
	}

	// Preserve used credits if the record already exists.
	var existing models.InstanceBudgetMonth
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE#%s", slug)).
		Where("SK", "=", fmt.Sprintf("BUDGET#%s", month)).
		First(&existing)

	used := int64(0)
	if err == nil {
		used = existing.UsedCredits
	} else if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	now := time.Now().UTC()
	budget := &models.InstanceBudgetMonth{
		InstanceSlug:    slug,
		Month:           month,
		IncludedCredits: req.IncludedCredits,
		UsedCredits:     used,
		UpdatedAt:       now,
	}
	if err := budget.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(budget).CreateOrUpdate(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to set budget"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "budget.set",
		Target:    fmt.Sprintf("instance_budget:%s:%s", slug, month),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to write audit log"}
	}

	return apptheory.JSON(http.StatusOK, budgetMonthResponse{
		InstanceSlug:    slug,
		Month:           month,
		IncludedCredits: budget.IncludedCredits,
		UsedCredits:     budget.UsedCredits,
		UpdatedAt:       budget.UpdatedAt,
	})
}
