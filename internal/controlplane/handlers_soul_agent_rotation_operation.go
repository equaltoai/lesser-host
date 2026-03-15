package controlplane

import (
	"context"
	"net/http"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulAgentRotationOperationResponse struct {
	Operation models.SoulOperation `json:"operation"`
	SafeTx    *safeTxPayload       `json:"safe_tx,omitempty"`
}

func (s *Server) handleSoulAgentGetRotationOperation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}
	identity, appErr := s.requireSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}

	op, appErr := s.loadSoulRotationOperationForIdentity(ctx.Context(), identity, strings.TrimSpace(ctx.AuthIdentity))
	if appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(http.StatusOK, buildSoulAgentRotationOperationResponse(op))
}

func (s *Server) handleSoulAgentRecordRotationOperationExecution(ctx *apptheory.Context) (*apptheory.Response, error) {
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

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}
	identity, appErr := s.requireSoulAgentWithDomainAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	txHash, appErr := parseSoulOperationExecutionTxHash(ctx)
	if appErr != nil {
		return nil, appErr
	}

	op, appErr := s.loadSoulRotationOperationForIdentity(ctx.Context(), identity, strings.TrimSpace(ctx.AuthIdentity))
	if appErr != nil {
		return nil, appErr
	}

	updated, appErr := s.recordSoulOperationExecution(ctx.Context(), strings.TrimSpace(ctx.AuthIdentity), ctx.RequestID, op, txHash)
	if appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(http.StatusOK, buildSoulAgentRotationOperationResponse(updated))
}

func (s *Server) loadSoulRotationOperationForIdentity(
	ctx context.Context,
	identity *models.SoulAgentIdentity,
	username string,
) (*models.SoulOperation, *apptheory.AppError) {
	if s == nil || identity == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(username) == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	rot, err := s.getSoulWalletRotationRequest(ctx, identity.AgentID, username)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "rotation operation not found"}
	}
	if err != nil || rot == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	_, txTo, appErr := s.soulRegistryContractAddress()
	if appErr != nil {
		return nil, appErr
	}
	opID := soulRotationOpID(s.cfg.SoulChainID, txTo, identity.AgentID, rot.CurrentWallet, rot.NewWallet, rot.Nonce, rot.Deadline)

	op, err := s.getSoulOperation(ctx, opID)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "rotation operation not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.ToLower(strings.TrimSpace(op.Kind)) != models.SoulOperationKindRotateWallet {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "rotation operation not found"}
	}
	return op, nil
}

func buildSoulAgentRotationOperationResponse(op *models.SoulOperation) soulAgentRotationOperationResponse {
	if op == nil {
		return soulAgentRotationOperationResponse{}
	}
	return soulAgentRotationOperationResponse{
		Operation: *op,
		SafeTx:    parseSafeTxPayload(op.SafePayloadJSON),
	}
}
