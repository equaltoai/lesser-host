package controlplane

import (
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) requirePortalApproved(ctx *apptheory.Context) *apptheory.AppError {
	if s == nil || ctx == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if isOperator(ctx) {
		return nil
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	if username == "" {
		return &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	user, found, err := s.getUserProfile(ctx, username)
	if err != nil {
		if appErr, ok := err.(*apptheory.AppError); ok {
			return appErr
		}
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !found {
		return &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	status := strings.ToLower(strings.TrimSpace(user.ApprovalStatus))
	if status == "" {
		if user.Approved {
			return nil
		}
		return &apptheory.AppError{Code: "app.forbidden", Message: "approval required"}
	}

	switch status {
	case models.UserApprovalStatusApproved:
		return nil
	case models.UserApprovalStatusRejected:
		return &apptheory.AppError{Code: "app.forbidden", Message: "approval rejected"}
	default:
		return &apptheory.AppError{Code: "app.forbidden", Message: "approval required"}
	}
}
