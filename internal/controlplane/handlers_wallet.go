package controlplane

import (
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type operatorLoginResponse struct {
	TokenType string    `json:"token_type"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`

	Username string `json:"username"`
	Role     string `json:"role"`
	Method   string `json:"method"`
}

func (s *Server) handleWalletChallenge(ctx *apptheory.Context) (*apptheory.Response, error) {
	var req walletChallengeRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	req.Address = strings.TrimSpace(req.Address)
	req.Username = strings.TrimSpace(req.Username)
	if req.Address == "" {
		return nil, newAppTheoryError("app.bad_request", "address is required")
	}
	if req.Username == "" {
		return nil, newAppTheoryError("app.bad_request", "username is required")
	}
	if req.ChainID == 0 {
		req.ChainID = 1
	}

	challenge, err := s.createWalletChallenge(ctx.Context(), req.Address, req.ChainID, req.Username)
	if err != nil {
		return nil, newAppTheoryError("app.internal", "failed to create challenge")
	}

	return apptheory.JSON(http.StatusOK, walletChallengeResponse{
		ID:        challenge.ID,
		Username:  challenge.Username,
		Address:   challenge.Address,
		ChainID:   challenge.ChainID,
		Nonce:     challenge.Nonce,
		Message:   challenge.Message,
		IssuedAt:  challenge.IssuedAt,
		ExpiresAt: challenge.ExpiresAt,
	})
}

func (s *Server) handleWalletLogin(ctx *apptheory.Context) (*apptheory.Response, error) {
	req, err := parseWalletLoginRequest(ctx)
	if err != nil {
		return nil, err
	}

	username, err := s.verifyWalletLoginChallenge(ctx, req)
	if err != nil {
		return nil, err
	}

	user, err := s.loadUser(ctx, username)
	if theoryErrors.IsNotFound(err) || user == nil {
		return nil, newAppTheoryError("app.unauthorized", "unauthorized")
	}
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	token, expiresAt, err := s.createOperatorSession(ctx.Context(), username, user.Role, "wallet")
	if err != nil {
		return nil, newAppTheoryError("app.internal", "failed to create session")
	}

	now := time.Now().UTC()
	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "auth.wallet.login",
		Target:    "operator_session",
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	applyAuditSourceProvenance(ctx, audit)
	_ = audit.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, newAppTheoryError("app.internal", "failed to write audit log")
	}

	return apptheory.JSON(http.StatusOK, operatorLoginResponse{
		TokenType: "Bearer",
		Token:     token,
		ExpiresAt: expiresAt,
		Username:  username,
		Role:      user.Role,
		Method:    "wallet",
	})
}

func parseWalletLoginRequest(ctx *apptheory.Context) (walletVerifyRequest, error) {
	var req walletVerifyRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return walletVerifyRequest{}, err
	}

	req.ChallengeID = strings.TrimSpace(req.ChallengeID)
	req.Address = strings.TrimSpace(req.Address)
	req.Signature = strings.TrimSpace(req.Signature)
	req.Message = strings.TrimSpace(req.Message)

	if req.ChallengeID == "" {
		return walletVerifyRequest{}, newAppTheoryError("app.bad_request", "challengeId is required")
	}
	if req.Address == "" {
		return walletVerifyRequest{}, newAppTheoryError("app.bad_request", "address is required")
	}
	if req.Signature == "" {
		return walletVerifyRequest{}, newAppTheoryError("app.bad_request", "signature is required")
	}
	if req.Message == "" {
		return walletVerifyRequest{}, newAppTheoryError("app.bad_request", "message is required")
	}

	return req, nil
}

func (s *Server) verifyWalletLoginChallenge(ctx *apptheory.Context, req walletVerifyRequest) (string, error) {
	if s == nil || ctx == nil {
		return "", newAppTheoryError("app.internal", "internal error")
	}

	challenge, err := s.getWalletChallenge(ctx.Context(), req.ChallengeID)
	if theoryErrors.IsNotFound(err) {
		return "", newAppTheoryError("app.unauthorized", "unauthorized")
	}
	if err != nil {
		return "", newAppTheoryError("app.internal", "internal error")
	}

	username := strings.TrimSpace(challenge.Username)
	if username == "" {
		return "", newAppTheoryError("app.unauthorized", "unauthorized")
	}

	address := strings.ToLower(strings.TrimSpace(req.Address))
	if address != strings.ToLower(strings.TrimSpace(challenge.Address)) {
		return "", newAppTheoryError("app.unauthorized", "unauthorized")
	}
	if req.Message != strings.TrimSpace(challenge.Message) {
		return "", newAppTheoryError("app.unauthorized", "unauthorized")
	}

	if verifyErr := verifyEthereumSignature(address, req.Message, req.Signature); verifyErr != nil {
		return "", newAppTheoryError("app.unauthorized", "invalid signature")
	}
	if consumeErr := s.consumeWalletChallenge(ctx.Context(), req.ChallengeID); consumeErr != nil {
		if theoryErrors.IsConditionFailed(consumeErr) || theoryErrors.IsNotFound(consumeErr) {
			return "", newAppTheoryError("app.unauthorized", "unauthorized")
		}
		return "", newAppTheoryError("app.internal", "internal error")
	}

	linked, err := s.walletLinkedUsername(ctx, walletTypeEthereum, address)
	if err != nil {
		return "", newAppTheoryError("app.internal", "internal error")
	}
	if linked == "" {
		return "", newAppTheoryError("app.unauthorized", "unauthorized")
	}
	if linked != username {
		return "", newAppTheoryError("app.unauthorized", "unauthorized")
	}

	return username, nil
}
