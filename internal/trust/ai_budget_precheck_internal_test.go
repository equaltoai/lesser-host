package trust

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestPrecheckAIBudgetBranches(t *testing.T) {
	t.Parallel()

	decision, appErr := (*Server)(nil).precheckAIBudget(context.Background(), testBudgetInstanceSlug, 0, 10000, false)
	require.Nil(t, appErr)
	require.True(t, decision.Allowed)
	require.Equal(t, "no_charge", decision.Reason)

	_, appErr = (&Server{}).precheckAIBudget(context.Background(), testBudgetInstanceSlug, 1, 10000, false)
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)

	db := ttmocks.NewMockExtendedDB()
	qBudget := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	qBudget.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qBudget).Maybe()
	qBudget.On("ConsistentRead").Return(qBudget).Maybe()
	s := &Server{store: store.New(db)}

	qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()
	decision, appErr = s.precheckAIBudget(context.Background(), testBudgetInstanceSlug, 2, 10000, false)
	require.Nil(t, appErr)
	require.False(t, decision.Allowed)
	require.Equal(t, budgetReasonNotConfigured, decision.Reason)

	qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{InstanceSlug: testBudgetInstanceSlug, IncludedCredits: 3, UsedCredits: 2}
	}).Once()
	decision, appErr = s.precheckAIBudget(context.Background(), testBudgetInstanceSlug, 2, 10000, false)
	require.Nil(t, appErr)
	require.False(t, decision.Allowed)
	require.Equal(t, budgetReasonExceeded, decision.Reason)

	qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{InstanceSlug: testBudgetInstanceSlug, IncludedCredits: 3, UsedCredits: 2}
	}).Once()
	decision, appErr = s.precheckAIBudget(context.Background(), testBudgetInstanceSlug, 2, 10000, true)
	require.Nil(t, appErr)
	require.True(t, decision.Allowed)
	require.True(t, decision.OverBudget)
	require.Equal(t, "precheck", decision.Reason)
}
