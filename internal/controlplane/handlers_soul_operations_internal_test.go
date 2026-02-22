package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type fakeEthClient struct {
	receipt *types.Receipt
	err     error

	callContract func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error)
}

func (f *fakeEthClient) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	if f != nil && f.callContract != nil {
		return f.callContract(ctx, msg)
	}
	return nil, errors.New("not implemented")
}

func (f *fakeEthClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	return f.receipt, f.err
}

func (f *fakeEthClient) Close() {}

type soulOperationsTestDB struct {
	db          *ttmocks.MockExtendedDB
	qOp         *ttmocks.MockQuery
	qID         *ttmocks.MockQuery
	qWalletAgent *ttmocks.MockQuery
	qAudit      *ttmocks.MockQuery
}

func newSoulOperationsTestDB() soulOperationsTestDB {
	db := ttmocks.NewMockExtendedDB()
	qOp := new(ttmocks.MockQuery)
	qID := new(ttmocks.MockQuery)
	qWalletAgent := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulOperation")).Return(qOp).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qID).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulWalletAgentIndex")).Return(qWalletAgent).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qOp, qID, qWalletAgent, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
	}

	return soulOperationsTestDB{db: db, qOp: qOp, qID: qID, qWalletAgent: qWalletAgent, qAudit: qAudit}
}

func opCtx() *apptheory.Context {
	ctx := &apptheory.Context{AuthIdentity: "op", RequestID: "rid"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	return ctx
}

func TestHandleListSoulOperations_DefaultStatusAndInvalidStatus(t *testing.T) {
	t.Parallel()

	tdb := newSoulOperationsTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qOp.On("All", mock.AnythingOfType("*[]*models.SoulOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulOperation](t, args, 0)
		*dest = []*models.SoulOperation{
			nil,
			{OperationID: "op1", Kind: models.SoulOperationKindMint, Status: models.SoulOperationStatusPending},
		}
	}).Once()

	ctx := opCtx()
	resp, err := s.handleListSoulOperations(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}

	var out listSoulOperationsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || len(out.Operations) != 1 || out.Operations[0].OperationID != "op1" {
		t.Fatalf("unexpected response: %#v", out)
	}

	ctxBad := opCtx()
	ctxBad.Request.Query = map[string][]string{"status": {"nope"}}
	if _, err := s.handleListSoulOperations(ctxBad); err == nil {
		t.Fatalf("expected invalid status error")
	}
}

func TestHandleRecordSoulOperationExecution_SuccessMint(t *testing.T) {
	t.Parallel()

	tdb := newSoulOperationsTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulRPCURL:                  "http://rpc",
		},
	}

	agentID := "0x" + strings.Repeat("11", 32)
	txHash := "0x" + strings.Repeat("ab", 32)

	op := &models.SoulOperation{
		OperationID:  "op1",
		Kind:         models.SoulOperationKindMint,
		AgentID:      agentID,
		Status:       models.SoulOperationStatusPending,
		SnapshotJSON: `{"k":"v"}`,
		CreatedAt:    time.Now().Add(-time.Minute).UTC(),
		UpdatedAt:    time.Now().Add(-time.Minute).UTC(),
	}

	tdb.qOp.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulOperation](t, args, 0)
		*dest = *op
	}).Once()

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive, Wallet: "0x0000000000000000000000000000000000000002"}
	}).Once()

	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
		return &fakeEthClient{
			receipt: &types.Receipt{
				Status:      1,
				BlockNumber: big.NewInt(123),
				GasUsed:     100,
				Logs:        []*types.Log{},
			},
		}, nil
	}

	body, _ := json.Marshal(recordSoulExecutionRequest{ExecTxHash: txHash})
	ctx := opCtx()
	ctx.Params = map[string]string{"id": "op1"}
	ctx.Request.Body = body

	resp, err := s.handleRecordSoulOperationExecution(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}

	var out models.SoulOperation
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status != models.SoulOperationStatusExecuted || out.ExecTxHash != strings.ToLower(txHash) || out.ExecBlockNumber != 123 {
		t.Fatalf("unexpected operation: %#v", out)
	}
	if out.ExecSuccess == nil || !*out.ExecSuccess {
		t.Fatalf("expected ExecSuccess true, got %#v", out.ExecSuccess)
	}
}

func TestSoulOperationHelpers_BlockNumberAndReceiptJSON(t *testing.T) {
	t.Parallel()

	if got := soulBlockNumber(nil); got != 0 {
		t.Fatalf("expected 0")
	}
	if got := soulReceiptSnapshotJSON("x", nil); got != "" {
		t.Fatalf("expected empty")
	}

	receipt := &types.Receipt{Status: 1, BlockNumber: big.NewInt(-1)}
	if got := soulBlockNumber(receipt); got != 0 {
		t.Fatalf("expected 0 for negative")
	}

	receipt.BlockNumber = big.NewInt(10)
	if got := soulBlockNumber(receipt); got != 10 {
		t.Fatalf("unexpected: %d", got)
	}
	if got := soulReceiptSnapshotJSON("0x" + strings.Repeat("ab", 32), receipt); strings.TrimSpace(got) == "" {
		t.Fatalf("expected snapshot json")
	}
}

func TestHandleGetSoulOperation_NotFoundAndBadRequest(t *testing.T) {
	t.Parallel()

	tdb := newSoulOperationsTestDB()
	s := &Server{store: store.New(tdb.db)}

	ctxMissing := opCtx()
	ctxMissing.Params = map[string]string{"id": " "}
	if _, err := s.handleGetSoulOperation(ctxMissing); err == nil {
		t.Fatalf("expected bad_request")
	}

	ctxNotFound := opCtx()
	ctxNotFound.Params = map[string]string{"id": "missing"}
	tdb.qOp.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, err := s.handleGetSoulOperation(ctxNotFound); err == nil {
		t.Fatalf("expected not_found")
	}

	tdb.qOp.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulOperation](t, args, 0)
		*dest = models.SoulOperation{OperationID: "op1", Kind: models.SoulOperationKindMint, Status: models.SoulOperationStatusPending}
	}).Once()
	ctxOK := opCtx()
	ctxOK.Params = map[string]string{"id": "op1"}
	resp, err := s.handleGetSoulOperation(ctxOK)
	if err != nil || resp.Status != 200 {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}
}

func TestSoulOperationSnapshotJSON_Success(t *testing.T) {
	t.Parallel()

	parsedABI, err := abi.JSON(strings.NewReader(soul.SoulRegistryABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}

	wantWallet := common.HexToAddress("0x000000000000000000000000000000000000beef")
	wantNonce := big.NewInt(7)
	walletRet, _ := parsedABI.Methods["getAgentWallet"].Outputs.Pack(wantWallet)
	nonceRet, _ := parsedABI.Methods["agentNonces"].Outputs.Pack(wantNonce)

	client := &fakeEthClient{callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
		if bytes.HasPrefix(msg.Data, parsedABI.Methods["getAgentWallet"].ID) {
			return walletRet, nil
		}
		if bytes.HasPrefix(msg.Data, parsedABI.Methods["agentNonces"].ID) {
			return nonceRet, nil
		}
		return nil, ethereum.NotFound
	}}

	s := &Server{cfg: config.Config{SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001"}}
	op := &models.SoulOperation{
		OperationID: "op1",
		Kind:        models.SoulOperationKindRotateWallet,
		AgentID:     "0x" + strings.Repeat("11", 32),
	}

	snap := s.soulOperationSnapshotJSON(context.Background(), client, op)
	if strings.TrimSpace(snap) == "" {
		t.Fatalf("expected snapshot json")
	}
	if !strings.Contains(snap, strings.ToLower(wantWallet.Hex())) {
		t.Fatalf("expected wallet in snapshot, got %q", snap)
	}
}

func TestApplySoulOperationSideEffects_RotateWallet(t *testing.T) {
	t.Parallel()

	tdb := newSoulOperationsTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	parsedABI, err := abi.JSON(strings.NewReader(soul.SoulRegistryABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}

	oldWallet := "0x0000000000000000000000000000000000000002"
	newWallet := "0x0000000000000000000000000000000000000003"
	walletRet, _ := parsedABI.Methods["getAgentWallet"].Outputs.Pack(common.HexToAddress(newWallet))

	client := &fakeEthClient{callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
		if bytes.HasPrefix(msg.Data, parsedABI.Methods["getAgentWallet"].ID) {
			return walletRet, nil
		}
		return nil, ethereum.NotFound
	}}

	agentID := "0x" + strings.Repeat("11", 32)
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Wallet: oldWallet, Status: models.SoulAgentStatusActive}
	}).Once()

	tdb.qWalletAgent.On("Delete").Return(nil).Once()
	tdb.qWalletAgent.On("CreateOrUpdate").Return(nil).Once()

	op := &models.SoulOperation{
		Kind:    models.SoulOperationKindRotateWallet,
		AgentID: agentID,
	}
	if err := s.applySoulOperationSideEffects(context.Background(), client, op); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}
