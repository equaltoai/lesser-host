package controlplane

import (
	"fmt"
	"net/http"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type operatorMeResponse struct {
	Username    string `json:"username"`
	Role        string `json:"role"`
	DisplayName string `json:"display_name,omitempty"`
}

func (s *Server) handleOperatorMe(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	if username == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	var user models.OperatorUser
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.OperatorUser{}).
		Where("PK", "=", fmt.Sprintf(models.KeyPatternUser, username)).
		Where("SK", "=", models.SKProfile).
		First(&user)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	return apptheory.JSON(http.StatusOK, operatorMeResponse{
		Username:    user.Username,
		Role:        user.Role,
		DisplayName: user.DisplayName,
	})
}

