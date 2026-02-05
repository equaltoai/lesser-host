package controlplane

import (
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func requireAdmin(ctx *apptheory.Context) error {
	if ctx == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(ctx.AuthIdentity) == "" {
		return &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if operatorRoleFromContext(ctx) != models.RoleAdmin {
		return &apptheory.AppError{Code: "app.forbidden", Message: "admin required"}
	}
	return nil
}

func isOperator(ctx *apptheory.Context) bool {
	if ctx == nil {
		return false
	}
	role := operatorRoleFromContext(ctx)
	return role == models.RoleAdmin || role == models.RoleOperator
}

func requireAuthenticated(ctx *apptheory.Context) error {
	if ctx == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(ctx.AuthIdentity) == "" {
		return &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	return nil
}

func requireOperator(ctx *apptheory.Context) error {
	if err := requireAuthenticated(ctx); err != nil {
		return err
	}
	if !isOperator(ctx) {
		return &apptheory.AppError{Code: "app.forbidden", Message: "operator required"}
	}
	return nil
}
