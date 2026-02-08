package controlplane

import (
	"fmt"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) getWalletCredential(ctx *apptheory.Context, username string, walletAddr string) (*models.WalletCredential, error) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	username = strings.TrimSpace(username)
	walletAddr = strings.ToLower(strings.TrimSpace(walletAddr))
	if username == "" || walletAddr == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "wallet not linked"}
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

func (s *Server) requireUserWalletCredential(ctx *apptheory.Context, username string) (*models.WalletCredential, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	username = strings.TrimSpace(username)
	if username == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	// Happy path: wallet-derived username with a matching credential record.
	if addr := walletAddressFromUsername(username); addr != "" {
		if cred, err := s.getWalletCredential(ctx, username, addr); err == nil && cred != nil {
			return cred, nil
		} else if err != nil && !theoryErrors.IsNotFound(err) {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
		}
	}

	// Fallback: pick the most recently used linked wallet.
	var creds []*models.WalletCredential
	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.WalletCredential{}).
		Where("PK", "=", fmt.Sprintf(models.KeyPatternUser, username)).
		Where("SK", "BEGINS_WITH", "WALLET#").
		Limit(25).
		All(&creds); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

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
	if best == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "wallet not linked"}
	}

	return best, nil
}

