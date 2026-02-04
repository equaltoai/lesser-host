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

