package controlplane

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestValidateTipRegistryConfigForAutoOps(t *testing.T) {
	t.Parallel()

	if appErr := (*Server)(nil).validateTipRegistryConfigForAutoOps(); appErr == nil || appErr.Code != "app.internal" {
		t.Fatalf("expected internal error, got %#v", appErr)
	}

	s := &Server{cfg: config.Config{}}
	if appErr := s.validateTipRegistryConfigForAutoOps(); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected conflict for missing config, got %#v", appErr)
	}

	s.cfg.TipChainID = 1
	s.cfg.TipContractAddress = "0x0000000000000000000000000000000000000001"
	s.cfg.TipTxMode = tipTxModeSafe
	s.cfg.TipAdminSafeAddress = testNope
	if appErr := s.validateTipRegistryConfigForAutoOps(); appErr == nil || !strings.Contains(appErr.Message, "safe") {
		t.Fatalf("expected safe not configured, got %#v", appErr)
	}

	s.cfg.TipAdminSafeAddress = "0x0000000000000000000000000000000000000002"
	s.cfg.TipDefaultHostWalletAddress = testNope
	if appErr := s.validateTipRegistryConfigForAutoOps(); appErr == nil || !strings.Contains(appErr.Message, "wallet") {
		t.Fatalf("expected wallet not configured, got %#v", appErr)
	}

	s.cfg.TipDefaultHostWalletAddress = "0x0000000000000000000000000000000000000003"
	s.cfg.TipDefaultHostFeeBps = 501
	if appErr := s.validateTipRegistryConfigForAutoOps(); appErr == nil || !strings.Contains(appErr.Message, "fee") {
		t.Fatalf("expected fee not configured, got %#v", appErr)
	}
}

func TestEncodeAutoTipRegistryTx(t *testing.T) {
	t.Parallel()

	hostID := common.HexToHash("0x01")
	wallet := common.HexToAddress("0x0000000000000000000000000000000000000004")

	for _, kind := range []string{
		models.TipRegistryOperationKindRegisterHost,
		models.TipRegistryOperationKindUpdateHost,
		models.TipRegistryOperationKindSetHostActive,
	} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			txData, walletAddr, feeBps, active, appErr := encodeAutoTipRegistryTx(kind, hostID, wallet, 250)
			if appErr != nil {
				t.Fatalf("encodeAutoTipRegistryTx: %v", appErr)
			}
			if !strings.HasPrefix(txData, "0x") {
				t.Fatalf("expected tx data hex, got %q", txData)
			}
			if kind == models.TipRegistryOperationKindSetHostActive {
				if walletAddr != "" || feeBps != 0 || active == nil || !*active {
					t.Fatalf("expected active-only tx, got wallet=%q fee=%d active=%v", walletAddr, feeBps, active)
				}
			} else if walletAddr == "" || feeBps != 250 || active != nil {
				t.Fatalf("unexpected tx metadata: wallet=%q fee=%d active=%v", walletAddr, feeBps, active)
			}
		})
	}

	if _, _, _, _, appErr := encodeAutoTipRegistryTx(testNope, hostID, wallet, 1); appErr == nil {
		t.Fatalf("expected error for unsupported kind")
	}
}

func TestBuildAutoTipRegistryOperation_NoRPC_DefaultsToRegister(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()

	tdb := newTipRegistryTestDB()
	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()

	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			TipEnabled:                  true,
			TipChainID:                  1,
			TipRPCURL:                   "",
			TipContractAddress:          "0x0000000000000000000000000000000000000001",
			TipAdminSafeAddress:         "0x0000000000000000000000000000000000000002",
			TipDefaultHostWalletAddress: "0x0000000000000000000000000000000000000003",
			TipDefaultHostFeeBps:        250,
			TipTxMode:                   "direct",
		},
	}

	op, audit, err := s.buildAutoTipRegistryOperation(
		context.Background(),
		"example.com",
		"Example.COM",
		"alice",
		"req",
		now,
	)
	if err != nil {
		t.Fatalf("buildAutoTipRegistryOperation: %v", err)
	}
	if op == nil || audit == nil {
		t.Fatalf("expected op+audit, got op=%v audit=%v", op, audit)
	}
	if op.Kind != models.TipRegistryOperationKindRegisterHost {
		t.Fatalf("expected register op, got %q", op.Kind)
	}
	if !strings.HasPrefix(op.ID, "tipop_") {
		t.Fatalf("expected tipop id, got %q", op.ID)
	}
	if audit.Action != "tip_registry.host.register.auto" {
		t.Fatalf("unexpected audit action: %q", audit.Action)
	}
}
