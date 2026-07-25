package controlplane

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type provisionConsentChallengeRequest struct {
	AdminUsername string `json:"admin_username,omitempty"`
}

type provisionConsentChallengeResponse struct {
	InstanceSlug  string                  `json:"instance_slug"`
	Stage         string                  `json:"stage"`
	AdminUsername string                  `json:"admin_username"`
	Wallet        walletChallengeResponse `json:"wallet"`
}

const (
	provisionInitAdminConsentKindV1 = "lesser.init_admin_consent.v1"
	// Lesser M9 accepts init-admin consent only when expires_at is no more
	// than one hour in the future. Keep host's challenge below that contract
	// with a small clock-skew margin while still allowing slow first deploys.
	provisionInitAdminConsentMaxFuture = time.Hour
	provisionInitAdminConsentTTL       = 55 * time.Minute
)

type provisionInitAdminConsentV1 struct {
	Kind      string    `json:"kind"`
	Instance  string    `json:"instance"`
	Username  string    `json:"username"`
	Nonce     string    `json:"nonce"`
	ExpiresAt time.Time `json:"expires_at"`
}

func parseProvisionConsentChallengeRequest(ctx *apptheory.Context) (provisionConsentChallengeRequest, error) {
	var req provisionConsentChallengeRequest
	if ctx == nil {
		return req, newAppTheoryError("app.internal", "internal error")
	}
	if len(ctx.Request.Body) == 0 {
		return req, nil
	}
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return req, err
	}
	return req, nil
}

func normalizeControlPlaneStage(stage string) string {
	stage = strings.TrimSpace(stage)
	if stage == "" {
		stage = defaultControlPlaneStage
	}
	return stage
}

func normalizeProvisionAdminUsername(slug, adminUsername string) (string, *apptheory.AppTheoryError) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if strings.TrimSpace(adminUsername) == "" {
		adminUsername = slug
	}
	normalized, err := soul.ValidateManagedHandle(adminUsername)
	if err != nil || normalized == "" {
		return "", newAppTheoryError("app.bad_request", "invalid admin_username")
	}
	return normalized, nil
}

func normalizeLinkedWalletAddress(cred *models.WalletCredential) (string, *apptheory.AppTheoryError) {
	walletAddr := strings.ToLower(strings.TrimSpace(cred.Address))
	if !common.IsHexAddress(walletAddr) {
		return "", newAppTheoryError("app.conflict", "wallet is not linked")
	}
	if reservedErr := validateNotReservedWalletAddress(walletAddr, "wallet"); reservedErr != nil {
		return "", reservedErr
	}
	return walletAddr, nil
}

func managedProvisionBaseDomain(slug string, parentDomain string) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	parentDomain = strings.ToLower(strings.TrimSpace(parentDomain))
	if parentDomain == "" {
		parentDomain = defaultManagedParentDomain
	}
	parentDomain = strings.Trim(parentDomain, ".")
	if slug == "" || parentDomain == "" {
		return ""
	}
	return fmt.Sprintf("%s.%s", slug, parentDomain)
}

func buildProvisionConsentMessage(stage, baseDomain, adminUsername, nonce string, expiresAt time.Time) string {
	payload := provisionInitAdminConsentV1{
		Kind:      provisionInitAdminConsentKindV1,
		Instance:  managedInstanceStageDomain(stage, baseDomain),
		Username:  strings.TrimSpace(adminUsername),
		Nonce:     strings.TrimSpace(nonce),
		ExpiresAt: expiresAt.UTC(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(raw)
}

func (s *Server) handlePortalProvisionConsentChallenge(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}
	if appErr := validateNotReservedWalletUsername(strings.TrimSpace(ctx.AuthIdentity)); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requirePortalApproved(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	req, err := parseProvisionConsentChallengeRequest(ctx)
	if err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))
	adminUsername, appErr := normalizeProvisionAdminUsername(slug, req.AdminUsername)
	if appErr != nil {
		return nil, appErr
	}

	stage := normalizeControlPlaneStage(s.cfg.Stage)

	cred, appErr := s.requireUserWalletCredential(ctx, strings.TrimSpace(ctx.AuthIdentity))
	if appErr != nil {
		return nil, appErr
	}

	walletAddr, appErr := normalizeLinkedWalletAddress(cred)
	if appErr != nil {
		return nil, appErr
	}

	nonce, err := generateNonce()
	if err != nil {
		return nil, newAppTheoryError("app.internal", "failed to create nonce")
	}
	id, err := newToken(16)
	if err != nil {
		return nil, newAppTheoryError("app.internal", "failed to create challenge id")
	}

	now := time.Now().UTC()
	expiresAt := now.Add(provisionInitAdminConsentTTL)
	baseDomain := managedProvisionBaseDomain(slug, s.cfg.ManagedParentDomain)
	msg := buildProvisionConsentMessage(stage, baseDomain, adminUsername, nonce, expiresAt)

	challenge := &models.ProvisionConsentChallenge{
		ID:            id,
		Username:      strings.TrimSpace(ctx.AuthIdentity),
		InstanceSlug:  slug,
		Stage:         stage,
		AdminUsername: adminUsername,
		WalletType:    strings.TrimSpace(cred.Type),
		WalletAddr:    walletAddr,
		ChainID:       cred.ChainID,
		Nonce:         nonce,
		Message:       msg,
		MessageHash:   sha256Hex(msg),
		IssuedAt:      now,
		ExpiresAt:     expiresAt,
	}
	_ = challenge.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(challenge).IfNotExists().Create(); err != nil {
		return nil, newAppTheoryError("app.internal", "failed to create consent challenge")
	}

	return apptheory.JSON(http.StatusOK, provisionConsentChallengeResponse{
		InstanceSlug:  slug,
		Stage:         stage,
		AdminUsername: adminUsername,
		Wallet: walletChallengeResponse{
			ID:        id,
			Username:  strings.TrimSpace(ctx.AuthIdentity),
			Address:   walletAddr,
			ChainID:   cred.ChainID,
			Nonce:     nonce,
			Message:   msg,
			IssuedAt:  now,
			ExpiresAt: expiresAt,
		},
	})
}

func (s *Server) getProvisionConsentChallenge(ctx *apptheory.Context, id string) (*models.ProvisionConsentChallenge, error) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, newAppTheoryError("app.bad_request", "consent_challenge_id is required")
	}

	var chall models.ProvisionConsentChallenge
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.ProvisionConsentChallenge{}).
		Where("PK", "=", fmt.Sprintf("PROVISION_CONSENT#%s", id)).
		Where("SK", "=", "CHALLENGE").
		Limit(1).
		First(&chall)
	if err != nil {
		return nil, err
	}
	return &chall, nil
}

func (s *Server) consumeProvisionConsentChallenge(ctx *apptheory.Context, chall *models.ProvisionConsentChallenge, message string, now time.Time) *apptheory.AppTheoryError {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if chall == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if strings.TrimSpace(message) == "" {
		return newAppTheoryError("app.bad_request", "consent_message is required")
	}

	update := &models.ProvisionConsentChallenge{
		ID:          strings.TrimSpace(chall.ID),
		ExpiresAt:   chall.ExpiresAt,
		Message:     "",
		MessageHash: sha256Hex(message),
		Consumed:    true,
		ConsumedAt:  now,
	}
	_ = update.UpdateKeys()

	err := s.store.DB.WithContext(ctx.Context()).
		Model(update).
		IfExists().
		// Use TableTheory's field-aware condition builder here instead of a raw
		// expression: DynamoDB treats "consumed" as a reserved word, so a raw
		// condition fails at runtime unless the attribute name is aliased.
		// ProvisionConsentChallenge.BeforeCreate persists the zero-value
		// Consumed=false, so the field condition is sufficient for single-use
		// challenge consumption and preserves the existing replay guard.
		WithCondition("Consumed", "=", false).
		Update("Consumed", "ConsumedAt", "Message", "MessageHash")
	if theoryErrors.IsConditionFailed(err) || theoryErrors.IsNotFound(err) {
		return newAppTheoryError("app.unauthorized", "unauthorized")
	}
	if err != nil {
		return newAppTheoryError("app.internal", "failed to consume consent challenge")
	}

	chall.Consumed = true
	chall.ConsumedAt = now
	chall.MessageHash = sha256Hex(message)
	return nil
}

func validateProvisionConsentChallenge(ctx *apptheory.Context, chall *models.ProvisionConsentChallenge, slug string, stage string, message string) *apptheory.AppTheoryError {
	if ctx == nil || chall == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if chall.Consumed {
		return newAppTheoryError("app.unauthorized", "unauthorized")
	}
	if !chall.ExpiresAt.IsZero() && time.Now().After(chall.ExpiresAt) {
		return newAppTheoryError("app.bad_request", "consent challenge expired")
	}
	if appErr := validateProvisionConsentChallengeScope(ctx, chall, slug, stage); appErr != nil {
		return appErr
	}
	return validateProvisionConsentChallengeMessage(chall, message)
}

func validateProvisionConsentChallengeScope(ctx *apptheory.Context, chall *models.ProvisionConsentChallenge, slug string, stage string) *apptheory.AppTheoryError {
	if strings.TrimSpace(chall.Username) == "" || strings.TrimSpace(ctx.AuthIdentity) == "" || strings.TrimSpace(chall.Username) != strings.TrimSpace(ctx.AuthIdentity) {
		return newAppTheoryError("app.forbidden", "consent challenge user mismatch")
	}
	if strings.TrimSpace(chall.InstanceSlug) != strings.TrimSpace(slug) {
		return newAppTheoryError("app.forbidden", "consent challenge slug mismatch")
	}
	if strings.TrimSpace(chall.Stage) != strings.TrimSpace(stage) {
		return newAppTheoryError("app.forbidden", "consent challenge stage mismatch")
	}
	return nil
}

func validateProvisionConsentChallengeMessage(chall *models.ProvisionConsentChallenge, message string) *apptheory.AppTheoryError {
	if strings.TrimSpace(message) == "" || strings.TrimSpace(chall.Message) == "" || message != chall.Message {
		return newAppTheoryError("app.forbidden", "consent challenge message mismatch")
	}
	if msgHash := strings.TrimSpace(chall.MessageHash); msgHash != "" && msgHash != sha256Hex(message) {
		return newAppTheoryError("app.forbidden", "consent challenge message hash mismatch")
	}

	return nil
}

func normalizeNotFound(err error) *apptheory.AppTheoryError {
	if theoryErrors.IsNotFound(err) {
		return newAppTheoryError("app.unauthorized", "unauthorized")
	}
	if appErr, ok := err.(*apptheory.AppTheoryError); ok {
		return appErr
	}
	return newAppTheoryError("app.internal", "internal error")
}
