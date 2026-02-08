package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type provisionConsentChallengeRequest struct {
	AdminUsername string `json:"admin_username,omitempty"`
}

type provisionConsentChallengeResponse struct {
	InstanceSlug  string                `json:"instance_slug"`
	Stage         string                `json:"stage"`
	AdminUsername string                `json:"admin_username"`
	Wallet        walletChallengeResponse `json:"wallet"`
}

func buildProvisionConsentMessage(slug, stage, adminUsername, walletAddr, nonce string, issuedAt, expiresAt time.Time) string {
	var sb strings.Builder

	sb.WriteString("lesser.host requests your consent to provision a managed instance.\n\n")
	sb.WriteString("Slug: ")
	sb.WriteString(strings.TrimSpace(slug))
	sb.WriteString("\nStage: ")
	sb.WriteString(strings.TrimSpace(stage))
	sb.WriteString("\nAdmin username: ")
	sb.WriteString(strings.TrimSpace(adminUsername))
	if walletAddr != "" {
		sb.WriteString("\nWallet: ")
		sb.WriteString(strings.ToLower(strings.TrimSpace(walletAddr)))
	}

	sb.WriteString("\n\nNonce: ")
	sb.WriteString(strings.TrimSpace(nonce))
	sb.WriteString("\nIssued At: ")
	sb.WriteString(issuedAt.UTC().Format(time.RFC3339))
	sb.WriteString("\nExpiration Time: ")
	sb.WriteString(expiresAt.UTC().Format(time.RFC3339))

	return sb.String()
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

	var req provisionConsentChallengeRequest
	if len(ctx.Request.Body) > 0 {
		if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
			return nil, parseErr
		}
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))
	adminUsername := strings.ToLower(strings.TrimSpace(req.AdminUsername))
	if adminUsername == "" {
		adminUsername = slug
	}
	if adminUsername == "" || !instanceSlugRE.MatchString(adminUsername) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid admin_username"}
	}

	stage := strings.TrimSpace(s.cfg.Stage)
	if stage == "" {
		stage = "lab"
	}

	cred, appErr := s.requireUserWalletCredential(ctx, strings.TrimSpace(ctx.AuthIdentity))
	if appErr != nil {
		return nil, appErr
	}

	walletAddr := strings.ToLower(strings.TrimSpace(cred.Address))
	if !common.IsHexAddress(walletAddr) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "wallet is not linked"}
	}
	if reservedErr := validateNotReservedWalletAddress(walletAddr, "wallet"); reservedErr != nil {
		return nil, reservedErr
	}

	nonce, err := generateNonce()
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create nonce"}
	}
	id, err := newToken(16)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create challenge id"}
	}

	now := time.Now().UTC()
	expiresAt := now.Add(10 * time.Minute)
	msg := buildProvisionConsentMessage(slug, stage, adminUsername, walletAddr, nonce, now, expiresAt)

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
		IssuedAt:      now,
		ExpiresAt:     expiresAt,
	}
	_ = challenge.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(challenge).IfNotExists().Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create consent challenge"}
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
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "consent_challenge_id is required"}
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

func (s *Server) deleteProvisionConsentChallenge(ctx *apptheory.Context, chall *models.ProvisionConsentChallenge) error {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if chall == nil {
		return nil
	}
	pk := strings.TrimSpace(chall.PK)
	sk := strings.TrimSpace(chall.SK)
	if pk == "" || sk == "" {
		_ = chall.UpdateKeys()
		pk = strings.TrimSpace(chall.PK)
		sk = strings.TrimSpace(chall.SK)
	}
	if pk == "" || sk == "" {
		return nil
	}
	return s.store.DB.WithContext(ctx.Context()).
		Model(&models.ProvisionConsentChallenge{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		Delete()
}

func validateProvisionConsentChallenge(ctx *apptheory.Context, chall *models.ProvisionConsentChallenge, slug string, stage string, message string) *apptheory.AppError {
	if ctx == nil || chall == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if !chall.ExpiresAt.IsZero() && time.Now().After(chall.ExpiresAt) {
		return &apptheory.AppError{Code: "app.bad_request", Message: "consent challenge expired"}
	}

	if strings.TrimSpace(chall.Username) == "" || strings.TrimSpace(ctx.AuthIdentity) == "" || strings.TrimSpace(chall.Username) != strings.TrimSpace(ctx.AuthIdentity) {
		return &apptheory.AppError{Code: "app.forbidden", Message: "consent challenge user mismatch"}
	}

	if strings.TrimSpace(chall.InstanceSlug) != strings.TrimSpace(slug) {
		return &apptheory.AppError{Code: "app.forbidden", Message: "consent challenge slug mismatch"}
	}

	if strings.TrimSpace(chall.Stage) != strings.TrimSpace(stage) {
		return &apptheory.AppError{Code: "app.forbidden", Message: "consent challenge stage mismatch"}
	}

	if strings.TrimSpace(message) == "" || strings.TrimSpace(chall.Message) == "" || strings.TrimSpace(message) != strings.TrimSpace(chall.Message) {
		return &apptheory.AppError{Code: "app.forbidden", Message: "consent challenge message mismatch"}
	}

	return nil
}

func normalizeNotFound(err error) *apptheory.AppError {
	if theoryErrors.IsNotFound(err) {
		return &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if appErr, ok := err.(*apptheory.AppError); ok {
		return appErr
	}
	return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
}

