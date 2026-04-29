package trust

import (
	"net/http"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

type trustAuthVerifyResponse struct {
	Status       string `json:"status"`
	InstanceSlug string `json:"instance_slug"`
}

func (s *Server) handleTrustAuthVerify(ctx *apptheory.Context) (*apptheory.Response, error) {
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	return apptheory.JSON(http.StatusOK, trustAuthVerifyResponse{
		Status:       "ok",
		InstanceSlug: instanceSlug,
	})
}
