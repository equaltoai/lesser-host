package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

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
	Username    string              `json:"username"`
	DisplayName string              `json:"displayName,omitempty"`
	Wallet      walletVerifyRequest `json:"wallet"`
}

type setupCreateAdminResponse struct {
	Username string `json:"username"`
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

func parseSetupCreateAdminRequestInput(ctx *apptheory.Context) (setupCreateAdminRequest, *apptheory.AppTheoryError) {
	var req setupCreateAdminRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		if appErr, ok := parseErr.(*apptheory.AppTheoryError); ok {
			return setupCreateAdminRequest{}, appErr
		}
		return setupCreateAdminRequest{}, newAppTheoryError("app.bad_request", "invalid request")
	}

	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.Wallet.ChallengeID = strings.TrimSpace(req.Wallet.ChallengeID)
	req.Wallet.Address = strings.TrimSpace(req.Wallet.Address)
	req.Wallet.Signature = strings.TrimSpace(req.Wallet.Signature)
	req.Wallet.Message = strings.TrimSpace(req.Wallet.Message)

	username, err := soul.ValidateManagedHandle(req.Username)
	if strings.TrimSpace(req.Username) == "" {
		return setupCreateAdminRequest{}, newAppTheoryError("app.bad_request", "username is required")
	}
	if err != nil {
		return setupCreateAdminRequest{}, newAppTheoryError("app.bad_request", err.Error())
	}
	req.Username = username
	if strings.EqualFold(req.Username, setupBootstrapUser) {
		return setupCreateAdminRequest{}, newAppTheoryError("app.bad_request", "username is reserved")
	}

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

func (s *Server) createSetupAdminUser(ctx *apptheory.Context, username string, displayName string, now time.Time) *apptheory.AppTheoryError {
	user := &models.User{
		Username:       strings.TrimSpace(username),
		Role:           models.RoleAdmin,
		Approved:       true,
		ApprovalStatus: models.UserApprovalStatusApproved,
		DisplayName:    strings.TrimSpace(displayName),
		CreatedAt:      now,
	}
	if err := user.UpdateKeys(); err != nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(user).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return newAppTheoryError("app.conflict", "username already exists")
		}
		return newAppTheoryError("app.internal", "failed to create admin")
	}
	return nil
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

func (s *Server) setControlPlanePrimaryAdmin(ctx *apptheory.Context, username string) *apptheory.AppTheoryError {
	cp := &models.ControlPlaneConfig{
		PrimaryAdminUsername: strings.TrimSpace(username),
		BootstrappedAt:       time.Time{},
	}
	_ = cp.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(cp).CreateOrUpdate(); err != nil {
		return newAppTheoryError("app.internal", "failed to update control plane config")
	}
	return nil
}

func (s *Server) handleSetupCreateAdmin(ctx *apptheory.Context) (*apptheory.Response, error) {
	if _, appErr := s.validateSetupCreateAdminState(ctx); appErr != nil {
		return nil, appErr
	}

	req, appErr := parseSetupCreateAdminRequestInput(ctx)
	if appErr != nil {
		return nil, appErr
	}

	if bootstrapAdminErr := s.rejectBootstrapWalletAsSetupAdmin(req.Wallet.Address); bootstrapAdminErr != nil {
		return nil, bootstrapAdminErr
	}

	adminWalletAddr, chainID, appErr := s.verifySetupCreateAdminWallet(ctx, req.Username, req.Wallet)
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

	audit := &models.AuditLogEntry{
		Actor:     fmt.Sprintf("bootstrap_wallet:%s", strings.ToLower(strings.TrimSpace(s.cfg.BootstrapWalletAddress))),
		Action:    "setup.create_admin",
		Target:    fmt.Sprintf("operator:%s", req.Username),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	applyAuditSourceProvenance(ctx, audit)
	_ = audit.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, newAppTheoryError("app.internal", "failed to write audit log")
	}

	return apptheory.JSON(http.StatusCreated, setupCreateAdminResponse{Username: req.Username})
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
