package trust

import (
	"context"
	"fmt"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/billing"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) precheckAIBudget(ctx context.Context, instanceSlug string, baseCredits int64, pricingMultiplierBps int64, allowOverage bool) (*ai.BudgetDecision, *apptheory.AppTheoryError) {
	instanceSlug = strings.TrimSpace(instanceSlug)
	creditsRequested := billing.PricedCredits(baseCredits, pricingMultiplierBps)
	month := time.Now().UTC().Format("2006-01")
	if creditsRequested <= 0 {
		return &ai.BudgetDecision{
			Allowed:          true,
			OverBudget:       false,
			Reason:           "no_charge",
			Month:            month,
			RequestedCredits: creditsRequested,
			DebitedCredits:   0,
		}, nil
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	var budget models.InstanceBudgetMonth
	err := s.store.DB.WithContext(ctx).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE#%s", instanceSlug)).
		Where("SK", "=", fmt.Sprintf("BUDGET#%s", month)).
		ConsistentRead().
		First(&budget)
	if theoryErrors.IsNotFound(err) {
		return &ai.BudgetDecision{
			Allowed:          false,
			OverBudget:       true,
			Reason:           budgetReasonNotConfigured,
			Month:            month,
			RequestedCredits: creditsRequested,
			DebitedCredits:   0,
		}, nil
	}
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	remaining := budget.IncludedCredits - budget.UsedCredits
	if remaining < creditsRequested && !allowOverage {
		return &ai.BudgetDecision{
			Allowed:          false,
			OverBudget:       true,
			Reason:           budgetReasonExceeded,
			Month:            month,
			IncludedCredits:  budget.IncludedCredits,
			UsedCredits:      budget.UsedCredits,
			RemainingCredits: remaining,
			RequestedCredits: creditsRequested,
			DebitedCredits:   0,
		}, nil
	}
	return &ai.BudgetDecision{
		Allowed:          true,
		OverBudget:       remaining < creditsRequested,
		Reason:           "precheck",
		Month:            month,
		IncludedCredits:  budget.IncludedCredits,
		UsedCredits:      budget.UsedCredits,
		RemainingCredits: remaining,
		RequestedCredits: creditsRequested,
		DebitedCredits:   0,
	}, nil
}
