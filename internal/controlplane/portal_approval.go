package controlplane

import (
	"strings"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) requirePortalApproved(ctx *apptheory.Context) *apptheory.AppTheoryError {
	if s == nil || ctx == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if isOperator(ctx) {
		return nil
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	if username == "" {
		return newAppTheoryError("app.unauthorized", "unauthorized")
	}

	user, found, err := s.getUserProfile(ctx, username)
	if err != nil {
		if appErr, ok := err.(*apptheory.AppTheoryError); ok {
			return appErr
		}
		return newAppTheoryError("app.internal", "internal error")
	}
	if !found {
		return newAppTheoryError("app.unauthorized", "unauthorized")
	}

	status := strings.ToLower(strings.TrimSpace(user.ApprovalStatus))
	if status == "" {
		if user.Approved {
			return nil
		}
		return newAppTheoryError("app.forbidden", "approval required")
	}

	switch status {
	case models.UserApprovalStatusApproved:
		return nil
	case models.UserApprovalStatusRejected:
		return newAppTheoryError("app.forbidden", "approval rejected")
	default:
		return newAppTheoryError("app.forbidden", "approval required")
	}
}
