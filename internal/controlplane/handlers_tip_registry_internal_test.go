package controlplane

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
	"github.com/equaltoai/lesser-host/internal/tips"
)

type tipRegistryTestDB struct {
	db         *ttmocks.MockExtendedDB
	qReg       *ttmocks.MockQuery
	qOp        *ttmocks.MockQuery
	qAudit     *ttmocks.MockQuery
	qWalletIdx *ttmocks.MockQuery
	qUser      *ttmocks.MockQuery
}

func newTipRegistryTestDB() tipRegistryTestDB {
	db := ttmocks.NewMockExtendedDB()
	qReg := new(ttmocks.MockQuery)
	qOp := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	qWalletIdx := new(ttmocks.MockQuery)
	qUser := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.TipHostRegistration")).Return(qReg).Maybe()
	db.On("Model", mock.AnythingOfType("*models.TipRegistryOperation")).Return(qOp).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WalletIndex")).Return(qWalletIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.User")).Return(qUser).Maybe()

	for _, q := range []*ttmocks.MockQuery{qReg, qOp, qAudit, qWalletIdx, qUser} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
	}
	qReg.On("Create").Return(nil).Maybe()
	qAudit.On("Create").Return(nil).Maybe()

	return tipRegistryTestDB{
		db:         db,
		qReg:       qReg,
		qOp:        qOp,
		qAudit:     qAudit,
		qWalletIdx: qWalletIdx,
		qUser:      qUser,
	}
}

func TestHandleTipHostRegistrationBegin_Success(t *testing.T) {
	t.Parallel()

	tdb := newTipRegistryTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			TipEnabled:          true,
			TipChainID:          1,
			TipContractAddress:  "0x0000000000000000000000000000000000000001",
			TipTxMode:           tipTxModeSafe,
			TipAdminSafeAddress: "0x0000000000000000000000000000000000000002",
		},
	}

	body, _ := json.Marshal(tipHostRegistrationBeginRequest{
		Domain:     "example.com",
		WalletAddr: "0x000000000000000000000000000000000000dEaD",
		HostFeeBps: 5,
	})
	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	resp, err := s.handleTipHostRegistrationBegin(&apptheory.Context{RequestID: "r1", Request: apptheory.Request{Body: body}})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 201 {
		t.Fatalf("expected 201, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out tipHostRegistrationBeginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Registration.ID == "" || out.Wallet.Message == "" || out.Wallet.Nonce == "" {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.Registration.DomainNormalized != testDomainExampleCom {
		t.Fatalf("expected normalized domain, got %#v", out.Registration.DomainNormalized)
	}
	if out.Wallet.Address != strings.ToLower("0x000000000000000000000000000000000000dEaD") {
		t.Fatalf("expected wallet lowercased, got %#v", out.Wallet.Address)
	}
	if len(out.Proofs) != 2 {
		t.Fatalf("expected 2 proof instructions, got %#v", out.Proofs)
	}
	if out.Proofs[0].DNSValue == "" || out.Proofs[0].DNSValue != out.Proofs[1].HTTPSBody {
		t.Fatalf("expected shared proof value, got %#v", out.Proofs)
	}
	if !strings.HasPrefix(out.Proofs[0].DNSName, tipRegistryProofPrefix) {
		t.Fatalf("unexpected dns name: %#v", out.Proofs[0].DNSName)
	}
}

func TestValidateOutboundHost_AndIsDeniedIP(t *testing.T) {
	t.Parallel()

	if err := validateOutboundHost(context.TODO(), " "); err == nil {
		t.Fatalf("expected error")
	}
	if err := validateOutboundHost(context.TODO(), "localhost"); err == nil {
		t.Fatalf("expected localhost blocked")
	}
	if err := validateOutboundHost(context.TODO(), "127.0.0.1"); err == nil {
		t.Fatalf("expected loopback blocked")
	}
	if err := validateOutboundHost(context.TODO(), "8.8.8.8"); err != nil {
		t.Fatalf("expected public ip allowed, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validateOutboundHost(ctx, "example.com"); err == nil {
		t.Fatalf("expected canceled ctx to fail resolve")
	}
}

func TestCreateTipRegistryOperationForRegistration_ValidatesAndStores(t *testing.T) {
	t.Parallel()

	tdb := newTipRegistryTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			TipEnabled:          true,
			TipChainID:          1,
			TipContractAddress:  "0x0000000000000000000000000000000000000001",
			TipTxMode:           tipTxModeSafe,
			TipAdminSafeAddress: "0x0000000000000000000000000000000000000002",
		},
	}

	tdb.qOp.On("Create").Return(nil).Once()
	if _, _, err := s.createTipRegistryOperationForRegistration(context.Background(), nil); err == nil {
		t.Fatalf("expected error")
	}

	// Safe mode requires safe configured.
	s2 := &Server{store: store.New(tdb.db), cfg: config.Config{TipContractAddress: "0x0000000000000000000000000000000000000001", TipTxMode: tipTxModeSafe}}
	if _, _, err := s2.createTipRegistryOperationForRegistration(context.Background(), &models.TipHostRegistration{Kind: models.TipRegistryOperationKindRegisterHost}); err == nil {
		t.Fatalf("expected conflict for missing safe address")
	}

	// Success path.
	reg := &models.TipHostRegistration{
		ID:               "r1",
		Kind:             models.TipRegistryOperationKindRegisterHost,
		DomainRaw:        "example.com",
		DomainNormalized: "example.com",
		HostIDHex:        "0x" + strings.Repeat("11", 32),
		WalletAddr:       "0x0000000000000000000000000000000000000003",
		HostFeeBps:       5,
	}
	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	op, safeTx, err := s.createTipRegistryOperationForRegistration(context.Background(), reg)
	if err != nil || op == nil || safeTx == nil {
		t.Fatalf("unexpected: op=%#v safeTx=%#v err=%v", op, safeTx, err)
	}
	if safeTx.To == "" || !strings.HasPrefix(safeTx.Data, "0x") {
		t.Fatalf("unexpected safe tx: %#v", safeTx)
	}
}

func TestTipRegistryAdminEndpoints_ListAndGet(t *testing.T) {
	t.Parallel()

	tdb := newTipRegistryTestDB()
	s := &Server{store: store.New(tdb.db)}

	ctx := &apptheory.Context{AuthIdentity: "alice"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	tdb.qOp.On("All", mock.AnythingOfType("*[]*models.TipRegistryOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.TipRegistryOperation](t, args, 0)
		*dest = []*models.TipRegistryOperation{
			nil,
			{ID: "op1", Status: models.TipRegistryOperationStatusPending},
		}
	}).Once()
	if resp, err := s.handleListTipRegistryOperations(ctx); err != nil || resp.Status != 200 {
		t.Fatalf("list ops: resp=%#v err=%v", resp, err)
	}

	tdb.qOp.On("First", mock.AnythingOfType("*models.TipRegistryOperation")).Return(theoryErrors.ErrItemNotFound).Once()
	getCtx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"id": "missing"},
	}
	getCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	if _, err := s.handleGetTipRegistryOperation(getCtx); err == nil {
		t.Fatalf("expected not_found")
	}
}

func TestTipRegistry_MoreHelperCoverage(t *testing.T) {
	t.Parallel()

	// requireTipRPCConfigured
	s := &Server{cfg: config.Config{}}
	if err := s.requireTipRPCConfigured(); err == nil {
		t.Fatalf("expected conflict when rpc url not set")
	}
	s.cfg.TipRPCURL = "https://rpc"
	if err := s.requireTipRPCConfigured(); err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	// requireTipSafeConfigured
	s.cfg.TipTxMode = tipTxModeSafe
	s.cfg.TipAdminSafeAddress = "not an address"
	if err := s.requireTipSafeConfigured(); err == nil {
		t.Fatalf("expected safe conflict")
	}
	s.cfg.TipAdminSafeAddress = "0x0000000000000000000000000000000000000002"
	if err := s.requireTipSafeConfigured(); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	s.cfg.TipTxMode = "direct"
	s.cfg.TipAdminSafeAddress = ""
	if err := s.requireTipSafeConfigured(); err != nil {
		t.Fatalf("expected safe skipped when not in safe mode")
	}

	// isHexHash32
	if !isHexHash32("0x" + strings.Repeat("ab", 32)) {
		t.Fatalf("expected valid hash")
	}
	if !isHexHash32(strings.Repeat("ab", 32)) {
		t.Fatalf("expected valid hash without 0x")
	}
	if isHexHash32("0x1234") {
		t.Fatalf("expected invalid short hash")
	}
	if isHexHash32("0x" + strings.Repeat("zz", 32)) {
		t.Fatalf("expected invalid hex")
	}

	// receipt snapshots
	r := &types.Receipt{
		Status:            1,
		GasUsed:           123,
		ContractAddress:   common.HexToAddress("0x0000000000000000000000000000000000000001"),
		BlockNumber:       big.NewInt(99),
		EffectiveGasPrice: big.NewInt(456),
		Logs:              []*types.Log{{}, {}},
	}
	snapJSON := tipRegistryReceiptSnapshotJSON(" 0xabc ", r)
	if strings.TrimSpace(snapJSON) == "" {
		t.Fatalf("expected non-empty snapshot json")
	}

	if got := tipRegistryBlockNumber(nil); got != 0 {
		t.Fatalf("expected 0 for nil receipt")
	}
	if got := tipRegistryBlockNumber(&types.Receipt{}); got != 0 {
		t.Fatalf("expected 0 for nil block number")
	}
	neg := big.NewInt(-1)
	if got := tipRegistryBlockNumber(&types.Receipt{BlockNumber: neg}); got != 0 {
		t.Fatalf("expected 0 for negative block number")
	}
	huge := new(big.Int).Lsh(big.NewInt(1), 80)
	if got := tipRegistryBlockNumber(&types.Receipt{BlockNumber: huge}); got != 0 {
		t.Fatalf("expected 0 for huge block number")
	}
}

func TestLoadAndCompleteTipHostRegistrationForVerify(t *testing.T) {
	t.Parallel()

	tdb := newTipRegistryTestDB()
	s := &Server{store: store.New(tdb.db)}
	ctx := &apptheory.Context{}

	// Not found.
	tdb.qReg.On("First", mock.AnythingOfType("*models.TipHostRegistration")).Return(theoryErrors.ErrItemNotFound).Once()
	_, appErr := s.loadTipHostRegistrationForVerify(ctx, "missing")
	require.NotNil(t, appErr)

	// Expired registration.
	tdb.qReg.On("First", mock.AnythingOfType("*models.TipHostRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.TipHostRegistration](t, args, 0)
		*dest = models.TipHostRegistration{ID: "r1", ExpiresAt: time.Now().UTC().Add(-1 * time.Hour)}
	}).Once()
	_, appErr = s.loadTipHostRegistrationForVerify(ctx, "r1")
	require.NotNil(t, appErr)

	// Completed registration.
	tdb.qReg.On("First", mock.AnythingOfType("*models.TipHostRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.TipHostRegistration](t, args, 0)
		*dest = models.TipHostRegistration{ID: "r2", Status: models.TipHostRegistrationStatusCompleted}
	}).Once()
	_, appErr = s.loadTipHostRegistrationForVerify(ctx, "r2")
	require.NotNil(t, appErr)

	// Happy path.
	tdb.qReg.On("First", mock.AnythingOfType("*models.TipHostRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.TipHostRegistration](t, args, 0)
		*dest = models.TipHostRegistration{ID: "r3", Status: models.TipHostRegistrationStatusPending}
	}).Once()
	reg, appErr := s.loadTipHostRegistrationForVerify(ctx, "r3")
	require.Nil(t, appErr)
	require.NotNil(t, reg)
	require.Equal(t, "r3", reg.ID)

	_, _, appErr = verifyTipHostRegistrationProofs(context.TODO(), nil, requiredProofSet{})
	require.NotNil(t, appErr)

	reg.DNSVerified = true
	reg.HTTPSVerified = false
	vDNS, vHTTPS, appErr := verifyTipHostRegistrationProofs(context.TODO(), reg, requiredProofSet{})
	require.Nil(t, appErr)
	require.True(t, vDNS)
	require.False(t, vHTTPS)

	// enforceTipRegistryUpdateProofPolicy: non-update kind is a no-op.
	require.NotNil(t, s.enforceTipRegistryUpdateProofPolicy(context.Background(), nil, false, false))
	require.Nil(t, s.enforceTipRegistryUpdateProofPolicy(context.Background(), &models.TipHostRegistration{Kind: models.TipRegistryOperationKindRegisterHost}, false, false))

	// completeTipHostRegistration updates verification flags.
	now := time.Unix(123, 0).UTC()
	reg = &models.TipHostRegistration{
		ID:               "r1",
		Kind:             models.TipRegistryOperationKindRegisterHost,
		DomainRaw:        "example.com",
		DomainNormalized: "example.com",
		HostIDHex:        "0x" + strings.Repeat("11", 32),
		ChainID:          1,
		WalletType:       "eth",
		WalletAddr:       "0x0000000000000000000000000000000000000003",
		HostFeeBps:       5,
		TxMode:           tipTxModeSafe,
		SafeAddress:      "0x0000000000000000000000000000000000000002",
		WalletNonce:      "n",
		WalletMessage:    "m",
		DNSToken:         "dns",
		HTTPToken:        "http",
		Status:           models.TipHostRegistrationStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(1 * time.Hour),
	}
	_ = reg.UpdateKeys()

	update, appErr := s.completeTipHostRegistration(&apptheory.Context{RequestID: "rid"}, reg, true, false, now)
	require.Nil(t, appErr)
	require.NotNil(t, update)
	require.True(t, update.DNSVerified)
	require.False(t, update.HTTPSVerified)
	require.Equal(t, models.TipHostRegistrationStatusCompleted, update.Status)
}

func TestCreateOrLoadTipRegistryOperation(t *testing.T) {
	t.Parallel()

	tdb := newTipRegistryTestDB()
	s := &Server{store: store.New(tdb.db)}

	if _, err := s.createOrLoadTipRegistryOperation(context.Background(), nil); err == nil {
		t.Fatalf("expected error")
	}

	// Create OK returns op.
	tdb.qOp.On("Create").Return(nil).Once()
	op := &models.TipRegistryOperation{ID: "op1"}
	_ = op.UpdateKeys()
	got, err := s.createOrLoadTipRegistryOperation(context.Background(), op)
	if err != nil || got == nil || got.ID != "op1" {
		t.Fatalf("unexpected got=%#v err=%v", got, err)
	}

	// Condition failed returns existing record when load succeeds.
	tdb.qOp.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	tdb.qOp.On("First", mock.AnythingOfType("*models.TipRegistryOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.TipRegistryOperation](t, args, 0)
		*dest = models.TipRegistryOperation{ID: "existing"}
		_ = dest.UpdateKeys()
	}).Once()
	op2 := &models.TipRegistryOperation{ID: "op2"}
	_ = op2.UpdateKeys()
	got, err = s.createOrLoadTipRegistryOperation(context.Background(), op2)
	if err != nil || got == nil || got.ID != "existing" {
		t.Fatalf("unexpected got=%#v err=%v", got, err)
	}
}

func TestHandleTipHostRegistrationVerify_FailsOnInvalidSignature(t *testing.T) {
	t.Parallel()

	tdb := newTipRegistryTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			TipEnabled:         true,
			TipChainID:         1,
			TipContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	ctx := &apptheory.Context{
		RequestID: "r1",
		Params:    map[string]string{"id": "reg1"},
		Request:   apptheory.Request{Body: []byte(`{"signature":"not-a-signature","proofs":["dns_txt"]}`)},
	}

	tdb.qReg.On("First", mock.AnythingOfType("*models.TipHostRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.TipHostRegistration](t, args, 0)
		*dest = models.TipHostRegistration{
			ID:            "reg1",
			WalletAddr:    "0x0000000000000000000000000000000000000003",
			WalletMessage: "message",
			Status:        models.TipHostRegistrationStatusPending,
		}
		_ = dest.UpdateKeys()
	}).Once()

	if _, err := s.handleTipHostRegistrationVerify(ctx); err == nil {
		t.Fatalf("expected forbidden for invalid signature")
	}
}

func TestHandleSetTipRegistryHostActive_CreatesOrLoadsOperation(t *testing.T) {
	t.Parallel()

	parsedABI, err := abi.JSON(strings.NewReader(tips.TipSplitterABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}

	hostID := tips.HostIDFromDomain("example.com")
	hostCall, _ := tips.EncodeGetHostCall(hostID)
	hostCallHex := "0x" + hex.EncodeToString(hostCall)

	hostWallet := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	ret, err := parsedABI.Methods["hosts"].Outputs.Pack(hostWallet, uint16(10), true)
	if err != nil {
		t.Fatalf("pack hosts outputs: %v", err)
	}
	hostResultHex := "0x" + hex.EncodeToString(ret)

	rpcSrv := newTipRegistryRPCTestServer(t, hostCallHex, hostResultHex, "", "", nil)
	t.Cleanup(rpcSrv.Close)

	tdb := newTipRegistryTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			TipEnabled:          true,
			TipChainID:          1,
			TipRPCURL:           rpcSrv.URL,
			TipContractAddress:  "0x0000000000000000000000000000000000000001",
			TipTxMode:           tipTxModeSafe,
			TipAdminSafeAddress: "0x0000000000000000000000000000000000000002",
		},
	}

	// Create succeeds.
	tdb.qOp.On("Create").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"domain": "example.com"}
	ctx.Request.Body = []byte(`{"active":true}`)
	resp, err := s.handleSetTipRegistryHostActive(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	// Condition failed: load existing.
	tdb.qOp.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	tdb.qOp.On("First", mock.AnythingOfType("*models.TipRegistryOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.TipRegistryOperation](t, args, 0)
		*dest = models.TipRegistryOperation{ID: "existing", Status: models.TipRegistryOperationStatusProposed}
		_ = dest.UpdateKeys()
	}).Once()

	ctx2 := adminCtx()
	ctx2.Params = map[string]string{"domain": "example.com"}
	ctx2.Request.Body = []byte(`{"active":false}`)
	resp, err = s.handleSetTipRegistryHostActive(ctx2)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("existing resp=%#v err=%v", resp, err)
	}
}

func TestHandleSetTipRegistryTokenAllowed_ValidatesAndCreates(t *testing.T) {
	t.Parallel()

	tdb := newTipRegistryTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			TipEnabled:          true,
			TipChainID:          1,
			TipContractAddress:  "0x0000000000000000000000000000000000000001",
			TipTxMode:           tipTxModeSafe,
			TipAdminSafeAddress: "0x0000000000000000000000000000000000000002",
		},
	}

	// Invalid address.
	invalidCtx := adminCtx()
	invalidCtx.Request.Body = []byte(`{"token_address":"nope","allowed":true}`)
	if _, err := s.handleSetTipRegistryTokenAllowed(invalidCtx); err == nil {
		t.Fatalf("expected validation error")
	}

	// Success.
	tdb.qOp.On("Create").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	ctx := adminCtx()
	ctx.Request.Body = []byte(`{"token_address":"0x00000000000000000000000000000000000000Ff","allowed":true}`)
	resp, err := s.handleSetTipRegistryTokenAllowed(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
}

func TestHandleEnsureTipRegistryHost_CreatesAutoOperation(t *testing.T) {
	t.Parallel()

	tdb := newTipRegistryTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			TipEnabled:                  true,
			TipChainID:                  1,
			TipContractAddress:          "0x0000000000000000000000000000000000000001",
			TipTxMode:                   tipTxModeSafe,
			TipAdminSafeAddress:         "0x0000000000000000000000000000000000000002",
			TipDefaultHostWalletAddress: "0x0000000000000000000000000000000000000003",
			TipDefaultHostFeeBps:        250,
		},
	}

	tdb.qOp.On("Create").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Maybe()
	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"domain": "example.com"}
	resp, err := s.handleEnsureTipRegistryHost(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
}

func TestHandleRecordTipRegistryOperationExecution_Validations(t *testing.T) {
	t.Parallel()

	tdb := newTipRegistryTestDB()

	// Tip registry not configured.
	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": "op1"}
	ctx.Request.Body = []byte("{\"exec_tx_hash\":\"0x" + strings.Repeat("11", 32) + "\"}")
	if _, err := s.handleRecordTipRegistryOperationExecution(ctx); err == nil {
		t.Fatalf("expected conflict")
	}

	// RPC not configured.
	s2 := &Server{store: store.New(tdb.db), cfg: config.Config{TipEnabled: true, TipChainID: 1, TipContractAddress: "0x0000000000000000000000000000000000000001"}}
	ctx2 := adminCtx()
	ctx2.Params = map[string]string{"id": "op1"}
	ctx2.Request.Body = []byte("{\"exec_tx_hash\":\"0x" + strings.Repeat("11", 32) + "\"}")
	if _, err := s2.handleRecordTipRegistryOperationExecution(ctx2); err == nil {
		t.Fatalf("expected conflict")
	}

	// Missing id.
	s3 := &Server{store: store.New(tdb.db), cfg: config.Config{TipEnabled: true, TipChainID: 1, TipContractAddress: "0x0000000000000000000000000000000000000001", TipRPCURL: "https://rpc"}}
	ctx3 := adminCtx()
	ctx3.Request.Body = []byte("{\"exec_tx_hash\":\"0x" + strings.Repeat("11", 32) + "\"}")
	if _, err := s3.handleRecordTipRegistryOperationExecution(ctx3); err == nil {
		t.Fatalf("expected bad_request")
	}

	// Invalid tx hash.
	ctx4 := adminCtx()
	ctx4.Params = map[string]string{"id": "op1"}
	ctx4.Request.Body = []byte(`{"exec_tx_hash":"nope"}`)
	if _, err := s3.handleRecordTipRegistryOperationExecution(ctx4); err == nil {
		t.Fatalf("expected bad_request")
	}

	// Operation not found.
	tdb.qOp.On("First", mock.AnythingOfType("*models.TipRegistryOperation")).Return(theoryErrors.ErrItemNotFound).Once()
	ctx5 := adminCtx()
	ctx5.Params = map[string]string{"id": "missing"}
	ctx5.Request.Body = []byte("{\"exec_tx_hash\":\"0x" + strings.Repeat("11", 32) + "\"}")
	if _, err := s3.handleRecordTipRegistryOperationExecution(ctx5); err == nil {
		t.Fatalf("expected not_found")
	}
}

func TestTipRegistryOutboundHostValidation_BlocksLocalAndPrivate(t *testing.T) {
	t.Parallel()

	if err := validateOutboundHost(context.Background(), ""); err == nil {
		t.Fatalf("expected error for empty host")
	}
	if err := validateOutboundHost(context.Background(), "localhost"); err == nil {
		t.Fatalf("expected error for localhost")
	}
	if err := validateOutboundHost(context.Background(), "127.0.0.1"); err == nil {
		t.Fatalf("expected error for loopback ip")
	}
	if err := validateOutboundHost(context.Background(), "10.0.0.1"); err == nil {
		t.Fatalf("expected error for rfc1918 ip")
	}
	if err := validateOutboundHost(context.Background(), "8.8.8.8"); err != nil {
		t.Fatalf("expected public ip to be allowed, got %v", err)
	}

	if verifyTipRegistryDNS(context.Background(), "", "proof") {
		t.Fatalf("expected dns proof to fail with empty domain")
	}
	if verifyTipRegistryHTTPS(context.Background(), "127.0.0.1", "proof") {
		t.Fatalf("expected https proof to fail for blocked host")
	}
}
