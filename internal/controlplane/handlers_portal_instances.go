package controlplane

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/domains"
	"github.com/equaltoai/lesser-host/internal/provisioning"
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

func (s *Server) handlePortalCreateInstance(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var req createInstanceRequest
	if err := parseJSON(ctx, &req); err != nil {
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

	// Idempotency: if the instance exists and is owned by the caller, return it.
	if existing, err := s.getInstance(ctx, slug); err == nil && existing != nil {
		if isOperator(ctx) || strings.TrimSpace(existing.Owner) == username {
			return apptheory.JSON(http.StatusOK, instanceResponseFromModel(existing))
		}
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "instance already exists"}
	} else if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
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
	aiModelSet := "openai:gpt-5-mini-2025-08-07"
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
		parentDomain = "greater.website"
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
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))
	update, fields, err := buildInstanceConfigUpdate(slug, req)
	if err != nil {
		return nil, err
	}
	if err := update.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(update).IfExists().Update(fields...); err != nil {
		if theoryErrors.IsConditionFailed(err) {
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

func (s *Server) handlePortalStartInstanceProvisioning(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))

	// Idempotency: if a job is already queued/running, return it.
	existingStatus := strings.ToLower(strings.TrimSpace(inst.ProvisionStatus))
	existingJobID := strings.TrimSpace(inst.ProvisionJobID)
	if (existingStatus == models.ProvisionJobStatusQueued || existingStatus == models.ProvisionJobStatusRunning) && existingJobID != "" {
		if job, jerr := s.store.GetProvisionJob(ctx.Context(), existingJobID); jerr == nil && job != nil {
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
		parentDomain = "greater.website"
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
		Action:    "portal.instance.provision.start",
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
	if _, err := time.Parse("2006-01", month); err != nil {
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
	if _, err := time.Parse("2006-01", month); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month must be YYYY-MM"}
	}

	var req setBudgetMonthRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
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
	if _, err := time.Parse("2006-01", month); err != nil {
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
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	rawDomain := strings.TrimSpace(req.Domain)
	domain, err := domains.NormalizeDomain(rawDomain)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	parentDomain := strings.TrimSpace(s.cfg.ManagedParentDomain)
	if parentDomain == "" {
		parentDomain = "greater.website"
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
		VerificationMethod: "dns_txt",
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
			Method:   "dns_txt",
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

	if strings.TrimSpace(item.Status) == models.DomainStatusVerified || strings.TrimSpace(item.Status) == models.DomainStatusActive {
		return apptheory.JSON(http.StatusOK, verifyDomainResponse{Domain: domainResponseFromModel(&item)})
	}

	token := strings.TrimSpace(item.VerificationToken)
	if token == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "domain is not eligible for verification"}
	}

	want := domainVerificationValuePrefix + token
	txtName := domainVerificationRecordPrefix + domain

	lookupCtx := ctx.Context()
	if lookupCtx == nil {
		lookupCtx = context.Background()
	}
	rc, cancel := context.WithTimeout(lookupCtx, 4*time.Second)
	defer cancel()

	records, err := net.DefaultResolver.LookupTXT(rc, txtName)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "verification record not found"}
	}

	found := false
	for _, r := range records {
		r = strings.TrimSpace(r)
		if r == want {
			found = true
			break
		}
	}
	if !found {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "verification record not found"}
	}

	now := time.Now().UTC()
	update := &models.Domain{
		Domain:             domain,
		InstanceSlug:       slug,
		Type:               strings.TrimSpace(item.Type),
		Status:             models.DomainStatusVerified,
		VerificationMethod: "dns_txt",
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
	item.VerificationMethod = "dns_txt"
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

	return apptheory.JSON(http.StatusOK, verifyDomainResponse{Domain: domainResponseFromModel(&item)})
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
		VerificationMethod: "dns_txt",
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
	item.VerificationMethod = "dns_txt"
	item.VerificationToken = token
	item.VerifiedAt = time.Time{}
	item.UpdatedAt = now

	return apptheory.JSON(http.StatusOK, addDomainResponse{
		Domain: domainResponseFromModel(&item),
		Verification: addDomainVerification{
			Method:   "dns_txt",
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
