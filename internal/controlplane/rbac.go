package controlplane

import (
	"strings"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func requireAdmin(ctx *apptheory.Context) error {
	if ctx == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if strings.TrimSpace(ctx.AuthIdentity) == "" {
		return newAppTheoryError("app.unauthorized", "unauthorized")
	}
	if operatorRoleFromContext(ctx) != models.RoleAdmin {
		return newAppTheoryError("app.forbidden", "admin required")
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
		return newAppTheoryError("app.internal", "internal error")
	}
	if strings.TrimSpace(ctx.AuthIdentity) == "" {
		return newAppTheoryError("app.unauthorized", "unauthorized")
	}
	return nil
}

func requireOperator(ctx *apptheory.Context) error {
	if err := requireAuthenticated(ctx); err != nil {
		return err
	}
	if !isOperator(ctx) {
		return newAppTheoryError("app.forbidden", "operator required")
	}
	return nil
}
