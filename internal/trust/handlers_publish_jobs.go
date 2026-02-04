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

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	maxPublishLinks = 50
)

type publishJobModuleRequest struct {
	Name    string         `json:"name"`
	Options map[string]any `json:"options,omitempty"`
}

type publishJobRequest struct {
	ActorURI    string                    `json:"actor_uri,omitempty"`
	ObjectURI   string                    `json:"object_uri,omitempty"`
	ContentHash string                    `json:"content_hash,omitempty"`
	Links       []string                  `json:"links,omitempty"`
	Modules     []publishJobModuleRequest `json:"modules,omitempty"`
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
	Name          string         `json:"name"`
	PolicyVersion string         `json:"policy_version"`
	Status        string         `json:"status"` // ok | not_checked_budget | unsupported | error
	Cached        bool           `json:"cached"`
	Budget        budgetDecision `json:"budget"`
	Result        any            `json:"result,omitempty"`
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

	// Per-instance render policy controls auto-escalation for render-based modules.
	renderPolicy := "suspicious"
	{
		var inst models.Instance
		err := s.store.DB.WithContext(ctx.Context()).
			Model(&models.Instance{}).
			Where("PK", "=", "INSTANCE#"+instanceSlug).
			Where("SK", "=", models.SKMetadata).
			First(&inst)
		if err == nil {
			renderPolicy = strings.ToLower(strings.TrimSpace(inst.RenderPolicy))
		}
		if renderPolicy != "always" && renderPolicy != "suspicious" {
			renderPolicy = "suspicious"
		}
	}

	var req publishJobRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	actorURI := strings.TrimSpace(req.ActorURI)
	objectURI := strings.TrimSpace(req.ObjectURI)
	contentHash := strings.TrimSpace(req.ContentHash)

	// Default modules when omitted.
	modules := req.Modules
	if len(modules) == 0 {
		// Auto-escalate to render for suspicious links by default (budgeted).
		modules = []publishJobModuleRequest{
			{Name: "link_safety_basic"},
			{Name: "link_safety_render"},
		}
	}

	// Canonicalize deterministically (no network) and cap links to avoid oversized items.
	canonical := make([]string, 0, len(req.Links))
	seen := map[string]struct{}{}
	for _, raw := range req.Links {
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
		switch name {
		case "link_safety_basic":
			moduleResp := s.runLinkSafetyBasicJob(ctx, instanceSlug, jobID, actorURI, objectURI, contentHash, linksHash, canonical)
			out.Modules = append(out.Modules, moduleResp)
		case "link_safety_render":
			moduleResp := s.runLinkSafetyRenderJob(ctx, instanceSlug, jobID, renderPolicy, canonical)
			out.Modules = append(out.Modules, moduleResp)
		case "link_preview_render":
			moduleResp := s.runLinkPreviewRenderJob(ctx, instanceSlug, jobID, renderPolicy, canonical)
			out.Modules = append(out.Modules, moduleResp)
		default:
			out.Modules = append(out.Modules, publishJobModuleResponse{
				Name:          name,
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
			})
		}
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

func (s *Server) runLinkSafetyBasicJob(
	ctx *apptheory.Context,
	instanceSlug string,
	jobID string,
	actorURI string,
	objectURI string,
	contentHash string,
	linksHash string,
	canonicalLinks []string,
) publishJobModuleResponse {
	now := time.Now().UTC()

	// Cache hit path (no charge).
	if cached, err := s.store.GetLinkSafetyBasicResult(ctx.Context(), jobID); err == nil {
		return publishJobModuleResponse{
			Name:          "link_safety_basic",
			PolicyVersion: linkSafetyBasicPolicyVersion,
			Status:        "ok",
			Cached:        true,
			Budget: budgetDecision{
				Allowed:          true,
				OverBudget:       false,
				Reason:           "cache_hit",
				RequestedCredits: int64(len(canonicalLinks)),
				DebitedCredits:   0,
			},
			Result: cached,
		}
	}

	links := make([]models.LinkSafetyBasicLinkResult, 0, len(canonicalLinks))
	for _, raw := range canonicalLinks {
		links = append(links, analyzeLinkSafetyBasic(ctx.Context(), nil, raw))
	}

	credits := int64(len(canonicalLinks))
	month := now.Format("2006-01")

	// No links: store a deterministic empty result without debiting.
	if credits == 0 {
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
					Result: cached,
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
			Result: item,
		}
	}

	// Budget pre-check to avoid ambiguous transaction condition failures.
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
		return publishJobModuleResponse{
			Name:          "link_safety_basic",
			PolicyVersion: linkSafetyBasicPolicyVersion,
			Status:        "not_checked_budget",
			Cached:        false,
			Budget: budgetDecision{
				Allowed:          false,
				OverBudget:       true,
				Reason:           "budget not configured",
				Month:            month,
				RequestedCredits: credits,
				DebitedCredits:   0,
			},
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
				RequestedCredits: credits,
				DebitedCredits:   0,
			},
		}
	}

	remaining := budget.IncludedCredits - budget.UsedCredits
	if remaining < credits {
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
				IncludedCredits:  budget.IncludedCredits,
				UsedCredits:      budget.UsedCredits,
				RemainingCredits: remaining,
				RequestedCredits: credits,
				DebitedCredits:   0,
			},
		}
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

	err = s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
			ub.Add("UsedCredits", credits)
			ub.Set("UpdatedAt", now)
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.ConditionExpression(
				"if_not_exists(usedCredits, :zero) + :delta <= if_not_exists(includedCredits, :zero)",
				map[string]any{
					":zero":  int64(0),
					":delta": credits,
				},
			),
		)
		tx.Put(auditBudget)
		tx.Create(item)
		tx.Put(auditScan)
		return nil
	})
	if theoryErrors.IsConditionFailed(err) {
		// If the result already exists, treat it as cache hit (no debit happened due to txn rollback).
		if cached, err2 := s.store.GetLinkSafetyBasicResult(ctx.Context(), jobID); err2 == nil {
			return publishJobModuleResponse{
				Name:          "link_safety_basic",
				PolicyVersion: linkSafetyBasicPolicyVersion,
				Status:        "ok",
				Cached:        true,
				Budget: budgetDecision{
					Allowed:          true,
					OverBudget:       false,
					Reason:           "cache_hit",
					RequestedCredits: credits,
					DebitedCredits:   0,
				},
				Result: cached,
			}
		}

		// Otherwise, assume we raced with another debit and are now over budget.
		var latest models.InstanceBudgetMonth
		if err3 := s.store.DB.WithContext(ctx.Context()).
			Model(&models.InstanceBudgetMonth{}).
			Where("PK", "=", pk).
			Where("SK", "=", sk).
			ConsistentRead().
			First(&latest); err3 == nil {
			remaining = latest.IncludedCredits - latest.UsedCredits
			reason := "budget exceeded"
			if remaining >= credits {
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
					RequestedCredits: credits,
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
				RequestedCredits: credits,
				DebitedCredits:   0,
			},
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
				RequestedCredits: credits,
				DebitedCredits:   0,
			},
		}
	}

	// Re-read budget for accurate reporting.
	var latest models.InstanceBudgetMonth
	_ = s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&latest)

	remaining = latest.IncludedCredits - latest.UsedCredits
	return publishJobModuleResponse{
		Name:          "link_safety_basic",
		PolicyVersion: linkSafetyBasicPolicyVersion,
		Status:        "ok",
		Cached:        false,
		Budget: budgetDecision{
			Allowed:          true,
			OverBudget:       false,
			Month:            month,
			IncludedCredits:  latest.IncludedCredits,
			UsedCredits:      latest.UsedCredits,
			RemainingCredits: remaining,
			RequestedCredits: credits,
			DebitedCredits:   credits,
		},
		Result: item,
	}
}
