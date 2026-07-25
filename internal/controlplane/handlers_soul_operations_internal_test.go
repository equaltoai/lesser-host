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
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/soulattestations"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type fakeEthClient struct {
	receipt *types.Receipt
	err     error
	tx      *types.Transaction
	pending bool
	txErr   error

	callContract func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error)
	filterLogs   func(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error)
}

func (f *fakeEthClient) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	if f != nil && f.callContract != nil {
		return f.callContract(ctx, msg)
	}
	return nil, errors.New("not implemented")
}

func (f *fakeEthClient) TransactionByHash(ctx context.Context, txHash common.Hash) (*types.Transaction, bool, error) {
	return f.tx, f.pending, f.txErr
}

func (f *fakeEthClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	return f.receipt, f.err
}

func (f *fakeEthClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	if f != nil && f.filterLogs != nil {
		return f.filterLogs(ctx, q)
	}
	return nil, errors.New("not implemented")
}

func (f *fakeEthClient) Close() {}

type soulOperationsTestDB struct {
	db           *ttmocks.MockExtendedDB
	qOp          *ttmocks.MockQuery
	qID          *ttmocks.MockQuery
	qPromotion   *ttmocks.MockQuery
	qLifecycle   *ttmocks.MockQuery
	qWalletAgent *ttmocks.MockQuery
	qChannel     *ttmocks.MockQuery
	qENS         *ttmocks.MockQuery
	qAudit       *ttmocks.MockQuery
}

func newSoulOperationsTestDB() soulOperationsTestDB {
	db := ttmocks.NewMockExtendedDB()
	qOp := new(ttmocks.MockQuery)
	qID := new(ttmocks.MockQuery)
	qPromotion := new(ttmocks.MockQuery)
	qLifecycle := new(ttmocks.MockQuery)
	qWalletAgent := new(ttmocks.MockQuery)
	qChannel := new(ttmocks.MockQuery)
	qENS := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulOperation")).Return(qOp).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qID).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(qPromotion).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentPromotionLifecycleEvent")).Return(qLifecycle).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulWalletAgentIndex")).Return(qWalletAgent).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(qChannel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(qENS).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qOp, qID, qPromotion, qLifecycle, qWalletAgent, qChannel, qENS, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("Update", mock.Anything, mock.Anything).Return(nil).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
	}

	return soulOperationsTestDB{
		db:           db,
		qOp:          qOp,
		qID:          qID,
		qPromotion:   qPromotion,
		qLifecycle:   qLifecycle,
		qWalletAgent: qWalletAgent,
		qChannel:     qChannel,
		qENS:         qENS,
		qAudit:       qAudit,
	}
}

func opCtx() *apptheory.Context {
	ctx := &apptheory.Context{AuthIdentity: "op", RequestID: "rid"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	return ctx
}

func latestPromotionLifecycleEventModelCall(t *testing.T, db *ttmocks.MockExtendedDB) *models.SoulAgentPromotionLifecycleEvent {
	t.Helper()

	for i := len(db.Calls) - 1; i >= 0; i-- {
		call := db.Calls[i]
		if call.Method != modelCallMethod || len(call.Arguments) == 0 {
			continue
		}
		event, ok := call.Arguments.Get(0).(*models.SoulAgentPromotionLifecycleEvent)
		if !ok || event == nil || strings.TrimSpace(event.EventType) == "" {
			continue
		}
		copy := *event
		return &copy
	}
	t.Fatalf("expected lifecycle event model call")
	return nil
}

func latestSoulAgentIdentityUpdateModelCall(t *testing.T, db *ttmocks.MockExtendedDB) *models.SoulAgentIdentity {
	t.Helper()

	for i := len(db.Calls) - 1; i >= 0; i-- {
		call := db.Calls[i]
		if call.Method != modelCallMethod || len(call.Arguments) == 0 {
			continue
		}
		identity, ok := call.Arguments.Get(0).(*models.SoulAgentIdentity)
		if !ok || identity == nil || strings.TrimSpace(identity.MintTxHash) == "" {
			continue
		}
		copy := *identity
		return &copy
	}
	t.Fatalf("expected soul identity update model call")
	return nil
}

func soulTestMintPayload(t *testing.T, contract string, agentIDHex string, wallet string, principal string) *safeTxPayload {
	t.Helper()
	agentID, ok := new(big.Int).SetString(strings.TrimPrefix(agentIDHex, "0x"), 16)
	if !ok {
		t.Fatalf("invalid agent id %q", agentIDHex)
	}
	walletAddr := common.HexToAddress(wallet)
	principalAddr := common.HexToAddress(principal)
	data, err := soul.EncodeSelfMintSoulCall(walletAddr, agentID, "https://example.com/meta.json", 0, principalAddr, big.NewInt(time.Now().Add(time.Hour).Unix()), []byte{0x01, 0x02})
	if err != nil {
		t.Fatalf("EncodeSelfMintSoulCall: %v", err)
	}
	return &safeTxPayload{To: contract, Value: "500000000000000", Data: hexutil.Encode(data)}
}

func soulTestRotatePayload(t *testing.T, contract string, agentIDHex string, newWallet string) *safeTxPayload {
	t.Helper()
	agentID, ok := new(big.Int).SetString(strings.TrimPrefix(agentIDHex, "0x"), 16)
	if !ok {
		t.Fatalf("invalid agent id %q", agentIDHex)
	}
	data, err := soul.EncodeRotateWalletCall(agentID, common.HexToAddress(newWallet), big.NewInt(7), big.NewInt(time.Now().Add(time.Hour).Unix()), []byte{0x01}, []byte{0x02})
	if err != nil {
		t.Fatalf("EncodeRotateWalletCall: %v", err)
	}
	return &safeTxPayload{To: contract, Value: "0", Data: hexutil.Encode(data)}
}

func soulTestPayloadJSON(t *testing.T, payload *safeTxPayload) string {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(b)
}

func soulTestTxFromPayload(t *testing.T, payload *safeTxPayload) *types.Transaction {
	t.Helper()
	to := common.HexToAddress(payload.To)
	value, ok := parseSoulOperationPayloadValue(payload.Value)
	if !ok {
		t.Fatalf("invalid value %q", payload.Value)
	}
	data, err := hexutil.Decode(payload.Data)
	if err != nil {
		t.Fatalf("decode payload data: %v", err)
	}
	return types.NewTx(&types.LegacyTx{To: &to, Value: value, Data: data})
}

func soulTestMintReceipt(contract common.Address, agentIDHex string, wallet string, principal string) *types.Receipt {
	agentID, _ := new(big.Int).SetString(strings.TrimPrefix(agentIDHex, "0x"), 16)
	walletAddr := common.HexToAddress(wallet)
	principalAddr := common.HexToAddress(principal)
	return &types.Receipt{
		Status:      1,
		BlockNumber: big.NewInt(123),
		GasUsed:     100,
		Logs: []*types.Log{
			{Address: contract, Topics: []common.Hash{soulMintedTopic, topicBig(agentID), topicAddress(walletAddr)}},
			{Address: contract, Topics: []common.Hash{principalDeclaredTopic, topicBig(agentID), topicAddress(principalAddr)}},
		},
	}
}

func soulTestCallContractForWalletPrincipal(t *testing.T, wallet string, principal string) func(context.Context, ethereum.CallMsg) ([]byte, error) {
	t.Helper()
	parsedABI, err := abi.JSON(strings.NewReader(soul.SoulRegistryABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}
	walletRet, _ := parsedABI.Methods["getAgentWallet"].Outputs.Pack(common.HexToAddress(wallet))
	principalRet, _ := parsedABI.Methods["principalOf"].Outputs.Pack(common.HexToAddress(principal))
	return func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
		if bytes.HasPrefix(msg.Data, parsedABI.Methods["getAgentWallet"].ID) {
			return walletRet, nil
		}
		if bytes.HasPrefix(msg.Data, parsedABI.Methods["principalOf"].ID) {
			return principalRet, nil
		}
		return nil, ethereum.NotFound
	}
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

func TestSyncMintPromotionAfterOperationExecution_EmitsLifecycleEvent(t *testing.T) {
	t.Parallel()

	tdb := newSoulOperationsTestDB()
	s := &Server{store: store.New(tdb.db)}

	agentID := "0x" + strings.Repeat("33", 32)
	tdb.qPromotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentPromotion](t, args, 0)
		*dest = models.SoulAgentPromotion{
			AgentID:         agentID,
			RegistrationID:  "reg-1",
			RequestedBy:     testUsernameAlice,
			Domain:          "example.com",
			LocalID:         "agent-bot",
			Wallet:          "0x00000000000000000000000000000000000000aa",
			Stage:           models.SoulAgentPromotionStageApproved,
			RequestStatus:   models.SoulAgentPromotionRequestStatusVerified,
			ApprovalStatus:  models.SoulAgentPromotionApprovalStatusApproved,
			ReadinessStatus: models.SoulAgentPromotionReadinessAwaitingMint,
			CreatedAt:       time.Date(2026, 3, 28, 18, 0, 0, 0, time.UTC),
			UpdatedAt:       time.Date(2026, 3, 28, 18, 0, 0, 0, time.UTC),
		}
	}).Once()
	tdb.qPromotion.On("CreateOrUpdate").Return(nil).Once()
	tdb.qLifecycle.On("Create").Return(nil).Once()

	now := time.Date(2026, 3, 28, 18, 5, 0, 0, time.UTC)
	appErr := s.syncMintPromotionAfterOperationExecution(context.Background(), &models.SoulOperation{
		OperationID: "op-1",
		Kind:        models.SoulOperationKindMint,
		AgentID:     agentID,
		Status:      models.SoulOperationStatusExecuted,
		ExecTxHash:  "0x" + strings.Repeat("ab", 32),
	}, "rid-1", now, true)
	if appErr != nil {
		t.Fatalf("unexpected appErr: %#v", appErr)
	}

	event := latestPromotionLifecycleEventModelCall(t, tdb.db)
	if event.EventType != models.SoulAgentPromotionEventTypeMintExecuted {
		t.Fatalf("unexpected event: %#v", event)
	}
	if event.OperationID != "op-1" || event.RequestID != "rid-1" {
		t.Fatalf("unexpected event linkage: %#v", event)
	}
	if event.ReadinessStatus != models.SoulAgentPromotionReadinessReadyForConversation {
		t.Fatalf("expected ready-for-conversation snapshot, got %#v", event)
	}
	if event.AnchorState != models.SoulAnchorStateImmutableOnchain ||
		event.AnchorEvidenceTxHash != "0x"+strings.Repeat("ab", 32) ||
		!event.AnchorEvidenceAt.Equal(now) {
		t.Fatalf("expected immutable anchor evidence on mint event, got %#v", event)
	}
}

func TestApplySoulOperationMintSideEffectsPreservesAgentIDAndPolicy(t *testing.T) {
	t.Parallel()

	tdb := newSoulOperationsTestDB()
	s := &Server{store: store.New(tdb.db)}

	agentID := "0x" + strings.Repeat("77", 32)
	txHash := "0x" + strings.Repeat("ef", 32)
	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:                          agentID,
			Status:                           models.SoulAgentStatusActive,
			LifecycleStatus:                  models.SoulAgentStatusActive,
			PolicyVersion:                    models.SoulPolicyVersionHostedBoundSoulV1,
			AnchorState:                      models.SoulAnchorStateHostedOffchain,
			OperationalBinding:               models.SoulOperationalBindingHostedBoundSoul,
			CapabilityPolicyVersion:          models.SoulCapabilityPolicyVersionV1,
			CallerAccessPaymentPolicyVersion: models.SoulCallerAccessPaymentPolicyVersionV1,
			EmailDefaultAllowed:              true,
			PhoneEntitlementStatus:           models.SoulPhoneEntitlementProvisioned,
			SMSAllowed:                       true,
			VoiceAllowed:                     true,
			PublicPaidCallerAccess:           models.SoulPublicPaidCallerAccessGrantable,
			PolicyMigrationState:             models.SoulPolicyMigrationStatePersistedV1,
		}
	}).Once()
	tdb.qID.On("Update", mock.Anything).Return(nil).Once()

	s.applySoulOperationMintSideEffects(context.Background(), &models.SoulOperation{
		Kind:       models.SoulOperationKindMint,
		AgentID:    agentID,
		ExecTxHash: txHash,
	}, agentID)

	update := latestSoulAgentIdentityUpdateModelCall(t, tdb.db)
	if update.AgentID != agentID || update.MintTxHash != txHash || update.AnchorState != models.SoulAnchorStateImmutableOnchain {
		t.Fatalf("unexpected mint side-effect identity update: %#v", update)
	}
	if update.PolicyVersion != models.SoulPolicyVersionHostedBoundSoulV1 ||
		update.OperationalBinding != models.SoulOperationalBindingHostedBoundSoul ||
		update.CapabilityPolicyVersion != models.SoulCapabilityPolicyVersionV1 ||
		update.CallerAccessPaymentPolicyVersion != models.SoulCallerAccessPaymentPolicyVersionV1 ||
		!update.EmailDefaultAllowed ||
		update.PhoneEntitlementStatus != models.SoulPhoneEntitlementProvisioned ||
		!update.SMSAllowed ||
		!update.VoiceAllowed ||
		update.PublicPaidCallerAccess != models.SoulPublicPaidCallerAccessGrantable ||
		update.PolicyMigrationState != models.SoulPolicyMigrationStatePersistedV1 {
		t.Fatalf("mint side effect churned capability/access policy: %#v", update)
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
	wallet := "0x0000000000000000000000000000000000000002"
	principal := testEthAddress3
	payload := soulTestMintPayload(t, s.cfg.SoulRegistryContractAddress, agentID, wallet, principal)

	op := &models.SoulOperation{
		OperationID:     "op1",
		Kind:            models.SoulOperationKindMint,
		AgentID:         agentID,
		Status:          models.SoulOperationStatusPending,
		SafePayloadJSON: soulTestPayloadJSON(t, payload),
		SnapshotJSON:    `{"k":"v"}`,
		CreatedAt:       time.Now().Add(-time.Minute).UTC(),
		UpdatedAt:       time.Now().Add(-time.Minute).UTC(),
	}

	tdb.qOp.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulOperation](t, args, 0)
		*dest = *op
	}).Once()

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive, Wallet: wallet}
	}).Once()
	tdb.qPromotion.On("First", mock.AnythingOfType("*models.SoulAgentPromotion")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentPromotion](t, args, 0)
		*dest = models.SoulAgentPromotion{
			AgentID:         agentID,
			RegistrationID:  "reg1",
			Stage:           models.SoulAgentPromotionStageApproved,
			RequestStatus:   models.SoulAgentPromotionRequestStatusVerified,
			ApprovalStatus:  models.SoulAgentPromotionApprovalStatusApproved,
			ReadinessStatus: models.SoulAgentPromotionReadinessAwaitingMint,
			CreatedAt:       time.Now().Add(-time.Minute).UTC(),
			UpdatedAt:       time.Now().Add(-time.Minute).UTC(),
		}
	}).Once()

	tdb.qPromotion.On("CreateOrUpdate").Return(nil).Once()

	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
		return &fakeEthClient{
			tx:           soulTestTxFromPayload(t, payload),
			receipt:      soulTestMintReceipt(common.HexToAddress(s.cfg.SoulRegistryContractAddress), agentID, wallet, principal),
			callContract: soulTestCallContractForWalletPrincipal(t, wallet, principal),
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

func TestRecordSoulOperationExecution_RejectsMismatchedTransactionPayload(t *testing.T) {
	t.Parallel()

	tdb := newSoulOperationsTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulRPCURL:                  "http://rpc",
		},
	}
	agentID := "0x" + strings.Repeat("11", 32)
	wallet := "0x0000000000000000000000000000000000000002"
	principal := testEthAddress3
	payload := soulTestMintPayload(t, s.cfg.SoulRegistryContractAddress, agentID, wallet, principal)
	wrongPayload := *payload
	wrongPayload.Data = "0x1234"
	op := &models.SoulOperation{OperationID: "op-mismatch", Kind: models.SoulOperationKindMint, AgentID: agentID, Status: models.SoulOperationStatusPending, SafePayloadJSON: soulTestPayloadJSON(t, payload)}

	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
		return &fakeEthClient{
			tx:           soulTestTxFromPayload(t, &wrongPayload),
			receipt:      soulTestMintReceipt(common.HexToAddress(s.cfg.SoulRegistryContractAddress), agentID, wallet, principal),
			callContract: soulTestCallContractForWalletPrincipal(t, wallet, principal),
		}, nil
	}

	_, appErr := s.recordSoulOperationExecution(context.Background(), "op", "rid", op, "0x"+strings.Repeat("ab", 32))
	if appErr == nil || appErr.Message != "execution receipt does not match operation" {
		t.Fatalf("expected receipt mismatch, got %#v", appErr)
	}
}

func TestRecordSoulOperationExecution_RejectsMissingMintEvent(t *testing.T) {
	t.Parallel()

	tdb := newSoulOperationsTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulRPCURL:                  "http://rpc",
		},
	}
	agentID := "0x" + strings.Repeat("11", 32)
	wallet := "0x0000000000000000000000000000000000000002"
	principal := testEthAddress3
	payload := soulTestMintPayload(t, s.cfg.SoulRegistryContractAddress, agentID, wallet, principal)
	op := &models.SoulOperation{OperationID: "op-missing-event", Kind: models.SoulOperationKindMint, AgentID: agentID, Status: models.SoulOperationStatusPending, SafePayloadJSON: soulTestPayloadJSON(t, payload)}

	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
		return &fakeEthClient{
			tx: soulTestTxFromPayload(t, payload),
			receipt: &types.Receipt{
				Status:      1,
				BlockNumber: big.NewInt(123),
				GasUsed:     100,
				Logs:        []*types.Log{},
			},
			callContract: soulTestCallContractForWalletPrincipal(t, wallet, principal),
		}, nil
	}

	_, appErr := s.recordSoulOperationExecution(context.Background(), "op", "rid", op, "0x"+strings.Repeat("ab", 32))
	if appErr == nil || appErr.Message != "execution receipt does not match operation" {
		t.Fatalf("expected receipt mismatch, got %#v", appErr)
	}
}

func TestRecordSoulOperationExecution_TerminalIdempotentAndImmutable(t *testing.T) {
	t.Parallel()

	txHash := "0x" + strings.Repeat("ab", 32)
	op := &models.SoulOperation{
		OperationID: "op-terminal",
		Kind:        models.SoulOperationKindRotateWallet,
		Status:      models.SoulOperationStatusExecuted,
		ExecTxHash:  strings.ToLower(txHash),
	}

	s := &Server{}
	got, appErr := s.recordSoulOperationExecution(context.Background(), "op", "rid", op, txHash)
	if appErr != nil {
		t.Fatalf("expected idempotent terminal record, got %#v", appErr)
	}
	if got != op {
		t.Fatalf("expected original terminal operation")
	}

	_, appErr = s.recordSoulOperationExecution(context.Background(), "op", "rid", op, "0x"+strings.Repeat("cd", 32))
	if appErr == nil || appErr.Code != "app.conflict" {
		t.Fatalf("expected conflict for different terminal tx hash, got %#v", appErr)
	}
}

func TestRecordSoulOperationExecution_ConditionalRaceIsIdempotent(t *testing.T) {
	t.Parallel()

	tdb := newSoulOperationsTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulRegistryContractAddress: testEthAddress1,
			SoulRPCURL:                  "http://rpc",
		},
	}

	agentID := "0x" + strings.Repeat("11", 32)
	txHash := "0x" + strings.Repeat("ab", 32)
	payload := soulTestRotatePayload(t, testEthAddress1, agentID, testEthAddress2)
	op := &models.SoulOperation{
		OperationID:     "op-race",
		Kind:            models.SoulOperationKindRotateWallet,
		AgentID:         agentID,
		Status:          models.SoulOperationStatusPending,
		SafePayloadJSON: soulTestPayloadJSON(t, payload),
	}
	terminal := *op
	terminal.Status = models.SoulOperationStatusFailed
	terminal.ExecTxHash = strings.ToLower(txHash)
	failed := false
	terminal.ExecSuccess = &failed

	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(theoryErrors.ErrConditionFailed).Once()
	tdb.qOp.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulOperation](t, args, 0)
		*dest = terminal
	}).Once()

	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
		return &fakeEthClient{
			tx: soulTestTxFromPayload(t, payload),
			receipt: &types.Receipt{
				Status:      0,
				BlockNumber: big.NewInt(77),
				GasUsed:     100,
			},
		}, nil
	}

	got, appErr := s.recordSoulOperationExecution(context.Background(), "op", "rid", op, txHash)
	if appErr != nil {
		t.Fatalf("expected idempotent condition-failed race, got %#v", appErr)
	}
	if got == nil || got.Status != models.SoulOperationStatusFailed || !strings.EqualFold(got.ExecTxHash, txHash) {
		t.Fatalf("unexpected raced operation: %#v", got)
	}
}

func TestRecordSoulOperationExecution_SafeInnerFailureRecordsFailed(t *testing.T) {
	t.Parallel()

	tdb := newSoulOperationsTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulRegistryContractAddress: testEthAddress1,
			SoulRPCURL:                  "http://rpc",
		},
	}

	agentID := "0x" + strings.Repeat("11", 32)
	txHash := "0x" + strings.Repeat("ab", 32)
	payload := soulTestRotatePayload(t, testEthAddress1, agentID, testEthAddress2)
	payload.SafeAddress = testEthAddress3
	op := &models.SoulOperation{
		OperationID:     "op-safe-failure",
		Kind:            models.SoulOperationKindRotateWallet,
		AgentID:         agentID,
		Status:          models.SoulOperationStatusPending,
		SafePayloadJSON: soulTestPayloadJSON(t, payload),
	}

	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
		return &fakeEthClient{
			tx: soulTestSafeExecTx(t, payload, 0),
			receipt: &types.Receipt{
				Status:      1,
				BlockNumber: big.NewInt(88),
				GasUsed:     100,
				Logs: []*types.Log{{
					Address: common.HexToAddress(payload.SafeAddress),
					Topics:  []common.Hash{safeExecutionFailureTopic},
				}},
			},
		}, nil
	}

	got, appErr := s.recordSoulOperationExecution(context.Background(), "op", "rid", op, txHash)
	if appErr != nil {
		t.Fatalf("expected Safe inner failure to record failed operation, got %#v", appErr)
	}
	if got.Status != models.SoulOperationStatusFailed || got.ExecSuccess == nil || *got.ExecSuccess {
		t.Fatalf("expected failed operation from Safe ExecutionFailure, got %#v", got)
	}
	if got.ExecBlockNumber != 88 || !strings.EqualFold(got.ExecTxHash, txHash) {
		t.Fatalf("unexpected execution metadata: %#v", got)
	}
}

func TestValidateSoulExecutionTransactionMatchesPayload_DirectAndSafe(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("11", 32)
	payload := soulTestMintPayload(t, testEthAddress1, agentID, testEthAddress2, testEthAddress3)
	expect := soulOperationReceiptExpectation{Payload: payload, To: common.HexToAddress(payload.To)}
	expect.Value, _ = parseSoulOperationPayloadValue(payload.Value)
	expect.Data, _ = hexutil.Decode(payload.Data)

	if appErr := validateSoulExecutionTransactionMatchesPayload(soulTestTxFromPayload(t, payload), expect); appErr != nil {
		t.Fatalf("expected direct payload to match, got %#v", appErr)
	}

	safePayload := *payload
	safePayload.SafeAddress = "0x0000000000000000000000000000000000000004"
	safeTx := soulTestSafeExecTx(t, &safePayload, 0)
	expect.Payload = &safePayload
	if appErr := validateSoulExecutionTransactionMatchesPayload(safeTx, expect); appErr != nil {
		t.Fatalf("expected safe payload to match, got %#v", appErr)
	}

	delegateCallTx := soulTestSafeExecTx(t, &safePayload, 1)
	if appErr := validateSoulExecutionTransactionMatchesPayload(delegateCallTx, expect); appErr == nil {
		t.Fatalf("expected delegatecall operation mismatch")
	}
}

func soulTestSafeExecTx(t *testing.T, payload *safeTxPayload, operation uint8) *types.Transaction {
	t.Helper()
	to := common.HexToAddress(payload.To)
	value, ok := parseSoulOperationPayloadValue(payload.Value)
	if !ok {
		t.Fatalf("invalid value %q", payload.Value)
	}
	innerData, err := hexutil.Decode(payload.Data)
	if err != nil {
		t.Fatalf("decode payload data: %v", err)
	}
	data, err := safeExecReceiptABI.Pack(
		"execTransaction",
		to,
		value,
		innerData,
		operation,
		big.NewInt(0),
		big.NewInt(0),
		big.NewInt(0),
		common.Address{},
		common.Address{},
		[]byte{0x01},
	)
	if err != nil {
		t.Fatalf("pack safe exec: %v", err)
	}
	safeAddress := common.HexToAddress(payload.SafeAddress)
	return types.NewTx(&types.LegacyTx{To: &safeAddress, Value: big.NewInt(0), Data: data})
}

func TestValidateSoulRotateWalletExecutionEffect_Success(t *testing.T) {
	t.Parallel()

	s := &Server{}
	agentIDHex := "0x" + strings.Repeat("11", 32)
	newWallet := testEthAddress3
	payload := soulTestRotatePayload(t, testEthAddress1, agentIDHex, newWallet)
	agentID, _ := new(big.Int).SetString(strings.TrimPrefix(agentIDHex, "0x"), 16)
	receipt := &types.Receipt{
		Status:      1,
		BlockNumber: big.NewInt(456),
		GasUsed:     100,
		Logs: []*types.Log{{
			Address: common.HexToAddress(payload.To),
			Topics:  []common.Hash{walletRotatedTopic, topicBig(agentID), topicAddress(common.HexToAddress(testEthAddress2)), topicAddress(common.HexToAddress(newWallet))},
		}},
	}
	client := &fakeEthClient{callContract: soulTestCallContractForWalletPrincipal(t, newWallet, common.Address{}.Hex())}
	data, _ := hexutil.Decode(payload.Data)
	expect := soulOperationReceiptExpectation{Payload: payload, To: common.HexToAddress(payload.To), Value: big.NewInt(0), Data: data}
	op := &models.SoulOperation{Kind: models.SoulOperationKindRotateWallet, AgentID: agentIDHex}

	if appErr := s.validateSoulRotateWalletExecutionEffect(context.Background(), client, op, expect, receipt); appErr != nil {
		t.Fatalf("expected rotate effect to validate, got %#v", appErr)
	}
}

func TestValidateSoulBurnExecutionEffect_Success(t *testing.T) {
	t.Parallel()

	s := &Server{}
	agentIDHex := "0x" + strings.Repeat("11", 32)
	agentID, _ := new(big.Int).SetString(strings.TrimPrefix(agentIDHex, "0x"), 16)
	data, err := soul.EncodeBurnSoulCall(agentID)
	if err != nil {
		t.Fatalf("EncodeBurnSoulCall: %v", err)
	}
	payload := &safeTxPayload{To: testEthAddress1, Value: "0", Data: hexutil.Encode(data)}
	expect := soulOperationReceiptExpectation{Payload: payload, To: common.HexToAddress(payload.To), Value: big.NewInt(0), Data: data}
	receipt := &types.Receipt{Status: 1, Logs: []*types.Log{{Address: common.HexToAddress(payload.To), Topics: []common.Hash{soulBurnedTopic, topicBig(agentID), topicAddress(common.HexToAddress(testEthAddress2))}}}}

	parsedABI, err := abi.JSON(strings.NewReader(soul.SoulRegistryABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}
	walletRet, _ := parsedABI.Methods["getAgentWallet"].Outputs.Pack(common.Address{})
	client := &fakeEthClient{callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
		if bytes.HasPrefix(msg.Data, parsedABI.Methods["getAgentWallet"].ID) {
			return walletRet, nil
		}
		return nil, ethereum.NotFound
	}}
	op := &models.SoulOperation{Kind: models.SoulOperationKindBurn, AgentID: agentIDHex}
	if appErr := s.validateSoulOperationSuccessEffect(context.Background(), client, op, expect, receipt); appErr != nil {
		t.Fatalf("expected burn effect to validate, got %#v", appErr)
	}
}

func TestSoulOperationReceiptHelperBranches(t *testing.T) {
	t.Parallel()

	if _, ok := parseSoulOperationPayloadValue("not-a-number"); ok {
		t.Fatalf("expected invalid payload value")
	}
	if _, appErr := parseSoulOperationReceiptExpectation(&models.SoulOperation{}); appErr == nil {
		t.Fatalf("expected missing payload mismatch")
	}
	if receiptLogs(nil) != nil {
		t.Fatalf("expected nil receipt logs")
	}
	if topicBig(nil) != (common.Hash{}) {
		t.Fatalf("expected nil big topic to be zero")
	}
	if !bigIntsEqual(nil, big.NewInt(0)) || bigIntsEqual(big.NewInt(1), nil) {
		t.Fatalf("unexpected big int equality")
	}
	if !abiOperationIsCall(uint8(0)) || abiOperationIsCall(uint8(1)) || !abiOperationIsCall(big.NewInt(0)) || abiOperationIsCall(big.NewInt(1)) || abiOperationIsCall("0") {
		t.Fatalf("unexpected Safe operation handling")
	}
}

func TestValidateSoulPublishRootExecutionEffect_SuccessAndMismatch(t *testing.T) {
	t.Parallel()

	root := common.HexToHash("0x" + strings.Repeat("aa", 32))
	data, err := soulattestations.EncodePublishRootCall(root, 123, 2)
	if err != nil {
		t.Fatalf("EncodePublishRootCall: %v", err)
	}
	payload := &safeTxPayload{To: testEthAddress1, Value: "0", Data: hexutil.Encode(data)}
	expect := soulOperationReceiptExpectation{Payload: payload, To: common.HexToAddress(payload.To), Value: big.NewInt(0), Data: data}
	op := &models.SoulOperation{Kind: models.SoulOperationKindPublishValidationRoot}
	receipt := &types.Receipt{Status: 1, Logs: []*types.Log{{Address: common.HexToAddress(payload.To), Topics: []common.Hash{rootPublishedTopic, root}}}}

	if appErr := validateSoulPublishRootExecutionEffect(op, expect, receipt); appErr != nil {
		t.Fatalf("expected publish root receipt to validate, got %#v", appErr)
	}
	receipt.Logs[0].Topics[1] = common.HexToHash("0x" + strings.Repeat("bb", 32))
	if appErr := validateSoulPublishRootExecutionEffect(op, expect, receipt); appErr == nil {
		t.Fatalf("expected root mismatch")
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
	if got := soulReceiptSnapshotJSON("0x"+strings.Repeat("ab", 32), receipt); strings.TrimSpace(got) == "" {
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
	newWallet := testEthAddress3
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
		*dest = models.SoulAgentIdentity{
			AgentID: agentID,
			Wallet:  oldWallet,
			LocalID: "ops",
			Domain:  "dev.simulacrum.greater.website",
			Status:  models.SoulAgentStatusActive,
		}
	}).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:     agentID,
			ChannelType: models.SoulChannelTypeENS,
			Identifier:  "ops.lessersoul.eth",
			Status:      models.SoulChannelStatusActive,
			UpdatedAt:   time.Now().UTC(),
		}
	}).Once()
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{
			ENSName: "ops.lessersoul.eth",
			AgentID: agentID,
			Wallet:  oldWallet,
		}
	}).Once()

	tdb.qWalletAgent.On("Delete").Return(nil).Once()
	tdb.qWalletAgent.On("CreateOrUpdate").Return(nil).Once()
	tdb.qENS.On("Update", "Wallet", "UpdatedAt").Return(nil).Once()

	op := &models.SoulOperation{
		Kind:    models.SoulOperationKindRotateWallet,
		AgentID: agentID,
	}
	if err := s.applySoulOperationSideEffects(context.Background(), client, op); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}
