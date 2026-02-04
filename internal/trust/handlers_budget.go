package trust

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type budgetDebitRequest struct {
	Credits int64  `json:"credits"`
	Month   string `json:"month,omitempty"` // YYYY-MM
}

type budgetDebitResponse struct {
	Allowed    bool   `json:"allowed"`
	OverBudget bool   `json:"over_budget"`
	Reason     string `json:"reason,omitempty"`

	InstanceSlug string `json:"instance_slug"`
	Month        string `json:"month"`

	IncludedCredits  int64 `json:"included_credits"`
	UsedCredits      int64 `json:"used_credits"`
	RemainingCredits int64 `json:"remaining_credits"`

	RequestedCredits int64 `json:"requested_credits"`
	DebitedCredits   int64 `json:"debited_credits"`
}

func (s *Server) handleBudgetDebit(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	allowOverage := strings.ToLower(strings.TrimSpace(instCfg.OveragePolicy)) == "allow"

	var req budgetDebitRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}
	if req.Credits <= 0 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "credits must be > 0"}
	}

	month := strings.TrimSpace(req.Month)
	if month == "" {
		month = time.Now().UTC().Format("2006-01")
	} else if _, err := time.Parse("2006-01", month); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month must be YYYY-MM"}
	}

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
		return apptheory.JSON(http.StatusOK, budgetDebitResponse{
			Allowed:          false,
			OverBudget:       true,
			Reason:           "budget not configured",
			InstanceSlug:     instanceSlug,
			Month:            month,
			IncludedCredits:  0,
			UsedCredits:      0,
			RemainingCredits: 0,
			RequestedCredits: req.Credits,
			DebitedCredits:   0,
		})
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	remaining := budget.IncludedCredits - budget.UsedCredits
	if remaining < req.Credits && !allowOverage {
		return apptheory.JSON(http.StatusOK, budgetDebitResponse{
			Allowed:          false,
			OverBudget:       true,
			Reason:           "budget exceeded",
			InstanceSlug:     instanceSlug,
			Month:            month,
			IncludedCredits:  budget.IncludedCredits,
			UsedCredits:      budget.UsedCredits,
			RemainingCredits: remaining,
			RequestedCredits: req.Credits,
			DebitedCredits:   0,
		})
	}

	now := time.Now().UTC()

	update := &models.InstanceBudgetMonth{
		InstanceSlug: instanceSlug,
		Month:        month,
		UpdatedAt:    now,
	}
	if err := update.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	includedDebited, overageDebited := billingPartsForDebit(budget.IncludedCredits, budget.UsedCredits, req.Credits)
	billingType := billingTypeFromParts(includedDebited, overageDebited)

	ledger := &models.UsageLedgerEntry{
		ID:                     usageLedgerEntryID(instanceSlug, month, strings.TrimSpace(ctx.RequestID), "budget.debit", month, req.Credits),
		InstanceSlug:           instanceSlug,
		Month:                  month,
		Module:                 "budget.debit",
		Target:                 month,
		Cached:                 false,
		Reason:                 billingType,
		RequestID:              strings.TrimSpace(ctx.RequestID),
		RequestedCredits:       req.Credits,
		DebitedCredits:         req.Credits,
		IncludedDebitedCredits: includedDebited,
		OverageDebitedCredits:  overageDebited,
		BillingType:            billingType,
		CreatedAt:              now,
	}
	_ = ledger.UpdateKeys()

	audit := &models.AuditLogEntry{
		Actor:     instanceSlug,
		Action:    "budget.debit",
		Target:    fmt.Sprintf("instance_budget:%s:%s", instanceSlug, month),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	err = s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		// Atomic debit guarded by included/used condition.
		if allowOverage {
			tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
				ub.Add("UsedCredits", req.Credits)
				ub.Set("UpdatedAt", now)
				return nil
			}, tabletheory.IfExists())
		} else {
			tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
				ub.Add("UsedCredits", req.Credits)
				ub.Set("UpdatedAt", now)
				return nil
			},
				tabletheory.IfExists(),
				tabletheory.ConditionExpression(
					"if_not_exists(usedCredits, :zero) + :delta <= if_not_exists(includedCredits, :zero)",
					map[string]any{
						":zero":  int64(0),
						":delta": req.Credits,
					},
				),
			)
		}
		tx.Put(ledger)
		tx.Put(audit)
		return nil
	})
	if theoryErrors.IsConditionFailed(err) {
		// Re-read to report current state.
		var latest models.InstanceBudgetMonth
		_ = s.store.DB.WithContext(ctx.Context()).
			Model(&models.InstanceBudgetMonth{}).
			Where("PK", "=", pk).
			Where("SK", "=", sk).
			ConsistentRead().
			First(&latest)

		remaining = latest.IncludedCredits - latest.UsedCredits
		return apptheory.JSON(http.StatusOK, budgetDebitResponse{
			Allowed:          false,
			OverBudget:       true,
			Reason:           "budget exceeded",
			InstanceSlug:     instanceSlug,
			Month:            month,
			IncludedCredits:  latest.IncludedCredits,
			UsedCredits:      latest.UsedCredits,
			RemainingCredits: remaining,
			RequestedCredits: req.Credits,
			DebitedCredits:   0,
		})
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	// Return updated budget (consistent read).
	var latest models.InstanceBudgetMonth
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&latest)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	remaining = latest.IncludedCredits - latest.UsedCredits
	overBudget := remaining < 0 || overageDebited > 0
	reason := "debited"
	if overageDebited > 0 {
		reason = "overage"
	}
	return apptheory.JSON(http.StatusOK, budgetDebitResponse{
		Allowed:          true,
		OverBudget:       overBudget,
		Reason:           reason,
		InstanceSlug:     instanceSlug,
		Month:            month,
		IncludedCredits:  latest.IncludedCredits,
		UsedCredits:      latest.UsedCredits,
		RemainingCredits: remaining,
		RequestedCredits: req.Credits,
		DebitedCredits:   req.Credits,
	})
}
