package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestStore_GetSoulAgentMintConversationUsesConsistentRead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", ctx).Return(db).Once()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(q).Once()
	q.On("Where", "PK", "=", "SOUL#AGENT#agent-1").Return(q).Once()
	q.On("Where", "SK", "=", "MINT_CONVERSATION#conv-1").Return(q).Once()
	q.On("ConsistentRead").Return(q).Once()
	q.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(theoryErrors.ErrItemNotFound).Once()

	_, err := New(db).GetSoulAgentMintConversation(ctx, " Agent-1 ", " conv-1 ")
	require.ErrorIs(t, err, theoryErrors.ErrItemNotFound)
	db.AssertExpectations(t)
	q.AssertExpectations(t)
}

func TestStore_GetSoulMintConversationIdempotencyUsesConsistentRead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := ttmocks.NewMockExtendedDBStrict()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", ctx).Return(db).Once()
	db.On("Model", mock.AnythingOfType("*models.SoulMintConversationIdempotency")).Return(q).Once()
	q.On("Where", "PK", "=", models.SoulMintConversationIdempotencyPK("inst-1", "reg-1", "idem-1")).Return(q).Once()
	q.On("Where", "SK", "=", "STATE").Return(q).Once()
	q.On("ConsistentRead").Return(q).Once()
	q.On("First", mock.AnythingOfType("*models.SoulMintConversationIdempotency")).Return(theoryErrors.ErrItemNotFound).Once()

	_, err := New(db).GetSoulMintConversationIdempotency(ctx, " inst-1 ", " reg-1 ", " idem-1 ")
	require.ErrorIs(t, err, theoryErrors.ErrItemNotFound)
	db.AssertExpectations(t)
	q.AssertExpectations(t)
}
