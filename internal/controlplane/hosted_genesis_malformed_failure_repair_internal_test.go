package controlplane

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/require"
	"github.com/theory-cloud/tabletheory/v3"
	"github.com/theory-cloud/tabletheory/v3/pkg/session"
	"github.com/theory-cloud/tabletheory/v3/pkg/testing/fakedb"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	hoststore "github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// TestHostedGenesisMalformedFailureAdvanceRepairsThenRetriesSameStep is the
// regression for equaltoai/lesser-host#1003.
//
// A top-level DynamoDB failure=BOOL(true) cannot be decoded into the typed
// *hostedgenesis.Failure model, so the old advance path returned
// soul_instance.internal before turn acceptance or MicroVM dispatch. The first
// advance now removes only that exact impossible shape under tenant, session,
// version, status, and value guards and returns a typed retry_same_step. The
// retried request can then bind the next turn normally.
func TestHostedGenesisMalformedFailureAdvanceRepairsThenRetriesSameStep(t *testing.T) {
	ctx := context.Background()
	fake := fakedb.New()
	rawDB, err := tabletheory.NewWithClient(session.Config{Region: "us-east-1"}, fake)
	require.NoError(t, err)
	require.NoError(t, rawDB.CreateTable(&models.HostedGenesisSession{}))
	db, ok := rawDB.(hoststore.DB)
	require.True(t, ok)

	reg := mintConversationHandleReg()
	stored := hostedGenesisRecoverySessionFixture(t, reg, hostedgenesis.StatusAssistantTurnReady, "checkpoint://hosted-genesis/conv-1/assistant/turn-ready")
	require.NoError(t, db.Model(&stored).Create())

	rows := fake.Items(models.MainTableName())
	require.Len(t, rows, 1)
	rows[0]["failure"] = &types.AttributeValueMemberBOOL{Value: true}
	fake.Reset()
	require.NoError(t, fake.Seed(models.MainTableName(), rows[0]))

	srv := &Server{store: hoststore.New(db)}
	regCtx := mintConversationRegistrationContext{
		reg:        &reg,
		inst:       &models.Instance{Slug: soulInstanceBootstrapTestInstanceSlug},
		agentIDHex: reg.AgentID,
	}
	req := soulMintConversationRequest{ConversationID: stored.ConversationID}
	_, appErr := srv.loadHostedGenesisTurnSession(ctx, regCtx, soulInstanceBootstrapTestInstanceSlug, req, "advance philosophy", "req-repair", time.Now().UTC())
	require.NotNil(t, appErr)
	require.Equal(t, soulInstanceBootstrapCodeConflict, appErr.Code)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)
	require.Equal(t, "malformed_failure_repaired", appErr.Details["reason"])
	require.Equal(t, true, appErr.Details["retryable"])
	require.Equal(t, string(hostedgenesis.RecoveryActionRetrySameStep), appErr.Details["recovery_action"])
	require.NotContains(t, appErr.Details, "restart_path")

	rows = fake.Items(models.MainTableName())
	require.Len(t, rows, 1)
	require.NotContains(t, rows[0], "failure")
	version, ok := rows[0]["version"].(*types.AttributeValueMemberN)
	require.True(t, ok)
	require.Equal(t, "4", version.Value)

	retried, appErr := srv.loadHostedGenesisTurnSession(ctx, regCtx, soulInstanceBootstrapTestInstanceSlug, req, "advance philosophy", "req-retry", time.Now().UTC())
	require.Nil(t, appErr)
	require.NotNil(t, retried.session)
	require.Equal(t, hostedgenesis.StatusInProgress, hostedgenesis.NormalizeStatus(retried.session.Status))
	require.Nil(t, retried.session.Failure)
	require.Equal(t, stored.Version+1, retried.expectedVersion)
	require.Equal(t, stored.MessageCount+1, retried.session.MessageCount)
}

func TestHostedGenesisMalformedFailedStateNamesRestartPath(t *testing.T) {
	appErr := hostedGenesisMalformedFailureRecoveryError(hostedgenesis.RecoveryActionRestartSoulBootstrap)
	require.Equal(t, soulInstanceBootstrapCodeConflict, appErr.Code)
	require.Equal(t, http.StatusConflict, appErr.StatusCode)
	require.Equal(t, "malformed_failure_requires_restart", appErr.Details["reason"])
	require.Equal(t, false, appErr.Details["retryable"])
	require.Equal(t, string(hostedgenesis.RecoveryActionRestartSoulBootstrap), appErr.Details["recovery_action"])
	require.Equal(t, "/api/v1/soul/instance/agents/register/begin", appErr.Details["restart_path"])
}
