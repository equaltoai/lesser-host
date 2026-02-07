package trust

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/billing"
	"github.com/equaltoai/lesser-host/internal/httpx"
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

type budgetDebitPrepared struct {
	InstanceSlug string
	RequestID    string
	Month        string
	Credits      int64
	AllowOverage bool
	PK           string
	SK           string
}

func (s *Server) handleBudgetDebit(ctx *apptheory.Context) (*apptheory.Response, error) {
	prepared, err := s.prepareBudgetDebit(ctx)
	if err != nil {
		return nil, err
	}

	budget, ok, err := s.loadInstanceBudgetMonth(ctx.Context(), prepared.PK, prepared.SK)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !ok {
		return apptheory.JSON(http.StatusOK, budgetDebitNotConfiguredResponse(prepared))
	}

	remaining := budget.IncludedCredits - budget.UsedCredits
	if remaining < prepared.Credits {
		if !prepared.AllowOverage {
			return apptheory.JSON(http.StatusOK, budgetDebitExceededResponse(prepared, budget, remaining))
		}
	}

	now := time.Now().UTC()

	includedDebited, overageDebited := billing.PartsForDebit(budget.IncludedCredits, budget.UsedCredits, prepared.Credits)
	billingType := billing.TypeFromParts(includedDebited, overageDebited)

	update := &models.InstanceBudgetMonth{
		InstanceSlug: prepared.InstanceSlug,
		Month:        prepared.Month,
		UpdatedAt:    now,
	}
	if updateErr := update.UpdateKeys(); updateErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	ledger := buildBudgetDebitLedgerEntry(prepared, now, billingType, includedDebited, overageDebited)
	audit := buildBudgetDebitAuditEntry(prepared, now)

	err = s.transactBudgetDebit(ctx.Context(), update, prepared.AllowOverage, prepared.Credits, now, ledger, audit)
	if theoryErrors.IsConditionFailed(err) {
		latest, _, _ := s.loadInstanceBudgetMonth(ctx.Context(), prepared.PK, prepared.SK)
		remaining = latest.IncludedCredits - latest.UsedCredits
		return apptheory.JSON(http.StatusOK, budgetDebitExceededResponse(prepared, latest, remaining))
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	latest, ok, err := s.loadInstanceBudgetMonth(ctx.Context(), prepared.PK, prepared.SK)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !ok {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	remaining = latest.IncludedCredits - latest.UsedCredits
	overBudget := remaining < 0
	if overageDebited > 0 {
		overBudget = true
	}

	reason := budgetReasonDebited
	if overageDebited > 0 {
		reason = budgetReasonOverage
	}

	return apptheory.JSON(http.StatusOK, budgetDebitResponse{
		Allowed:          true,
		OverBudget:       overBudget,
		Reason:           reason,
		InstanceSlug:     prepared.InstanceSlug,
		Month:            prepared.Month,
		IncludedCredits:  latest.IncludedCredits,
		UsedCredits:      latest.UsedCredits,
		RemainingCredits: remaining,
		RequestedCredits: prepared.Credits,
		DebitedCredits:   prepared.Credits,
	})
}

func (s *Server) prepareBudgetDebit(ctx *apptheory.Context) (budgetDebitPrepared, error) {
	if ctx == nil {
		return budgetDebitPrepared{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s == nil {
		return budgetDebitPrepared{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.store == nil {
		return budgetDebitPrepared{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.store.DB == nil {
		return budgetDebitPrepared{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return budgetDebitPrepared{}, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	instCfg := s.loadInstanceTrustConfig(ctx.Context(), instanceSlug)
	allowOverage := strings.ToLower(strings.TrimSpace(instCfg.OveragePolicy)) == overagePolicyAllow

	var req budgetDebitRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return budgetDebitPrepared{}, err
	}
	if req.Credits <= 0 {
		return budgetDebitPrepared{}, &apptheory.AppError{Code: "app.bad_request", Message: "credits must be > 0"}
	}

	month, err := normalizeBudgetMonth(req.Month, time.Now().UTC())
	if err != nil {
		return budgetDebitPrepared{}, &apptheory.AppError{Code: "app.bad_request", Message: "month must be YYYY-MM"}
	}

	return budgetDebitPrepared{
		InstanceSlug: instanceSlug,
		RequestID:    strings.TrimSpace(ctx.RequestID),
		Month:        month,
		Credits:      req.Credits,
		AllowOverage: allowOverage,
		PK:           fmt.Sprintf("INSTANCE#%s", instanceSlug),
		SK:           fmt.Sprintf("BUDGET#%s", month),
	}, nil
}

func normalizeBudgetMonth(raw string, now time.Time) (string, error) {
	month := strings.TrimSpace(raw)
	if month == "" {
		return now.Format("2006-01"), nil
	}
	_, err := time.Parse("2006-01", month)
	if err != nil {
		return "", err
	}
	return month, nil
}

func (s *Server) loadInstanceBudgetMonth(ctx context.Context, pk, sk string) (models.InstanceBudgetMonth, bool, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return models.InstanceBudgetMonth{}, false, fmt.Errorf("store not initialized")
	}

	var budget models.InstanceBudgetMonth
	err := s.store.DB.WithContext(ctx).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&budget)
	if theoryErrors.IsNotFound(err) {
		return models.InstanceBudgetMonth{}, false, nil
	}
	if err != nil {
		return models.InstanceBudgetMonth{}, false, err
	}
	return budget, true, nil
}

func (s *Server) transactBudgetDebit(
	ctx context.Context,
	update *models.InstanceBudgetMonth,
	allowOverage bool,
	credits int64,
	now time.Time,
	ledger *models.UsageLedgerEntry,
	audit *models.AuditLogEntry,
) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not initialized")
	}

	return s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		conditions := []core.TransactCondition{tabletheory.IfExists()}
		if !allowOverage {
			conditions = append(conditions,
				tabletheory.ConditionExpression(
					"if_not_exists(usedCredits, :zero) + :delta <= if_not_exists(includedCredits, :zero)",
					map[string]any{
						":zero":  int64(0),
						":delta": credits,
					},
				),
			)
		}

		tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
			ub.Add("UsedCredits", credits)
			ub.Set("UpdatedAt", now)
			return nil
		}, conditions...)
		tx.Put(ledger)
		tx.Put(audit)
		return nil
	})
}

func buildBudgetDebitLedgerEntry(prepared budgetDebitPrepared, now time.Time, billingType string, includedDebited, overageDebited int64) *models.UsageLedgerEntry {
	ledger := &models.UsageLedgerEntry{
		ID:                     billing.UsageLedgerEntryID(prepared.InstanceSlug, prepared.Month, prepared.RequestID, "budget.debit", prepared.Month, prepared.Credits),
		InstanceSlug:           prepared.InstanceSlug,
		Month:                  prepared.Month,
		Module:                 "budget.debit",
		Target:                 prepared.Month,
		Cached:                 false,
		Reason:                 billingType,
		RequestID:              prepared.RequestID,
		RequestedCredits:       prepared.Credits,
		ListCredits:            prepared.Credits,
		PricingMultiplierBps:   10000,
		DebitedCredits:         prepared.Credits,
		IncludedDebitedCredits: includedDebited,
		OverageDebitedCredits:  overageDebited,
		BillingType:            billingType,
		CreatedAt:              now,
	}
	_ = ledger.UpdateKeys()
	return ledger
}

func buildBudgetDebitAuditEntry(prepared budgetDebitPrepared, now time.Time) *models.AuditLogEntry {
	audit := &models.AuditLogEntry{
		Actor:     prepared.InstanceSlug,
		Action:    "budget.debit",
		Target:    fmt.Sprintf("instance_budget:%s:%s", prepared.InstanceSlug, prepared.Month),
		RequestID: prepared.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	return audit
}

func budgetDebitNotConfiguredResponse(prepared budgetDebitPrepared) budgetDebitResponse {
	return budgetDebitResponse{
		Allowed:          false,
		OverBudget:       true,
		Reason:           "budget not configured",
		InstanceSlug:     prepared.InstanceSlug,
		Month:            prepared.Month,
		IncludedCredits:  0,
		UsedCredits:      0,
		RemainingCredits: 0,
		RequestedCredits: prepared.Credits,
		DebitedCredits:   0,
	}
}

func budgetDebitExceededResponse(prepared budgetDebitPrepared, budget models.InstanceBudgetMonth, remaining int64) budgetDebitResponse {
	return budgetDebitResponse{
		Allowed:          false,
		OverBudget:       true,
		Reason:           "budget exceeded",
		InstanceSlug:     prepared.InstanceSlug,
		Month:            prepared.Month,
		IncludedCredits:  budget.IncludedCredits,
		UsedCredits:      budget.UsedCredits,
		RemainingCredits: remaining,
		RequestedCredits: prepared.Credits,
		DebitedCredits:   0,
	}
}
