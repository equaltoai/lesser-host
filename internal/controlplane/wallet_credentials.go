package controlplane

import (
	"fmt"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) getWalletCredential(ctx *apptheory.Context, username string, walletAddr string) (*models.WalletCredential, error) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	username = strings.TrimSpace(username)
	walletAddr = strings.ToLower(strings.TrimSpace(walletAddr))
	if username == "" || walletAddr == "" {
		return nil, newAppTheoryError("app.bad_request", "wallet not linked")
	}

	var cred models.WalletCredential
	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.WalletCredential{}).
		Where("PK", "=", fmt.Sprintf(models.KeyPatternUser, username)).
		Where("SK", "=", fmt.Sprintf("WALLET#%s", walletAddr)).
		Limit(1).
		First(&cred); err != nil {
		return nil, err
	}

	return &cred, nil
}

func (s *Server) credentialForWalletUsername(ctx *apptheory.Context, username string) (*models.WalletCredential, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	addr := walletAddressFromUsername(username)
	if addr == "" {
		return nil, nil
	}

	cred, err := s.getWalletCredential(ctx, username, addr)
	if err == nil && cred != nil {
		return cred, nil
	}
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	return nil, nil
}

func mostRecentlyUsedWalletCredential(creds []*models.WalletCredential) *models.WalletCredential {
	var best *models.WalletCredential
	for _, cred := range creds {
		if cred == nil {
			continue
		}
		if strings.TrimSpace(cred.Address) == "" {
			continue
		}
		if best == nil || cred.LastUsed.After(best.LastUsed) {
			best = cred
		}
	}
	return best
}

func (s *Server) requireUserWalletCredential(ctx *apptheory.Context, username string) (*models.WalletCredential, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	username = strings.TrimSpace(username)
	if username == "" {
		return nil, newAppTheoryError("app.unauthorized", "unauthorized")
	}

	// Happy path: wallet-derived username with a matching credential record.
	cred, appErr := s.credentialForWalletUsername(ctx, username)
	if appErr != nil {
		return nil, appErr
	}
	if cred != nil {
		return cred, nil
	}

	// Fallback: pick the most recently used linked wallet.
	var creds []*models.WalletCredential
	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.WalletCredential{}).
		Where("PK", "=", fmt.Sprintf(models.KeyPatternUser, username)).
		Where("SK", "BEGINS_WITH", "WALLET#").
		Limit(25).
		All(&creds); err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	best := mostRecentlyUsedWalletCredential(creds)
	if best == nil {
		return nil, newAppTheoryError("app.conflict", "wallet not linked")
	}

	return best, nil
}
