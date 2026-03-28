package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type listSoulOperationsResponse struct {
	Operations []models.SoulOperation `json:"operations"`
	Count      int                    `json:"count"`
}

func (s *Server) handleListSoulOperations(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	status := strings.ToLower(strings.TrimSpace(httpx.FirstQueryValue(ctx.Request.Query, "status")))
	if status == "" {
		status = models.SoulOperationStatusPending
	}

	switch status {
	case models.SoulOperationStatusPending, models.SoulOperationStatusProposed, models.SoulOperationStatusExecuted, models.SoulOperationStatusFailed:
	default:
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid status"}
	}

	var items []*models.SoulOperation
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulOperation{}).
		Index("gsi1").
		Where("gsi1PK", "=", fmt.Sprintf("SOUL_OP_STATUS#%s", status)).
		All(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list operations"}
	}

	out := make([]models.SoulOperation, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, *item)
	}
	return apptheory.JSON(http.StatusOK, listSoulOperationsResponse{Operations: out, Count: len(out)})
}

func (s *Server) handleGetSoulOperation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "id is required"}
	}

	op, err := s.getSoulOperation(ctx.Context(), id)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "operation not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	return apptheory.JSON(http.StatusOK, op)
}

type recordSoulExecutionRequest struct {
	ExecTxHash string `json:"exec_tx_hash"`
}

func (s *Server) handleRecordSoulOperationExecution(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulRPCConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	id, txHash, appErr := parseSoulOperationExecutionInput(ctx)
	if appErr != nil {
		return nil, appErr
	}

	op, appErr := s.loadSoulOperationForExecution(ctx.Context(), id)
	if appErr != nil {
		return nil, appErr
	}

	updated, appErr := s.recordSoulOperationExecution(ctx.Context(), strings.TrimSpace(ctx.AuthIdentity), ctx.RequestID, op, txHash)
	if appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(http.StatusOK, updated)
}

func (s *Server) recordSoulOperationExecution(ctx context.Context, actor string, requestID string, op *models.SoulOperation, txHash string) (*models.SoulOperation, *apptheory.AppError) {
	if op == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	txHash = strings.TrimSpace(txHash)
	if !isHexHash32(txHash) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "exec_tx_hash is required"}
	}

	client, appErr := s.dialSoulRPCClient(ctx)
	if appErr != nil {
		return nil, appErr
	}
	defer client.Close()

	receipt, appErr := getTransactionReceipt(ctx, client, txHash)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	success := receipt.Status == 1
	blockNum := soulBlockNumber(receipt)
	receiptJSON := soulReceiptSnapshotJSON(txHash, receipt)
	snapshotJSON := strings.TrimSpace(op.SnapshotJSON)
	chainSnapshotJSON := s.soulOperationSnapshotJSON(ctx, client, op)
	if strings.TrimSpace(chainSnapshotJSON) != "" {
		snapshotJSON = chainSnapshotJSON
	}

	update := &models.SoulOperation{
		OperationID:     op.OperationID,
		Kind:            op.Kind,
		AgentID:         op.AgentID,
		SafePayloadJSON: op.SafePayloadJSON,
		ExecTxHash:      strings.ToLower(txHash),
		ExecBlockNumber: blockNum,
		ExecSuccess:     &success,
		ReceiptJSON:     receiptJSON,
		SnapshotJSON:    snapshotJSON,
		Status: func() string {
			if success {
				return models.SoulOperationStatusExecuted
			}
			return models.SoulOperationStatusFailed
		}(),
		CreatedAt:  op.CreatedAt,
		UpdatedAt:  now,
		ExecutedAt: now,
	}
	_ = update.UpdateKeys()

	if err := s.store.DB.WithContext(ctx).Model(update).IfExists().Update(
		"ExecTxHash",
		"ExecBlockNumber",
		"ExecSuccess",
		"ReceiptJSON",
		"SnapshotJSON",
		"Status",
		"UpdatedAt",
		"ExecutedAt",
	); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to update operation"}
	}

	if success {
		_ = s.applySoulOperationSideEffects(ctx, client, update)
	}
	if strings.EqualFold(strings.TrimSpace(update.Kind), models.SoulOperationKindMint) {
		if promotion, err := s.getSoulAgentPromotion(ctx, strings.TrimSpace(update.AgentID)); err == nil && promotion != nil {
			promotion.MintOperationID = strings.TrimSpace(update.OperationID)
			promotion.MintOperationStatus = strings.ToLower(strings.TrimSpace(update.Status))
			if success {
				promotion = updateSoulAgentPromotionForMintExecution(promotion, update, now)
			} else {
				promotion.UpdatedAt = now
			}
			_ = s.saveSoulAgentPromotion(ctx, promotion)
		}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(actor),
		Action:    "soul.operation.record_execution",
		Target:    fmt.Sprintf("soul_operation:%s", op.OperationID),
		RequestID: strings.TrimSpace(requestID),
		CreatedAt: now,
	}
	s.tryWriteAuditLogWithContext(ctx, audit)

	return update, nil
}

func parseSoulOperationExecutionInput(ctx *apptheory.Context) (id string, txHash string, appErr *apptheory.AppError) {
	if ctx == nil {
		return "", "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	id = strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return "", "", &apptheory.AppError{Code: "app.bad_request", Message: "id is required"}
	}

	txHash, appErr = parseSoulOperationExecutionTxHash(ctx)
	if appErr != nil {
		return "", "", appErr
	}
	return id, txHash, nil
}

func parseSoulOperationExecutionTxHash(ctx *apptheory.Context) (string, *apptheory.AppError) {
	if ctx == nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	var req recordSoulExecutionRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		appErr, ok := err.(*apptheory.AppError)
		if ok {
			return "", appErr
		}
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid request"}
	}
	txHash := strings.TrimSpace(req.ExecTxHash)
	if !isHexHash32(txHash) {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "exec_tx_hash is required"}
	}
	return txHash, nil
}

func (s *Server) loadSoulOperationForExecution(ctx context.Context, id string) (*models.SoulOperation, *apptheory.AppError) {
	op, err := s.getSoulOperation(ctx, id)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "operation not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return op, nil
}

func (s *Server) dialSoulRPCClient(ctx context.Context) (ethRPCClient, *apptheory.AppError) {
	dial := s.dialEVM
	if dial == nil {
		dial = func(ctx context.Context, rpcURL string) (ethRPCClient, error) { return dialEthClient(ctx, rpcURL) }
	}

	client, err := dial(ctx, s.cfg.SoulRPCURL)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to connect to rpc"}
	}
	return client, nil
}

func getTransactionReceipt(ctx context.Context, client ethRPCClient, txHash string) (*types.Receipt, *apptheory.AppError) {
	if client == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	receipt, err := client.TransactionReceipt(ctx, common.HexToHash(txHash))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "receipt not found"}
	}
	return receipt, nil
}

func soulReceiptSnapshotJSON(txHash string, receipt *types.Receipt) string {
	if receipt == nil {
		return ""
	}
	snap := map[string]any{
		"tx_hash":          strings.TrimSpace(txHash),
		"block_number":     receipt.BlockNumber.Uint64(),
		"status":           receipt.Status,
		"gas_used":         receipt.GasUsed,
		"contract_address": strings.ToLower(receipt.ContractAddress.Hex()),
		"logs":             len(receipt.Logs),
	}
	if receipt.EffectiveGasPrice != nil {
		snap["effective_gas_price_wei"] = receipt.EffectiveGasPrice.String()
	}
	b, _ := json.Marshal(snap)
	return string(b)
}

func soulBlockNumber(receipt *types.Receipt) int64 {
	if receipt == nil || receipt.BlockNumber == nil {
		return 0
	}
	if receipt.BlockNumber.Sign() < 0 || receipt.BlockNumber.BitLen() > 63 {
		return 0
	}
	return receipt.BlockNumber.Int64()
}

func (s *Server) soulOperationSnapshotJSON(ctx context.Context, client ethRPCClient, op *models.SoulOperation) string {
	if s == nil || client == nil || op == nil {
		return ""
	}
	agentIDHex := strings.ToLower(strings.TrimSpace(op.AgentID))
	if agentIDHex == "" {
		return ""
	}

	agentInt, ok := new(big.Int).SetString(strings.TrimPrefix(agentIDHex, "0x"), 16)
	if !ok {
		return ""
	}

	contractAddr := common.HexToAddress(strings.TrimSpace(s.cfg.SoulRegistryContractAddress))
	wallet, err := s.soulRegistryGetAgentWallet(ctx, client, contractAddr, agentInt)
	if err != nil {
		return ""
	}
	nonce, err := s.soulRegistryGetAgentNonce(ctx, client, contractAddr, agentInt)
	if err != nil {
		return ""
	}
	if nonce == nil {
		nonce = new(big.Int)
	}

	transferCnt := s.soulRegistryGetTransferCount(ctx, client, contractAddr, agentInt)
	lastTransferred := s.soulRegistryGetLastTransferredAt(ctx, client, contractAddr, agentInt)

	now := time.Now().UTC()
	snap := map[string]any{
		"agent_id":            agentIDHex,
		"wallet":              strings.ToLower(wallet.Hex()),
		"nonce":               nonce.String(),
		"transfer_count":      transferCnt.String(),
		"last_transferred_at": lastTransferred.String(),
		"observed_at":         now.Format(time.RFC3339Nano),
		"operation_id":        strings.TrimSpace(op.OperationID),
		"kind":                strings.TrimSpace(op.Kind),
	}
	b, _ := json.Marshal(snap)
	return string(b)
}

func (s *Server) applySoulOperationSideEffects(ctx context.Context, client ethRPCClient, op *models.SoulOperation) error {
	if s == nil || s.store == nil || s.store.DB == nil || op == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	kind := strings.ToLower(strings.TrimSpace(op.Kind))
	agentID := strings.ToLower(strings.TrimSpace(op.AgentID))
	if agentID == "" {
		return nil
	}

	switch kind {
	case models.SoulOperationKindMint:
		s.applySoulOperationMintSideEffects(ctx, op, agentID)
	case models.SoulOperationKindRotateWallet:
		s.applySoulOperationRotateWalletSideEffects(ctx, client, agentID)
	case models.SoulOperationKindBurn:
		s.applySoulOperationBurnSideEffects(ctx, agentID)
	}

	return nil
}

func (s *Server) applySoulOperationMintSideEffects(ctx context.Context, op *models.SoulOperation, agentID string) {
	identity, err := s.getSoulAgentIdentity(ctx, agentID)
	if err != nil || identity == nil {
		return
	}

	// Never implicitly reinstate a non-active agent by recording a mint receipt.
	status := models.SoulAgentStatusActive
	current := strings.TrimSpace(identity.LifecycleStatus)
	if current == "" {
		current = strings.TrimSpace(identity.Status)
	}
	switch current {
	case models.SoulAgentStatusSuspended,
		models.SoulAgentStatusSelfSuspended,
		models.SoulAgentStatusArchived,
		models.SoulAgentStatusSucceeded,
		models.SoulAgentStatusBurned:
		status = current
	}

	now := time.Now().UTC()
	update := &models.SoulAgentIdentity{
		AgentID:         agentID,
		Status:          status,
		LifecycleStatus: status,
		MintTxHash:      strings.ToLower(strings.TrimSpace(op.ExecTxHash)),
		MintedAt:        now,
		UpdatedAt:       now,
	}
	_ = update.UpdateKeys()
	_ = s.store.DB.WithContext(ctx).Model(update).IfExists().Update("Status", "LifecycleStatus", "MintTxHash", "MintedAt", "UpdatedAt")
}

func (s *Server) applySoulOperationRotateWalletSideEffects(ctx context.Context, client ethRPCClient, agentID string) {
	if client == nil {
		return
	}
	agentInt, ok := new(big.Int).SetString(strings.TrimPrefix(agentID, "0x"), 16)
	if !ok {
		return
	}
	contractAddr := common.HexToAddress(strings.TrimSpace(s.cfg.SoulRegistryContractAddress))
	onChainWallet, err := s.soulRegistryGetAgentWallet(ctx, client, contractAddr, agentInt)
	if err != nil || (onChainWallet == common.Address{}) {
		return
	}

	identity, err := s.getSoulAgentIdentity(ctx, agentID)
	if err != nil || identity == nil {
		return
	}
	oldWallet := strings.ToLower(strings.TrimSpace(identity.Wallet))
	newWallet := strings.ToLower(onChainWallet.Hex())

	now := time.Now().UTC()
	identity.Wallet = newWallet
	identity.UpdatedAt = now
	_ = identity.UpdateKeys()

	_ = s.store.DB.WithContext(ctx).Model(identity).IfExists().Update("Wallet", "UpdatedAt")

	// Wallet → agent index maintenance (best-effort).
	if oldWallet != "" && !strings.EqualFold(oldWallet, newWallet) {
		del := &models.SoulWalletAgentIndex{Wallet: oldWallet, AgentID: agentID}
		_ = del.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(del).Delete()
	}
	if newWallet != "" {
		wi := &models.SoulWalletAgentIndex{Wallet: newWallet, AgentID: agentID}
		_ = wi.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(wi).CreateOrUpdate()
	}

	ensChannel, err := getSoulAgentItemBySK[models.SoulAgentChannel](s, ctx, agentID, "CHANNEL#ens")
	if err != nil || ensChannel == nil || strings.TrimSpace(ensChannel.Identifier) == "" {
		return
	}

	resolution := &models.SoulAgentENSResolution{
		ENSName:   ensChannel.Identifier,
		AgentID:   agentID,
		Wallet:    newWallet,
		LocalID:   identity.LocalID,
		Domain:    identity.Domain,
		Status:    identity.Status,
		UpdatedAt: now,
	}
	_ = resolution.UpdateKeys()

	var existing models.SoulAgentENSResolution
	err = s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentENSResolution{}).
		Where("PK", "=", resolution.GetPK()).
		Where("SK", "=", resolution.GetSK()).
		First(&existing)
	if err == nil {
		if !strings.EqualFold(strings.TrimSpace(existing.AgentID), agentID) {
			return
		}
		existing.Wallet = newWallet
		existing.UpdatedAt = now
		_ = existing.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(&existing).Update("Wallet", "UpdatedAt")
		return
	}

	_ = s.store.DB.WithContext(ctx).Model(resolution).CreateOrUpdate()
}

func (s *Server) applySoulOperationBurnSideEffects(ctx context.Context, agentID string) {
	identity, err := s.getSoulAgentIdentity(ctx, agentID)
	if err != nil || identity == nil {
		return
	}

	now := time.Now().UTC()
	oldWallet := strings.ToLower(strings.TrimSpace(identity.Wallet))

	// Transition identity status to burned.
	update := &models.SoulAgentIdentity{
		AgentID:         agentID,
		Status:          models.SoulAgentStatusBurned,
		LifecycleStatus: models.SoulAgentStatusBurned,
		Wallet:          "",
		UpdatedAt:       now,
	}
	_ = update.UpdateKeys()
	_ = s.store.DB.WithContext(ctx).Model(update).IfExists().Update("Status", "LifecycleStatus", "Wallet", "UpdatedAt")

	// Clean up wallet→agent index.
	if oldWallet != "" {
		del := &models.SoulWalletAgentIndex{Wallet: oldWallet, AgentID: agentID}
		_ = del.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(del).Delete()
	}

	// Clean up domain→agent index.
	domain := strings.TrimSpace(identity.Domain)
	if domain != "" {
		del := &models.SoulDomainAgentIndex{Domain: domain, AgentID: agentID}
		_ = del.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(del).Delete()
	}

	// Clean up capability indexes.
	for _, cap := range identity.Capabilities {
		cap = strings.TrimSpace(cap)
		if cap == "" {
			continue
		}
		del := &models.SoulCapabilityAgentIndex{Capability: cap, AgentID: agentID}
		_ = del.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(del).Delete()
	}
}
