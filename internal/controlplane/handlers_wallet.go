package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

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
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	req.Address = strings.TrimSpace(req.Address)
	req.Username = strings.TrimSpace(req.Username)
	if req.Address == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "address is required"}
	}
	if req.Username == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "username is required"}
	}
	if req.ChainID == 0 {
		req.ChainID = 1
	}

	challenge, err := s.createWalletChallenge(ctx.Context(), req.Address, req.ChainID, req.Username)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create challenge"}
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
	var req walletVerifyRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	req.ChallengeID = strings.TrimSpace(req.ChallengeID)
	req.Address = strings.TrimSpace(req.Address)
	req.Signature = strings.TrimSpace(req.Signature)
	req.Message = strings.TrimSpace(req.Message)

	if req.ChallengeID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "challengeId is required"}
	}
	if req.Address == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "address is required"}
	}
	if req.Signature == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "signature is required"}
	}
	if req.Message == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "message is required"}
	}

	challenge, err := s.getWalletChallenge(ctx.Context(), req.ChallengeID)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	username := strings.TrimSpace(challenge.Username)
	if username == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	address := strings.ToLower(strings.TrimSpace(req.Address))
	if address != strings.ToLower(strings.TrimSpace(challenge.Address)) {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if req.Message != strings.TrimSpace(challenge.Message) {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	if err := verifyEthereumSignature(address, req.Message, req.Signature); err != nil {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "invalid signature"}
	}
	_ = s.deleteWalletChallenge(ctx.Context(), req.ChallengeID)

	linked, err := s.walletLinkedUsername(ctx, "ethereum", address)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if linked == "" || linked != username {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	var user models.User
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.User{}).
		Where("PK", "=", fmt.Sprintf(models.KeyPatternUser, username)).
		Where("SK", "=", models.SKProfile).
		First(&user)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	token, expiresAt, err := s.createOperatorSession(ctx.Context(), username, user.Role, "wallet")
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create session"}
	}

	now := time.Now().UTC()
	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "auth.wallet.login",
		Target:    "operator_session",
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to write audit log"}
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
