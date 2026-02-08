package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type portalWalletChallengeRequest struct {
	Address string `json:"address"`
	ChainID int    `json:"chainId,omitempty"`
}

type portalWalletLoginRequest struct {
	ChallengeID string `json:"challengeId"`
	Address     string `json:"address"`
	Signature   string `json:"signature"`
	Message     string `json:"message"`

	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type portalMeResponse struct {
	Username    string `json:"username"`
	Role        string `json:"role"`
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
	Method      string `json:"method,omitempty"`
}

func portalUsernameForWalletAddress(address string) string {
	address = strings.ToLower(strings.TrimSpace(address))
	address = strings.TrimPrefix(address, "0x")
	address = strings.TrimSpace(address)
	return "wallet-" + address
}

func parsePortalWalletLogin(ctx *apptheory.Context) (portalWalletLoginRequest, error) {
	var req portalWalletLoginRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return req, err
	}

	req.ChallengeID = strings.TrimSpace(req.ChallengeID)
	req.Address = strings.TrimSpace(req.Address)
	req.Signature = strings.TrimSpace(req.Signature)
	req.Message = strings.TrimSpace(req.Message)
	req.Email = strings.TrimSpace(req.Email)
	req.DisplayName = strings.TrimSpace(req.DisplayName)

	if req.ChallengeID == "" {
		return req, &apptheory.AppError{Code: "app.bad_request", Message: "challengeId is required"}
	}
	if req.Address == "" {
		return req, &apptheory.AppError{Code: "app.bad_request", Message: "address is required"}
	}
	if req.Signature == "" {
		return req, &apptheory.AppError{Code: "app.bad_request", Message: "signature is required"}
	}
	if req.Message == "" {
		return req, &apptheory.AppError{Code: "app.bad_request", Message: "message is required"}
	}

	return req, nil
}

func (s *Server) handlePortalWalletChallenge(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var req portalWalletChallengeRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	req.Address = strings.TrimSpace(req.Address)
	if req.Address == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "address is required"}
	}
	if req.ChainID == 0 {
		req.ChainID = 1
	}

	username := portalUsernameForWalletAddress(req.Address)
	challenge, err := s.createWalletChallenge(ctx.Context(), req.Address, req.ChainID, username)
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

func (s *Server) validatePortalWalletLoginChallenge(ctx *apptheory.Context, req portalWalletLoginRequest) (*models.WalletChallenge, string, string, error) {
	challenge, err := s.getWalletChallenge(ctx.Context(), req.ChallengeID)
	if theoryErrors.IsNotFound(err) {
		return nil, "", "", &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if err != nil {
		return nil, "", "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	username := strings.TrimSpace(challenge.Username)
	if username == "" {
		return nil, "", "", &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	address := strings.ToLower(strings.TrimSpace(req.Address))
	if address != strings.ToLower(strings.TrimSpace(challenge.Address)) {
		return nil, "", "", &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if strings.TrimSpace(req.Message) != strings.TrimSpace(challenge.Message) {
		return nil, "", "", &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	if err := verifyEthereumSignature(address, req.Message, req.Signature); err != nil {
		return nil, "", "", &apptheory.AppError{Code: "app.unauthorized", Message: "invalid signature"}
	}
	_ = s.deleteWalletChallenge(ctx.Context(), req.ChallengeID)

	return challenge, username, address, nil
}

func defaultDisplayNameForWallet(address string) string {
	short := strings.ToLower(strings.TrimPrefix(address, "0x"))
	if len(short) > 8 {
		short = short[:8]
	}
	return "Wallet " + short
}

func (s *Server) getUserProfile(ctx *apptheory.Context, username string) (models.User, bool, error) {
	var user models.User
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.User{}).
		Where("PK", "=", fmt.Sprintf(models.KeyPatternUser, username)).
		Where("SK", "=", models.SKProfile).
		First(&user)
	if theoryErrors.IsNotFound(err) {
		return models.User{}, false, nil
	}
	if err != nil {
		return models.User{}, false, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return user, true, nil
}

func (s *Server) createPortalWalletUser(ctx *apptheory.Context, username string, address string, chainID int, email string, displayName string, now time.Time) (models.User, error) {
	if displayName == "" {
		displayName = defaultDisplayNameForWallet(address)
	}

	newUser := &models.User{
		Username:    username,
		Role:        models.RoleCustomer,
		Approved:    false,
		DisplayName: displayName,
		Email:       email,
		CreatedAt:   now,
	}
	_ = newUser.UpdateKeys()

	cred := &models.WalletCredential{
		Username: username,
		Address:  address,
		ChainID:  chainID,
		Type:     "ethereum",
		LinkedAt: now,
		LastUsed: now,
	}
	_ = cred.UpdateKeys()

	index := &models.WalletIndex{}
	index.UpdateKeys("ethereum", address, username)

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Create(newUser)
		tx.Create(cred)
		tx.Create(index)
		return nil
	}); err != nil {
		if !theoryErrors.IsConditionFailed(err) {
			return models.User{}, &apptheory.AppError{Code: "app.internal", Message: "failed to create user"}
		}

		if user, found, getErr := s.getUserProfile(ctx, username); getErr == nil && found {
			return user, nil
		}
	}

	return *newUser, nil
}

func (s *Server) linkPortalWalletToCustomer(ctx *apptheory.Context, user models.User, username string, address string, chainID int, email string, now time.Time) error {
	if strings.TrimSpace(user.Role) != models.RoleCustomer {
		return &apptheory.AppError{Code: "app.forbidden", Message: "wallet is not linked to this user"}
	}

	cred := &models.WalletCredential{
		Username: username,
		Address:  address,
		ChainID:  chainID,
		Type:     "ethereum",
		LinkedAt: now,
		LastUsed: now,
	}
	_ = cred.UpdateKeys()

	index := &models.WalletIndex{}
	index.UpdateKeys("ethereum", address, username)

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Create(cred)
		tx.Create(index)
		return nil
	}); err != nil {
		if !theoryErrors.IsConditionFailed(err) {
			return &apptheory.AppError{Code: "app.internal", Message: "failed to link wallet"}
		}
	}

	if email != "" && strings.TrimSpace(user.Email) == "" {
		update := &models.User{
			Username: username,
			Email:    email,
		}
		_ = update.UpdateKeys()
		_ = s.store.DB.WithContext(ctx.Context()).Model(update).IfExists().Update("Email")
	}

	return nil
}

func (s *Server) ensurePortalWalletUser(
	ctx *apptheory.Context,
	username string,
	address string,
	chainID int,
	linked string,
	email string,
	displayName string,
	now time.Time,
) (models.User, error) {
	user, found, err := s.getUserProfile(ctx, username)
	if err != nil {
		return models.User{}, err
	}

	if !found {
		return s.createPortalWalletUser(ctx, username, address, chainID, email, displayName, now)
	}

	if linked == "" {
		if err := s.linkPortalWalletToCustomer(ctx, user, username, address, chainID, email, now); err != nil {
			return models.User{}, err
		}
	}

	return user, nil
}

func (s *Server) handlePortalWalletLogin(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	req, err := parsePortalWalletLogin(ctx)
	if err != nil {
		return nil, err
	}

	challenge, username, address, err := s.validatePortalWalletLoginChallenge(ctx, req)
	if err != nil {
		return nil, err
	}

	linked, err := s.walletLinkedUsername(ctx, "ethereum", address)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if linked != "" && linked != username {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "wallet is already linked to a different user"}
	}

	now := time.Now().UTC()
	user, err := s.ensurePortalWalletUser(ctx, username, address, challenge.ChainID, linked, req.Email, req.DisplayName, now)
	if err != nil {
		return nil, err
	}

	role := strings.TrimSpace(user.Role)
	if role == "" {
		role = models.RoleCustomer
	}

	token, expiresAt, err := s.createOperatorSession(ctx.Context(), username, role, "wallet")
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create session"}
	}

	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "auth.portal.wallet.login",
		Target:    "operator_session",
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, operatorLoginResponse{
		TokenType: "Bearer",
		Token:     token,
		ExpiresAt: expiresAt,
		Username:  username,
		Role:      role,
		Method:    "wallet",
	})
}

func (s *Server) handlePortalMe(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	if username == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	var user models.User
	err := s.store.DB.WithContext(ctx.Context()).
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

	return apptheory.JSON(http.StatusOK, portalMeResponse{
		Username:    user.Username,
		Role:        strings.TrimSpace(user.Role),
		DisplayName: strings.TrimSpace(user.DisplayName),
		Email:       strings.TrimSpace(user.Email),
		Method:      strings.TrimSpace(operatorMethodFromContext(ctx)),
	})
}
