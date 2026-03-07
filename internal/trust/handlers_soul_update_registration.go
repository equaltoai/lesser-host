package trust

import (
	"net/http"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

func (s *Server) handleSoulAgentUpdateRegistration(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	if s.soul == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	result, appErr := s.soul.UpdateSoulAgentRegistrationForInstance(
		ctx.Context(),
		instanceSlug,
		ctx.RequestID,
		ctx.Param("agentId"),
		ctx.Request.Body,
	)
	if appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, result)
}
