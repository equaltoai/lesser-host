package store

import (
	"context"
	"testing"

	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// TestStore_SoulMintConversationGSI4_TerminalWritesMaintainKeys pins the gsi4
// key maintenance on the two store-layer conversation UpdateWithBuilder sites
// (issue #1067 part C2): FailHostedGenesisSessionAndConversation and
// PublishHostedGenesisSessionAndConversation. Each transaction's conversation
// write must carry the gsi4 keys derived from the stored CreatedAt, so a
// healed/backfilled item can never silently drop out of the index.
func TestStore_SoulMintConversationGSI4_TerminalWritesMaintainKeys(t *testing.T) {
	t.Parallel()

	t.Run("fail terminalizes with gsi4 keys maintained", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		db := ttmocks.NewMockExtendedDBStrict()
		tx := new(ttmocks.MockTransactionBuilder)
		db.TransactWriteBuilder = tx
		capture := &captureHostedGenesisUpdateBuilder{}
		tx.UpdateBuilder = capture
		db.On("TransactWrite", ctx, mock.Anything).Return(nil).Once()
		tx.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tx).Once()
		tx.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tx).Once()
		tx.On("UpdateWithBuilder", mock.MatchedBy(matchesFailedHostedGenesisIdempotency), mock.Anything,
			mock.MatchedBy(hasFailedHostedGenesisIdempotencyConditions)).Return(tx).Once()
		tx.On("Execute").Return(nil).Once()

		session, conversation := validStoreHostedGenesisFailure()
		st := New(db)
		require.NoError(t, st.FailHostedGenesisSessionAndConversation(ctx, session, 7, hostedgenesis.StatusInProgress, conversation))

		require.Equal(t, models.SoulMintConversationGSI4PK(session.AgentID), capture.sets["GSI4PK"], "failure write must maintain gsi4PK")
		require.Equal(t, models.SoulMintConversationGSI4SK(conversation.CreatedAt, conversation.ConversationID), capture.sets["GSI4SK"], "failure write must maintain gsi4SK")
	})

	t.Run("publish maintains gsi4 keys", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		db := ttmocks.NewMockExtendedDBStrict()
		tx := new(ttmocks.MockTransactionBuilder)
		db.TransactWriteBuilder = tx
		capture := &captureHostedGenesisUpdateBuilder{}
		tx.UpdateBuilder = capture
		db.On("TransactWrite", ctx, mock.Anything).Return(nil).Once()
		tx.On("UpdateWithBuilder", mock.AnythingOfType("*models.HostedGenesisSession"), mock.Anything, mock.Anything).Return(tx).Once()
		tx.On("UpdateWithBuilder", mock.AnythingOfType("*models.SoulAgentMintConversation"), mock.Anything, mock.Anything).Return(tx).Once()
		tx.On("Execute").Return(nil).Once()

		session, conversation := validStoreHostedGenesisPublication()
		st := New(db)
		require.NoError(t, st.PublishHostedGenesisSessionAndConversation(ctx, session, 7, hostedgenesis.StatusDeclarationReady, conversation))

		require.Equal(t, models.SoulMintConversationGSI4PK(session.AgentID), capture.sets["GSI4PK"], "publication write must maintain gsi4PK")
		require.Equal(t, models.SoulMintConversationGSI4SK(conversation.CreatedAt, conversation.ConversationID), capture.sets["GSI4SK"], "publication write must maintain gsi4SK")
	})
}
