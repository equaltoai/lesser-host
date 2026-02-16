package trust

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/billing"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	maxPublishLinks = 50
)

type publishJobModuleRequest struct {
	Name    string         `json:"name"`
	Options map[string]any `json:"options,omitempty"`
}

type publishJobPricing struct {
	AuthorAtPublish bool `json:"author_at_publish,omitempty"`
}

type publishJobRequest struct {
	ActorURI    string                    `json:"actor_uri,omitempty"`
	ObjectURI   string                    `json:"object_uri,omitempty"`
	ContentHash string                    `json:"content_hash,omitempty"`
	Links       []string                  `json:"links,omitempty"`
	Modules     []publishJobModuleRequest `json:"modules,omitempty"`
	Pricing     *publishJobPricing        `json:"pricing,omitempty"`
}

type publishJobResponse struct {
	JobID       string                     `json:"job_id"`
	ActorURI    string                     `json:"actor_uri,omitempty"`
	ObjectURI   string                     `json:"object_uri,omitempty"`
	ContentHash string                     `json:"content_hash,omitempty"`
	LinksHash   string                     `json:"links_hash"`
	Modules     []publishJobModuleResponse `json:"modules"`
}

type budgetDecision struct {
	Allowed    bool   `json:"allowed"`
	OverBudget bool   `json:"over_budget"`
	Reason     string `json:"reason,omitempty"`

	Month string `json:"month,omitempty"`

	IncludedCredits  int64 `json:"included_credits,omitempty"`
	UsedCredits      int64 `json:"used_credits,omitempty"`
	RemainingCredits int64 `json:"remaining_credits,omitempty"`

	RequestedCredits int64 `json:"requested_credits"`
	DebitedCredits   int64 `json:"debited_credits"`
}

type publishJobModuleResponse struct {
	Name           string         `json:"name"`
	PolicyVersion  string         `json:"policy_version"`
	Status         string         `json:"status"` // ok | not_checked_budget | unsupported | error
	Cached         bool           `json:"cached"`
	Budget         budgetDecision `json:"budget"`
	AttestationID  string         `json:"attestation_id,omitempty"`
	AttestationURL string         `json:"attestation_url,omitempty"`
	Result         any            `json:"result,omitempty"`
}

func (s *Server) handlePublishJob(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	// Instance config drives module defaults and billing behavior.
	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	renderPolicy := instCfg.RenderPolicy

	var req publishJobRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	actorURI := strings.TrimSpace(req.ActorURI)
	objectURI := strings.TrimSpace(req.ObjectURI)
	contentHash := strings.TrimSpace(req.ContentHash)

	// Default modules when omitted.
	modules := req.Modules
	if len(modules) == 0 {
		modules = defaultPublishJobModules()
	}

	pricingMultiplierBps := int64(10000)
	if req.Pricing != nil && req.Pricing.AuthorAtPublish {
		pricingMultiplierBps = authorPublishDiscountBPS
	}

	canonical := canonicalizePublishLinks(req.Links)

	linksHash := linkSafetyBasicLinksHash(canonical)

	// For now we only support link_safety_basic; return a stable job id regardless.
	jobID := linkSafetyBasicJobID(actorURI, objectURI, contentHash, linksHash)

	out := publishJobResponse{
		JobID:       jobID,
		ActorURI:    actorURI,
		ObjectURI:   objectURI,
		ContentHash: contentHash,
		LinksHash:   linksHash,
	}

	for _, mod := range modules {
		name := strings.ToLower(strings.TrimSpace(mod.Name))
		if name == "" {
			continue
		}
		out.Modules = append(out.Modules, s.runPublishJobModule(ctx, instanceSlug, jobID, actorURI, objectURI, contentHash, linksHash, renderPolicy, instCfg, pricingMultiplierBps, canonical, name))
	}

	if len(out.Modules) == 0 {
		// No modules requested; return a no-op response.
		out.Modules = []publishJobModuleResponse{
			{
				Name:          "link_safety_basic",
				PolicyVersion: linkSafetyBasicPolicyVersion,
				Status:        "ok",
				Cached:        true,
				Budget: budgetDecision{
					Allowed:          true,
					OverBudget:       false,
					Reason:           "no modules requested",
					RequestedCredits: 0,
					DebitedCredits:   0,
				},
				Result: models.LinkSafetyBasicResult{
					ID:            jobID,
					PolicyVersion: linkSafetyBasicPolicyVersion,
					ActorURI:      actorURI,
					ObjectURI:     objectURI,
					ContentHash:   contentHash,
					LinksHash:     linksHash,
					Links:         []models.LinkSafetyBasicLinkResult{},
					Summary:       models.LinkSafetyBasicSummary{TotalLinks: 0, OverallRisk: "low"},
				},
			},
		}
	}

	return apptheory.JSON(http.StatusOK, out)
}

func defaultPublishJobModules() []publishJobModuleRequest {
	// Auto-escalate to render for suspicious links by default (budgeted).
	return []publishJobModuleRequest{
		{Name: "link_safety_basic"},
		{Name: "link_safety_render"},
	}
}

func canonicalizePublishLinks(links []string) []string {
	canonical := make([]string, 0, len(links))
	seen := map[string]struct{}{}
	for _, raw := range links {
		if len(canonical) >= maxPublishLinks {
			break
		}
		key := strings.TrimSpace(normalizeLinkURLDeterministic(raw))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		canonical = append(canonical, key)
	}
	return canonical
}

func disabledPublishJobModuleResponse(name string, policyVersion string) publishJobModuleResponse {
	return publishJobModuleResponse{
		Name:          strings.TrimSpace(name),
		PolicyVersion: strings.TrimSpace(policyVersion),
		Status:        "disabled",
		Cached:        false,
		Budget: budgetDecision{
			Allowed:          true,
			OverBudget:       false,
			Reason:           "disabled",
			RequestedCredits: 0,
			DebitedCredits:   0,
		},
	}
}

func unsupportedPublishJobModuleResponse(name string) publishJobModuleResponse {
	return publishJobModuleResponse{
		Name:          strings.TrimSpace(name),
		PolicyVersion: "",
		Status:        "unsupported",
		Cached:        false,
		Budget: budgetDecision{
			Allowed:          true,
			OverBudget:       false,
			Reason:           "unsupported module",
			RequestedCredits: 0,
			DebitedCredits:   0,
		},
	}
}

func (s *Server) runPublishJobModule(
	ctx *apptheory.Context,
	instanceSlug string,
	jobID string,
	actorURI string,
	objectURI string,
	contentHash string,
	linksHash string,
	renderPolicy string,
	instCfg instanceTrustConfig,
	pricingMultiplierBps int64,
	canonicalLinks []string,
	moduleName string,
) publishJobModuleResponse {
	switch moduleName {
	case "link_safety_basic":
		if !instCfg.LinkSafetyEnabled {
			return disabledPublishJobModuleResponse("link_safety_basic", linkSafetyBasicPolicyVersion)
		}
		return s.runLinkSafetyBasicJob(ctx, instanceSlug, jobID, actorURI, objectURI, contentHash, linksHash, canonicalLinks, instCfg.OveragePolicy, pricingMultiplierBps)
	case "link_safety_render":
		if !instCfg.RendersEnabled {
			return disabledPublishJobModuleResponse("link_safety_render", rendering.RenderPolicyVersion)
		}
		return s.runLinkSafetyRenderJob(ctx, instanceSlug, jobID, renderPolicy, instCfg.OveragePolicy, pricingMultiplierBps, canonicalLinks)
	case "link_preview_render":
		if !instCfg.RendersEnabled {
			return disabledPublishJobModuleResponse("link_preview_render", rendering.RenderPolicyVersion)
		}
		return s.runLinkPreviewRenderJob(ctx, instanceSlug, jobID, renderPolicy, instCfg.OveragePolicy, pricingMultiplierBps, canonicalLinks)
	case "link_render_summary":
		if !instCfg.RendersEnabled {
			return disabledPublishJobModuleResponse("link_render_summary", linkRenderSummaryPolicyVersion)
		}
		return s.runLinkRenderSummaryJob(ctx, instanceSlug, renderPolicy, instCfg, pricingMultiplierBps, canonicalLinks)
	default:
		return unsupportedPublishJobModuleResponse(moduleName)
	}
}

const (
	authorPublishDiscountBPS int64 = 5000 // 50% discount when explicitly flagged as "author at publish".
)

var publishJobIDRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Server) handleGetPublishJob(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	jobID := strings.TrimSpace(ctx.Param("jobId"))
	if !publishJobIDRE.MatchString(jobID) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid job id"}
	}

	item, err := s.store.GetLinkSafetyBasicResult(ctx.Context(), jobID)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "job not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	return apptheory.JSON(http.StatusOK, item)
}

func analyzeLinkSafetyBasicBatch(ctx *apptheory.Context, canonicalLinks []string) []models.LinkSafetyBasicLinkResult {
	out := make([]models.LinkSafetyBasicLinkResult, 0, len(canonicalLinks))
	for _, raw := range canonicalLinks {
		out = append(out, analyzeLinkSafetyBasic(ctx.Context(), nil, raw))
	}
	return out
}

func (s *Server) storeLinkSafetyBasicNoLinksResult(
	ctx *apptheory.Context,
	instanceSlug string,
	jobID string,
	actorURI string,
	objectURI string,
	contentHash string,
	linksHash string,
	now time.Time,
) publishJobModuleResponse {
	item := &models.LinkSafetyBasicResult{
		ID:            jobID,
		PolicyVersion: linkSafetyBasicPolicyVersion,
		ActorURI:      actorURI,
		ObjectURI:     objectURI,
		ContentHash:   contentHash,
		LinksHash:     linksHash,
		Links:         []models.LinkSafetyBasicLinkResult{},
		Summary:       models.LinkSafetyBasicSummary{TotalLinks: 0, OverallRisk: "low"},
		CreatedAt:     now,
		ExpiresAt:     now.Add(7 * 24 * time.Hour),
		RequestID:     strings.TrimSpace(ctx.RequestID),
	}
	_ = item.UpdateKeys()

	audit := &models.AuditLogEntry{
		Actor:     instanceSlug,
		Action:    "link_safety_basic.check",
		Target:    fmt.Sprintf("link_safety_basic:%s", jobID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Create(item)
		tx.Put(audit)
		return nil
	})
	if theoryErrors.IsConditionFailed(err) {
		if cached, err2 := s.store.GetLinkSafetyBasicResult(ctx.Context(), jobID); err2 == nil {
			attID, _ := s.ensureLinkSafetyBasicAttestation(ctx.Context(), cached)
			return publishJobModuleResponse{
				Name:          "link_safety_basic",
				PolicyVersion: linkSafetyBasicPolicyVersion,
				Status:        "ok",
				Cached:        true,
				Budget: budgetDecision{
					Allowed:          true,
					OverBudget:       false,
					Reason:           "cache_hit",
					RequestedCredits: 0,
					DebitedCredits:   0,
				},
				AttestationID:  strings.TrimSpace(attID),
				AttestationURL: attestationURL(ctx, attID),
				Result:         cached,
			}
		}
	}
	if err != nil {
		return publishJobModuleResponse{
			Name:          "link_safety_basic",
			PolicyVersion: linkSafetyBasicPolicyVersion,
			Status:        "error",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          true,
				OverBudget:       false,
				Reason:           "internal error",
				RequestedCredits: 0,
				DebitedCredits:   0,
			},
		}
	}

	attID, _ := s.ensureLinkSafetyBasicAttestation(ctx.Context(), item)
	return publishJobModuleResponse{
		Name:          "link_safety_basic",
		PolicyVersion: linkSafetyBasicPolicyVersion,
		Status:        "ok",
		Cached:        false,
		Budget: budgetDecision{
			Allowed:          true,
			OverBudget:       false,
			Reason:           "no_links",
			RequestedCredits: 0,
			DebitedCredits:   0,
		},
		AttestationID:  strings.TrimSpace(attID),
		AttestationURL: attestationURL(ctx, attID),
		Result:         item,
	}
}

func (s *Server) precheckLinkSafetyBasicBudget(
	ctx *apptheory.Context,
	instanceSlug string,
	month string,
	pk string,
	sk string,
	creditsPriced int64,
	overagePolicy string,
) (*models.InstanceBudgetMonth, *publishJobModuleResponse) {
	var budget models.InstanceBudgetMonth
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&budget)
	if theoryErrors.IsNotFound(err) {
		resp := publishJobModuleResponse{
			Name:          "link_safety_basic",
			PolicyVersion: linkSafetyBasicPolicyVersion,
			Status:        "not_checked_budget",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          false,
				OverBudget:       true,
				Reason:           "budget not configured",
				Month:            month,
				RequestedCredits: creditsPriced,
				DebitedCredits:   0,
			},
		}
		return nil, &resp
	}
	if err != nil {
		resp := publishJobModuleResponse{
			Name:          "link_safety_basic",
			PolicyVersion: linkSafetyBasicPolicyVersion,
			Status:        "error",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          true,
				OverBudget:       false,
				Reason:           "internal error",
				RequestedCredits: creditsPriced,
				DebitedCredits:   0,
			},
		}
		return nil, &resp
	}

	remaining := budget.IncludedCredits - budget.UsedCredits
	allowOverage := strings.ToLower(strings.TrimSpace(overagePolicy)) == overagePolicyAllow
	if remaining < creditsPriced && !allowOverage {
		resp := publishJobModuleResponse{
			Name:          "link_safety_basic",
			PolicyVersion: linkSafetyBasicPolicyVersion,
			Status:        "not_checked_budget",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          false,
				OverBudget:       true,
				Reason:           "budget exceeded",
				Month:            month,
				IncludedCredits:  budget.IncludedCredits,
				UsedCredits:      budget.UsedCredits,
				RemainingCredits: remaining,
				RequestedCredits: creditsPriced,
				DebitedCredits:   0,
			},
		}
		return nil, &resp
	}

	return &budget, nil
}

func (s *Server) transactDebitBudgetAndStoreLinkSafetyBasic(
	ctx *apptheory.Context,
	allowOverage bool,
	maxUsed int64,
	update *models.InstanceBudgetMonth,
	ledger *models.UsageLedgerEntry,
	item *models.LinkSafetyBasicResult,
	auditBudget *models.AuditLogEntry,
	auditScan *models.AuditLogEntry,
	creditsPriced int64,
	now time.Time,
) error {
	if allowOverage {
		return s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
			tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
				ub.Add("UsedCredits", creditsPriced)
				ub.Set("UpdatedAt", now)
				return nil
			}, tabletheory.IfExists())
			tx.Put(auditBudget)
			tx.Put(ledger)
			tx.Create(item)
			tx.Put(auditScan)
			return nil
		})
	}

	return s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
			ub.Add("UsedCredits", creditsPriced)
			ub.Set("UpdatedAt", now)
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.ConditionExpression(
				"attribute_not_exists(usedCredits) OR usedCredits <= :max",
				map[string]any{
					":max": maxUsed,
				},
			),
		)
		tx.Put(auditBudget)
		tx.Put(ledger)
		tx.Create(item)
		tx.Put(auditScan)
		return nil
	})
}

func (s *Server) handleLinkSafetyBasicConditionFailed(
	ctx *apptheory.Context,
	instanceSlug string,
	month string,
	pk string,
	sk string,
	jobID string,
	actorURI string,
	objectURI string,
	contentHash string,
	linksHash string,
	creditsRequested int64,
	creditsPriced int64,
	pricingMultiplierBps int64,
	now time.Time,
) publishJobModuleResponse {
	if cached, err2 := s.store.GetLinkSafetyBasicResult(ctx.Context(), jobID); err2 == nil {
		return s.linkSafetyBasicCacheHitResponse(
			ctx,
			instanceSlug,
			month,
			jobID,
			actorURI,
			objectURI,
			contentHash,
			linksHash,
			creditsRequested,
			creditsPriced,
			pricingMultiplierBps,
			now,
			cached,
		)
	}

	var latest models.InstanceBudgetMonth
	if err3 := s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&latest); err3 == nil {
		remaining := latest.IncludedCredits - latest.UsedCredits
		reason := "budget exceeded"
		if remaining >= creditsPriced {
			reason = "budget conflict"
		}
		return publishJobModuleResponse{
			Name:          "link_safety_basic",
			PolicyVersion: linkSafetyBasicPolicyVersion,
			Status:        "not_checked_budget",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          false,
				OverBudget:       true,
				Reason:           reason,
				Month:            month,
				IncludedCredits:  latest.IncludedCredits,
				UsedCredits:      latest.UsedCredits,
				RemainingCredits: remaining,
				RequestedCredits: creditsPriced,
				DebitedCredits:   0,
			},
		}
	}

	return publishJobModuleResponse{
		Name:          "link_safety_basic",
		PolicyVersion: linkSafetyBasicPolicyVersion,
		Status:        "not_checked_budget",
		Cached:        false,
		Budget: budgetDecision{
			Allowed:          false,
			OverBudget:       true,
			Reason:           "budget exceeded",
			Month:            month,
			RequestedCredits: creditsPriced,
			DebitedCredits:   0,
		},
	}
}

func (s *Server) linkSafetyBasicDebitedResponse(
	ctx *apptheory.Context,
	instanceSlug string,
	month string,
	pk string,
	sk string,
	creditsPriced int64,
	overageDebited int64,
	overagePolicy string,
	now time.Time,
	item *models.LinkSafetyBasicResult,
) publishJobModuleResponse {
	var latest models.InstanceBudgetMonth
	_ = s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&latest)

	remaining := latest.IncludedCredits - latest.UsedCredits
	overBudget := remaining < 0
	if strings.ToLower(strings.TrimSpace(overagePolicy)) == overagePolicyAllow && overageDebited > 0 {
		overBudget = true
	}

	reason := "debited"
	if overageDebited > 0 {
		reason = "overage"
	}
	attID, _ := s.ensureLinkSafetyBasicAttestation(ctx.Context(), item)
	return publishJobModuleResponse{
		Name:          "link_safety_basic",
		PolicyVersion: linkSafetyBasicPolicyVersion,
		Status:        "ok",
		Cached:        false,
		Budget: budgetDecision{
			Allowed:          true,
			OverBudget:       overBudget,
			Month:            month,
			IncludedCredits:  latest.IncludedCredits,
			UsedCredits:      latest.UsedCredits,
			RemainingCredits: remaining,
			RequestedCredits: creditsPriced,
			DebitedCredits:   creditsPriced,
			Reason:           reason,
		},
		AttestationID:  strings.TrimSpace(attID),
		AttestationURL: attestationURL(ctx, attID),
		Result:         item,
	}
}

func (s *Server) runLinkSafetyBasicJob(
	ctx *apptheory.Context,
	instanceSlug string,
	jobID string,
	actorURI string,
	objectURI string,
	contentHash string,
	linksHash string,
	canonicalLinks []string,
	overagePolicy string,
	pricingMultiplierBps int64,
) publishJobModuleResponse {
	now := time.Now().UTC()
	creditsRequested := int64(len(canonicalLinks))
	creditsPriced := billing.PricedCredits(creditsRequested, pricingMultiplierBps)
	month := now.Format("2006-01")

	// Cache hit path (no charge).
	if cached, err := s.store.GetLinkSafetyBasicResult(ctx.Context(), jobID); err == nil {
		return s.linkSafetyBasicCacheHitResponse(
			ctx,
			instanceSlug,
			month,
			jobID,
			actorURI,
			objectURI,
			contentHash,
			linksHash,
			creditsRequested,
			creditsPriced,
			pricingMultiplierBps,
			now,
			cached,
		)
	}

	// No links: store a deterministic empty result without debiting.
	if creditsRequested == 0 || creditsPriced == 0 {
		return s.storeLinkSafetyBasicNoLinksResult(ctx, instanceSlug, jobID, actorURI, objectURI, contentHash, linksHash, now)
	}

	links := analyzeLinkSafetyBasicBatch(ctx, canonicalLinks)

	// Budget pre-check to avoid ambiguous transaction condition failures.
	pk := fmt.Sprintf("INSTANCE#%s", instanceSlug)
	sk := fmt.Sprintf("BUDGET#%s", month)
	budget, precheckResp := s.precheckLinkSafetyBasicBudget(ctx, instanceSlug, month, pk, sk, creditsPriced, overagePolicy)
	if precheckResp != nil {
		return *precheckResp
	}

	item := &models.LinkSafetyBasicResult{
		ID:            jobID,
		PolicyVersion: linkSafetyBasicPolicyVersion,
		ActorURI:      actorURI,
		ObjectURI:     objectURI,
		ContentHash:   contentHash,
		LinksHash:     linksHash,
		Links:         append([]models.LinkSafetyBasicLinkResult(nil), links...),
		Summary:       computeLinkSafetyBasicSummary(links),
		CreatedAt:     now,
		ExpiresAt:     now.Add(7 * 24 * time.Hour),
		RequestID:     strings.TrimSpace(ctx.RequestID),
	}
	_ = item.UpdateKeys()

	update := &models.InstanceBudgetMonth{
		InstanceSlug: instanceSlug,
		Month:        month,
		UpdatedAt:    now,
	}
	_ = update.UpdateKeys()

	includedDebited, overageDebited := billing.PartsForDebit(budget.IncludedCredits, budget.UsedCredits, creditsPriced)
	billingType := billing.TypeFromParts(includedDebited, overageDebited)

	ledger := &models.UsageLedgerEntry{
		ID:                     billing.UsageLedgerEntryID(instanceSlug, month, strings.TrimSpace(ctx.RequestID), "link_safety_basic", jobID, creditsPriced),
		InstanceSlug:           instanceSlug,
		Month:                  month,
		Module:                 "link_safety_basic",
		Target:                 jobID,
		Cached:                 false,
		Reason:                 billingType,
		RequestID:              strings.TrimSpace(ctx.RequestID),
		RequestedCredits:       creditsPriced,
		ListCredits:            creditsRequested,
		PricingMultiplierBps:   pricingMultiplierBps,
		DebitedCredits:         creditsPriced,
		IncludedDebitedCredits: includedDebited,
		OverageDebitedCredits:  overageDebited,
		BillingType:            billingType,
		ActorURI:               actorURI,
		ObjectURI:              objectURI,
		ContentHash:            contentHash,
		LinksHash:              linksHash,
		CreatedAt:              now,
	}
	_ = ledger.UpdateKeys()

	auditBudget := &models.AuditLogEntry{
		Actor:     instanceSlug,
		Action:    "budget.debit",
		Target:    fmt.Sprintf("instance_budget:%s:%s", instanceSlug, month),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}
	_ = auditBudget.UpdateKeys()

	auditScan := &models.AuditLogEntry{
		Actor:     instanceSlug,
		Action:    "link_safety_basic.check",
		Target:    fmt.Sprintf("link_safety_basic:%s", jobID),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}
	_ = auditScan.UpdateKeys()

	allowOverage := strings.ToLower(strings.TrimSpace(overagePolicy)) == overagePolicyAllow
	maxUsed := budget.IncludedCredits - creditsPriced
	txnErr := s.transactDebitBudgetAndStoreLinkSafetyBasic(ctx, allowOverage, maxUsed, update, ledger, item, auditBudget, auditScan, creditsPriced, now)
	if theoryErrors.IsConditionFailed(txnErr) {
		return s.handleLinkSafetyBasicConditionFailed(
			ctx,
			instanceSlug,
			month,
			pk,
			sk,
			jobID,
			actorURI,
			objectURI,
			contentHash,
			linksHash,
			creditsRequested,
			creditsPriced,
			pricingMultiplierBps,
			now,
		)
	}
	if txnErr != nil {
		fmt.Printf("link_safety_basic transact error request_id=%s instance=%s job_id=%s err=%v\n", strings.TrimSpace(ctx.RequestID), instanceSlug, jobID, txnErr)
		return publishJobModuleResponse{
			Name:          "link_safety_basic",
			PolicyVersion: linkSafetyBasicPolicyVersion,
			Status:        "error",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          true,
				OverBudget:       false,
				Reason:           "internal error",
				RequestedCredits: creditsPriced,
				DebitedCredits:   0,
			},
		}
	}

	return s.linkSafetyBasicDebitedResponse(ctx, instanceSlug, month, pk, sk, creditsPriced, overageDebited, overagePolicy, now, item)
}

func (s *Server) linkSafetyBasicCacheHitResponse(
	ctx *apptheory.Context,
	instanceSlug string,
	month string,
	jobID string,
	actorURI string,
	objectURI string,
	contentHash string,
	linksHash string,
	creditsRequested int64,
	creditsPriced int64,
	pricingMultiplierBps int64,
	now time.Time,
	cached *models.LinkSafetyBasicResult,
) publishJobModuleResponse {
	ledger := &models.UsageLedgerEntry{
		ID:                   billing.UsageLedgerEntryID(instanceSlug, month, strings.TrimSpace(ctx.RequestID), "link_safety_basic", jobID, 0),
		InstanceSlug:         instanceSlug,
		Month:                month,
		Module:               "link_safety_basic",
		Target:               jobID,
		Cached:               true,
		Reason:               "cache_hit",
		RequestID:            strings.TrimSpace(ctx.RequestID),
		RequestedCredits:     creditsPriced,
		ListCredits:          creditsRequested,
		PricingMultiplierBps: pricingMultiplierBps,
		DebitedCredits:       0,
		BillingType:          models.BillingTypeNone,
		ActorURI:             actorURI,
		ObjectURI:            objectURI,
		ContentHash:          contentHash,
		LinksHash:            linksHash,
		CreatedAt:            now,
	}
	_ = ledger.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(ledger).IfNotExists().Create()

	attID, _ := s.ensureLinkSafetyBasicAttestation(ctx.Context(), cached)
	return publishJobModuleResponse{
		Name:          "link_safety_basic",
		PolicyVersion: linkSafetyBasicPolicyVersion,
		Status:        "ok",
		Cached:        true,
		Budget: budgetDecision{
			Allowed:          true,
			OverBudget:       false,
			Reason:           "cache_hit",
			RequestedCredits: creditsPriced,
			DebitedCredits:   0,
		},
		AttestationID:  strings.TrimSpace(attID),
		AttestationURL: attestationURL(ctx, attID),
		Result:         cached,
	}
}
