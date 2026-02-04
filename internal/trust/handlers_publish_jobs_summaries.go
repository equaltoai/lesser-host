package trust

import (
	"fmt"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	linkRenderSummaryPolicyVersion = "v1"
	linkRenderSummaryCreditCost    = int64(2) // credits per cache-miss summary
)

type linkRenderSummaryLinkResult struct {
	URL           string `json:"url"`
	NormalizedURL string `json:"normalized_url,omitempty"`
	Risk          string `json:"risk,omitempty"`

	RenderID string `json:"render_id,omitempty"`

	Status string `json:"status"` // ok|queued|skipped|blocked|invalid|not_checked_budget|error
	Cached bool   `json:"cached"`

	Summary string `json:"summary,omitempty"`
}

type linkRenderSummarySummary struct {
	TotalLinks int `json:"total_links"`

	Candidates int `json:"candidates"`
	Cached     int `json:"cached"`
	Generated  int `json:"generated"`

	Queued           int `json:"queued"`
	Skipped          int `json:"skipped"`
	Blocked          int `json:"blocked"`
	Invalid          int `json:"invalid"`
	NotCheckedBudget int `json:"not_checked_budget"`
	Errors           int `json:"errors"`
}

type linkRenderSummaryResult struct {
	RenderPolicy  string                        `json:"render_policy"`
	PolicyVersion string                        `json:"policy_version"`
	Summary       linkRenderSummarySummary      `json:"summary"`
	Links         []linkRenderSummaryLinkResult `json:"links"`
}

func (s *Server) runLinkRenderSummaryJob(
	ctx *apptheory.Context,
	instanceSlug string,
	jobID string,
	renderPolicy string,
	overagePolicy string,
	pricingMultiplierBps int64,
	canonicalLinks []string,
) publishJobModuleResponse {
	if s == nil || s.store == nil || s.store.DB == nil {
		return publishJobModuleResponse{
			Name:          "link_render_summary",
			PolicyVersion: linkRenderSummaryPolicyVersion,
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
			Name:          "link_render_summary",
			PolicyVersion: linkRenderSummaryPolicyVersion,
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
	if s.artifacts == nil {
		return publishJobModuleResponse{
			Name:          "link_render_summary",
			PolicyVersion: linkRenderSummaryPolicyVersion,
			Status:        "error",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          true,
				OverBudget:       false,
				Reason:           "artifact store not configured",
				RequestedCredits: 0,
				DebitedCredits:   0,
			},
		}
	}

	now := time.Now().UTC()
	month := now.Format("2006-01")

	policy := strings.ToLower(strings.TrimSpace(renderPolicy))
	if policy != "always" && policy != "suspicious" {
		policy = "suspicious"
	}
	allowOverage := strings.ToLower(strings.TrimSpace(overagePolicy)) == "allow"

	out := linkRenderSummaryResult{
		RenderPolicy:  policy,
		PolicyVersion: linkRenderSummaryPolicyVersion,
		Summary: linkRenderSummarySummary{
			TotalLinks: len(canonicalLinks),
		},
		Links: make([]linkRenderSummaryLinkResult, 0, len(canonicalLinks)),
	}

	type missingSummary struct {
		Index    int
		Artifact *models.RenderArtifact
	}
	var missing []missingSummary

	for _, raw := range canonicalLinks {
		raw = strings.TrimSpace(raw)
		analysis := analyzeLinkSafetyBasic(ctx.Context(), nil, raw)
		risk := strings.ToLower(strings.TrimSpace(analysis.Risk))
		normalized := strings.TrimSpace(analysis.NormalizedURL)
		if normalized == "" {
			normalized = raw
		}

		linkOut := linkRenderSummaryLinkResult{
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

		out.Summary.Candidates++

		renderID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, normalized)
		linkOut.RenderID = renderID

		artifact, err := s.store.GetRenderArtifact(ctx.Context(), renderID)
		if theoryErrors.IsNotFound(err) {
			out.Summary.Queued++
			linkOut.Status = "queued"
			out.Links = append(out.Links, linkOut)
			continue
		}
		if err != nil {
			out.Summary.Errors++
			linkOut.Status = "error"
			out.Links = append(out.Links, linkOut)
			continue
		}

		if strings.TrimSpace(artifact.ErrorCode) != "" {
			out.Summary.Errors++
			linkOut.Status = "error"
			out.Links = append(out.Links, linkOut)
			continue
		}

		if strings.TrimSpace(artifact.SnapshotObjectKey) == "" {
			out.Summary.Queued++
			linkOut.Status = "queued"
			out.Links = append(out.Links, linkOut)
			continue
		}

		if strings.TrimSpace(artifact.Summary) != "" && strings.TrimSpace(artifact.SummaryPolicyVersion) == linkRenderSummaryPolicyVersion {
			out.Summary.Cached++
			linkOut.Status = "ok"
			linkOut.Cached = true
			linkOut.Summary = strings.TrimSpace(artifact.Summary)
			out.Links = append(out.Links, linkOut)
			continue
		}

		// Needs generation.
		linkOut.Status = "queued"
		out.Links = append(out.Links, linkOut)
		missing = append(missing, missingSummary{
			Index:    len(out.Links) - 1,
			Artifact: artifact,
		})
	}

	creditsRequested := pricedCredits(int64(out.Summary.Candidates)*linkRenderSummaryCreditCost, pricingMultiplierBps)
	creditsNeeded := pricedCredits(int64(len(missing))*linkRenderSummaryCreditCost, pricingMultiplierBps)

	if len(missing) == 0 {
		return publishJobModuleResponse{
			Name:          "link_render_summary",
			PolicyVersion: linkRenderSummaryPolicyVersion,
			Status:        "ok",
			Cached:        true,
			Budget: budgetDecision{
				Allowed:          true,
				OverBudget:       false,
				Reason:           "cache_hit",
				RequestedCredits: creditsRequested,
				DebitedCredits:   0,
			},
			Result: out,
		}
	}

	// Load budget once; rely on conditional debits in transactions for concurrency safety.
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
		for _, ms := range missing {
			if ms.Index >= 0 && ms.Index < len(out.Links) {
				out.Links[ms.Index].Status = "not_checked_budget"
			}
		}
		out.Summary.NotCheckedBudget = len(missing)
		return publishJobModuleResponse{
			Name:          "link_render_summary",
			PolicyVersion: linkRenderSummaryPolicyVersion,
			Status:        "not_checked_budget",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          false,
				OverBudget:       true,
				Reason:           "budget not configured",
				Month:            month,
				RequestedCredits: creditsNeeded,
				DebitedCredits:   0,
			},
			Result: out,
		}
	}
	if err != nil {
		return publishJobModuleResponse{
			Name:          "link_render_summary",
			PolicyVersion: linkRenderSummaryPolicyVersion,
			Status:        "error",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          true,
				OverBudget:       false,
				Reason:           "internal error",
				Month:            month,
				RequestedCredits: creditsNeeded,
				DebitedCredits:   0,
			},
			Result: out,
		}
	}

	remaining := budget.IncludedCredits - budget.UsedCredits
	if remaining < creditsNeeded && !allowOverage {
		for _, ms := range missing {
			if ms.Index >= 0 && ms.Index < len(out.Links) {
				out.Links[ms.Index].Status = "not_checked_budget"
			}
		}
		out.Summary.NotCheckedBudget = len(missing)
		return publishJobModuleResponse{
			Name:          "link_render_summary",
			PolicyVersion: linkRenderSummaryPolicyVersion,
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
				RequestedCredits: creditsNeeded,
				DebitedCredits:   0,
			},
			Result: out,
		}
	}

	// Generate summaries sequentially (bounded by snapshot fetch + compute).
	usedCredits := budget.UsedCredits
	totalDebited := int64(0)
	totalOverage := int64(0)

	perSummaryCost := pricedCredits(linkRenderSummaryCreditCost, pricingMultiplierBps)

	for _, ms := range missing {
		if ms.Index < 0 || ms.Index >= len(out.Links) || ms.Artifact == nil {
			continue
		}

		if !allowOverage {
			if (budget.IncludedCredits - usedCredits) < perSummaryCost {
				out.Links[ms.Index].Status = "not_checked_budget"
				out.Summary.NotCheckedBudget++
				continue
			}
		}

		snapshotKey := strings.TrimSpace(ms.Artifact.SnapshotObjectKey)
		if snapshotKey == "" {
			out.Links[ms.Index].Status = "queued"
			continue
		}

		body, _, _, err := s.artifacts.GetObject(ctx.Context(), snapshotKey, 512*1024)
		if err != nil {
			out.Links[ms.Index].Status = "error"
			out.Summary.Errors++
			continue
		}

		summary := summarizeSnapshot(string(body), 512)
		if strings.TrimSpace(summary) == "" {
			out.Links[ms.Index].Status = "error"
			out.Summary.Errors++
			continue
		}

		includedDebited, overageDebited := billingPartsForDebit(budget.IncludedCredits, usedCredits, perSummaryCost)
		billingType := billingTypeFromParts(includedDebited, overageDebited)

		updateBudget := &models.InstanceBudgetMonth{
			InstanceSlug: instanceSlug,
			Month:        month,
			UpdatedAt:    now,
		}
		_ = updateBudget.UpdateKeys()

		updateArtifact := &models.RenderArtifact{
			ID:        strings.TrimSpace(ms.Artifact.ID),
			ExpiresAt: ms.Artifact.ExpiresAt,
		}
		_ = updateArtifact.UpdateKeys()

		ledger := &models.UsageLedgerEntry{
			ID:                     usageLedgerEntryID(instanceSlug, month, strings.TrimSpace(ctx.RequestID), "link_render_summary", strings.TrimSpace(ms.Artifact.ID), perSummaryCost),
			InstanceSlug:           instanceSlug,
			Month:                  month,
			Module:                 "link_render_summary",
			Target:                 strings.TrimSpace(ms.Artifact.ID),
			Cached:                 false,
			Reason:                 billingType,
			RequestID:              strings.TrimSpace(ctx.RequestID),
			RequestedCredits:       perSummaryCost,
			DebitedCredits:         perSummaryCost,
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

		auditSummary := &models.AuditLogEntry{
			Actor:     instanceSlug,
			Action:    "render.summary.generate",
			Target:    fmt.Sprintf("render:%s", strings.TrimSpace(ms.Artifact.ID)),
			RequestID: strings.TrimSpace(ctx.RequestID),
			CreatedAt: now,
		}
		_ = auditSummary.UpdateKeys()

		err = s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
			if allowOverage {
				tx.UpdateWithBuilder(updateBudget, func(ub core.UpdateBuilder) error {
					ub.Add("UsedCredits", perSummaryCost)
					ub.Set("UpdatedAt", now)
					return nil
				}, tabletheory.IfExists())
			} else {
				tx.UpdateWithBuilder(updateBudget, func(ub core.UpdateBuilder) error {
					ub.Add("UsedCredits", perSummaryCost)
					ub.Set("UpdatedAt", now)
					return nil
				},
					tabletheory.IfExists(),
					tabletheory.ConditionExpression(
						"if_not_exists(usedCredits, :zero) + :delta <= if_not_exists(includedCredits, :zero)",
						map[string]any{
							":zero":  int64(0),
							":delta": perSummaryCost,
						},
					),
				)
			}

			// Only write the summary if it isn't already present for this policy version.
			tx.UpdateWithBuilder(updateArtifact, func(ub core.UpdateBuilder) error {
				ub.Set("SummaryPolicyVersion", linkRenderSummaryPolicyVersion)
				ub.Set("Summary", summary)
				ub.Set("SummarizedAt", now)
				return nil
			},
				tabletheory.IfExists(),
				tabletheory.ConditionExpression(
					"(attribute_not_exists(summaryPolicyVersion) OR summaryPolicyVersion <> :v) OR (attribute_not_exists(summary) OR summary = :empty)",
					map[string]any{
						":v":     linkRenderSummaryPolicyVersion,
						":empty": "",
					},
				),
			)

			tx.Put(ledger)
			tx.Put(auditBudget)
			tx.Put(auditSummary)
			return nil
		})
		if theoryErrors.IsConditionFailed(err) {
			// Either we raced on budget or summary; re-read artifact and, if present, treat as cache hit.
			if refreshed, err2 := s.store.GetRenderArtifact(ctx.Context(), strings.TrimSpace(ms.Artifact.ID)); err2 == nil && refreshed != nil {
				if strings.TrimSpace(refreshed.Summary) != "" && strings.TrimSpace(refreshed.SummaryPolicyVersion) == linkRenderSummaryPolicyVersion {
					out.Links[ms.Index].Status = "ok"
					out.Links[ms.Index].Cached = true
					out.Links[ms.Index].Summary = strings.TrimSpace(refreshed.Summary)
					out.Summary.Cached++
					continue
				}
			}

			out.Links[ms.Index].Status = "not_checked_budget"
			out.Summary.NotCheckedBudget++
			continue
		}
		if err != nil {
			out.Links[ms.Index].Status = "error"
			out.Summary.Errors++
			continue
		}

		usedCredits += perSummaryCost
		totalDebited += perSummaryCost
		totalOverage += overageDebited

		out.Links[ms.Index].Status = "ok"
		out.Links[ms.Index].Cached = false
		out.Links[ms.Index].Summary = summary
		out.Summary.Generated++
	}

	// Best-effort refreshed budget for reporting.
	var latest models.InstanceBudgetMonth
	_ = s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&latest)

	remaining = latest.IncludedCredits - latest.UsedCredits
	overBudget := remaining < 0 || totalOverage > 0
	reason := "debited"
	if totalOverage > 0 {
		reason = "overage"
	}

	status := "ok"
	if out.Summary.NotCheckedBudget > 0 && out.Summary.Generated == 0 {
		status = "not_checked_budget"
	}

	return publishJobModuleResponse{
		Name:          "link_render_summary",
		PolicyVersion: linkRenderSummaryPolicyVersion,
		Status:        status,
		Cached:        out.Summary.Generated == 0 && out.Summary.Cached > 0,
		Budget: budgetDecision{
			Allowed:          status != "not_checked_budget",
			OverBudget:       overBudget,
			Reason:           reason,
			Month:            month,
			IncludedCredits:  latest.IncludedCredits,
			UsedCredits:      latest.UsedCredits,
			RemainingCredits: remaining,
			RequestedCredits: creditsNeeded,
			DebitedCredits:   totalDebited,
		},
		Result: out,
	}
}

func summarizeSnapshot(in string, max int) string {
	in = strings.ReplaceAll(in, "\r\n", "\n")
	in = strings.ReplaceAll(in, "\r", "\n")

	lines := strings.Split(in, "\n")
	out := make([]string, 0, 12)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
		if len(out) >= 12 {
			break
		}
	}

	joined := strings.Join(out, " ")
	joined = strings.Join(strings.Fields(joined), " ")
	if strings.TrimSpace(joined) == "" {
		return ""
	}

	if max <= 0 {
		max = 512
	}
	if len(joined) > max {
		joined = strings.TrimSpace(joined[:max])
	}
	return joined
}
