package trust

import (
	"net/http"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
)

type trustAuthVerifyResponse struct {
	Status       string `json:"status"`
	InstanceSlug string `json:"instance_slug"`
}

func (s *Server) handleTrustAuthVerify(ctx *apptheory.Context) (*apptheory.Response, error) {
	if ctx == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return nil, newAppTheoryError("app.unauthorized", "unauthorized")
	}
	return apptheory.JSON(http.StatusOK, trustAuthVerifyResponse{
		Status:       "ok",
		InstanceSlug: instanceSlug,
	})
}
