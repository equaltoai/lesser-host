package controlplane

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
	"github.com/equaltoai/lesser-host/internal/tips"
)

func requireTipRegistryAppErrCode(t *testing.T, err error, want string) {
	t.Helper()

	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected *apptheory.AppError, got %T: %v", err, err)
	}
	if appErr.Code != want {
		t.Fatalf("expected app error %q, got %q", want, appErr.Code)
	}
}

func newConfiguredTipRegistryServer(tdb tipRegistryTestDB) *Server {
	return &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			TipEnabled:          true,
			TipChainID:          1,
			TipContractAddress:  "0x0000000000000000000000000000000000000001",
			TipTxMode:           tipTxModeSafe,
			TipAdminSafeAddress: "0x0000000000000000000000000000000000000002",
		},
	}
}

func TestTipRegistryWalletHelpers_Branches(t *testing.T) {
	t.Parallel()

	t.Run("normalize_wallet_rejects_empty_invalid_reserved_and_privileged", func(t *testing.T) {
		t.Parallel()

		tdb := newTipRegistryTestDB()
		s := newConfiguredTipRegistryServer(tdb)

		_, err := s.normalizeTipRegistryWalletAddress(context.Background(), " ")
		requireTipRegistryAppErrCode(t, err, "app.bad_request")

		_, err = s.normalizeTipRegistryWalletAddress(context.Background(), "not-an-address")
		requireTipRegistryAppErrCode(t, err, "app.bad_request")

		_, err = s.normalizeTipRegistryWalletAddress(context.Background(), reservedWalletLesserHostAdmin)
		requireTipRegistryAppErrCode(t, err, "app.bad_request")

		tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.WalletIndex](t, args, 0)
			*dest = models.WalletIndex{Username: "alice"}
		}).Once()
		tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.User](t, args, 0)
			*dest = models.User{Username: "alice", Role: models.RoleAdmin}
		}).Once()

		_, err = s.normalizeTipRegistryWalletAddress(context.Background(), "0x00000000000000000000000000000000000000aa")
		requireTipRegistryAppErrCode(t, err, "app.bad_request")
	})

	t.Run("normalize_wallet_success_lowercases", func(t *testing.T) {
		t.Parallel()

		tdb := newTipRegistryTestDB()
		s := newConfiguredTipRegistryServer(tdb)

		tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()

		got, err := s.normalizeTipRegistryWalletAddress(context.Background(), "0x00000000000000000000000000000000000000Aa")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got != "0x00000000000000000000000000000000000000aa" {
			t.Fatalf("expected lowercased address, got %q", got)
		}
	})

	t.Run("wallet_from_registration_branches", func(t *testing.T) {
		t.Parallel()

		tdb := newTipRegistryTestDB()
		s := newConfiguredTipRegistryServer(tdb)

		_, _, appErr := s.tipRegistryWalletFromRegistration(context.Background(), &models.TipHostRegistration{WalletAddr: "nope"})
		if appErr == nil || appErr.Code != appErrCodeBadRequest {
			t.Fatalf("expected invalid wallet error, got %#v", appErr)
		}

		_, _, appErr = s.tipRegistryWalletFromRegistration(context.Background(), &models.TipHostRegistration{WalletAddr: reservedWalletTipSplitterLesser})
		if appErr == nil || appErr.Code != appErrCodeBadRequest {
			t.Fatalf("expected reserved wallet error, got %#v", appErr)
		}

		tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
		wallet, lower, appErr := s.tipRegistryWalletFromRegistration(context.Background(), &models.TipHostRegistration{WalletAddr: "0x00000000000000000000000000000000000000Bb"})
		if appErr != nil {
			t.Fatalf("unexpected appErr: %#v", appErr)
		}
		if lower != "0x00000000000000000000000000000000000000bb" || wallet != common.HexToAddress(lower) {
			t.Fatalf("unexpected wallet result: wallet=%s lower=%q", wallet.Hex(), lower)
		}
	})
}

func TestHandleTipHostRegistrationBegin_Branches(t *testing.T) {
	t.Parallel()

	t.Run("invalid_kind", func(t *testing.T) {
		t.Parallel()

		tdb := newTipRegistryTestDB()
		s := newConfiguredTipRegistryServer(tdb)

		body, _ := json.Marshal(tipHostRegistrationBeginRequest{
			Kind:       "nope",
			Domain:     "example.com",
			WalletAddr: "0x000000000000000000000000000000000000dEaD",
			HostFeeBps: 5,
		})
		_, err := s.handleTipHostRegistrationBegin(&apptheory.Context{Request: apptheory.Request{Body: body}})
		requireTipRegistryAppErrCode(t, err, "app.bad_request")
	})

	t.Run("invalid_domain", func(t *testing.T) {
		t.Parallel()

		tdb := newTipRegistryTestDB()
		s := newConfiguredTipRegistryServer(tdb)

		body, _ := json.Marshal(tipHostRegistrationBeginRequest{
			Domain:     "http://example.com",
			WalletAddr: "0x000000000000000000000000000000000000dEaD",
			HostFeeBps: 5,
		})
		_, err := s.handleTipHostRegistrationBegin(&apptheory.Context{Request: apptheory.Request{Body: body}})
		requireTipRegistryAppErrCode(t, err, "app.bad_request")
	})

	t.Run("host_fee_out_of_range", func(t *testing.T) {
		t.Parallel()

		tdb := newTipRegistryTestDB()
		s := newConfiguredTipRegistryServer(tdb)

		tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()

		body, _ := json.Marshal(tipHostRegistrationBeginRequest{
			Domain:     "example.com",
			WalletAddr: "0x000000000000000000000000000000000000dEaD",
			HostFeeBps: 501,
		})
		_, err := s.handleTipHostRegistrationBegin(&apptheory.Context{Request: apptheory.Request{Body: body}})
		requireTipRegistryAppErrCode(t, err, "app.bad_request")
	})

	t.Run("registration_create_failure", func(t *testing.T) {
		t.Parallel()

		db := ttmocks.NewMockExtendedDB()
		qReg := new(ttmocks.MockQuery)
		qWalletIdx := new(ttmocks.MockQuery)
		qUser := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db).Maybe()
		db.On("Model", mock.AnythingOfType("*models.TipHostRegistration")).Return(qReg).Maybe()
		db.On("Model", mock.AnythingOfType("*models.WalletIndex")).Return(qWalletIdx).Maybe()
		db.On("Model", mock.AnythingOfType("*models.User")).Return(qUser).Maybe()

		for _, q := range []*ttmocks.MockQuery{qReg, qWalletIdx, qUser} {
			q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
			q.On("Index", mock.Anything).Return(q).Maybe()
			q.On("Limit", mock.Anything).Return(q).Maybe()
			q.On("IfExists").Return(q).Maybe()
			q.On("IfNotExists").Return(q).Maybe()
			q.On("Delete").Return(nil).Maybe()
			q.On("Update", mock.Anything).Return(nil).Maybe()
		}

		qReg.ExpectedCalls = nil
		qReg.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qReg).Maybe()
		qReg.On("Index", mock.Anything).Return(qReg).Maybe()
		qReg.On("Limit", mock.Anything).Return(qReg).Maybe()
		qReg.On("IfExists").Return(qReg).Maybe()
		qReg.On("IfNotExists").Return(qReg).Maybe()
		qReg.On("Delete").Return(nil).Maybe()
		qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
		qReg.On("Create").Return(errors.New("boom")).Once()

		s := &Server{
			store: store.New(db),
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
		_, err := s.handleTipHostRegistrationBegin(&apptheory.Context{Request: apptheory.Request{Body: body}})
		requireTipRegistryAppErrCode(t, err, "app.internal")
	})
}

func TestCreateTipRegistryOperationForRegistration_Branches(t *testing.T) {
	t.Parallel()

	makeReg := func() *models.TipHostRegistration {
		return &models.TipHostRegistration{
			ID:               "reg1",
			Kind:             models.TipRegistryOperationKindRegisterHost,
			DomainRaw:        "Example.COM",
			DomainNormalized: "example.com",
			HostIDHex:        tips.HostIDFromDomain("example.com").Hex(),
			WalletAddr:       "0x0000000000000000000000000000000000000003",
			HostFeeBps:       5,
		}
	}

	t.Run("condition_failed_loads_existing_operation", func(t *testing.T) {
		t.Parallel()

		tdb := newTipRegistryTestDB()
		s := newConfiguredTipRegistryServer(tdb)

		tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
		tdb.qOp.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
		tdb.qOp.On("First", mock.AnythingOfType("*models.TipRegistryOperation")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.TipRegistryOperation](t, args, 0)
			*dest = models.TipRegistryOperation{
				ID:              "existing",
				Kind:            models.TipRegistryOperationKindRegisterHost,
				Status:          models.TipRegistryOperationStatusProposed,
				ContractAddress: "0x0000000000000000000000000000000000000001",
				TxTo:            "0x0000000000000000000000000000000000000001",
				TxData:          "0x1234",
				TxValue:         "0",
			}
		}).Once()

		op, safeTx, err := s.createTipRegistryOperationForRegistration(context.Background(), makeReg())
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if op == nil || op.ID != "existing" {
			t.Fatalf("expected existing operation, got %#v", op)
		}
		if safeTx == nil || safeTx.SafeAddress == "" || safeTx.To == "" {
			t.Fatalf("unexpected safe tx: %#v", safeTx)
		}
	})

	t.Run("create_failure_returns_internal", func(t *testing.T) {
		t.Parallel()

		tdb := newTipRegistryTestDB()
		s := newConfiguredTipRegistryServer(tdb)

		tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
		tdb.qOp.On("Create").Return(errors.New("boom")).Once()

		_, _, err := s.createTipRegistryOperationForRegistration(context.Background(), makeReg())
		requireTipRegistryAppErrCode(t, err, "app.internal")
	})
}

func TestHandleTipRegistryAdminReadHandlers_Branches(t *testing.T) {
	t.Parallel()

	t.Run("list_internal_error", func(t *testing.T) {
		t.Parallel()

		tdb := newTipRegistryTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qOp.On("All", mock.AnythingOfType("*[]*models.TipRegistryOperation")).Return(errors.New("boom")).Once()

		_, err := s.handleListTipRegistryOperations(adminCtx())
		requireTipRegistryAppErrCode(t, err, "app.internal")
	})

	t.Run("get_missing_id", func(t *testing.T) {
		t.Parallel()

		tdb := newTipRegistryTestDB()
		s := &Server{store: store.New(tdb.db)}

		ctx := adminCtx()
		ctx.Params = map[string]string{"id": " "}
		_, err := s.handleGetTipRegistryOperation(ctx)
		requireTipRegistryAppErrCode(t, err, "app.bad_request")
	})

	t.Run("get_internal_error", func(t *testing.T) {
		t.Parallel()

		tdb := newTipRegistryTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qOp.On("First", mock.AnythingOfType("*models.TipRegistryOperation")).Return(errors.New("boom")).Once()

		ctx := adminCtx()
		ctx.Params = map[string]string{"id": "op1"}
		_, err := s.handleGetTipRegistryOperation(ctx)
		requireTipRegistryAppErrCode(t, err, "app.internal")
	})

	t.Run("get_success", func(t *testing.T) {
		t.Parallel()

		tdb := newTipRegistryTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qOp.On("First", mock.AnythingOfType("*models.TipRegistryOperation")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.TipRegistryOperation](t, args, 0)
			*dest = models.TipRegistryOperation{ID: "op2", Status: models.TipRegistryOperationStatusProposed}
		}).Once()

		ctx := adminCtx()
		ctx.Params = map[string]string{"id": "op2"}
		resp, err := s.handleGetTipRegistryOperation(ctx)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != 200 {
			t.Fatalf("expected 200, got %d", resp.Status)
		}

		var out models.TipRegistryOperation
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.ID != "op2" {
			t.Fatalf("expected op2, got %#v", out)
		}
	})
}

func TestRequireTipRegistryHostRegistered_Branches(t *testing.T) {
	t.Parallel()

	hostID := tips.HostIDFromDomain("example.com")
	parsedABI, err := abi.JSON(strings.NewReader(tips.TipSplitterABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}

	hostCall, _ := tips.EncodeGetHostCall(hostID)
	hostCallHex := "0x" + hex.EncodeToString(hostCall)

	packHostResult := func(wallet common.Address) string {
		ret, err := parsedABI.Methods["hosts"].Outputs.Pack(wallet, uint16(10), true)
		if err != nil {
			t.Fatalf("pack hosts outputs: %v", err)
		}
		return "0x" + hex.EncodeToString(ret)
	}

	t.Run("nil_server_and_invalid_contract", func(t *testing.T) {
		t.Parallel()

		appErr := (*Server)(nil).requireTipRegistryHostRegistered(context.Background(), hostID)
		if appErr == nil || appErr.Code != appErrCodeInternal {
			t.Fatalf("expected internal error, got %#v", appErr)
		}

		s := &Server{cfg: config.Config{TipRPCURL: "http://127.0.0.1:1", TipContractAddress: "nope"}}
		appErr = s.requireTipRegistryHostRegistered(context.Background(), hostID)
		if appErr == nil || appErr.Code != appErrCodeConflict {
			t.Fatalf("expected conflict, got %#v", appErr)
		}
	})

	t.Run("dial_failure_returns_internal", func(t *testing.T) {
		t.Parallel()

		s := &Server{cfg: config.Config{
			TipRPCURL:          "http://127.0.0.1:1",
			TipContractAddress: "0x0000000000000000000000000000000000000001",
		}}

		appErr := s.requireTipRegistryHostRegistered(context.Background(), hostID)
		if appErr == nil || appErr.Code != "app.internal" {
			t.Fatalf("expected internal error, got %#v", appErr)
		}
	})

	t.Run("host_not_registered", func(t *testing.T) {
		t.Parallel()

		rpcSrv := newTipRegistryRPCTestServer(t, hostCallHex, packHostResult(common.Address{}), "", "", nil)
		t.Cleanup(rpcSrv.Close)

		s := &Server{cfg: config.Config{
			TipRPCURL:          rpcSrv.URL,
			TipContractAddress: "0x0000000000000000000000000000000000000001",
		}}

		appErr := s.requireTipRegistryHostRegistered(context.Background(), hostID)
		if appErr == nil || appErr.Code != "app.bad_request" {
			t.Fatalf("expected bad_request, got %#v", appErr)
		}
	})

	t.Run("registered_host_returns_nil", func(t *testing.T) {
		t.Parallel()

		rpcSrv := newTipRegistryRPCTestServer(t, hostCallHex, packHostResult(common.HexToAddress("0x00000000000000000000000000000000000000aa")), "", "", nil)
		t.Cleanup(rpcSrv.Close)

		s := &Server{cfg: config.Config{
			TipRPCURL:          rpcSrv.URL,
			TipContractAddress: "0x0000000000000000000000000000000000000001",
		}}

		if appErr := s.requireTipRegistryHostRegistered(context.Background(), hostID); appErr != nil {
			t.Fatalf("expected nil appErr, got %#v", appErr)
		}
	})
}

func TestHandleSetTipRegistryTokenAllowed_ConditionFailedUsesExisting(t *testing.T) {
	t.Parallel()

	tdb := newTipRegistryTestDB()
	s := newConfiguredTipRegistryServer(tdb)

	tdb.qOp.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	tdb.qOp.On("First", mock.AnythingOfType("*models.TipRegistryOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.TipRegistryOperation](t, args, 0)
		allowed := true
		*dest = models.TipRegistryOperation{
			ID:              "existing-token-op",
			Kind:            models.TipRegistryOperationKindSetToken,
			Status:          models.TipRegistryOperationStatusProposed,
			TokenAddress:    "0x00000000000000000000000000000000000000ff",
			TokenAllowed:    &allowed,
			ContractAddress: "0x0000000000000000000000000000000000000001",
			TxTo:            "0x0000000000000000000000000000000000000001",
			TxData:          "0x1234",
			TxValue:         "0",
		}
	}).Once()

	ctx := adminCtx()
	ctx.Request.Body = []byte(`{"token_address":"0x00000000000000000000000000000000000000Ff","allowed":true}`)
	resp, err := s.handleSetTipRegistryTokenAllowed(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	var out createTipRegistryOperationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Operation.ID != "existing-token-op" {
		t.Fatalf("expected existing operation, got %#v", out.Operation)
	}
}

func TestHandleEnsureTipRegistryHost_Branches(t *testing.T) {
	t.Parallel()

	t.Run("missing_store", func(t *testing.T) {
		t.Parallel()

		s := &Server{cfg: config.Config{TipEnabled: true}}
		_, err := s.handleEnsureTipRegistryHost(adminCtx())
		requireTipRegistryAppErrCode(t, err, "app.internal")
	})

	t.Run("default_wallet_not_configured", func(t *testing.T) {
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

		ctx := adminCtx()
		ctx.Params = map[string]string{"domain": "example.com"}
		_, err := s.handleEnsureTipRegistryHost(ctx)
		requireTipRegistryAppErrCode(t, err, "app.conflict")
	})

	t.Run("default_fee_not_configured", func(t *testing.T) {
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
				TipDefaultHostFeeBps:        501,
			},
		}

		ctx := adminCtx()
		ctx.Params = map[string]string{"domain": "example.com"}
		_, err := s.handleEnsureTipRegistryHost(ctx)
		requireTipRegistryAppErrCode(t, err, "app.conflict")
	})

	t.Run("invalid_domain", func(t *testing.T) {
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

		ctx := adminCtx()
		ctx.Params = map[string]string{"domain": "http://example.com"}
		_, err := s.handleEnsureTipRegistryHost(ctx)
		requireTipRegistryAppErrCode(t, err, "app.bad_request")
	})
}

func TestTipRegistryCompleteRegistration_ErrorBranches(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qReg := new(ttmocks.MockQuery)

	configureTipRegistrationQuery(qReg, db)
	s := &Server{store: store.New(db)}

	_, appErr := s.completeTipHostRegistration(nil, nil, false, false, time.Now().UTC())
	if appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error, got %#v", appErr)
	}

	configureTipRegistrationQuery(qReg, db)
	qReg.On("Update", mock.Anything).Return(errors.New("boom")).Once()

	now := time.Unix(123, 0).UTC()
	reg := &models.TipHostRegistration{
		ID:               "reg-fail",
		Kind:             models.TipRegistryOperationKindRegisterHost,
		DomainRaw:        "example.com",
		DomainNormalized: "example.com",
		HostIDHex:        tips.HostIDFromDomain("example.com").Hex(),
		ChainID:          1,
		WalletType:       walletTypeEthereum,
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
		ExpiresAt:        now.Add(time.Hour),
	}

	_, appErr = s.completeTipHostRegistration(&apptheory.Context{}, reg, true, true, now)
	if appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error, got %#v", appErr)
	}
}

func TestTipRegistryMiscHelpers_ErrorBranches(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{TipContractAddress: "not-an-address", TipTxMode: tipTxModeSafe, TipAdminSafeAddress: "nope"}}

	if _, _, appErr := s.tipRegistryContractAddress(); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected contract conflict, got %#v", appErr)
	}
	if _, appErr := s.tipRegistrySafeAddress(); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected safe conflict, got %#v", appErr)
	}
	if _, err := dialEthClient(context.Background(), " "); err == nil {
		t.Fatalf("expected empty rpc url error")
	}
	if got := tipRegistryReceiptSnapshotJSON("0x1", nil); got != "" {
		t.Fatalf("expected empty receipt snapshot, got %q", got)
	}
	if got := (*Server)(nil).tipRegistryOperationSnapshotJSON(context.Background(), nil, nil); got != "" {
		t.Fatalf("expected empty snapshot for nil inputs, got %q", got)
	}
	if got := (&Server{}).tipRegistryOperationSnapshotJSON(context.Background(), nil, &models.TipRegistryOperation{Kind: "unknown"}); got != "" {
		t.Fatalf("expected empty snapshot for unknown kind, got %q", got)
	}
	dial := newTipRegistrySSRFProtectedDialContext()
	if _, err := dial(context.Background(), "tcp", "missing-port"); err == nil {
		t.Fatalf("expected invalid address to fail")
	}
}

func configureTipRegistrationQuery(qReg *ttmocks.MockQuery, db *ttmocks.MockExtendedDB) {
	qReg.ExpectedCalls = nil
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.TipHostRegistration")).Return(qReg).Maybe()
	qReg.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qReg).Maybe()
	qReg.On("Index", mock.Anything).Return(qReg).Maybe()
	qReg.On("Limit", mock.Anything).Return(qReg).Maybe()
	qReg.On("IfExists").Return(qReg).Maybe()
	qReg.On("IfNotExists").Return(qReg).Maybe()
	qReg.On("Delete").Return(nil).Maybe()
}
