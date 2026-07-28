package store

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/require"
	"github.com/theory-cloud/tabletheory/v2"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	"github.com/theory-cloud/tabletheory/v2/pkg/session"
	"github.com/theory-cloud/tabletheory/v2/pkg/testing/fakedb"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestStore_RepairHostedGenesisMalformedFailureIsGuardedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	st, fake, db := newMalformedFailureFakeStore(t)
	item := validStoreHostedGenesisSession()
	item.Status = string(hostedgenesis.StatusAssistantTurnReady)
	require.NoError(t, db.Model(item).Create())
	seedHostedGenesisMalformedFailure(t, fake)

	_, err := st.GetHostedGenesisSession(ctx, item.InstanceSlug, item.ConversationID)
	require.ErrorContains(t, err, "cannot convert bool to hostedgenesis.Failure")

	action, err := st.RepairHostedGenesisMalformedFailure(ctx, item.InstanceSlug, item.RegistrationID, item.AgentID, item.ConversationID)
	require.NoError(t, err)
	require.Equal(t, hostedgenesis.RecoveryActionRetrySameStep, action)

	repaired, err := st.GetHostedGenesisSession(ctx, item.InstanceSlug, item.ConversationID)
	require.NoError(t, err)
	require.Nil(t, repaired.Failure)
	require.Equal(t, string(hostedgenesis.StatusAssistantTurnReady), repaired.Status)
	require.Equal(t, item.LatestTurnID, repaired.LatestTurnID)
	require.Equal(t, item.Version+1, repaired.Version)

	rows := fake.Items(models.MainTableName())
	require.Len(t, rows, 1)
	require.NotContains(t, rows[0], "failure")

	action, err = st.RepairHostedGenesisMalformedFailure(ctx, item.InstanceSlug, item.RegistrationID, item.AgentID, item.ConversationID)
	require.NoError(t, err)
	require.Empty(t, action)
}

func TestStore_RepairHostedGenesisMalformedFailedStateRequiresRestart(t *testing.T) {
	ctx := context.Background()
	st, fake, db := newMalformedFailureFakeStore(t)
	item := validStoreHostedGenesisSession()
	item.Status = string(hostedgenesis.StatusFailed)
	item.Failure = &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeAssistantTurnFailed,
		Message:   hostedgenesis.FailureMessage(hostedgenesis.FailureCodeAssistantTurnFailed),
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			MaxAttempts:       3,
			RetryAfterSeconds: 5,
			Reason:            string(hostedgenesis.FailureCodeAssistantTurnFailed),
		},
	}
	require.NoError(t, db.Model(item).Create())
	seedHostedGenesisMalformedFailure(t, fake)

	action, err := st.RepairHostedGenesisMalformedFailure(ctx, item.InstanceSlug, item.RegistrationID, item.AgentID, item.ConversationID)
	require.NoError(t, err)
	require.Equal(t, hostedgenesis.RecoveryActionRestartSoulBootstrap, action)

	rows := fake.Items(models.MainTableName())
	require.Len(t, rows, 1)
	failure, ok := rows[0]["failure"].(*types.AttributeValueMemberBOOL)
	require.True(t, ok)
	require.True(t, failure.Value)
	version, ok := rows[0]["version"].(*types.AttributeValueMemberN)
	require.True(t, ok)
	require.Equal(t, "0", version.Value)
}

func TestStore_RepairHostedGenesisMalformedFailureRejectsRouteBindingMismatch(t *testing.T) {
	for _, tc := range []struct {
		name           string
		registrationID string
		agentID        string
	}{
		{name: "registration", registrationID: "another-registration"},
		{name: "agent", agentID: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st, fake, db := newMalformedFailureFakeStore(t)
			item := validStoreHostedGenesisSession()
			item.Status = string(hostedgenesis.StatusAssistantTurnReady)
			require.NoError(t, db.Model(item).Create())
			seedHostedGenesisMalformedFailure(t, fake)

			registrationID := tc.registrationID
			agentID := tc.agentID
			if registrationID == "" {
				registrationID = item.RegistrationID
			}
			if agentID == "" {
				agentID = item.AgentID
			}
			action, err := st.RepairHostedGenesisMalformedFailure(ctx, item.InstanceSlug, registrationID, agentID, item.ConversationID)
			require.ErrorIs(t, err, theoryErrors.ErrConditionFailed)
			require.Empty(t, action)

			rows := fake.Items(models.MainTableName())
			require.Len(t, rows, 1)
			failure, ok := rows[0]["failure"].(*types.AttributeValueMemberBOOL)
			require.True(t, ok)
			require.True(t, failure.Value)
			version, ok := rows[0]["version"].(*types.AttributeValueMemberN)
			require.True(t, ok)
			require.Equal(t, "0", version.Value)
		})
	}
}

func newMalformedFailureFakeStore(t *testing.T) (*Store, *fakedb.Fake, DB) {
	t.Helper()
	fake := fakedb.New()
	rawDB, err := tabletheory.NewWithClient(session.Config{Region: "us-east-1"}, fake)
	require.NoError(t, err)
	require.NoError(t, rawDB.CreateTable(&models.HostedGenesisSession{}))
	db, ok := rawDB.(DB)
	require.True(t, ok)
	return New(db), fake, db
}

func seedHostedGenesisMalformedFailure(t *testing.T, fake *fakedb.Fake) {
	t.Helper()
	rows := fake.Items(models.MainTableName())
	require.Len(t, rows, 1)
	rows[0]["failure"] = &types.AttributeValueMemberBOOL{Value: true}
	fake.Reset()
	require.NoError(t, fake.Seed(models.MainTableName(), rows[0]))
}
