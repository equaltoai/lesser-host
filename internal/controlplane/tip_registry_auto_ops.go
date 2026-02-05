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

func (s *Server) buildAutoTipRegistryOperation(ctx context.Context, domain string, domainRaw string, actor string, requestID string, now time.Time) (*models.TipRegistryOperation, *models.AuditLogEntry, error) {
	if s == nil || !s.cfg.TipEnabled {
		return nil, nil, nil
	}
	if s.cfg.TipChainID <= 0 || strings.TrimSpace(s.cfg.TipContractAddress) == "" {
		return nil, nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry is not configured"}
	}
	if s.cfg.TipTxMode == tipTxModeSafe && !common.IsHexAddress(strings.TrimSpace(s.cfg.TipAdminSafeAddress)) {
		return nil, nil, &apptheory.AppError{Code: "app.conflict", Message: "tip registry safe is not configured"}
	}
	if !common.IsHexAddress(strings.TrimSpace(s.cfg.TipDefaultHostWalletAddress)) {
		return nil, nil, &apptheory.AppError{Code: "app.conflict", Message: "tip default host wallet is not configured"}
	}
	if s.cfg.TipDefaultHostFeeBps > 500 {
		return nil, nil, &apptheory.AppError{Code: "app.conflict", Message: "tip default host fee is not configured"}
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

	opKind := models.TipRegistryOperationKindRegisterHost
	if strings.TrimSpace(s.cfg.TipRPCURL) != "" {
		if client, err := dialEthClient(ctx, s.cfg.TipRPCURL); err == nil {
			if host, err := tipSplitterGetHost(ctx, client, contractAddr, hostID); err == nil && host != nil && host.Wallet != (common.Address{}) {
				switch {
				case host.Wallet == desiredWallet && host.FeeBps == desiredFee && host.IsActive:
					opKind = ""
				case host.Wallet != desiredWallet || host.FeeBps != desiredFee:
					opKind = models.TipRegistryOperationKindUpdateHost
				default:
					opKind = models.TipRegistryOperationKindSetHostActive
				}
			}
			client.Close()
		}
	}
	if opKind == "" {
		return nil, nil, nil
	}

	var data []byte
	var active *bool
	var walletAddr string
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
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to encode transaction"}
	}

	txData := "0x" + hex.EncodeToString(data)
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
