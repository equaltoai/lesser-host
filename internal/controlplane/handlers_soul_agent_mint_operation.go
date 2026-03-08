package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulAgentMintOperationResponse struct {
	Operation models.SoulOperation `json:"operation"`
	SafeTx    *safeTxPayload       `json:"safe_tx,omitempty"`
}

func (s *Server) handleSoulAgentGetMintOperation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	agentCtx, appErr := s.requireMintConversationAgentContext(ctx, false)
	if appErr != nil {
		return nil, appErr
	}

	op, appErr := s.loadSoulMintOperationForIdentity(ctx.Context(), agentCtx.identity)
	if appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(http.StatusOK, buildSoulAgentMintOperationResponse(op))
}

func (s *Server) handleSoulAgentRecordMintOperationExecution(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulRPCConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	agentCtx, appErr := s.requireMintConversationAgentContext(ctx, false)
	if appErr != nil {
		return nil, appErr
	}
	txHash, appErr := parseSoulOperationExecutionTxHash(ctx)
	if appErr != nil {
		return nil, appErr
	}

	op, appErr := s.loadSoulMintOperationForIdentity(ctx.Context(), agentCtx.identity)
	if appErr != nil {
		return nil, appErr
	}

	updated, appErr := s.recordSoulOperationExecution(ctx.Context(), strings.TrimSpace(ctx.AuthIdentity), ctx.RequestID, op, txHash)
	if appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(http.StatusOK, buildSoulAgentMintOperationResponse(updated))
}

func (s *Server) loadSoulMintOperationForIdentity(ctx context.Context, identity *models.SoulAgentIdentity) (*models.SoulOperation, *apptheory.AppError) {
	opID := s.soulMintOperationID(identity)
	if opID == "" {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "mint operation not found"}
	}

	op, err := s.getSoulOperation(ctx, opID)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "mint operation not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.ToLower(strings.TrimSpace(op.Kind)) != models.SoulOperationKindMint {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "mint operation not found"}
	}
	return s.maybeMigrateLegacySoulMintOperation(ctx, identity, op)
}

func (s *Server) maybeMigrateLegacySoulMintOperation(
	ctx context.Context,
	identity *models.SoulAgentIdentity,
	op *models.SoulOperation,
) (*models.SoulOperation, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil || identity == nil || op == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.ToLower(strings.TrimSpace(op.Kind)) != models.SoulOperationKindMint {
		return op, nil
	}
	if strings.ToLower(strings.TrimSpace(op.Status)) != models.SoulOperationStatusPending {
		return op, nil
	}

	payload := parseSafeTxPayload(op.SafePayloadJSON)
	if payload == nil || strings.TrimSpace(payload.SafeAddress) == "" {
		return op, nil
	}

	directPayload, _, _, appErr := s.buildSoulMintPayload(&models.SoulAgentRegistration{
		AgentID: identity.AgentID,
		Wallet:  identity.Wallet,
	}, identity.PrincipalAddress)
	if appErr != nil {
		return nil, appErr
	}

	encoded, err := json.Marshal(directPayload)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to encode mint transaction"}
	}
	encodedJSON := strings.TrimSpace(string(encoded))
	if encodedJSON == strings.TrimSpace(op.SafePayloadJSON) {
		return op, nil
	}

	updated := &models.SoulOperation{
		OperationID:     op.OperationID,
		Kind:            op.Kind,
		AgentID:         op.AgentID,
		Status:          op.Status,
		SafePayloadJSON: encodedJSON,
		ExecTxHash:      op.ExecTxHash,
		ExecBlockNumber: op.ExecBlockNumber,
		ExecSuccess:     op.ExecSuccess,
		ReceiptJSON:     op.ReceiptJSON,
		SnapshotJSON:    op.SnapshotJSON,
		CreatedAt:       op.CreatedAt,
		UpdatedAt:       time.Now().UTC(),
		ExecutedAt:      op.ExecutedAt,
	}
	_ = updated.UpdateKeys()

	if err := s.store.DB.WithContext(ctx).Model(updated).IfExists().Update("SafePayloadJSON", "UpdatedAt"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to update mint operation"}
	}

	return updated, nil
}

func (s *Server) soulMintOperationID(identity *models.SoulAgentIdentity) string {
	if s == nil || identity == nil {
		return ""
	}
	agentID := strings.ToLower(strings.TrimSpace(identity.AgentID))
	wallet := strings.ToLower(strings.TrimSpace(identity.Wallet))
	principalAddress := strings.ToLower(strings.TrimSpace(identity.PrincipalAddress))
	if agentID == "" || wallet == "" || principalAddress == "" {
		return ""
	}

	metaURI := strings.TrimSpace(identity.MetaURI)
	if metaURI == "" {
		metaURI = s.soulMetaURI(agentID)
	}

	return soulOpID(
		models.SoulOperationKindMint,
		s.cfg.SoulChainID,
		strings.TrimSpace(s.cfg.SoulRegistryContractAddress),
		agentID,
		wallet,
		metaURI,
		"selfMintSoul|principal="+principalAddress,
	)
}

func buildSoulAgentMintOperationResponse(op *models.SoulOperation) soulAgentMintOperationResponse {
	if op == nil {
		return soulAgentMintOperationResponse{}
	}
	return soulAgentMintOperationResponse{
		Operation: *op,
		SafeTx:    parseSafeTxPayload(op.SafePayloadJSON),
	}
}

func parseSafeTxPayload(raw string) *safeTxPayload {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var payload safeTxPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	if strings.TrimSpace(payload.SafeAddress) == "" &&
		strings.TrimSpace(payload.To) == "" &&
		strings.TrimSpace(payload.Value) == "" &&
		strings.TrimSpace(payload.Data) == "" {
		return nil
	}
	return &payload
}
