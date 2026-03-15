package controlplane

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandleSoulAgentGetRotationOperation_Success(t *testing.T) {
	t.Parallel()

	tdb := newSoulCommPortalTestDB()
	s := newSoulPortalServer(tdb)
	agentID := soulLifecycleTestAgentIDHex
	seedSoulAgentPortalAccess(t, tdb, agentID, models.SoulAgentStatusActive)

	tdb.base.qRotation.On("First", mock.AnythingOfType("*models.SoulWalletRotationRequest")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulWalletRotationRequest](t, args, 0)
		*dest = models.SoulWalletRotationRequest{
			AgentID:       agentID,
			Username:      "admin",
			CurrentWallet: "0x00000000000000000000000000000000000000aa",
			NewWallet:     "0x00000000000000000000000000000000000000bb",
			Nonce:         "7",
			Deadline:      1700000000,
		}
	}).Once()

	opID := soulRotationOpID(s.cfg.SoulChainID, "0x0000000000000000000000000000000000000001", agentID, "0x00000000000000000000000000000000000000aa", "0x00000000000000000000000000000000000000bb", "7", 1700000000)
	tdb.base.qOp.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulOperation](t, args, 0)
		*dest = models.SoulOperation{
			OperationID:     opID,
			Kind:            models.SoulOperationKindRotateWallet,
			AgentID:         agentID,
			Status:          models.SoulOperationStatusPending,
			SafePayloadJSON: `{"safe_address":"0x00000000000000000000000000000000000000cc","to":"0x0000000000000000000000000000000000000001","value":"0","data":"0x1234"}`,
			CreatedAt:       time.Now().Add(-time.Minute).UTC(),
			UpdatedAt:       time.Now().UTC(),
		}
	}).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": agentID}

	resp, err := s.handleSoulAgentGetRotationOperation(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var out soulAgentRotationOperationResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Equal(t, opID, out.Operation.OperationID)
	require.NotNil(t, out.SafeTx)
	require.Equal(t, "0x00000000000000000000000000000000000000cc", out.SafeTx.SafeAddress)
}

func TestHandleSoulAgentRecordRotationOperationExecution_Success(t *testing.T) {
	t.Parallel()

	tdb := newSoulCommPortalTestDB()
	s := newSoulPortalServer(tdb)
	agentID := soulLifecycleTestAgentIDHex
	seedSoulAgentPortalAccess(t, tdb, agentID, models.SoulAgentStatusActive)

	tdb.base.qRotation.On("First", mock.AnythingOfType("*models.SoulWalletRotationRequest")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulWalletRotationRequest](t, args, 0)
		*dest = models.SoulWalletRotationRequest{
			AgentID:       agentID,
			Username:      "admin",
			CurrentWallet: "0x00000000000000000000000000000000000000aa",
			NewWallet:     "0x00000000000000000000000000000000000000bb",
			Nonce:         "7",
			Deadline:      1700000000,
		}
	}).Once()

	opID := soulRotationOpID(s.cfg.SoulChainID, "0x0000000000000000000000000000000000000001", agentID, "0x00000000000000000000000000000000000000aa", "0x00000000000000000000000000000000000000bb", "7", 1700000000)
	tdb.base.qOp.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulOperation](t, args, 0)
		*dest = models.SoulOperation{
			OperationID:     opID,
			Kind:            models.SoulOperationKindRotateWallet,
			AgentID:         agentID,
			Status:          models.SoulOperationStatusPending,
			SafePayloadJSON: `{"to":"0x0000000000000000000000000000000000000001","value":"0","data":"0x1234"}`,
			CreatedAt:       time.Now().Add(-time.Minute).UTC(),
			UpdatedAt:       time.Now().UTC(),
		}
	}).Once()

	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
		return &fakeEthClient{
			receipt: &types.Receipt{
				Status:      0,
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

	resp, err := s.handleSoulAgentRecordRotationOperationExecution(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var out soulAgentRotationOperationResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Equal(t, models.SoulOperationStatusFailed, out.Operation.Status)
	require.Equal(t, strings.ToLower(txHash), out.Operation.ExecTxHash)
}
