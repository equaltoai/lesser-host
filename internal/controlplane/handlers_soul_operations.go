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

	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "id is required"}
	}

	var req recordSoulExecutionRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}
	txHash := strings.TrimSpace(req.ExecTxHash)
	if !isHexHash32(txHash) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "exec_tx_hash is required"}
	}

	op, err := s.getSoulOperation(ctx.Context(), id)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "operation not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	dial := s.dialEVM
	if dial == nil {
		dial = func(ctx context.Context, rpcURL string) (ethRPCClient, error) { return dialEthClient(ctx, rpcURL) }
	}

	client, err := dial(ctx.Context(), s.cfg.SoulRPCURL)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to connect to rpc"}
	}
	defer client.Close()

	receipt, err := client.TransactionReceipt(ctx.Context(), common.HexToHash(txHash))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "receipt not found"}
	}

	now := time.Now().UTC()
	success := receipt.Status == 1
	blockNum := soulBlockNumber(receipt)
	receiptJSON := soulReceiptSnapshotJSON(txHash, receipt)
	snapshotJSON := strings.TrimSpace(op.SnapshotJSON)
	chainSnapshotJSON := s.soulOperationSnapshotJSON(ctx.Context(), client, op)
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

	if err := s.store.DB.WithContext(ctx.Context()).Model(update).IfExists().Update(
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
		_ = s.applySoulOperationSideEffects(ctx.Context(), client, update)
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.operation.record_execution",
		Target:    fmt.Sprintf("soul_operation:%s", op.OperationID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, update)
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

	now := time.Now().UTC()
	snap := map[string]any{
		"agent_id":     agentIDHex,
		"wallet":       strings.ToLower(wallet.Hex()),
		"nonce":        nonce.String(),
		"observed_at":  now.Format(time.RFC3339Nano),
		"operation_id": strings.TrimSpace(op.OperationID),
		"kind":         strings.TrimSpace(op.Kind),
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
		identity, err := s.getSoulAgentIdentity(ctx, agentID)
		if err != nil || identity == nil {
			return nil
		}

		// Never implicitly reinstate a suspended agent by recording a mint receipt.
		status := models.SoulAgentStatusActive
		if strings.TrimSpace(identity.Status) == models.SoulAgentStatusSuspended {
			status = models.SoulAgentStatusSuspended
		}

		update := &models.SoulAgentIdentity{
			AgentID:    agentID,
			Status:     status,
			MintTxHash: strings.ToLower(strings.TrimSpace(op.ExecTxHash)),
			MintedAt:   time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}
		_ = update.UpdateKeys()
		_ = s.store.DB.WithContext(ctx).Model(update).IfExists().Update("Status", "MintTxHash", "MintedAt", "UpdatedAt")
	case models.SoulOperationKindRotateWallet:
		if client == nil {
			return nil
		}
		agentInt, ok := new(big.Int).SetString(strings.TrimPrefix(agentID, "0x"), 16)
		if !ok {
			return nil
		}
		contractAddr := common.HexToAddress(strings.TrimSpace(s.cfg.SoulRegistryContractAddress))
		onChainWallet, err := s.soulRegistryGetAgentWallet(ctx, client, contractAddr, agentInt)
		if err != nil || (onChainWallet == common.Address{}) {
			return nil
		}

		identity, err := s.getSoulAgentIdentity(ctx, agentID)
		if err != nil || identity == nil {
			return nil
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
	}

	return nil
}
