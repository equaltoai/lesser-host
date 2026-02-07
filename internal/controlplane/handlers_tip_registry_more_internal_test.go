package controlplane

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
	"github.com/equaltoai/lesser-host/internal/tips"
)

func TestTipRegistryVerifyInputAndProofs_MoreBranches(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{Request: apptheory.Request{Body: []byte(`{`)}}
	if _, _, appErr := parseTipHostRegistrationVerifyInput(ctx); appErr == nil {
		t.Fatalf("expected invalid JSON error")
	}

	ctx.Request.Body = []byte(`{"signature":"s","proofs":["nope"]}`)
	if _, _, appErr := parseTipHostRegistrationVerifyInput(ctx); appErr == nil {
		t.Fatalf("expected invalid proof error")
	}

	reg := &models.TipHostRegistration{DomainNormalized: "", DNSToken: ""}
	if _, _, appErr := verifyTipHostRegistrationProofs(context.Background(), reg, requiredProofSet{requireDNS: true}); appErr == nil {
		t.Fatalf("expected dns proof not found error")
	}

	reg = &models.TipHostRegistration{DomainNormalized: "localhost", DNSToken: "t", HTTPToken: "t"}
	if _, _, appErr := verifyTipHostRegistrationProofs(context.Background(), reg, requiredProofSet{requireHTTPS: true}); appErr == nil {
		t.Fatalf("expected https proof not found error")
	}

	if ok := verifyTipRegistryDNS(context.Background(), "", "x"); ok {
		t.Fatalf("expected empty dns inputs to fail")
	}
	if ok := verifyTipRegistryHTTPS(context.Background(), "localhost", "x"); ok {
		t.Fatalf("expected localhost https proof to fail")
	}
}

func TestEnforceTipRegistryUpdateProofPolicy_RequiresBoth(t *testing.T) {
	t.Parallel()

	parsedABI, err := abi.JSON(strings.NewReader(tips.TipSplitterABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}

	hostID := common.HexToHash("0x" + strings.Repeat("11", 32))
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

	s := &Server{cfg: config.Config{
		TipRPCURL:          rpcSrv.URL,
		TipContractAddress: "0x0000000000000000000000000000000000000001",
	}}

	reg := &models.TipHostRegistration{
		Kind:       models.TipRegistryOperationKindUpdateHost,
		HostIDHex:  hostID.Hex(),
		WalletAddr: "0x00000000000000000000000000000000000000bb", // wallet change
		HostFeeBps: 10,
	}
	appErr := s.enforceTipRegistryUpdateProofPolicy(context.Background(), reg, true, false)
	if appErr == nil || appErr.Code != appErrCodeBadRequest || !strings.Contains(appErr.Message, "requires both") {
		t.Fatalf("expected bad_request requires both proofs, got %#v", appErr)
	}

	// Update kind: errors from the RPC requirement surface to caller.
	s2 := &Server{cfg: config.Config{TipContractAddress: "0x0000000000000000000000000000000000000001"}}
	appErr = s2.enforceTipRegistryUpdateProofPolicy(context.Background(), reg, true, true)
	if appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected conflict for missing rpc, got %#v", appErr)
	}
}

func TestHandleTipHostRegistrationVerify_Validations(t *testing.T) {
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

	// Missing id.
	if _, err := s.handleTipHostRegistrationVerify(&apptheory.Context{Request: apptheory.Request{Body: []byte(`{}`)}}); err == nil {
		t.Fatalf("expected id required error")
	}

	// Invalid JSON.
	ctx := &apptheory.Context{
		Params:  map[string]string{"id": "reg1"},
		Request: apptheory.Request{Body: []byte(`{`)},
	}
	if _, err := s.handleTipHostRegistrationVerify(ctx); err == nil {
		t.Fatalf("expected invalid JSON error")
	}
}

func TestHandleEnsureTipRegistryHost_NoOpAndCreate(t *testing.T) {
	t.Parallel()

	parsedABI, err := abi.JSON(strings.NewReader(tips.TipSplitterABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}

	// No-op when on-chain host already matches desired settings.
	{
		hostWallet := common.HexToAddress("0x00000000000000000000000000000000000000aa")
		desiredFee := uint16(10)

		hostID := tips.HostIDFromDomain("example.com")
		hostCall, _ := tips.EncodeGetHostCall(hostID)
		hostCallHex := "0x" + hex.EncodeToString(hostCall)

		ret, err := parsedABI.Methods["hosts"].Outputs.Pack(hostWallet, desiredFee, true)
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
				TipEnabled:                  true,
				TipChainID:                  1,
				TipRPCURL:                   rpcSrv.URL,
				TipContractAddress:          "0x0000000000000000000000000000000000000001",
				TipTxMode:                   tipTxModeSafe,
				TipAdminSafeAddress:         "0x0000000000000000000000000000000000000002",
				TipDefaultHostWalletAddress: hostWallet.Hex(),
				TipDefaultHostFeeBps:        desiredFee,
			},
		}

		ctx := adminCtx()
		ctx.Params = map[string]string{"domain": "example.com"}
		resp, err := s.handleEnsureTipRegistryHost(ctx)
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("noop resp=%#v err=%v", resp, err)
		}

		var out map[string]any
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if noop, ok := out["noop"].(bool); !ok || !noop {
			t.Fatalf("expected noop true, got %#v", out)
		}
	}

	// Create operation when op kind is resolvable without RPC.
	{
		tdb := newTipRegistryTestDB()
		s := &Server{
			store: store.New(tdb.db),
			cfg: config.Config{
				TipEnabled:                  true,
				TipChainID:                  1,
				TipContractAddress:          "0x0000000000000000000000000000000000000001",
				TipTxMode:                   tipTxModeSafe,
				TipAdminSafeAddress:         "0x0000000000000000000000000000000000000002",
				TipDefaultHostWalletAddress: "0x00000000000000000000000000000000000000aa",
				TipDefaultHostFeeBps:        10,
			},
		}

		// Operation write path.
		tdb.qOp.On("Create").Return(nil).Once()
		tdb.qAudit.On("Create").Return(nil).Maybe()

		ctx := adminCtx()
		ctx.RequestID = "rid"
		ctx.Params = map[string]string{"domain": "example.org"}
		resp, err := s.handleEnsureTipRegistryHost(ctx)
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("create resp=%#v err=%v", resp, err)
		}
	}
}

func TestHandleTipHostRegistrationVerify_SucceedsWithDNSProof(t *testing.T) {
	seed := time.Now().UTC()

	tdb := newTipRegistryTestDB()
	key, addr := generateWalletKey(t)

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

	msg := "tip registry verify"
	sig := signWalletMessage(t, key, msg)

	domain := "example.com"
	reg := models.TipHostRegistration{
		ID:               "reg1",
		Kind:             models.TipRegistryOperationKindRegisterHost,
		DomainRaw:        domain,
		DomainNormalized: domain,
		HostIDHex:        tips.HostIDFromDomain(domain).Hex(),
		ChainID:          1,
		WalletType:       "ethereum",
		WalletAddr:       strings.ToLower(addr),
		HostFeeBps:       5,
		TxMode:           tipTxModeSafe,
		SafeAddress:      strings.ToLower(strings.TrimSpace(s.cfg.TipAdminSafeAddress)),
		WalletNonce:      "n",
		WalletMessage:    msg,
		DNSToken:         "tok",
		HTTPToken:        "tok",
		Status:           models.TipHostRegistrationStatusPending,
		CreatedAt:        seed.Add(-1 * time.Minute),
		UpdatedAt:        seed.Add(-1 * time.Minute),
		ExpiresAt:        seed.Add(10 * time.Minute),
	}
	_ = reg.UpdateKeys()

	tdb.qReg.On("First", mock.AnythingOfType("*models.TipHostRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.TipHostRegistration](t, args, 0)
		*dest = reg
	}).Once()

	tdb.qOp.On("Create").Return(nil).Once()

	txtName := tipRegistryProofPrefix + domain
	txtValue := tipRegistryProofValue + reg.DNSToken

	body := []byte(`{"signature":"` + sig + `","proofs":["dns_txt"]}`)
	ctx := &apptheory.Context{
		RequestID: "rid",
		Params:    map[string]string{"id": reg.ID},
		Request:   apptheory.Request{Body: body},
	}

	withDNSTXTResolver(t, txtName, txtValue, func() {
		resp, err := s.handleTipHostRegistrationVerify(ctx)
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}

		var out tipHostRegistrationVerifyResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Registration.Status != models.TipHostRegistrationStatusCompleted {
			t.Fatalf("expected completed registration, got %#v", out.Registration)
		}
		if !out.Registration.WalletVerified || !out.Registration.DNSVerified || out.Registration.HTTPSVerified {
			t.Fatalf("expected wallet+dns verified, got %#v", out.Registration)
		}
		if out.Operation.ID == "" || out.Operation.Kind != models.TipRegistryOperationKindRegisterHost {
			t.Fatalf("unexpected operation: %#v", out.Operation)
		}
		if out.SafeTx == nil || !strings.HasPrefix(out.SafeTx.Data, "0x") {
			t.Fatalf("expected safe tx payload, got %#v", out.SafeTx)
		}
	})
}
