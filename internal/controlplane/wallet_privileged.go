package controlplane

import (
	"context"
	"fmt"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) validateNotPrivilegedWalletAddress(ctx context.Context, walletType string, address string, field string) *apptheory.AppError {
	if s == nil || s.store == nil || s.store.DB == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		ctx = context.Background()
	}

	walletType = strings.TrimSpace(walletType)
	if walletType == "" {
		walletType = "ethereum"
	}
	address = strings.ToLower(strings.TrimSpace(address))
	if address == "" {
		return &apptheory.AppError{Code: "app.bad_request", Message: "wallet address is required"}
	}

	var index models.WalletIndex
	err := s.store.DB.WithContext(ctx).
		Model(&models.WalletIndex{}).
		Where("PK", "=", fmt.Sprintf("WALLET#%s#%s", walletType, address)).
		Limit(1).
		First(&index)
	if theoryErrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	username := strings.TrimSpace(index.Username)
	if username == "" {
		return nil
	}

	var user models.User
	err = s.store.DB.WithContext(ctx).
		Model(&models.User{}).
		Where("PK", "=", fmt.Sprintf(models.KeyPatternUser, username)).
		Where("SK", "=", models.SKProfile).
		Limit(1).
		First(&user)
	if theoryErrors.IsNotFound(err) {
		// Wallet index without a user profile is unexpected and indicates store drift.
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	role := strings.ToLower(strings.TrimSpace(user.Role))
	if role != models.RoleAdmin && role != models.RoleOperator {
		return nil
	}

	field = strings.TrimSpace(field)
	msg := "wallet is not allowed"
	if field != "" {
		msg = field + " is not allowed"
	}
	return &apptheory.AppError{Code: "app.bad_request", Message: msg}
}
