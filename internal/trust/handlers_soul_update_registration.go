package trust

import (
	"net/http"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
)

func (s *Server) handleSoulAgentUpdateRegistration(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || ctx == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return nil, newAppTheoryError("app.unauthorized", "unauthorized")
	}

	if s.soul == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
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
