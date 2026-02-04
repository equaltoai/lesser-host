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
	if s == nil || s.store == nil || s.store.DB == nil {
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
	if ctx == nil {
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

	policy := strings.ToLower(strings.TrimSpace(renderPolicy))
	if policy != "always" && policy != "suspicious" {
		policy = "suspicious"
	}

	out := linkRenderResult{
		RenderPolicy: policy,
		Summary: linkRenderSummary{
			TotalLinks: len(canonicalLinks),
		},
		Links: make([]linkRenderLinkResult, 0, len(canonicalLinks)),
	}

	var missing []missingRenderRequest
	candidateCount := 0
	queuedCount := 0
	cachedCount := 0

	for _, raw := range canonicalLinks {
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
		case "invalid":
			out.Summary.Invalid++
			linkOut.Status = "invalid"
			out.Links = append(out.Links, linkOut)
			continue
		case "blocked":
			out.Summary.Blocked++
			linkOut.Status = "blocked"
			out.Links = append(out.Links, linkOut)
			continue
		}

		if !shouldRenderLink(policy, risk) {
			out.Summary.Skipped++
			linkOut.Status = "skipped"
			out.Links = append(out.Links, linkOut)
			continue
		}

		candidateCount++

		retentionClass := retentionClassForRisk(risk)
		renderID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, normalized)

		artifact, err := s.store.GetRenderArtifact(ctx.Context(), renderID)
		if err == nil && artifact != nil {
			cachedCount++
			// Best-effort retention upgrade (no new render work).
			if retentionClass == models.RenderRetentionClassEvidence {
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
				if updated {
					artifact.RequestID = strings.TrimSpace(ctx.RequestID)
					artifact.RequestedBy = strings.TrimSpace(instanceSlug)
					_ = artifact.UpdateKeys()
					_ = s.store.PutRenderArtifact(ctx.Context(), artifact)
				}
			}

			r := renderArtifactResponseFromModel(ctx, artifact, true)
			linkOut.Render = &r
			linkOut.Status = r.Status
			out.Links = append(out.Links, linkOut)
			continue
		}
		if err != nil && !theoryErrors.IsNotFound(err) {
			linkOut.Status = "error"
			out.Links = append(out.Links, linkOut)
			continue
		}

		linkOut.Status = "queued"
		out.Links = append(out.Links, linkOut)
		missing = append(missing, missingRenderRequest{
			Index:          len(out.Links) - 1,
			NormalizedURL:  normalized,
			RetentionClass: retentionClass,
		})
	}

	out.Summary.Candidates = candidateCount
	out.Summary.Cached = cachedCount

	creditsRequested := int64(candidateCount) * linkRenderCreditCost
	creditsNeeded := int64(len(missing)) * linkRenderCreditCost

	creditsRequestedPriced := billing.PricedCredits(creditsRequested, pricingMultiplierBps)
	creditsNeededPriced := billing.PricedCredits(creditsNeeded, pricingMultiplierBps)

	if len(missing) == 0 {
		out.Summary.Queued = 0
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
		for _, mr := range missing {
			if mr.Index >= 0 && mr.Index < len(out.Links) {
				out.Links[mr.Index].Status = "error"
			}
		}
		out.Summary.Queued = 0
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

	// Budget check (charge only on cache miss renders).
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
		for _, mr := range missing {
			if mr.Index >= 0 && mr.Index < len(out.Links) {
				out.Links[mr.Index].Status = "not_checked_budget"
			}
		}
		out.Summary.Queued = 0
		return publishJobModuleResponse{
			Name:          moduleName,
			PolicyVersion: rendering.RenderPolicyVersion,
			Status:        "not_checked_budget",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          false,
				OverBudget:       true,
				Reason:           "budget not configured",
				Month:            month,
				RequestedCredits: creditsRequestedPriced,
				DebitedCredits:   0,
			},
			Result: out,
		}
	}
	if err != nil {
		for _, mr := range missing {
			if mr.Index >= 0 && mr.Index < len(out.Links) {
				out.Links[mr.Index].Status = "error"
			}
		}
		out.Summary.Queued = 0
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

	remaining := budget.IncludedCredits - budget.UsedCredits
	if remaining < creditsNeededPriced && strings.ToLower(strings.TrimSpace(overagePolicy)) != "allow" {
		for _, mr := range missing {
			if mr.Index >= 0 && mr.Index < len(out.Links) {
				out.Links[mr.Index].Status = "not_checked_budget"
			}
		}
		out.Summary.Queued = 0
		return publishJobModuleResponse{
			Name:          moduleName,
			PolicyVersion: rendering.RenderPolicyVersion,
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
				RequestedCredits: creditsRequestedPriced,
				DebitedCredits:   0,
			},
			Result: out,
		}
	}

	update := &models.InstanceBudgetMonth{
		InstanceSlug: instanceSlug,
		Month:        month,
		UpdatedAt:    now,
	}
	_ = update.UpdateKeys()

	includedDebited, overageDebited := billing.BillingPartsForDebit(budget.IncludedCredits, budget.UsedCredits, creditsNeededPriced)
	billingType := billing.BillingTypeFromParts(includedDebited, overageDebited)

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
		if strings.ToLower(strings.TrimSpace(overagePolicy)) == "allow" {
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
		for _, mr := range missing {
			if mr.Index >= 0 && mr.Index < len(out.Links) {
				out.Links[mr.Index].Status = "not_checked_budget"
			}
		}
		out.Summary.Queued = 0
		return publishJobModuleResponse{
			Name:          moduleName,
			PolicyVersion: rendering.RenderPolicyVersion,
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
				RequestedCredits: creditsRequestedPriced,
				DebitedCredits:   0,
			},
			Result: out,
		}
	}
	if err != nil {
		for _, mr := range missing {
			if mr.Index >= 0 && mr.Index < len(out.Links) {
				out.Links[mr.Index].Status = "error"
			}
		}
		out.Summary.Queued = 0
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

	for _, mr := range missing {
		artifact, queued, err := s.queueRender(ctx, mr.NormalizedURL, mr.RetentionClass, 0)
		if err != nil {
			if mr.Index >= 0 && mr.Index < len(out.Links) {
				out.Links[mr.Index].Status = "error"
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

	out.Summary.Queued = queuedCount

	// Best-effort current remaining for reporting (may be stale if overage allowed).
	remainingAfter := budget.IncludedCredits - (budget.UsedCredits + creditsNeededPriced)
	overBudget := remainingAfter < 0 || overageDebited > 0
	reason := "debited"
	if overageDebited > 0 {
		reason = "overage"
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
