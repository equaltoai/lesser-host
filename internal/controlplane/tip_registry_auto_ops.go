package controlplane

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/domains"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/tips"
)

func (s *Server) validateTipRegistryConfigForAutoOps() *apptheory.AppError {
	if s == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.cfg.TipChainID <= 0 || strings.TrimSpace(s.cfg.TipContractAddress) == "" {
		return &apptheory.AppError{Code: "app.conflict", Message: "tip registry is not configured"}
	}
	if s.cfg.TipTxMode == tipTxModeSafe && !common.IsHexAddress(strings.TrimSpace(s.cfg.TipAdminSafeAddress)) {
		return &apptheory.AppError{Code: "app.conflict", Message: "tip registry safe is not configured"}
	}
	if !common.IsHexAddress(strings.TrimSpace(s.cfg.TipDefaultHostWalletAddress)) {
		return &apptheory.AppError{Code: "app.conflict", Message: "tip default host wallet is not configured"}
	}
	if isReservedWalletAddress(s.cfg.TipDefaultHostWalletAddress) {
		return &apptheory.AppError{Code: "app.conflict", Message: "tip default host wallet is reserved"}
	}
	if s.cfg.TipDefaultHostFeeBps > 500 {
		return &apptheory.AppError{Code: "app.conflict", Message: "tip default host fee is not configured"}
	}
	return nil
}

func (s *Server) determineAutoTipRegistryOpKind(ctx context.Context, contractAddr common.Address, hostID common.Hash, desiredWallet common.Address, desiredFee uint16) string {
	opKind := models.TipRegistryOperationKindRegisterHost
	rpcURL := strings.TrimSpace(s.cfg.TipRPCURL)
	if rpcURL == "" {
		return opKind
	}

	client, dialErr := dialEthClient(ctx, rpcURL)
	if dialErr != nil {
		return opKind
	}
	defer client.Close()

	host, getHostErr := tipSplitterGetHost(ctx, client, contractAddr, hostID)
	if getHostErr != nil || host == nil || host.Wallet == (common.Address{}) {
		return opKind
	}

	switch {
	case host.Wallet == desiredWallet && host.FeeBps == desiredFee && host.IsActive:
		return ""
	case host.Wallet != desiredWallet || host.FeeBps != desiredFee:
		return models.TipRegistryOperationKindUpdateHost
	default:
		return models.TipRegistryOperationKindSetHostActive
	}
}

func encodeAutoTipRegistryTx(opKind string, hostID common.Hash, desiredWallet common.Address, desiredFee uint16) (string, string, int64, *bool, *apptheory.AppError) {
	var data []byte
	var err error
	var active *bool

	walletAddr := ""
	hostFeeBps := int64(desiredFee)

	switch opKind {
	case models.TipRegistryOperationKindRegisterHost:
		walletAddr = strings.ToLower(desiredWallet.Hex())
		data, err = tips.EncodeRegisterHostCall(hostID, desiredWallet, desiredFee)
	case models.TipRegistryOperationKindUpdateHost:
		walletAddr = strings.ToLower(desiredWallet.Hex())
		data, err = tips.EncodeUpdateHostCall(hostID, desiredWallet, desiredFee)
	case models.TipRegistryOperationKindSetHostActive:
		v := true
		active = &v
		hostFeeBps = 0
		data, err = tips.EncodeSetHostActiveCall(hostID, true)
	default:
		err = fmt.Errorf("unsupported tip registry op kind")
	}
	if err != nil {
		return "", "", 0, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to encode transaction"}
	}

	txData := "0x" + hex.EncodeToString(data)
	return txData, walletAddr, hostFeeBps, active, nil
}

func (s *Server) buildAutoTipRegistryOperation(ctx context.Context, domain string, domainRaw string, actor string, requestID string, now time.Time) (*models.TipRegistryOperation, *models.AuditLogEntry, error) {
	if s == nil || !s.cfg.TipEnabled {
		return nil, nil, nil
	}
	if s.store == nil || s.store.DB == nil {
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if appErr := s.validateTipRegistryConfigForAutoOps(); appErr != nil {
		return nil, nil, appErr
	}

	domainNormalized, err := domains.NormalizeDomain(domain)
	if err != nil {
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to normalize domain"}
	}

	hostID := tips.HostIDFromDomain(domainNormalized)
	contractAddr := common.HexToAddress(strings.TrimSpace(s.cfg.TipContractAddress))
	txTo := strings.ToLower(contractAddr.Hex())

	desiredWallet := common.HexToAddress(strings.TrimSpace(s.cfg.TipDefaultHostWalletAddress))
	desiredFee := s.cfg.TipDefaultHostFeeBps

	opKind := s.determineAutoTipRegistryOpKind(ctx, contractAddr, hostID, desiredWallet, desiredFee)
	if opKind == "" {
		return nil, nil, nil
	}

	txData, walletAddr, hostFeeBps, active, appErr := encodeAutoTipRegistryTx(opKind, hostID, desiredWallet, desiredFee)
	if appErr != nil {
		return nil, nil, appErr
	}
	if walletAddr != "" {
		if appErr := s.validateNotPrivilegedWalletAddress(ctx, "ethereum", walletAddr, "tip default host wallet"); appErr != nil {
			return nil, nil, appErr
		}
	}
	opID := tipRegistryOpID(opKind, s.cfg.TipChainID, txTo, hostID.Hex(), walletAddr, hostFeeBps, "", active, nil)

	op := &models.TipRegistryOperation{
		ID:               opID,
		Kind:             opKind,
		ChainID:          s.cfg.TipChainID,
		ContractAddress:  txTo,
		TxMode:           s.cfg.TipTxMode,
		SafeAddress:      strings.ToLower(strings.TrimSpace(s.cfg.TipAdminSafeAddress)),
		DomainRaw:        strings.TrimSpace(domainRaw),
		DomainNormalized: domainNormalized,
		HostIDHex:        strings.ToLower(hostID.Hex()),
		WalletAddr:       walletAddr,
		HostFeeBps:       hostFeeBps,
		Active:           active,
		TxTo:             txTo,
		TxData:           txData,
		TxValue:          "0",
		Status:           models.TipRegistryOperationStatusProposed,
		CreatedAt:        now,
		UpdatedAt:        now,
		ProposedAt:       now,
	}
	_ = op.UpdateKeys()

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(actor),
		Action:    "tip_registry.host.register.auto",
		Target:    fmt.Sprintf("tip_registry_operation:%s", op.ID),
		RequestID: strings.TrimSpace(requestID),
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	return op, audit, nil
}
