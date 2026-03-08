package controlplane

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func stubSoulMintPortalAccess(t *testing.T, tdb soulCommPortalTestDB, identity models.SoulAgentIdentity) {
	t.Helper()

	tdb.base.qIdentity.On("First", mockAnythingSoulIdentity()).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = identity
	}).Maybe()

	tdb.base.qDomain.On("First", mockAnythingSoulDomain()).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: identity.Domain, InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Maybe()

	tdb.base.qInstance.On("First", mockAnythingInstance()).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Maybe()
}

func mockAnythingSoulIdentity() any { return mock.AnythingOfType("*models.SoulAgentIdentity") }
func mockAnythingSoulDomain() any   { return mock.AnythingOfType("*models.Domain") }
func mockAnythingInstance() any     { return mock.AnythingOfType("*models.Instance") }

func TestHandleSoulAgentGetMintOperation_Success(t *testing.T) {
	t.Parallel()

	tdb := newSoulCommPortalTestDB()
	s := newSoulPortalServer(tdb)
	s.cfg.SoulMintSignerKey = strings.Repeat("ab", 32)
	agentID := soulLifecycleTestAgentIDHex
	identity := models.SoulAgentIdentity{
		AgentID:          agentID,
		Domain:           "example.com",
		LocalID:          "agent-0",
		Wallet:           "0x00000000000000000000000000000000000000aa",
		PrincipalAddress: "0x00000000000000000000000000000000000000aa",
		MetaURI:          "https://lab.lesser.host/api/v1/soul/agents/" + agentID + "/registration",
		Status:           models.SoulAgentStatusPending,
		LifecycleStatus:  models.SoulAgentStatusPending,
		UpdatedAt:        time.Now().UTC(),
	}
	stubSoulMintPortalAccess(t, tdb, identity)

	opID := s.soulMintOperationID(&identity)
	tdb.base.qOp.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulOperation](t, args, 0)
		*dest = models.SoulOperation{
			OperationID:     opID,
			Kind:            models.SoulOperationKindMint,
			AgentID:         agentID,
			Status:          models.SoulOperationStatusPending,
			SafePayloadJSON: `{"safe_address":"0x00000000000000000000000000000000000000bb","to":"0x00000000000000000000000000000000000000cc","value":"0","data":"0x1234"}`,
			CreatedAt:       time.Now().Add(-time.Minute).UTC(),
			UpdatedAt:       time.Now().UTC(),
		}
	}).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": agentID}

	resp, err := s.handleSoulAgentGetMintOperation(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var out soulAgentMintOperationResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Equal(t, opID, out.Operation.OperationID)
	require.NotNil(t, out.SafeTx)
	require.True(t, strings.HasPrefix(out.SafeTx.Data, "0x"))
	require.Empty(t, out.SafeTx.SafeAddress)
}

func TestHandleSoulAgentGetMintOperation_NotFound(t *testing.T) {
	t.Parallel()

	tdb := newSoulCommPortalTestDB()
	s := newSoulPortalServer(tdb)
	s.cfg.SoulMintSignerKey = strings.Repeat("ab", 32)
	agentID := soulLifecycleTestAgentIDHex
	identity := models.SoulAgentIdentity{
		AgentID:          agentID,
		Domain:           "example.com",
		LocalID:          "agent-0",
		Wallet:           "0x00000000000000000000000000000000000000aa",
		PrincipalAddress: "0x00000000000000000000000000000000000000aa",
		Status:           models.SoulAgentStatusPending,
		LifecycleStatus:  models.SoulAgentStatusPending,
		UpdatedAt:        time.Now().UTC(),
	}
	stubSoulMintPortalAccess(t, tdb, identity)
	tdb.base.qOp.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": agentID}

	_, err := s.handleSoulAgentGetMintOperation(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.not_found", appErr.Code)
}

func TestHandleSoulAgentRecordMintOperationExecution_Success(t *testing.T) {
	t.Parallel()

	tdb := newSoulCommPortalTestDB()
	s := newSoulPortalServer(tdb)
	s.cfg.SoulMintSignerKey = strings.Repeat("ab", 32)
	agentID := soulLifecycleTestAgentIDHex
	identity := models.SoulAgentIdentity{
		AgentID:          agentID,
		Domain:           "example.com",
		LocalID:          "agent-0",
		Wallet:           "0x00000000000000000000000000000000000000aa",
		PrincipalAddress: "0x00000000000000000000000000000000000000aa",
		MetaURI:          "https://lab.lesser.host/api/v1/soul/agents/" + agentID + "/registration",
		Status:           models.SoulAgentStatusPending,
		LifecycleStatus:  models.SoulAgentStatusPending,
		UpdatedAt:        time.Now().UTC(),
	}
	stubSoulMintPortalAccess(t, tdb, identity)

	opID := s.soulMintOperationID(&identity)
	tdb.base.qOp.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulOperation](t, args, 0)
		*dest = models.SoulOperation{
			OperationID:     opID,
			Kind:            models.SoulOperationKindMint,
			AgentID:         agentID,
			Status:          models.SoulOperationStatusPending,
			SafePayloadJSON: `{"safe_address":"0x00000000000000000000000000000000000000bb","to":"0x00000000000000000000000000000000000000cc","value":"0","data":"0x1234"}`,
			CreatedAt:       time.Now().Add(-time.Minute).UTC(),
			UpdatedAt:       time.Now().UTC(),
		}
	}).Once()

	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
		return &fakeEthClient{
			receipt: &types.Receipt{
				Status:      1,
				BlockNumber: big.NewInt(42),
				GasUsed:     100,
				Logs:        []*types.Log{},
			},
		}, nil
	}

	txHash := "0x" + strings.Repeat("ab", 32)
	body, _ := json.Marshal(recordSoulExecutionRequest{ExecTxHash: txHash})
	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": agentID}
	ctx.Request.Body = body

	resp, err := s.handleSoulAgentRecordMintOperationExecution(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var out soulAgentMintOperationResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Equal(t, models.SoulOperationStatusExecuted, out.Operation.Status)
	require.Equal(t, strings.ToLower(txHash), out.Operation.ExecTxHash)
	require.NotNil(t, out.SafeTx)
	require.Empty(t, out.SafeTx.SafeAddress)
}
