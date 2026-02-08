package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
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
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
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
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
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
		BootstrapWalletAddress:    bootstrapWallet,

		PrimaryAdminSet:      primaryAdmin != "",
		PrimaryAdminUsername: primaryAdmin,

		Stage: strings.TrimSpace(s.cfg.Stage),
	}

	return apptheory.JSON(http.StatusOK, resp)
}

func (s *Server) handleSetupBootstrapChallenge(ctx *apptheory.Context) (*apptheory.Response, error) {
	locked, _, err := s.controlPlaneLocked(ctx)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !locked {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "control plane is already bootstrapped"}
	}

	bootstrapWallet := strings.ToLower(strings.TrimSpace(s.cfg.BootstrapWalletAddress))
	if bootstrapWallet == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "bootstrap wallet is not configured"}
	}

	var req setupBootstrapChallengeRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return nil, parseErr
	}

	if strings.TrimSpace(req.Address) == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "address is required"}
	}
	if req.ChainID == 0 {
		req.ChainID = 1
	}

	if strings.ToLower(strings.TrimSpace(req.Address)) != bootstrapWallet {
		return nil, &apptheory.AppError{Code: "app.forbidden", Message: "wallet does not match bootstrap credential"}
	}

	challenge, err := s.createWalletChallenge(ctx.Context(), bootstrapWallet, req.ChainID, setupBootstrapUser)
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

func (s *Server) handleSetupBootstrapVerify(ctx *apptheory.Context) (*apptheory.Response, error) {
	locked, _, err := s.controlPlaneLocked(ctx)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !locked {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "control plane is already bootstrapped"}
	}

	bootstrapWallet := strings.ToLower(strings.TrimSpace(s.cfg.BootstrapWalletAddress))
	if bootstrapWallet == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "bootstrap wallet is not configured"}
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
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create setup session"}
	}

	now := time.Now().UTC()
	expiresAt := now.Add(1 * time.Hour)

	session := &models.SetupSession{
		ID:           token,
		Purpose:      setupPurposeBootstrap,
		WalletType:   walletTypeEthereum,
		WalletAddr:   bootstrapWallet,
		IssuedAt:     now,
		ExpiresAt:    expiresAt,
		InstanceLock: true,
	}
	if err := session.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create setup session"}
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(session).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create setup session"}
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
		return setupBootstrapVerifyInput{}, &apptheory.AppError{Code: "app.bad_request", Message: "challengeId is required"}
	}
	if address == "" {
		return setupBootstrapVerifyInput{}, &apptheory.AppError{Code: "app.bad_request", Message: "address is required"}
	}
	if signature == "" {
		return setupBootstrapVerifyInput{}, &apptheory.AppError{Code: "app.bad_request", Message: "signature is required"}
	}
	if message == "" {
		return setupBootstrapVerifyInput{}, &apptheory.AppError{Code: "app.bad_request", Message: "message is required"}
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
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if strings.ToLower(strings.TrimSpace(in.Address)) != bootstrapWallet {
		return &apptheory.AppError{Code: "app.forbidden", Message: "wallet does not match bootstrap credential"}
	}

	challenge, err := s.getWalletChallenge(ctx.Context(), in.ChallengeID)
	if theoryErrors.IsNotFound(err) {
		return &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if strings.TrimSpace(challenge.Username) != setupBootstrapUser {
		return &apptheory.AppError{Code: "app.forbidden", Message: "challenge is not bound to bootstrap identity"}
	}
	if strings.ToLower(strings.TrimSpace(challenge.Address)) != bootstrapWallet {
		return &apptheory.AppError{Code: "app.forbidden", Message: "challenge address mismatch"}
	}
	if strings.TrimSpace(challenge.Message) != in.Message {
		return &apptheory.AppError{Code: "app.forbidden", Message: "message mismatch"}
	}

	if verifyErr := verifyEthereumSignature(bootstrapWallet, in.Message, in.Signature); verifyErr != nil {
		return &apptheory.AppError{Code: "app.unauthorized", Message: "invalid signature"}
	}
	_ = s.deleteWalletChallenge(ctx.Context(), in.ChallengeID)

	return nil
}

func (s *Server) requireSetupSession(ctx *apptheory.Context) (*models.SetupSession, error) {
	token := httpx.BearerToken(ctx.Request.Headers)
	if token == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	var session models.SetupSession
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SetupSession{}).
		Where("PK", "=", fmt.Sprintf("SETUP_SESSION#%s", token)).
		Where("SK", "=", "SESSION").
		First(&session)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if !session.ExpiresAt.IsZero() && time.Now().After(session.ExpiresAt) {
		_ = s.store.DB.WithContext(ctx.Context()).
			Model(&models.SetupSession{}).
			Where("PK", "=", session.PK).
			Where("SK", "=", session.SK).
			Delete()
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if strings.TrimSpace(session.Purpose) != setupPurposeBootstrap {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	bootstrapWallet := strings.ToLower(strings.TrimSpace(s.cfg.BootstrapWalletAddress))
	if bootstrapWallet == "" || strings.ToLower(strings.TrimSpace(session.WalletAddr)) != bootstrapWallet {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
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
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "address is required"}
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

func (s *Server) validateSetupCreateAdminState(ctx *apptheory.Context) (*models.ControlPlaneConfig, *apptheory.AppError) {
	locked, cfg, err := s.controlPlaneLocked(ctx)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !locked {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "control plane is already bootstrapped"}
	}
	if cfg != nil && strings.TrimSpace(cfg.PrimaryAdminUsername) != "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "primary admin already created"}
	}

	if _, setupErr := s.requireSetupSession(ctx); setupErr != nil {
		if appErr, ok := setupErr.(*apptheory.AppError); ok {
			return nil, appErr
		}
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	return cfg, nil
}

func parseSetupCreateAdminRequestInput(ctx *apptheory.Context) (setupCreateAdminRequest, *apptheory.AppError) {
	var req setupCreateAdminRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		if appErr, ok := parseErr.(*apptheory.AppError); ok {
			return setupCreateAdminRequest{}, appErr
		}
		return setupCreateAdminRequest{}, &apptheory.AppError{Code: "app.bad_request", Message: "invalid request"}
	}

	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.Wallet.ChallengeID = strings.TrimSpace(req.Wallet.ChallengeID)
	req.Wallet.Address = strings.TrimSpace(req.Wallet.Address)
	req.Wallet.Signature = strings.TrimSpace(req.Wallet.Signature)
	req.Wallet.Message = strings.TrimSpace(req.Wallet.Message)

	if req.Username == "" {
		return setupCreateAdminRequest{}, &apptheory.AppError{Code: "app.bad_request", Message: "username is required"}
	}
	if strings.EqualFold(req.Username, setupBootstrapUser) {
		return setupCreateAdminRequest{}, &apptheory.AppError{Code: "app.bad_request", Message: "username is reserved"}
	}

	if req.Wallet.ChallengeID == "" {
		return setupCreateAdminRequest{}, &apptheory.AppError{Code: "app.bad_request", Message: "wallet.challengeId is required"}
	}
	if req.Wallet.Address == "" {
		return setupCreateAdminRequest{}, &apptheory.AppError{Code: "app.bad_request", Message: "wallet.address is required"}
	}
	if req.Wallet.Signature == "" {
		return setupCreateAdminRequest{}, &apptheory.AppError{Code: "app.bad_request", Message: "wallet.signature is required"}
	}
	if req.Wallet.Message == "" {
		return setupCreateAdminRequest{}, &apptheory.AppError{Code: "app.bad_request", Message: "wallet.message is required"}
	}

	return req, nil
}

func (s *Server) verifySetupCreateAdminWallet(ctx *apptheory.Context, username string, wallet walletVerifyRequest) (string, int, *apptheory.AppError) {
	challenge, err := s.getWalletChallenge(ctx.Context(), wallet.ChallengeID)
	if theoryErrors.IsNotFound(err) {
		return "", 0, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if err != nil {
		return "", 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(challenge.Username) != strings.TrimSpace(username) {
		return "", 0, &apptheory.AppError{Code: "app.forbidden", Message: "wallet challenge username mismatch"}
	}

	adminWalletAddr := strings.ToLower(strings.TrimSpace(wallet.Address))
	if adminWalletAddr != strings.ToLower(strings.TrimSpace(challenge.Address)) {
		return "", 0, &apptheory.AppError{Code: "app.forbidden", Message: "wallet challenge address mismatch"}
	}
	if strings.TrimSpace(wallet.Message) != strings.TrimSpace(challenge.Message) {
		return "", 0, &apptheory.AppError{Code: "app.forbidden", Message: "wallet challenge message mismatch"}
	}

	existing, err := s.walletLinkedUsername(ctx, walletTypeEthereum, adminWalletAddr)
	if err != nil {
		return "", 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if existing != "" {
		return "", 0, &apptheory.AppError{Code: "app.conflict", Message: "wallet is already linked to a user"}
	}

	if err := verifyEthereumSignature(adminWalletAddr, wallet.Message, wallet.Signature); err != nil {
		return "", 0, &apptheory.AppError{Code: "app.unauthorized", Message: "invalid signature"}
	}
	_ = s.deleteWalletChallenge(ctx.Context(), wallet.ChallengeID)

	return adminWalletAddr, challenge.ChainID, nil
}

func (s *Server) createSetupAdminUser(ctx *apptheory.Context, username string, displayName string, now time.Time) *apptheory.AppError {
	user := &models.User{
		Username:       strings.TrimSpace(username),
		Role:           models.RoleAdmin,
		Approved:       true,
		ApprovalStatus: models.UserApprovalStatusApproved,
		DisplayName:    strings.TrimSpace(displayName),
		CreatedAt:      now,
	}
	if err := user.UpdateKeys(); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(user).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return &apptheory.AppError{Code: "app.conflict", Message: "username already exists"}
		}
		return &apptheory.AppError{Code: "app.internal", Message: "failed to create admin"}
	}
	return nil
}

func (s *Server) linkSetupAdminWallet(ctx *apptheory.Context, username string, walletAddr string, chainID int, now time.Time) *apptheory.AppError {
	cred := &models.WalletCredential{
		Username: strings.TrimSpace(username),
		Address:  strings.ToLower(strings.TrimSpace(walletAddr)),
		ChainID:  chainID,
		Type:     walletTypeEthereum,
		LinkedAt: now,
		LastUsed: now,
	}
	if err := cred.UpdateKeys(); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(cred).IfNotExists().Create(); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to link wallet"}
	}

	index := &models.WalletIndex{}
	index.UpdateKeys(walletTypeEthereum, walletAddr, username)
	if err := s.store.DB.WithContext(ctx.Context()).Model(index).IfNotExists().Create(); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to link wallet"}
	}

	return nil
}

func (s *Server) setControlPlanePrimaryAdmin(ctx *apptheory.Context, username string) *apptheory.AppError {
	cp := &models.ControlPlaneConfig{
		PrimaryAdminUsername: strings.TrimSpace(username),
		BootstrappedAt:       time.Time{},
	}
	_ = cp.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(cp).CreateOrUpdate(); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to update control plane config"}
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
	_ = audit.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to write audit log"}
	}

	return apptheory.JSON(http.StatusCreated, setupCreateAdminResponse{Username: req.Username})
}

func (s *Server) handleSetupFinalize(ctx *apptheory.Context) (*apptheory.Response, error) {
	locked, cfg, err := s.controlPlaneLocked(ctx)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !locked {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "control plane is already bootstrapped"}
	}
	if cfg == nil || strings.TrimSpace(cfg.PrimaryAdminUsername) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "primary admin is not configured"}
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	if username == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	role := operatorRoleFromContext(ctx)
	if role != models.RoleAdmin {
		return nil, &apptheory.AppError{Code: "app.forbidden", Message: "admin required"}
	}
	if username != strings.TrimSpace(cfg.PrimaryAdminUsername) {
		return nil, &apptheory.AppError{Code: "app.forbidden", Message: "only the primary admin can finalize"}
	}

	now := time.Now().UTC()
	cfg.BootstrappedAt = now
	_ = cfg.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(cfg).CreateOrUpdate(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to finalize setup"}
	}

	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "setup.finalize",
		Target:    "control_plane",
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to write audit log"}
	}

	t := now.UTC()
	return apptheory.JSON(http.StatusOK, setupFinalizeResponse{
		Locked:         false,
		BootstrappedAt: &t,
	})
}
