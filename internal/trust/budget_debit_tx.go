package trust

import (
	"time"

	"github.com/theory-cloud/tabletheory/v2"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func addBudgetDebitUpdate(
	tx core.TransactionBuilder,
	update *models.InstanceBudgetMonth,
	credits int64,
	now time.Time,
	allowOverage bool,
	expectedIncludedCredits int64,
	maxUsed int64,
) {
	if tx == nil || update == nil {
		return
	}
	tx.UpdateWithBuilder(
		update,
		func(ub core.UpdateBuilder) error {
			ub.Add("UsedCredits", credits)
			ub.Set("UpdatedAt", now)
			return nil
		},
		budgetDebitConditions(allowOverage, expectedIncludedCredits, maxUsed)...,
	)
}

func budgetDebitConditions(allowOverage bool, expectedIncludedCredits int64, maxUsed int64) []core.TransactCondition {
	conditions := []core.TransactCondition{
		tabletheory.IfExists(),
		tabletheory.Condition("IncludedCredits", "=", expectedIncludedCredits),
	}
	if allowOverage {
		return conditions
	}
	return append(conditions,
		tabletheory.ConditionExpression(
			"attribute_not_exists(usedCredits) OR usedCredits <= :max",
			map[string]any{
				":max": maxUsed,
			},
		),
	)
}
