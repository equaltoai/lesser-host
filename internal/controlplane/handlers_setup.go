package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	"github.com/theory-cloud/tabletheory/v3"
	"github.com/theory-cloud/tabletheory/v3/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	setupPurposeBootstrap = "bootstrap"
	setupBootstrapUser    = "bootstrap"
)

type setupStatusResponse struct {
	ControlPlaneState string     `json:"control_plane_state"`
	Locked            bool       `json:"locked"`
	FinalizeAllowed   bool       `json:"finalize_allowed"`
	BootstrappedAt    *time.Time `json:"bootstrapped_at,omitempty"`

	BootstrapWalletAddressSet bool   `json:"bootstrap_wallet_address_set"`
	BootstrapWalletAddress    string `json:"bootstrap_wallet_address,omitempty"`

	PrimaryAdminSet      bool   `json:"primary_admin_set"`
	PrimaryAdminUsername string `json:"primary_admin_username,omitempty"`

	Stage string `json:"stage"`
}

type setupBootstrapChallengeRequest struct {
	Address string `json:"address"`
	ChainID int    `json:"chainId,omitempty"`
}

type setupBootstrapVerifyRequest struct {
	ChallengeID      string `json:"challengeId,omitempty"`
	ChallengeIDSnake string `json:"challenge_id,omitempty"`
	Address          string `json:"address"`
	Signature        string `json:"signature"`
	Message          string `json:"message,omitempty"`
	Challenge        string `json:"challenge,omitempty"`
}

type setupBootstrapVerifyResponse struct {
	TokenType string    `json:"token_type"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type setupCreateAdminRequest struct {
	Username    string                             `json:"username"`
	DisplayName string                             `json:"displayName,omitempty"`
	Wallet      *walletVerifyRequest               `json:"wallet,omitempty"`
	Passkey     *webAuthnFinishRegistrationRequest `json:"passkey,omitempty"`
}

type setupCreateAdminResponse struct {
	Username  string    `json:"username"`
	TokenType string    `json:"token_type,omitempty"`
	Token     string    `json:"token,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Role      string    `json:"role,omitempty"`
	Method    string    `json:"method,omitempty"`
}

type setupPasskeyRegisterBeginRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName,omitempty"`
}

type setupFinalizeResponse struct {
	Locked         bool       `json:"locked"`
	BootstrappedAt *time.Time `json:"bootstrapped_at,omitempty"`
}

func (s *Server) loadControlPlaneConfig(ctx *apptheory.Context) (*models.ControlPlaneConfig, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	var cfg models.ControlPlaneConfig
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.ControlPlaneConfig{}).
		Where("PK", "=", "CONTROL_PLANE").
		Where("SK", "=", "CONFIG").
		First(&cfg)
	if theoryErrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (s *Server) controlPlaneLocked(ctx *apptheory.Context) (locked bool, cfg *models.ControlPlaneConfig, err error) {
	cfg, err = s.loadControlPlaneConfig(ctx)
	if err != nil {
		return false, nil, err
	}
	if cfg == nil {
		return true, nil, nil
	}
	return cfg.BootstrappedAt.IsZero(), cfg, nil
}

func (s *Server) handleSetupStatus(ctx *apptheory.Context) (*apptheory.Response, error) {
	locked, cfg, err := s.controlPlaneLocked(ctx)
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	var (
		bootstrappedAt *time.Time
		primaryAdmin   string
	)
	if cfg != nil {
		primaryAdmin = strings.TrimSpace(cfg.PrimaryAdminUsername)
		if !cfg.BootstrappedAt.IsZero() {
			t := cfg.BootstrappedAt.UTC()
			bootstrappedAt = &t
		}
	}

	bootstrapWallet := strings.TrimSpace(s.cfg.BootstrapWalletAddress)
	// Only expose the bootstrap wallet address while the control plane is locked
	// (pre-bootstrap). Once bootstrapped, omit the address to avoid leaking
	// sensitive configuration to unauthenticated callers.
	exposedWallet := ""
	if locked {
		exposedWallet = bootstrapWallet
	}
	resp := setupStatusResponse{
		ControlPlaneState: func() string {
			if locked {
				return "locked"
			}
			return "active"
		}(),
		Locked:          locked,
		FinalizeAllowed: locked && primaryAdmin != "",
		BootstrappedAt:  bootstrappedAt,

		BootstrapWalletAddressSet: bootstrapWallet != "",
		BootstrapWalletAddress:    exposedWallet,

		PrimaryAdminSet:      primaryAdmin != "",
		PrimaryAdminUsername: primaryAdmin,

		Stage: strings.TrimSpace(s.cfg.Stage),
	}

	return apptheory.JSON(http.StatusOK, resp)
}

func (s *Server) handleSetupBootstrapChallenge(ctx *apptheory.Context) (*apptheory.Response, error) {
	locked, _, err := s.controlPlaneLocked(ctx)
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if !locked {
		return nil, newAppTheoryError("app.conflict", "control plane is already bootstrapped")
	}

	bootstrapWallet := strings.ToLower(strings.TrimSpace(s.cfg.BootstrapWalletAddress))
	if bootstrapWallet == "" {
		return nil, newAppTheoryError("app.conflict", "bootstrap wallet is not configured")
	}

	var req setupBootstrapChallengeRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return nil, parseErr
	}

	if strings.TrimSpace(req.Address) == "" {
		return nil, newAppTheoryError("app.bad_request", "address is required")
	}
	if req.ChainID == 0 {
		req.ChainID = 1
	}

	if strings.ToLower(strings.TrimSpace(req.Address)) != bootstrapWallet {
		return nil, newAppTheoryError("app.forbidden", "wallet does not match bootstrap credential")
	}

	challenge, err := s.createWalletChallenge(ctx.Context(), bootstrapWallet, req.ChainID, setupBootstrapUser)
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

func (s *Server) handleSetupBootstrapVerify(ctx *apptheory.Context) (*apptheory.Response, error) {
	locked, _, err := s.controlPlaneLocked(ctx)
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if !locked {
		return nil, newAppTheoryError("app.conflict", "control plane is already bootstrapped")
	}

	bootstrapWallet := strings.ToLower(strings.TrimSpace(s.cfg.BootstrapWalletAddress))
	if bootstrapWallet == "" {
		return nil, newAppTheoryError("app.conflict", "bootstrap wallet is not configured")
	}

	in, err := parseSetupBootstrapVerifyInput(ctx)
	if err != nil {
		return nil, err
	}
	if verifyErr := s.verifySetupBootstrapChallenge(ctx, bootstrapWallet, in); verifyErr != nil {
		return nil, verifyErr
	}

	token, err := newToken(32)
	if err != nil {
		return nil, newAppTheoryError("app.internal", "failed to create setup session")
	}

	now := time.Now().UTC()
	expiresAt := now.Add(1 * time.Hour)

	storedID := sha256HexTrimmed(token)

	session := &models.SetupSession{
		ID:           storedID,
		Purpose:      setupPurposeBootstrap,
		WalletType:   walletTypeEthereum,
		WalletAddr:   bootstrapWallet,
		IssuedAt:     now,
		ExpiresAt:    expiresAt,
		InstanceLock: true,
	}
	if err := session.UpdateKeys(); err != nil {
		return nil, newAppTheoryError("app.internal", "failed to create setup session")
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(session).Create(); err != nil {
		return nil, newAppTheoryError("app.internal", "failed to create setup session")
	}

	return apptheory.JSON(http.StatusOK, setupBootstrapVerifyResponse{
		TokenType: "Bearer",
		Token:     token,
		ExpiresAt: expiresAt,
	})
}

type setupBootstrapVerifyInput struct {
	ChallengeID string
	Address     string
	Signature   string
	Message     string
}

func parseSetupBootstrapVerifyInput(ctx *apptheory.Context) (setupBootstrapVerifyInput, error) {
	var raw setupBootstrapVerifyRequest
	if parseErr := httpx.ParseJSON(ctx, &raw); parseErr != nil {
		return setupBootstrapVerifyInput{}, parseErr
	}

	challengeID := strings.TrimSpace(raw.ChallengeID)
	if challengeID == "" {
		challengeID = strings.TrimSpace(raw.ChallengeIDSnake)
	}
	message := strings.TrimSpace(raw.Message)
	if message == "" {
		message = strings.TrimSpace(raw.Challenge)
	}

	address := strings.TrimSpace(raw.Address)
	signature := strings.TrimSpace(raw.Signature)

	if challengeID == "" {
		return setupBootstrapVerifyInput{}, newAppTheoryError("app.bad_request", "challengeId is required")
	}
	if address == "" {
		return setupBootstrapVerifyInput{}, newAppTheoryError("app.bad_request", "address is required")
	}
	if signature == "" {
		return setupBootstrapVerifyInput{}, newAppTheoryError("app.bad_request", "signature is required")
	}
	if message == "" {
		return setupBootstrapVerifyInput{}, newAppTheoryError("app.bad_request", "message is required")
	}

	return setupBootstrapVerifyInput{
		ChallengeID: challengeID,
		Address:     address,
		Signature:   signature,
		Message:     message,
	}, nil
}

func (s *Server) verifySetupBootstrapChallenge(ctx *apptheory.Context, bootstrapWallet string, in setupBootstrapVerifyInput) error {
	if s == nil || ctx == nil {
		return newAppTheoryError("app.internal", "internal error")
	}

	if strings.ToLower(strings.TrimSpace(in.Address)) != bootstrapWallet {
		return newAppTheoryError("app.forbidden", "wallet does not match bootstrap credential")
	}

	challenge, err := s.getWalletChallenge(ctx.Context(), in.ChallengeID)
	if theoryErrors.IsNotFound(err) {
		return newAppTheoryError("app.unauthorized", "unauthorized")
	}
	if err != nil {
		return newAppTheoryError("app.internal", "internal error")
	}

	if strings.TrimSpace(challenge.Username) != setupBootstrapUser {
		return newAppTheoryError("app.forbidden", "challenge is not bound to bootstrap identity")
	}
	if strings.ToLower(strings.TrimSpace(challenge.Address)) != bootstrapWallet {
		return newAppTheoryError("app.forbidden", "challenge address mismatch")
	}
	if strings.TrimSpace(challenge.Message) != in.Message {
		return newAppTheoryError("app.forbidden", "message mismatch")
	}

	if verifyErr := verifyEthereumSignature(bootstrapWallet, in.Message, in.Signature); verifyErr != nil {
		return newAppTheoryError("app.unauthorized", "invalid signature")
	}
	if consumeErr := s.consumeWalletChallenge(ctx.Context(), in.ChallengeID); consumeErr != nil {
		if theoryErrors.IsConditionFailed(consumeErr) || theoryErrors.IsNotFound(consumeErr) {
			return newAppTheoryError("app.unauthorized", "unauthorized")
		}
		return newAppTheoryError("app.internal", "internal error")
	}

	return nil
}

func (s *Server) requireSetupSession(ctx *apptheory.Context) (*models.SetupSession, error) {
	token := httpx.BearerToken(ctx.Request.Headers)
	if token == "" {
		return nil, newAppTheoryError("app.unauthorized", "unauthorized")
	}

	var session models.SetupSession
	var err error
	candidates := []string{sha256HexTrimmed(token), token} // hash lookup first; fallback to legacy plaintext.
	found := false
	for _, id := range candidates {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		err = s.store.DB.WithContext(ctx.Context()).
			Model(&models.SetupSession{}).
			Where("PK", "=", fmt.Sprintf("SETUP_SESSION#%s", id)).
			Where("SK", "=", "SESSION").
			First(&session)
		if theoryErrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, newAppTheoryError("app.internal", "internal error")
		}
		found = true
		break
	}
	if !found {
		return nil, newAppTheoryError("app.unauthorized", "unauthorized")
	}

	if !session.ExpiresAt.IsZero() && time.Now().After(session.ExpiresAt) {
		_ = s.store.DB.WithContext(ctx.Context()).
			Model(&models.SetupSession{}).
			Where("PK", "=", session.PK).
			Where("SK", "=", session.SK).
			Delete()
		return nil, newAppTheoryError("app.unauthorized", "unauthorized")
	}
	if strings.TrimSpace(session.Purpose) != setupPurposeBootstrap {
		return nil, newAppTheoryError("app.unauthorized", "unauthorized")
	}

	bootstrapWallet := strings.ToLower(strings.TrimSpace(s.cfg.BootstrapWalletAddress))
	if bootstrapWallet == "" || strings.ToLower(strings.TrimSpace(session.WalletAddr)) != bootstrapWallet {
		return nil, newAppTheoryError("app.unauthorized", "unauthorized")
	}

	return &session, nil
}

func (s *Server) walletLinkedUsername(ctx *apptheory.Context, walletType, address string) (string, error) {
	walletType = strings.TrimSpace(walletType)
	if walletType == "" {
		walletType = walletTypeEthereum
	}
	address = strings.ToLower(strings.TrimSpace(address))
	if address == "" {
		return "", newAppTheoryError("app.bad_request", "address is required")
	}

	var index models.WalletIndex
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.WalletIndex{}).
		Where("PK", "=", fmt.Sprintf("WALLET#%s#%s", walletType, address)).
		Limit(1).
		First(&index)
	if theoryErrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(index.Username), nil
}

func (s *Server) validateSetupCreateAdminState(ctx *apptheory.Context) (*models.ControlPlaneConfig, *apptheory.AppTheoryError) {
	locked, cfg, err := s.controlPlaneLocked(ctx)
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if !locked {
		return nil, newAppTheoryError("app.conflict", "control plane is already bootstrapped")
	}
	if cfg != nil && strings.TrimSpace(cfg.PrimaryAdminUsername) != "" {
		return nil, newAppTheoryError("app.conflict", "primary admin already created")
	}

	if _, setupErr := s.requireSetupSession(ctx); setupErr != nil {
		if appErr, ok := setupErr.(*apptheory.AppTheoryError); ok {
			return nil, appErr
		}
		return nil, newAppTheoryError("app.unauthorized", "unauthorized")
	}

	return cfg, nil
}

func validateSetupAdminIdentity(username string, displayName string) (string, string, *apptheory.AppTheoryError) {
	displayName = strings.TrimSpace(displayName)
	username = strings.TrimSpace(username)
	if username == "" {
		return "", "", newAppTheoryError("app.bad_request", "username is required")
	}

	normalized, err := soul.ValidateManagedHandle(username)
	if err != nil {
		return "", "", newAppTheoryError("app.bad_request", err.Error())
	}
	if strings.EqualFold(normalized, setupBootstrapUser) {
		return "", "", newAppTheoryError("app.bad_request", "username is reserved")
	}
	return normalized, displayName, nil
}

func parseSetupPasskeyRegisterBeginRequestInput(ctx *apptheory.Context) (setupPasskeyRegisterBeginRequest, *apptheory.AppTheoryError) {
	var req setupPasskeyRegisterBeginRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		if appErr, ok := parseErr.(*apptheory.AppTheoryError); ok {
			return setupPasskeyRegisterBeginRequest{}, appErr
		}
		return setupPasskeyRegisterBeginRequest{}, newAppTheoryError("app.bad_request", "invalid request")
	}

	username, displayName, appErr := validateSetupAdminIdentity(req.Username, req.DisplayName)
	if appErr != nil {
		return setupPasskeyRegisterBeginRequest{}, appErr
	}
	req.Username = username
	req.DisplayName = displayName
	return req, nil
}

func (s *Server) setupAdminExistingUserConflict(ctx *apptheory.Context, username string) *apptheory.AppTheoryError {
	user, err := s.loadUser(ctx, username)
	if theoryErrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if strings.TrimSpace(user.Role) == models.RoleAdmin {
		return newAppTheoryError("app.conflict", "setup primary admin candidate already exists; resolve partial setup state before retrying")
	}
	return newAppTheoryError("app.conflict", "username already exists")
}

func parseSetupCreateAdminRequestInput(ctx *apptheory.Context) (setupCreateAdminRequest, *apptheory.AppTheoryError) {
	var req setupCreateAdminRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		if appErr, ok := parseErr.(*apptheory.AppTheoryError); ok {
			return setupCreateAdminRequest{}, appErr
		}
		return setupCreateAdminRequest{}, newAppTheoryError("app.bad_request", "invalid request")
	}

	username, displayName, appErr := validateSetupAdminIdentity(req.Username, req.DisplayName)
	if appErr != nil {
		return setupCreateAdminRequest{}, appErr
	}
	req.Username = username
	req.DisplayName = displayName

	if (req.Wallet == nil) == (req.Passkey == nil) {
		return setupCreateAdminRequest{}, newAppTheoryError("app.bad_request", "exactly one admin credential path is required")
	}

	if req.Wallet != nil {
		req.Wallet.ChallengeID = strings.TrimSpace(req.Wallet.ChallengeID)
		req.Wallet.Address = strings.TrimSpace(req.Wallet.Address)
		req.Wallet.Signature = strings.TrimSpace(req.Wallet.Signature)
		req.Wallet.Message = strings.TrimSpace(req.Wallet.Message)

		if req.Wallet.ChallengeID == "" {
			return setupCreateAdminRequest{}, newAppTheoryError("app.bad_request", "wallet.challengeId is required")
		}
		if req.Wallet.Address == "" {
			return setupCreateAdminRequest{}, newAppTheoryError("app.bad_request", "wallet.address is required")
		}
		if req.Wallet.Signature == "" {
			return setupCreateAdminRequest{}, newAppTheoryError("app.bad_request", "wallet.signature is required")
		}
		if req.Wallet.Message == "" {
			return setupCreateAdminRequest{}, newAppTheoryError("app.bad_request", "wallet.message is required")
		}
	}

	if req.Passkey != nil {
		req.Passkey.Challenge = strings.TrimSpace(req.Passkey.Challenge)
		req.Passkey.CredentialName = strings.TrimSpace(req.Passkey.CredentialName)
		if req.Passkey.Challenge == "" {
			return setupCreateAdminRequest{}, newAppTheoryError("app.bad_request", "passkey.challenge is required")
		}
		if len(req.Passkey.Response) == 0 {
			return setupCreateAdminRequest{}, newAppTheoryError("app.bad_request", "passkey.response is required")
		}
	}

	return req, nil
}

func (s *Server) verifySetupCreateAdminWallet(ctx *apptheory.Context, username string, wallet walletVerifyRequest) (string, int, *apptheory.AppTheoryError) {
	challenge, err := s.getWalletChallenge(ctx.Context(), wallet.ChallengeID)
	if theoryErrors.IsNotFound(err) {
		return "", 0, newAppTheoryError("app.unauthorized", "unauthorized")
	}
	if err != nil {
		return "", 0, newAppTheoryError("app.internal", "internal error")
	}
	if strings.TrimSpace(challenge.Username) != strings.TrimSpace(username) {
		return "", 0, newAppTheoryError("app.forbidden", "wallet challenge username mismatch")
	}

	adminWalletAddr := strings.ToLower(strings.TrimSpace(wallet.Address))
	if adminWalletAddr != strings.ToLower(strings.TrimSpace(challenge.Address)) {
		return "", 0, newAppTheoryError("app.forbidden", "wallet challenge address mismatch")
	}
	if strings.TrimSpace(wallet.Message) != strings.TrimSpace(challenge.Message) {
		return "", 0, newAppTheoryError("app.forbidden", "wallet challenge message mismatch")
	}

	existing, err := s.walletLinkedUsername(ctx, walletTypeEthereum, adminWalletAddr)
	if err != nil {
		return "", 0, newAppTheoryError("app.internal", "internal error")
	}
	if existing != "" {
		return "", 0, newAppTheoryError("app.conflict", "wallet is already linked to a user")
	}

	if err := verifyEthereumSignature(adminWalletAddr, wallet.Message, wallet.Signature); err != nil {
		return "", 0, newAppTheoryError("app.unauthorized", "invalid signature")
	}
	if consumeErr := s.consumeWalletChallenge(ctx.Context(), wallet.ChallengeID); consumeErr != nil {
		if theoryErrors.IsConditionFailed(consumeErr) || theoryErrors.IsNotFound(consumeErr) {
			return "", 0, newAppTheoryError("app.unauthorized", "unauthorized")
		}
		return "", 0, newAppTheoryError("app.internal", "internal error")
	}

	return adminWalletAddr, challenge.ChainID, nil
}

func (s *Server) rejectBootstrapWalletAsSetupAdmin(walletAddr string) *apptheory.AppTheoryError {
	bootstrapWallet := strings.ToLower(strings.TrimSpace(s.cfg.BootstrapWalletAddress))
	adminWallet := strings.ToLower(strings.TrimSpace(walletAddr))
	if bootstrapWallet == "" || adminWallet == "" || adminWallet != bootstrapWallet {
		return nil
	}
	return newAppTheoryError("app.forbidden", "bootstrap wallet is one-time setup authority and cannot be the primary admin wallet")
}

func buildSetupAdminUserModel(username string, displayName string, now time.Time) (*models.User, *apptheory.AppTheoryError) {
	user := &models.User{
		Username:       strings.TrimSpace(username),
		Role:           models.RoleAdmin,
		Approved:       true,
		ApprovalStatus: models.UserApprovalStatusApproved,
		DisplayName:    strings.TrimSpace(displayName),
		CreatedAt:      now,
	}
	if err := user.UpdateKeys(); err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	return user, nil
}

func (s *Server) createSetupAdminUser(ctx *apptheory.Context, username string, displayName string, now time.Time) *apptheory.AppTheoryError {
	user, appErr := buildSetupAdminUserModel(username, displayName, now)
	if appErr != nil {
		return appErr
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(user).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return newAppTheoryError("app.conflict", "username already exists")
		}
		return newAppTheoryError("app.internal", "failed to create admin")
	}
	return nil
}

func buildSetupCreateAdminAudit(ctx *apptheory.Context, bootstrapWallet string, username string, now time.Time) *models.AuditLogEntry {
	audit := &models.AuditLogEntry{
		Actor:     fmt.Sprintf("bootstrap_wallet:%s", strings.ToLower(strings.TrimSpace(bootstrapWallet))),
		Action:    "setup.create_admin",
		Target:    fmt.Sprintf("operator:%s", username),
		RequestID: ctx.RequestID,
		CreatedAt: now.UTC(),
	}
	applyAuditSourceProvenance(ctx, audit)
	_ = audit.UpdateKeys()
	return audit
}

func (s *Server) linkSetupAdminWallet(ctx *apptheory.Context, username string, walletAddr string, chainID int, now time.Time) *apptheory.AppTheoryError {
	cred := &models.WalletCredential{
		Username: strings.TrimSpace(username),
		Address:  strings.ToLower(strings.TrimSpace(walletAddr)),
		ChainID:  chainID,
		Type:     walletTypeEthereum,
		LinkedAt: now,
		LastUsed: now,
	}
	if err := cred.UpdateKeys(); err != nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(cred).IfNotExists().Create(); err != nil {
		return newAppTheoryError("app.internal", "failed to link wallet")
	}

	index := &models.WalletIndex{}
	index.UpdateKeys(walletTypeEthereum, walletAddr, username)
	if err := s.store.DB.WithContext(ctx.Context()).Model(index).IfNotExists().Create(); err != nil {
		return newAppTheoryError("app.internal", "failed to link wallet")
	}

	return nil
}

func buildControlPlanePrimaryAdminUpdate(username string) *models.ControlPlaneConfig {
	cp := &models.ControlPlaneConfig{
		PrimaryAdminUsername: strings.TrimSpace(username),
		BootstrappedAt:       time.Time{},
	}
	_ = cp.UpdateKeys()
	return cp
}

func (s *Server) setControlPlanePrimaryAdmin(ctx *apptheory.Context, username string) *apptheory.AppTheoryError {
	cp := buildControlPlanePrimaryAdminUpdate(username)
	if err := s.store.DB.WithContext(ctx.Context()).Model(cp).CreateOrUpdate(); err != nil {
		return newAppTheoryError("app.internal", "failed to update control plane config")
	}
	return nil
}

func (s *Server) handleSetupWebAuthnRegisterBegin(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := s.ensureWebAuthnConfigured(); err != nil {
		return nil, err
	}
	if _, appErr := s.validateSetupCreateAdminState(ctx); appErr != nil {
		return nil, appErr
	}

	req, appErr := parseSetupPasskeyRegisterBeginRequestInput(ctx)
	if appErr != nil {
		return nil, appErr
	}
	if appErr := s.setupAdminExistingUserConflict(ctx, req.Username); appErr != nil {
		return nil, appErr
	}

	return s.beginWebAuthnRegistration(ctx, req.Username, req.DisplayName, nil)
}

func (s *Server) handleSetupCreateAdminWithWallet(ctx *apptheory.Context, req setupCreateAdminRequest) (*apptheory.Response, error) {
	if req.Wallet == nil {
		return nil, newAppTheoryError("app.bad_request", "wallet credential path is required")
	}
	if bootstrapAdminErr := s.rejectBootstrapWalletAsSetupAdmin(req.Wallet.Address); bootstrapAdminErr != nil {
		return nil, bootstrapAdminErr
	}

	adminWalletAddr, chainID, appErr := s.verifySetupCreateAdminWallet(ctx, req.Username, *req.Wallet)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	if appErr := s.createSetupAdminUser(ctx, req.Username, req.DisplayName, now); appErr != nil {
		return nil, appErr
	}
	if appErr := s.linkSetupAdminWallet(ctx, req.Username, adminWalletAddr, chainID, now); appErr != nil {
		return nil, appErr
	}
	if appErr := s.setControlPlanePrimaryAdmin(ctx, req.Username); appErr != nil {
		return nil, appErr
	}

	audit := buildSetupCreateAdminAudit(ctx, s.cfg.BootstrapWalletAddress, req.Username, now)
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, newAppTheoryError("app.internal", "failed to write audit log")
	}

	return apptheory.JSON(http.StatusCreated, setupCreateAdminResponse{Username: req.Username})
}

func (s *Server) mapSetupPasskeyCreateAdminConditionFailure(ctx *apptheory.Context, username string, challenge string) *apptheory.AppTheoryError {
	if appErr := s.setupAdminExistingUserConflict(ctx, username); appErr != nil {
		return appErr
	}
	if _, err := s.getWebAuthnChallenge(ctx, challenge); theoryErrors.IsNotFound(err) {
		return newAppTheoryError("app.unauthorized", "unauthorized")
	} else if err != nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	cfg, err := s.loadControlPlaneConfig(ctx)
	if err != nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if cfg != nil && strings.TrimSpace(cfg.PrimaryAdminUsername) != "" {
		return newAppTheoryError("app.conflict", "primary admin already created")
	}
	return newAppTheoryError("app.conflict", "setup state changed before the admin passkey was committed; reload setup status and retry")
}

func (s *Server) handleSetupCreateAdminWithPasskey(ctx *apptheory.Context, cfg *models.ControlPlaneConfig, req setupCreateAdminRequest) (*apptheory.Response, error) {
	if err := s.ensureWebAuthnConfigured(); err != nil {
		return nil, err
	}
	if req.Passkey == nil {
		return nil, newAppTheoryError("app.bad_request", "passkey credential path is required")
	}
	if appErr := s.setupAdminExistingUserConflict(ctx, req.Username); appErr != nil {
		return nil, appErr
	}

	storedCredential, err := s.completeWebAuthnRegistration(ctx, req.Username, req.DisplayName, *req.Passkey, nil)
	if err != nil {
		return nil, err
	}

	now := storedCredential.CreatedAt.UTC()
	user, appErr := buildSetupAdminUserModel(req.Username, req.DisplayName, now)
	if appErr != nil {
		return nil, appErr
	}
	controlPlane := buildControlPlanePrimaryAdminUpdate(req.Username)
	setupAudit := buildSetupCreateAdminAudit(ctx, s.cfg.BootstrapWalletAddress, req.Username, now)
	passkeyAudit := &models.AuditLogEntry{
		Actor:     req.Username,
		Action:    "auth.webauthn.register",
		Target:    fmt.Sprintf("webauthn_credential:%s", storedCredential.ID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	applyAuditSourceProvenance(ctx, passkeyAudit)
	_ = passkeyAudit.UpdateKeys()

	token, sessionModel, expiresAt, err := buildOperatorSessionModel(req.Username, models.RoleAdmin, "webauthn", now)
	if err != nil {
		return nil, newAppTheoryError("app.internal", "failed to create operator session")
	}

	challengeModel := &models.WebAuthnChallenge{Challenge: req.Passkey.Challenge}
	_ = challengeModel.UpdateKeys()

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Create(user)
		tx.Create(storedCredential)
		tx.Create(sessionModel)
		if cfg == nil {
			tx.Create(controlPlane)
		} else {
			tx.UpdateWithBuilder(controlPlane, func(ub core.UpdateBuilder) error {
				ub.Set("PrimaryAdminUsername", controlPlane.PrimaryAdminUsername)
				ub.Set("BootstrappedAt", controlPlane.BootstrappedAt)
				return nil
			},
				tabletheory.IfExists(),
				tabletheory.Condition("PrimaryAdminUsername", "=", ""),
				tabletheory.Condition("BootstrappedAt", "=", time.Time{}),
			)
		}
		tx.Put(setupAudit)
		tx.Put(passkeyAudit)
		tx.Delete(
			challengeModel,
			tabletheory.IfExists(),
			tabletheory.Condition("TTL", ">", now.Unix()),
			tabletheory.Condition("UserID", "=", req.Username),
			tabletheory.Condition("Type", "=", "registration"),
		)
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, s.mapSetupPasskeyCreateAdminConditionFailure(ctx, req.Username, req.Passkey.Challenge)
		}
		return nil, newAppTheoryError("app.internal", "failed to create admin")
	}

	return apptheory.JSON(http.StatusCreated, setupCreateAdminResponse{
		Username:  req.Username,
		TokenType: "Bearer",
		Token:     token,
		ExpiresAt: expiresAt,
		Role:      models.RoleAdmin,
		Method:    "webauthn",
	})
}

func (s *Server) handleSetupCreateAdmin(ctx *apptheory.Context) (*apptheory.Response, error) {
	cfg, appErr := s.validateSetupCreateAdminState(ctx)
	if appErr != nil {
		return nil, appErr
	}

	req, appErr := parseSetupCreateAdminRequestInput(ctx)
	if appErr != nil {
		return nil, appErr
	}

	if req.Passkey != nil {
		return s.handleSetupCreateAdminWithPasskey(ctx, cfg, req)
	}
	return s.handleSetupCreateAdminWithWallet(ctx, req)
}

func (s *Server) requirePrimaryAdminPasskey(ctx *apptheory.Context, username string) *apptheory.AppTheoryError {
	creds, err := s.listUserWebAuthnCredentials(ctx, username)
	if err != nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if len(creds) == 0 {
		return newAppTheoryError("app.conflict", "primary admin passkey is required before finalize")
	}
	return nil
}

func (s *Server) handleSetupFinalize(ctx *apptheory.Context) (*apptheory.Response, error) {
	locked, cfg, err := s.controlPlaneLocked(ctx)
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if !locked {
		return nil, newAppTheoryError("app.conflict", "control plane is already bootstrapped")
	}
	if cfg == nil || strings.TrimSpace(cfg.PrimaryAdminUsername) == "" {
		return nil, newAppTheoryError("app.conflict", "primary admin is not configured")
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	if username == "" {
		return nil, newAppTheoryError("app.unauthorized", "unauthorized")
	}
	role := operatorRoleFromContext(ctx)
	if role != models.RoleAdmin {
		return nil, newAppTheoryError("app.forbidden", "admin required")
	}
	if username != strings.TrimSpace(cfg.PrimaryAdminUsername) {
		return nil, newAppTheoryError("app.forbidden", "only the primary admin can finalize")
	}
	if appErr := s.requirePrimaryAdminPasskey(ctx, username); appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	cfg.BootstrappedAt = now
	_ = cfg.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(cfg).CreateOrUpdate(); err != nil {
		return nil, newAppTheoryError("app.internal", "failed to finalize setup")
	}

	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "setup.finalize",
		Target:    "control_plane",
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	applyAuditSourceProvenance(ctx, audit)
	_ = audit.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, newAppTheoryError("app.internal", "failed to write audit log")
	}

	t := now.UTC()
	return apptheory.JSON(http.StatusOK, setupFinalizeResponse{
		Locked:         false,
		BootstrappedAt: &t,
	})
}
