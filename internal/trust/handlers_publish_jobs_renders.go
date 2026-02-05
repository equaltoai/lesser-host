package trust

import (
	"fmt"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/billing"
	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	linkRenderCreditCost int64 = 5 // credits per cache-miss render
)

type linkRenderLinkResult struct {
	URL           string `json:"url"`
	NormalizedURL string `json:"normalized_url,omitempty"`
	Risk          string `json:"risk,omitempty"`

	Status string                  `json:"status"` // ok|queued|skipped|blocked|invalid|not_checked_budget|error
	Render *renderArtifactResponse `json:"render,omitempty"`
}

type linkRenderSummary struct {
	TotalLinks int `json:"total_links"`

	Candidates int `json:"candidates"`
	Cached     int `json:"cached"`
	Queued     int `json:"queued"`
	Skipped    int `json:"skipped"`
	Blocked    int `json:"blocked"`
	Invalid    int `json:"invalid"`
}

type linkRenderResult struct {
	RenderPolicy string                 `json:"render_policy"`
	Summary      linkRenderSummary      `json:"summary"`
	Links        []linkRenderLinkResult `json:"links"`
}

func (s *Server) runLinkSafetyRenderJob(ctx *apptheory.Context, instanceSlug string, jobID string, renderPolicy string, overagePolicy string, pricingMultiplierBps int64, canonicalLinks []string) publishJobModuleResponse {
	return s.runLinkRenderJob(ctx, instanceSlug, jobID, "link_safety_render", renderPolicy, overagePolicy, pricingMultiplierBps, canonicalLinks)
}

func (s *Server) runLinkPreviewRenderJob(ctx *apptheory.Context, instanceSlug string, jobID string, renderPolicy string, overagePolicy string, pricingMultiplierBps int64, canonicalLinks []string) publishJobModuleResponse {
	return s.runLinkRenderJob(ctx, instanceSlug, jobID, "link_preview_render", renderPolicy, overagePolicy, pricingMultiplierBps, canonicalLinks)
}

type missingRenderRequest struct {
	Index          int
	NormalizedURL  string
	RetentionClass string
}

type linkRenderDecision struct {
	link      linkRenderLinkResult
	candidate bool
	cached    bool
	missing   *missingRenderCandidate
}

type missingRenderCandidate struct {
	NormalizedURL  string
	RetentionClass string
}

func (s *Server) runLinkRenderJob(
	ctx *apptheory.Context,
	instanceSlug string,
	jobID string,
	moduleName string,
	renderPolicy string,
	overagePolicy string,
	pricingMultiplierBps int64,
	canonicalLinks []string,
) publishJobModuleResponse {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return publishJobModuleResponse{
			Name:          moduleName,
			PolicyVersion: rendering.RenderPolicyVersion,
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
	now := time.Now().UTC()

	policy := normalizeRenderPolicy(renderPolicy)
	out, missing := s.buildLinkRenderResult(ctx, now, policy, strings.TrimSpace(instanceSlug), canonicalLinks)

	creditsRequested := int64(out.Summary.Candidates) * linkRenderCreditCost
	creditsNeeded := int64(len(missing)) * linkRenderCreditCost
	creditsRequestedPriced := billing.PricedCredits(creditsRequested, pricingMultiplierBps)
	creditsNeededPriced := billing.PricedCredits(creditsNeeded, pricingMultiplierBps)

	if len(missing) == 0 {
		return publishJobModuleResponse{
			Name:          moduleName,
			PolicyVersion: rendering.RenderPolicyVersion,
			Status:        "ok",
			Cached:        true,
			Budget: budgetDecision{
				Allowed:          true,
				OverBudget:       false,
				Reason:           "cache_hit",
				RequestedCredits: creditsRequestedPriced,
				DebitedCredits:   0,
			},
			Result: out,
		}
	}

	month := now.Format("2006-01")
	if strings.TrimSpace(s.cfg.PreviewQueueURL) == "" || s.queues == nil {
		setMissingLinkRenderStatuses(&out, missing, "error")
		return publishJobModuleResponse{
			Name:          moduleName,
			PolicyVersion: rendering.RenderPolicyVersion,
			Status:        "error",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          true,
				OverBudget:       false,
				Reason:           "render queue not configured",
				Month:            month,
				RequestedCredits: creditsRequestedPriced,
				DebitedCredits:   0,
			},
			Result: out,
		}
	}

	budget, _, overageDebited, errKind, err := s.debitLinkRenderBudget(
		ctx,
		strings.TrimSpace(instanceSlug),
		moduleName,
		strings.TrimSpace(jobID),
		month,
		now,
		overagePolicy,
		creditsRequestedPriced,
		creditsNeededPriced,
	)
	if err != nil {
		if errKind == budgetErrKindInternal {
			setMissingLinkRenderStatuses(&out, missing, "error")
			return publishJobModuleResponse{
				Name:          moduleName,
				PolicyVersion: rendering.RenderPolicyVersion,
				Status:        "error",
				Cached:        false,
				Budget: budgetDecision{
					Allowed:          true,
					OverBudget:       false,
					Reason:           "internal error",
					Month:            month,
					RequestedCredits: creditsRequestedPriced,
					DebitedCredits:   0,
				},
				Result: out,
			}
		}

		setMissingLinkRenderStatuses(&out, missing, "not_checked_budget")
		reason := "budget not configured"
		budgetAllowed := false
		if errKind == budgetErrKindExceeded {
			reason = "budget exceeded"
		}

		remaining := budget.IncludedCredits - budget.UsedCredits
		return publishJobModuleResponse{
			Name:          moduleName,
			PolicyVersion: rendering.RenderPolicyVersion,
			Status:        "not_checked_budget",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          budgetAllowed,
				OverBudget:       true,
				Reason:           reason,
				Month:            month,
				IncludedCredits:  budget.IncludedCredits,
				UsedCredits:      budget.UsedCredits,
				RemainingCredits: remaining,
				RequestedCredits: creditsRequestedPriced,
				DebitedCredits:   0,
			},
			Result: out,
		}
	}

	queuedCount := s.queueMissingRenders(ctx, strings.TrimSpace(instanceSlug), now, missing, &out)
	out.Summary.Queued = queuedCount

	// Best-effort current remaining for reporting (may be stale if overage allowed).
	remainingAfter := budget.IncludedCredits - (budget.UsedCredits + creditsNeededPriced)
	overBudget := remainingAfter < 0 || overageDebited > 0
	reason := budgetReasonDebited
	if overageDebited > 0 {
		reason = budgetReasonOverage
	}

	return publishJobModuleResponse{
		Name:          moduleName,
		PolicyVersion: rendering.RenderPolicyVersion,
		Status:        "ok",
		Cached:        false,
		Budget: budgetDecision{
			Allowed:          true,
			OverBudget:       overBudget,
			Reason:           reason,
			Month:            month,
			IncludedCredits:  budget.IncludedCredits,
			UsedCredits:      budget.UsedCredits + creditsNeededPriced,
			RemainingCredits: remainingAfter,
			RequestedCredits: creditsRequestedPriced,
			DebitedCredits:   creditsNeededPriced,
		},
		Result: out,
	}
}

func normalizeRenderPolicy(renderPolicy string) string {
	policy := strings.ToLower(strings.TrimSpace(renderPolicy))
	switch policy {
	case renderPolicyAlways, renderPolicySuspicious:
		return policy
	default:
		return renderPolicySuspicious
	}
}

func (s *Server) buildLinkRenderResult(ctx *apptheory.Context, now time.Time, policy string, instanceSlug string, canonicalLinks []string) (linkRenderResult, []missingRenderRequest) {
	out := linkRenderResult{
		RenderPolicy: strings.TrimSpace(policy),
		Summary: linkRenderSummary{
			TotalLinks: len(canonicalLinks),
		},
		Links: make([]linkRenderLinkResult, 0, len(canonicalLinks)),
	}

	missing := make([]missingRenderRequest, 0)
	for _, raw := range canonicalLinks {
		decision := s.decideLinkRender(ctx, now, policy, instanceSlug, raw)
		out.Links = append(out.Links, decision.link)

		switch decision.link.Status {
		case statusInvalid:
			out.Summary.Invalid++
		case statusBlocked:
			out.Summary.Blocked++
		case statusSkipped:
			out.Summary.Skipped++
		}

		if decision.candidate {
			out.Summary.Candidates++
		}
		if decision.cached {
			out.Summary.Cached++
		}
		if decision.missing != nil {
			missing = append(missing, missingRenderRequest{
				Index:          len(out.Links) - 1,
				NormalizedURL:  decision.missing.NormalizedURL,
				RetentionClass: decision.missing.RetentionClass,
			})
		}
	}

	return out, missing
}

func (s *Server) decideLinkRender(ctx *apptheory.Context, now time.Time, policy string, instanceSlug string, raw string) linkRenderDecision {
	raw = strings.TrimSpace(raw)
	analysis := analyzeLinkSafetyBasic(ctx.Context(), nil, raw)
	risk := strings.ToLower(strings.TrimSpace(analysis.Risk))
	normalized := strings.TrimSpace(analysis.NormalizedURL)
	if normalized == "" {
		normalized = raw
	}

	linkOut := linkRenderLinkResult{
		URL:           raw,
		NormalizedURL: normalized,
		Risk:          risk,
	}

	switch risk {
	case statusInvalid:
		linkOut.Status = statusInvalid
		return linkRenderDecision{link: linkOut}
	case statusBlocked:
		linkOut.Status = statusBlocked
		return linkRenderDecision{link: linkOut}
	}

	if !shouldRenderLink(policy, risk) {
		linkOut.Status = statusSkipped
		return linkRenderDecision{link: linkOut}
	}

	retentionClass := retentionClassForRisk(risk)
	renderID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, normalized)
	artifact, err := s.store.GetRenderArtifact(ctx.Context(), renderID)
	if err == nil && artifact != nil {
		s.maybeUpgradeRenderArtifactRetention(ctx, artifact, retentionClass, now, instanceSlug)
		r := renderArtifactResponseFromModel(ctx, artifact, true)
		linkOut.Render = &r
		linkOut.Status = r.Status
		return linkRenderDecision{
			link:      linkOut,
			candidate: true,
			cached:    true,
		}
	}
	if err != nil && !theoryErrors.IsNotFound(err) {
		linkOut.Status = statusError
		return linkRenderDecision{
			link:      linkOut,
			candidate: true,
		}
	}

	linkOut.Status = statusQueued
	return linkRenderDecision{
		link:      linkOut,
		candidate: true,
		missing: &missingRenderCandidate{
			NormalizedURL:  normalized,
			RetentionClass: retentionClass,
		},
	}
}

func (s *Server) maybeUpgradeRenderArtifactRetention(ctx *apptheory.Context, artifact *models.RenderArtifact, retentionClass string, now time.Time, instanceSlug string) {
	if ctx == nil || artifact == nil || strings.TrimSpace(retentionClass) != models.RenderRetentionClassEvidence {
		return
	}

	desiredDays, desiredClass := rendering.RetentionForClass(retentionClass)
	desiredExpiresAt := rendering.ExpiresAtForRetention(now, desiredDays)
	updated := false
	if artifact.ExpiresAt.Before(desiredExpiresAt) {
		artifact.ExpiresAt = desiredExpiresAt
		updated = true
	}
	if artifact.RetentionClass != models.RenderRetentionClassEvidence && desiredClass == models.RenderRetentionClassEvidence {
		artifact.RetentionClass = desiredClass
		updated = true
	}
	if !updated {
		return
	}

	artifact.RequestID = strings.TrimSpace(ctx.RequestID)
	artifact.RequestedBy = strings.TrimSpace(instanceSlug)
	_ = artifact.UpdateKeys()
	_ = s.store.PutRenderArtifact(ctx.Context(), artifact)
}

func setMissingLinkRenderStatuses(out *linkRenderResult, missing []missingRenderRequest, status string) {
	if out == nil {
		return
	}
	for _, mr := range missing {
		if mr.Index >= 0 && mr.Index < len(out.Links) {
			out.Links[mr.Index].Status = status
		}
	}
}

func (s *Server) debitLinkRenderBudget(
	ctx *apptheory.Context,
	instanceSlug string,
	moduleName string,
	jobID string,
	month string,
	now time.Time,
	overagePolicy string,
	creditsRequestedPriced int64,
	creditsNeededPriced int64,
) (models.InstanceBudgetMonth, int64, int64, string, error) {
	pk := fmt.Sprintf("INSTANCE#%s", instanceSlug)
	sk := fmt.Sprintf("BUDGET#%s", month)

	var budget models.InstanceBudgetMonth
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&budget)
	if theoryErrors.IsNotFound(err) {
		return models.InstanceBudgetMonth{}, 0, 0, budgetErrKindNotConfigured, err
	}
	if err != nil {
		return models.InstanceBudgetMonth{}, 0, 0, budgetErrKindInternal, err
	}

	remaining := budget.IncludedCredits - budget.UsedCredits
	allowOverage := strings.ToLower(strings.TrimSpace(overagePolicy)) == overagePolicyAllow
	if remaining < creditsNeededPriced && !allowOverage {
		return budget, 0, 0, budgetErrKindExceeded, fmt.Errorf("budget exceeded")
	}

	update := &models.InstanceBudgetMonth{
		InstanceSlug: instanceSlug,
		Month:        month,
		UpdatedAt:    now,
	}
	_ = update.UpdateKeys()

	includedDebited, overageDebited := billing.PartsForDebit(budget.IncludedCredits, budget.UsedCredits, creditsNeededPriced)
	billingType := billing.TypeFromParts(includedDebited, overageDebited)

	ledger := &models.UsageLedgerEntry{
		ID:                     billing.UsageLedgerEntryID(instanceSlug, month, strings.TrimSpace(ctx.RequestID), moduleName, jobID, creditsNeededPriced),
		InstanceSlug:           instanceSlug,
		Month:                  month,
		Module:                 moduleName,
		Target:                 jobID,
		Cached:                 false,
		Reason:                 billingType,
		RequestID:              strings.TrimSpace(ctx.RequestID),
		RequestedCredits:       creditsRequestedPriced,
		DebitedCredits:         creditsNeededPriced,
		IncludedDebitedCredits: includedDebited,
		OverageDebitedCredits:  overageDebited,
		BillingType:            billingType,
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

	err = s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		if allowOverage {
			tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
				ub.Add("UsedCredits", creditsNeededPriced)
				ub.Set("UpdatedAt", now)
				return nil
			}, tabletheory.IfExists())
		} else {
			tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
				ub.Add("UsedCredits", creditsNeededPriced)
				ub.Set("UpdatedAt", now)
				return nil
			},
				tabletheory.IfExists(),
				tabletheory.ConditionExpression(
					"if_not_exists(usedCredits, :zero) + :delta <= if_not_exists(includedCredits, :zero)",
					map[string]any{
						":zero":  int64(0),
						":delta": creditsNeededPriced,
					},
				),
			)
		}
		tx.Put(auditBudget)
		tx.Put(ledger)
		return nil
	})
	if theoryErrors.IsConditionFailed(err) {
		return budget, 0, 0, budgetErrKindExceeded, err
	}
	if err != nil {
		return budget, 0, 0, budgetErrKindInternal, err
	}

	return budget, includedDebited, overageDebited, "", nil
}

func (s *Server) queueMissingRenders(ctx *apptheory.Context, instanceSlug string, now time.Time, missing []missingRenderRequest, out *linkRenderResult) int {
	if s == nil || ctx == nil || out == nil {
		return 0
	}

	queuedCount := 0
	for _, mr := range missing {
		artifact, queued, err := s.queueRender(ctx, mr.NormalizedURL, mr.RetentionClass, 0)
		if err != nil {
			if mr.Index >= 0 && mr.Index < len(out.Links) {
				out.Links[mr.Index].Status = statusError
			}
			continue
		}

		if queued {
			queuedCount++
			audit := &models.AuditLogEntry{
				Actor:     instanceSlug,
				Action:    "render.queue",
				Target:    fmt.Sprintf("render:%s", strings.TrimSpace(artifact.ID)),
				RequestID: strings.TrimSpace(ctx.RequestID),
				CreatedAt: now,
			}
			_ = audit.UpdateKeys()
			_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()
		}

		if mr.Index >= 0 && mr.Index < len(out.Links) {
			r := renderArtifactResponseFromModel(ctx, artifact, !queued)
			out.Links[mr.Index].Render = &r
			out.Links[mr.Index].Status = r.Status
		}
	}

	return queuedCount
}

func shouldRenderLink(renderPolicy string, risk string) bool {
	renderPolicy = strings.ToLower(strings.TrimSpace(renderPolicy))
	risk = strings.ToLower(strings.TrimSpace(risk))

	// Never render invalid/SSRF-blocked links.
	if risk == "invalid" || risk == "blocked" {
		return false
	}

	switch renderPolicy {
	case "always":
		return true
	default: // suspicious
		return risk == "medium" || risk == "high"
	}
}

func retentionClassForRisk(risk string) string {
	risk = strings.ToLower(strings.TrimSpace(risk))
	switch risk {
	case "medium", "high":
		return models.RenderRetentionClassEvidence
	default:
		return models.RenderRetentionClassBenign
	}
}
