package controlplane

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/domains"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type portalUsageSummaryResponse struct {
	InstanceSlug string `json:"instance_slug"`
	Month        string `json:"month"`

	Requests    int64 `json:"requests"`
	CacheHits   int64 `json:"cache_hits"`
	CacheMisses int64 `json:"cache_misses"`

	CacheHitRate float64 `json:"cache_hit_rate"`

	ListCredits      int64 `json:"list_credits"`
	RequestedCredits int64 `json:"requested_credits"`
	DebitedCredits   int64 `json:"debited_credits"`
	DiscountCredits  int64 `json:"discount_credits"`

	IncludedCredits int64 `json:"included_credits,omitempty"`
	UsedCredits     int64 `json:"used_credits,omitempty"`
}

type portalBudgetsResponse struct {
	Budgets []budgetMonthResponse `json:"budgets"`
	Count   int                   `json:"count"`
}

func (s *Server) requireInstanceAccess(ctx *apptheory.Context, slug string) (*models.Instance, error) {
	if err := requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}

	inst, err := s.getInstance(ctx, slug)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "instance not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if isOperator(ctx) {
		return inst, nil
	}

	owner := strings.TrimSpace(inst.Owner)
	if owner == "" || owner != strings.TrimSpace(ctx.AuthIdentity) {
		return nil, &apptheory.AppError{Code: "app.forbidden", Message: "forbidden"}
	}

	return inst, nil
}

func (s *Server) maybeReturnExistingPortalInstance(ctx *apptheory.Context, slug string, username string) (*models.Instance, bool, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, false, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	existing, err := s.getInstance(ctx, slug)
	if err == nil && existing != nil {
		if isOperator(ctx) || strings.TrimSpace(existing.Owner) == strings.TrimSpace(username) {
			return existing, true, nil
		}
		return nil, false, &apptheory.AppError{Code: "app.conflict", Message: "instance already exists"}
	}
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, false, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	return nil, false, nil
}

func (s *Server) requirePortalCreateInstancePrereqs(ctx *apptheory.Context) *apptheory.AppError {
	if err := requireAuthenticated(ctx); err != nil {
		if appErr, ok := err.(*apptheory.AppError); ok {
			return appErr
		}
		return &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return appErr
	}
	if appErr := s.requirePortalApproved(ctx); appErr != nil {
		return appErr
	}
	return nil
}

func (s *Server) handlePortalCreateInstance(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requirePortalCreateInstancePrereqs(ctx); appErr != nil {
		return nil, appErr
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

	username := strings.TrimSpace(ctx.AuthIdentity)

	existing, ok, appErr := s.maybeReturnExistingPortalInstance(ctx, slug, username)
	if appErr != nil {
		return nil, appErr
	}
	if ok {
		return apptheory.JSON(http.StatusOK, instanceResponseFromModel(existing))
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
		Owner:                  username,
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
		Actor:     username,
		Action:    "portal.instance.create",
		Target:    fmt.Sprintf("instance:%s", slug),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = auditInstance.UpdateKeys()

	auditDomain := &models.AuditLogEntry{
		Actor:     username,
		Action:    "portal.domain.primary.create",
		Target:    fmt.Sprintf("domain:%s", primaryDomain.Domain),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = auditDomain.UpdateKeys()

	tipOp, auditTipOp, err := s.buildAutoTipRegistryOperation(ctx.Context(), primaryDomain.Domain, primaryDomain.DomainRaw, username, ctx.RequestID, now)
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

func (s *Server) handlePortalListInstances(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	pk := fmt.Sprintf("OWNER#%s", username)

	var items []*models.Instance
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.Instance{}).
		Index("gsi1").
		Where("gsi1PK", "=", pk).
		All(&items)
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

func (s *Server) handlePortalGetInstance(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}
	return apptheory.JSON(http.StatusOK, instanceResponseFromModel(inst))
}

func (s *Server) handlePortalUpdateInstanceConfig(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	var req updateInstanceConfigRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return nil, parseErr
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))
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
		Action:    "portal.instance.config.update",
		Target:    fmt.Sprintf("instance:%s", slug),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	updated, err := s.getInstance(ctx, slug)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	return apptheory.JSON(http.StatusOK, instanceResponseFromModel(updated))
}

func (s *Server) verifyPortalStartProvisionConsent(ctx *apptheory.Context, slug string, req startInstanceProvisionRequest) (startInstanceProvisionRequest, *apptheory.AppError) {
	consentChallengeID := strings.TrimSpace(req.ConsentChallengeID)
	consentMessage := strings.TrimSpace(req.ConsentMessage)
	consentSignature := strings.TrimSpace(req.ConsentSignature)
	if consentChallengeID == "" {
		return req, &apptheory.AppError{Code: "app.bad_request", Message: "consent_challenge_id is required"}
	}
	if consentMessage == "" {
		return req, &apptheory.AppError{Code: "app.bad_request", Message: "consent_message is required"}
	}
	if consentSignature == "" {
		return req, &apptheory.AppError{Code: "app.bad_request", Message: "consent_signature is required"}
	}

	chall, loadErr := s.getProvisionConsentChallenge(ctx, consentChallengeID)
	if loadErr != nil {
		return req, normalizeNotFound(loadErr)
	}

	stage := strings.TrimSpace(s.cfg.Stage)
	if stage == "" {
		stage = "lab"
	}

	if appErr := validateProvisionConsentChallenge(ctx, chall, slug, stage, consentMessage); appErr != nil {
		return req, appErr
	}
	if reservedErr := validateNotReservedWalletAddress(strings.TrimSpace(chall.WalletAddr), "wallet"); reservedErr != nil {
		return req, reservedErr
	}
	if verifyErr := verifyEthereumSignature(strings.TrimSpace(chall.WalletAddr), strings.TrimSpace(chall.Message), consentSignature); verifyErr != nil {
		return req, &apptheory.AppError{Code: "app.forbidden", Message: "invalid signature"}
	}
	_ = s.deleteProvisionConsentChallenge(ctx, chall)

	if reqAdmin := strings.ToLower(strings.TrimSpace(req.AdminUsername)); reqAdmin != "" && reqAdmin != strings.ToLower(strings.TrimSpace(chall.AdminUsername)) {
		return req, &apptheory.AppError{Code: "app.bad_request", Message: "admin_username does not match consent challenge"}
	}

	// Canonicalize consent artifacts from the stored challenge message.
	req.AdminUsername = strings.TrimSpace(chall.AdminUsername)
	req.AdminWalletType = strings.TrimSpace(chall.WalletType)
	req.AdminWalletAddress = strings.TrimSpace(chall.WalletAddr)
	req.AdminWalletChainID = chall.ChainID
	req.ConsentMessage = strings.TrimSpace(chall.Message)
	req.ConsentSignature = consentSignature

	return req, nil
}

func (s *Server) handlePortalStartInstanceProvisioning(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	if appErr := validateNotReservedWalletUsername(strings.TrimSpace(ctx.AuthIdentity)); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requirePortalApproved(ctx); appErr != nil {
		return nil, appErr
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))

	if job, ok := s.getExistingProvisionJobAndNudge(ctx, inst); ok {
		return apptheory.JSON(http.StatusOK, provisionJobResponseFromModel(job))
	}

	req, err := parseStartInstanceProvisionRequest(ctx)
	if err != nil {
		return nil, err
	}

	req, appErr := s.verifyPortalStartProvisionConsent(ctx, slug, req)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	job, baseDomain, region, appErr := s.buildManagedProvisionJob(slug, req, ctx.RequestID, now)
	if appErr != nil {
		return nil, appErr
	}

	if appErr := s.createManagedProvisionJobTx(ctx, job, slug, baseDomain, region, ctx.AuthIdentity, "portal.instance.provision.start", ctx.RequestID, now); appErr != nil {
		return nil, appErr
	}

	s.enqueueProvisionJobBestEffort(ctx, job.ID)

	return apptheory.JSON(http.StatusAccepted, provisionJobResponseFromModel(job))
}

func (s *Server) handlePortalGetInstanceProvisioning(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
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

func (s *Server) handlePortalListInstanceBudgets(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	pk := fmt.Sprintf("INSTANCE#%s", strings.TrimSpace(inst.Slug))

	var items []*models.InstanceBudgetMonth
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "BEGINS_WITH", "BUDGET#").
		Limit(120).
		All(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list budgets"}
	}

	out := make([]budgetMonthResponse, 0, len(items))
	for _, b := range items {
		out = append(out, budgetMonthResponse{
			InstanceSlug:    b.InstanceSlug,
			Month:           b.Month,
			IncludedCredits: b.IncludedCredits,
			UsedCredits:     b.UsedCredits,
			UpdatedAt:       b.UpdatedAt,
		})
	}

	return apptheory.JSON(http.StatusOK, portalBudgetsResponse{Budgets: out, Count: len(out)})
}

func (s *Server) handlePortalGetInstanceBudgetMonth(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	month := strings.TrimSpace(ctx.Param("month"))
	if month == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month is required"}
	}
	if _, parseErr := time.Parse("2006-01", month); parseErr != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month must be YYYY-MM"}
	}

	var item models.InstanceBudgetMonth
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE#%s", strings.TrimSpace(inst.Slug))).
		Where("SK", "=", fmt.Sprintf("BUDGET#%s", month)).
		ConsistentRead().
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return apptheory.JSON(http.StatusOK, budgetMonthResponse{
			InstanceSlug: strings.TrimSpace(inst.Slug),
			Month:        month,
		})
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	return apptheory.JSON(http.StatusOK, budgetMonthResponse{
		InstanceSlug:    item.InstanceSlug,
		Month:           item.Month,
		IncludedCredits: item.IncludedCredits,
		UsedCredits:     item.UsedCredits,
		UpdatedAt:       item.UpdatedAt,
	})
}

func (s *Server) handlePortalSetInstanceBudgetMonth(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))
	month := strings.TrimSpace(ctx.Param("month"))
	if month == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month is required"}
	}
	if _, parseErr := time.Parse("2006-01", month); parseErr != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month must be YYYY-MM"}
	}

	var req setBudgetMonthRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return nil, parseErr
	}
	if req.IncludedCredits < 0 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "included_credits must be >= 0"}
	}

	// Preserve used credits if the record already exists.
	var existing models.InstanceBudgetMonth
	err = s.store.DB.WithContext(ctx.Context()).
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
	if req.IncludedCredits < used {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "included_credits cannot be less than used_credits"}
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
		Action:    "portal.budget.set",
		Target:    fmt.Sprintf("instance_budget:%s:%s", slug, month),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, budgetMonthResponse{
		InstanceSlug:    slug,
		Month:           month,
		IncludedCredits: budget.IncludedCredits,
		UsedCredits:     budget.UsedCredits,
		UpdatedAt:       budget.UpdatedAt,
	})
}

func (s *Server) handlePortalListInstanceUsage(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))
	month := strings.TrimSpace(ctx.Param("month"))
	if month == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month is required"}
	}
	if _, parseErr := time.Parse("2006-01", month); parseErr != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month must be YYYY-MM"}
	}

	pk := fmt.Sprintf("USAGE#%s#%s", slug, month)
	var items []*models.UsageLedgerEntry
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.UsageLedgerEntry{}).
		Where("PK", "=", pk).
		Limit(500).
		All(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list usage"}
	}

	return apptheory.JSON(http.StatusOK, listUsageResponse{
		Entries: items,
		Count:   len(items),
	})
}

func (s *Server) handlePortalGetInstanceUsageSummary(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))
	month := strings.TrimSpace(ctx.Param("month"))
	if month == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month is required"}
	}
	if _, err := time.Parse("2006-01", month); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month must be YYYY-MM"}
	}

	pk := fmt.Sprintf("USAGE#%s#%s", slug, month)
	var items []*models.UsageLedgerEntry
	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.UsageLedgerEntry{}).
		Where("PK", "=", pk).
		Limit(2000).
		All(&items); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to load usage"}
	}

	var (
		totalRequests    int64
		cacheHits        int64
		cacheMisses      int64
		listCredits      int64
		requestedCredits int64
		debitedCredits   int64
		discountCredits  int64
	)

	for _, e := range items {
		totalRequests++
		if e != nil && e.Cached {
			cacheHits++
		} else {
			cacheMisses++
		}

		if e != nil {
			listCredits += e.ListCredits
			requestedCredits += e.RequestedCredits
			debitedCredits += e.DebitedCredits
			if e.ListCredits > e.RequestedCredits {
				discountCredits += e.ListCredits - e.RequestedCredits
			}
		}
	}

	cacheHitRate := float64(0)
	if totalRequests > 0 {
		cacheHitRate = float64(cacheHits) / float64(totalRequests)
	}

	out := portalUsageSummaryResponse{
		InstanceSlug:     slug,
		Month:            month,
		Requests:         totalRequests,
		CacheHits:        cacheHits,
		CacheMisses:      cacheMisses,
		CacheHitRate:     cacheHitRate,
		ListCredits:      listCredits,
		RequestedCredits: requestedCredits,
		DebitedCredits:   debitedCredits,
		DiscountCredits:  discountCredits,
	}

	// Best-effort: include budget totals for the month.
	var budget models.InstanceBudgetMonth
	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE#%s", slug)).
		Where("SK", "=", fmt.Sprintf("BUDGET#%s", month)).
		ConsistentRead().
		First(&budget); err == nil {
		out.IncludedCredits = budget.IncludedCredits
		out.UsedCredits = budget.UsedCredits
	}

	return apptheory.JSON(http.StatusOK, out)
}

func (s *Server) handlePortalListInstanceDomains(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))

	var items []*models.Domain
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.Domain{}).
		Index("gsi1").
		Where("gsi1PK", "=", fmt.Sprintf("INSTANCE_DOMAINS#%s", slug)).
		All(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list domains"}
	}

	out := make([]domainResponse, 0, len(items))
	for _, d := range items {
		out = append(out, domainResponseFromModel(d))
	}

	return apptheory.JSON(http.StatusOK, listDomainsResponse{Domains: out, Count: len(out)})
}

func (s *Server) handlePortalAddInstanceDomain(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))

	var req addDomainRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return nil, parseErr
	}

	rawDomain := strings.TrimSpace(req.Domain)
	domain, err := domains.NormalizeDomain(rawDomain)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	parentDomain := strings.TrimSpace(s.cfg.ManagedParentDomain)
	if parentDomain == "" {
		parentDomain = defaultManagedParentDomain
	}
	primary := fmt.Sprintf("%s.%s", slug, strings.TrimPrefix(parentDomain, "."))
	if domain == primary {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "domain is already managed as the primary domain"}
	}

	token, err := newToken(16)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create verification token"}
	}

	now := time.Now().UTC()
	item := &models.Domain{
		Domain:             domain,
		DomainRaw:          rawDomain,
		InstanceSlug:       slug,
		Type:               models.DomainTypeVanity,
		Status:             models.DomainStatusPending,
		VerificationMethod: domainVerificationMethodDNSTXT,
		VerificationToken:  token,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	_ = item.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(item).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "domain already exists"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to add domain"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "portal.domain.add",
		Target:    fmt.Sprintf("domain:%s", domain),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	txtName := domainVerificationRecordPrefix + domain
	txtValue := domainVerificationValuePrefix + token

	return apptheory.JSON(http.StatusCreated, addDomainResponse{
		Domain: domainResponseFromModel(item),
		Verification: addDomainVerification{
			Method:   domainVerificationMethodDNSTXT,
			TXTName:  txtName,
			TXTValue: txtValue,
		},
	})
}

func (s *Server) handlePortalVerifyInstanceDomain(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))

	domain, err := domains.NormalizeDomain(ctx.Param("domain"))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	item, err := s.loadInstanceDomain(ctx, domain, slug)
	if err != nil {
		return nil, err
	}

	if domainIsVerifiedOrActive(strings.TrimSpace(item.Status)) {
		return apptheory.JSON(http.StatusOK, verifyDomainResponse{Domain: domainResponseFromModel(item)})
	}

	token := strings.TrimSpace(item.VerificationToken)
	if token == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "domain is not eligible for verification"}
	}

	want := domainVerificationValuePrefix + token
	txtName := domainVerificationRecordPrefix + domain

	if verifyErr := verifyDomainTXT(ctx.Context(), txtName, want); verifyErr != nil {
		return nil, verifyErr
	}

	now := time.Now().UTC()
	update := &models.Domain{
		Domain:             domain,
		InstanceSlug:       slug,
		Type:               strings.TrimSpace(item.Type),
		Status:             models.DomainStatusVerified,
		VerificationMethod: domainVerificationMethodDNSTXT,
		VerificationToken:  "",
		VerifiedAt:         now,
		UpdatedAt:          now,
	}
	_ = update.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(update).IfExists().Update(
		"Status",
		"VerificationMethod",
		"VerificationToken",
		"VerifiedAt",
		"UpdatedAt",
	); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to verify domain"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "portal.domain.verify",
		Target:    fmt.Sprintf("domain:%s", domain),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	item.Status = models.DomainStatusVerified
	item.VerificationMethod = domainVerificationMethodDNSTXT
	item.VerificationToken = ""
	item.VerifiedAt = now
	item.UpdatedAt = now

	// Create an operator review request for vanity domain activation.
	if strings.TrimSpace(item.Type) == models.DomainTypeVanity {
		req := &models.VanityDomainRequest{
			Domain:       domain,
			DomainRaw:    strings.TrimSpace(item.DomainRaw),
			InstanceSlug: slug,
			RequestedBy:  strings.TrimSpace(ctx.AuthIdentity),
			Status:       models.VanityDomainRequestStatusPending,
			VerifiedAt:   now,
			RequestedAt:  now,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		_ = req.UpdateKeys()
		_ = s.store.DB.WithContext(ctx.Context()).Model(req).CreateOrUpdate()
	}

	if s.cfg.TipEnabled {
		_, _, _ = s.ensureTipRegistryHostOperation(ctx.Context(), domain, strings.TrimSpace(item.DomainRaw), strings.TrimSpace(ctx.AuthIdentity), ctx.RequestID)
	}

	return apptheory.JSON(http.StatusOK, verifyDomainResponse{Domain: domainResponseFromModel(item)})
}

func domainIsVerifiedOrActive(status string) bool {
	switch strings.TrimSpace(status) {
	case models.DomainStatusVerified, models.DomainStatusActive:
		return true
	default:
		return false
	}
}

func (s *Server) loadInstanceDomain(ctx *apptheory.Context, domain string, slug string) (*models.Domain, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var item models.Domain
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.Domain{}).
		Where("PK", "=", fmt.Sprintf("DOMAIN#%s", domain)).
		Where("SK", "=", models.SKMetadata).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(item.InstanceSlug) != strings.TrimSpace(slug) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
	}

	return &item, nil
}

func verifyDomainTXT(ctx context.Context, name string, want string) error {
	lookupCtx := ctx
	if lookupCtx == nil {
		lookupCtx = context.Background()
	}

	rc, cancel := context.WithTimeout(lookupCtx, 4*time.Second)
	defer cancel()

	records, err := net.DefaultResolver.LookupTXT(rc, name)
	if err != nil {
		return &apptheory.AppError{Code: "app.bad_request", Message: "verification record not found"}
	}

	for _, record := range records {
		if strings.TrimSpace(record) == want {
			return nil
		}
	}
	return &apptheory.AppError{Code: "app.bad_request", Message: "verification record not found"}
}

func (s *Server) handlePortalRotateInstanceDomain(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}
	slug := strings.ToLower(strings.TrimSpace(inst.Slug))

	domain, err := domains.NormalizeDomain(ctx.Param("domain"))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	var item models.Domain
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.Domain{}).
		Where("PK", "=", fmt.Sprintf("DOMAIN#%s", domain)).
		Where("SK", "=", models.SKMetadata).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(item.InstanceSlug) != slug {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
	}
	if strings.TrimSpace(item.Type) == models.DomainTypePrimary {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "primary domain cannot be rotated"}
	}

	token, err := newToken(16)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create verification token"}
	}

	now := time.Now().UTC()
	update := &models.Domain{
		Domain:             domain,
		InstanceSlug:       slug,
		Type:               strings.TrimSpace(item.Type),
		Status:             models.DomainStatusPending,
		VerificationMethod: domainVerificationMethodDNSTXT,
		VerificationToken:  token,
		VerifiedAt:         time.Time{},
		UpdatedAt:          now,
	}
	_ = update.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(update).IfExists().Update(
		"Status",
		"VerificationMethod",
		"VerificationToken",
		"VerifiedAt",
		"UpdatedAt",
	); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to rotate domain"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "portal.domain.rotate",
		Target:    fmt.Sprintf("domain:%s", domain),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	txtName := domainVerificationRecordPrefix + domain
	txtValue := domainVerificationValuePrefix + token

	item.Status = models.DomainStatusPending
	item.VerificationMethod = domainVerificationMethodDNSTXT
	item.VerificationToken = token
	item.VerifiedAt = time.Time{}
	item.UpdatedAt = now

	return apptheory.JSON(http.StatusOK, addDomainResponse{
		Domain: domainResponseFromModel(&item),
		Verification: addDomainVerification{
			Method:   domainVerificationMethodDNSTXT,
			TXTName:  txtName,
			TXTValue: txtValue,
		},
	})
}

func (s *Server) handlePortalDisableInstanceDomain(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}
	slug := strings.ToLower(strings.TrimSpace(inst.Slug))

	domain, err := domains.NormalizeDomain(ctx.Param("domain"))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	var item models.Domain
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.Domain{}).
		Where("PK", "=", fmt.Sprintf("DOMAIN#%s", domain)).
		Where("SK", "=", models.SKMetadata).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(item.InstanceSlug) != slug {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
	}
	if strings.TrimSpace(item.Type) == models.DomainTypePrimary {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "primary domain cannot be disabled"}
	}

	now := time.Now().UTC()
	update := &models.Domain{
		Domain:       domain,
		InstanceSlug: slug,
		Type:         strings.TrimSpace(item.Type),
		Status:       models.DomainStatusDisabled,
		UpdatedAt:    now,
	}
	_ = update.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(update).IfExists().Update("Status", "UpdatedAt"); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to disable domain"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "portal.domain.disable",
		Target:    fmt.Sprintf("domain:%s", domain),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	item.Status = models.DomainStatusDisabled
	item.UpdatedAt = now

	return apptheory.JSON(http.StatusOK, verifyDomainResponse{Domain: domainResponseFromModel(&item)})
}

func (s *Server) handlePortalDeleteInstanceDomain(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))
	domain, err := domains.NormalizeDomain(ctx.Param("domain"))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	var item models.Domain
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.Domain{}).
		Where("PK", "=", fmt.Sprintf("DOMAIN#%s", domain)).
		Where("SK", "=", models.SKMetadata).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(item.InstanceSlug) != slug {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
	}

	if strings.TrimSpace(item.Type) == models.DomainTypePrimary {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "primary domain cannot be removed"}
	}

	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.Domain{}).
		Where("PK", "=", item.PK).
		Where("SK", "=", item.SK).
		Delete(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to delete domain"}
	}

	now := time.Now().UTC()
	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "portal.domain.delete",
		Target:    fmt.Sprintf("domain:%s", domain),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, map[string]any{
		"deleted": true,
		"domain":  domain,
	})
}
