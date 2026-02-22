package controlplane

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	webAuthnChallengeDuration = 5 * time.Minute
	maxWebAuthnCredentials    = 10
)

type webAuthnBeginLoginRequest struct {
	Username string `json:"username"`
}

type webAuthnBeginResponse struct {
	PublicKey map[string]any `json:"publicKey"`
	Challenge string         `json:"challenge"`
}

type webAuthnFinishRegistrationRequest struct {
	Challenge      string         `json:"challenge"`
	Response       map[string]any `json:"response"`
	CredentialName string         `json:"credential_name"`
}

type webAuthnFinishLoginRequest struct {
	Username   string         `json:"username"`
	Challenge  string         `json:"challenge"`
	Response   map[string]any `json:"response"`
	DeviceName string         `json:"device_name"`
}

type webAuthnCredentialSummary struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

type webAuthnCredentialsResponse struct {
	Credentials []webAuthnCredentialSummary `json:"credentials"`
}

type webAuthnUpdateCredentialRequest struct {
	Name string `json:"name"`
}

func (s *Server) ensureWebAuthnConfigured() error {
	if s == nil || s.webAuthn == nil {
		return &apptheory.AppError{Code: "app.conflict", Message: "webauthn is not configured"}
	}
	return nil
}

func (s *Server) listUserWebAuthnCredentials(ctx *apptheory.Context, username string) ([]*models.WebAuthnCredential, error) {
	var creds []*models.WebAuthnCredential
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.WebAuthnCredential{}).
		Where("PK", "=", "USER#"+username).
		Where("SK", "BEGINS_WITH", "WEBAUTHN_CRED#").
		All(&creds)
	if err != nil {
		return nil, err
	}
	return creds, nil
}

func (s *Server) storeWebAuthnChallenge(ctx *apptheory.Context, c *models.WebAuthnChallenge) error {
	if err := c.UpdateKeys(); err != nil {
		return err
	}
	return s.store.DB.WithContext(ctx.Context()).Model(c).Create()
}

func (s *Server) getWebAuthnChallenge(ctx *apptheory.Context, challenge string) (*models.WebAuthnChallenge, error) {
	var c models.WebAuthnChallenge
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.WebAuthnChallenge{}).
		Where("PK", "=", "CHALLENGE#"+challenge).
		Where("SK", "=", "WEBAUTHN").
		First(&c)
	if err != nil {
		return nil, err
	}
	if !c.ExpiresAt.IsZero() && time.Now().After(c.ExpiresAt) {
		_ = s.deleteWebAuthnChallenge(ctx, challenge)
		return nil, theoryErrors.ErrItemNotFound
	}
	return &c, nil
}

func (s *Server) deleteWebAuthnChallenge(ctx *apptheory.Context, challenge string) error {
	return s.store.DB.WithContext(ctx.Context()).
		Model(&models.WebAuthnChallenge{}).
		Where("PK", "=", "CHALLENGE#"+challenge).
		Where("SK", "=", "WEBAUTHN").
		Delete()
}

func (s *Server) handleWebAuthnRegisterBegin(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := s.ensureWebAuthnConfigured(); err != nil {
		return nil, err
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	if username == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	creds, err := s.listUserWebAuthnCredentials(ctx, username)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	user := &webAuthnUser{
		id:          username,
		name:        username,
		displayName: username,
		credentials: []webauthn.Credential{},
	}
	for _, cred := range creds {
		user.credentials = append(user.credentials, *toWebAuthnCredential(cred))
	}

	options, sessionData, err := s.webAuthn.BeginRegistration(user)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to begin registration"}
	}

	sessionBytes, err := json.Marshal(sessionData)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to store challenge"}
	}

	challenge := &models.WebAuthnChallenge{
		Challenge:   sessionData.Challenge,
		UserID:      username,
		SessionData: sessionBytes,
		ExpiresAt:   time.Now().UTC().Add(webAuthnChallengeDuration),
		Type:        "registration",
	}
	if storeErr := s.storeWebAuthnChallenge(ctx, challenge); storeErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to store challenge"}
	}

	// Marshal publicKey options as JSON then back into a map for stable response typing.
	raw, err := json.Marshal(options)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	var publicKey map[string]any
	if err := json.Unmarshal(raw, &publicKey); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	return apptheory.JSON(http.StatusOK, webAuthnBeginResponse{
		PublicKey: publicKey,
		Challenge: challenge.Challenge,
	})
}

func (s *Server) handleWebAuthnRegisterFinish(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := s.ensureWebAuthnConfigured(); err != nil {
		return nil, err
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	if username == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	var req webAuthnFinishRegistrationRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	req.Challenge = strings.TrimSpace(req.Challenge)
	if req.Challenge == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "challenge is required"}
	}

	session, err := s.loadWebAuthnSession(ctx, req.Challenge, username, "registration")
	if err != nil {
		return nil, err
	}

	user, creds, err := s.buildWebAuthnUser(ctx, username)
	if err != nil {
		return nil, err
	}
	if len(creds) >= maxWebAuthnCredentials {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "max credentials reached"}
	}

	respBytes, err := json.Marshal(req.Response)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid response"}
	}

	parsed, err := protocol.ParseCredentialCreationResponseBytes(respBytes)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid response"}
	}

	credential, err := s.webAuthn.CreateCredential(user, session, parsed)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "registration failed"}
	}

	now := time.Now().UTC()
	name := strings.TrimSpace(req.CredentialName)
	if name == "" {
		name = fmt.Sprintf("Passkey %d", len(creds)+1)
	}

	userPresent := credential.Flags.UserPresent
	userVerified := credential.Flags.UserVerified
	stored := &models.WebAuthnCredential{
		ID:              base64.StdEncoding.EncodeToString(credential.ID),
		UserID:          username,
		PublicKey:       credential.PublicKey,
		AttestationType: credential.AttestationType,
		AAGUID:          credential.Authenticator.AAGUID,
		SignCount:       credential.Authenticator.SignCount,
		CloneWarning:    credential.Authenticator.CloneWarning,
		UserPresent:     &userPresent,
		UserVerified:    &userVerified,
		BackupEligible:  credential.Flags.BackupEligible,
		BackupState:     credential.Flags.BackupState,
		CreatedAt:       now,
		LastUsedAt:      now,
		Name:            name,
	}
	if err := stored.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(stored).IfNotExists().Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to store credential"}
	}

	_ = s.deleteWebAuthnChallenge(ctx, req.Challenge)

	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "auth.webauthn.register",
		Target:    fmt.Sprintf("webauthn_credential:%s", stored.ID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to write audit log"}
	}

	return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleWebAuthnLoginBegin(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := s.ensureWebAuthnConfigured(); err != nil {
		return nil, err
	}

	var req webAuthnBeginLoginRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "username is required"}
	}

	creds, err := s.listUserWebAuthnCredentials(ctx, username)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if len(creds) == 0 {
		// Avoid leaking whether a username exists or has credentials.
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	user := &webAuthnUser{
		id:          username,
		name:        username,
		displayName: username,
		credentials: []webauthn.Credential{},
	}
	for _, cred := range creds {
		user.credentials = append(user.credentials, *toWebAuthnCredential(cred))
	}

	options, sessionData, err := s.webAuthn.BeginLogin(user)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to begin login"}
	}

	sessionBytes, err := json.Marshal(sessionData)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to store challenge"}
	}

	challenge := &models.WebAuthnChallenge{
		Challenge:   sessionData.Challenge,
		UserID:      username,
		SessionData: sessionBytes,
		ExpiresAt:   time.Now().UTC().Add(webAuthnChallengeDuration),
		Type:        "login",
	}
	if storeErr := s.storeWebAuthnChallenge(ctx, challenge); storeErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to store challenge"}
	}

	raw, err := json.Marshal(options)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	var publicKey map[string]any
	if err := json.Unmarshal(raw, &publicKey); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	return apptheory.JSON(http.StatusOK, webAuthnBeginResponse{
		PublicKey: publicKey,
		Challenge: challenge.Challenge,
	})
}

func (s *Server) handleWebAuthnLoginFinish(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := s.ensureWebAuthnConfigured(); err != nil {
		return nil, err
	}

	req, err := parseWebAuthnLoginFinishRequest(ctx)
	if err != nil {
		return nil, err
	}
	username := req.Username

	session, err := s.loadWebAuthnSession(ctx, req.Challenge, username, "login")
	if err != nil {
		return nil, err
	}

	user, creds, err := s.buildWebAuthnUser(ctx, username)
	if err != nil {
		return nil, err
	}
	if len(creds) == 0 {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	respBytes, err := json.Marshal(req.Response)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid response"}
	}

	parsed, err := protocol.ParseCredentialRequestResponseBytes(respBytes)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid response"}
	}

	credential, err := s.webAuthn.ValidateLogin(user, session, parsed)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "invalid credential"}
	}

	_ = s.deleteWebAuthnChallenge(ctx, req.Challenge)

	credID := base64.StdEncoding.EncodeToString(credential.ID)
	now := time.Now().UTC()

	loginUserPresent := credential.Flags.UserPresent
	loginUserVerified := credential.Flags.UserVerified
	update := &models.WebAuthnCredential{
		ID:             credID,
		UserID:         username,
		SignCount:      credential.Authenticator.SignCount,
		CloneWarning:   credential.Authenticator.CloneWarning,
		UserPresent:    &loginUserPresent,
		UserVerified:   &loginUserVerified,
		BackupEligible: credential.Flags.BackupEligible,
		BackupState:    credential.Flags.BackupState,
		LastUsedAt:     now,
	}
	if updateErr := update.UpdateKeys(); updateErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	// Update mutable fields.
	_ = s.store.DB.WithContext(ctx.Context()).Model(update).Update(
		"SignCount",
		"CloneWarning",
		"UserPresent",
		"UserVerified",
		"BackupEligible",
		"BackupState",
		"LastUsedAt",
	)

	op, err := s.loadUser(ctx, username)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	token, expiresAt, err := s.createOperatorSession(ctx.Context(), username, op.Role, "webauthn")
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create session"}
	}

	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "auth.webauthn.login",
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
		Role:      op.Role,
		Method:    "webauthn",
	})
}

func parseWebAuthnLoginFinishRequest(ctx *apptheory.Context) (webAuthnFinishLoginRequest, error) {
	var req webAuthnFinishLoginRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return webAuthnFinishLoginRequest{}, err
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		return webAuthnFinishLoginRequest{}, &apptheory.AppError{Code: "app.bad_request", Message: "username is required"}
	}
	req.Challenge = strings.TrimSpace(req.Challenge)
	if req.Challenge == "" {
		return webAuthnFinishLoginRequest{}, &apptheory.AppError{Code: "app.bad_request", Message: "challenge is required"}
	}
	return req, nil
}

func (s *Server) loadWebAuthnSession(ctx *apptheory.Context, challenge string, username string, expectedType string) (webauthn.SessionData, error) {
	if s == nil || ctx == nil {
		return webauthn.SessionData{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	challengeData, err := s.getWebAuthnChallenge(ctx, challenge)
	if theoryErrors.IsNotFound(err) {
		return webauthn.SessionData{}, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if err != nil {
		return webauthn.SessionData{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if challengeData.UserID != username {
		return webauthn.SessionData{}, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if strings.TrimSpace(challengeData.Type) != expectedType {
		return webauthn.SessionData{}, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	var session webauthn.SessionData
	if unmarshalErr := json.Unmarshal(challengeData.SessionData, &session); unmarshalErr != nil {
		return webauthn.SessionData{}, &apptheory.AppError{Code: "app.internal", Message: "invalid session"}
	}
	return session, nil
}

func (s *Server) buildWebAuthnUser(ctx *apptheory.Context, username string) (*webAuthnUser, []*models.WebAuthnCredential, error) {
	creds, err := s.listUserWebAuthnCredentials(ctx, username)
	if err != nil {
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	user := &webAuthnUser{
		id:          username,
		name:        username,
		displayName: username,
		credentials: []webauthn.Credential{},
	}
	for _, cred := range creds {
		if cred == nil {
			continue
		}
		user.credentials = append(user.credentials, *toWebAuthnCredential(cred))
	}

	return user, creds, nil
}

func (s *Server) handleWebAuthnCredentials(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := s.ensureWebAuthnConfigured(); err != nil {
		return nil, err
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	if username == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	creds, err := s.listUserWebAuthnCredentials(ctx, username)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	out := make([]webAuthnCredentialSummary, 0, len(creds))
	for _, c := range creds {
		out = append(out, webAuthnCredentialSummary{
			ID:         c.ID,
			Name:       c.Name,
			CreatedAt:  c.CreatedAt,
			LastUsedAt: c.LastUsedAt,
		})
	}

	return apptheory.JSON(http.StatusOK, webAuthnCredentialsResponse{Credentials: out})
}

func (s *Server) handleWebAuthnDeleteCredential(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := s.ensureWebAuthnConfigured(); err != nil {
		return nil, err
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	if username == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	credID := strings.TrimSpace(ctx.Param("credentialId"))
	if credID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "credentialId is required"}
	}

	pk := "USER#" + username
	sk := "WEBAUTHN_CRED#" + credID

	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.WebAuthnCredential{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		Delete()
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to delete credential"}
	}

	now := time.Now().UTC()
	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "auth.webauthn.delete_credential",
		Target:    fmt.Sprintf("webauthn_credential:%s", credID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to write audit log"}
	}

	return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleWebAuthnUpdateCredential(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := s.ensureWebAuthnConfigured(); err != nil {
		return nil, err
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	if username == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	credID := strings.TrimSpace(ctx.Param("credentialId"))
	if credID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "credentialId is required"}
	}

	var req webAuthnUpdateCredentialRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "name is required"}
	}

	model := &models.WebAuthnCredential{
		ID:     credID,
		UserID: username,
		Name:   req.Name,
	}
	if err := model.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(model).IfExists().Update("Name"); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.not_found", Message: "credential not found"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to update credential"}
	}

	now := time.Now().UTC()
	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "auth.webauthn.update_credential",
		Target:    fmt.Sprintf("webauthn_credential:%s", credID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(audit).Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to write audit log"}
	}

	return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
}
