package controlplane

import (
	"fmt"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	ctxKeyOperatorRole      = "operator.role"
	ctxKeyOperatorSessionID = "operator.session_id"
	ctxKeyOperatorMethod    = "operator.method"
)

func operatorRoleFromContext(ctx *apptheory.Context) string {
	if ctx == nil {
		return ""
	}
	role, _ := ctx.Get(ctxKeyOperatorRole).(string)
	return strings.TrimSpace(role)
}

// OperatorAuthHook authenticates an operator request using a bearer token.
func (s *Server) OperatorAuthHook(ctx *apptheory.Context) (string, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	token := bearerToken(ctx.Request.Headers)
	if token == "" {
		return "", nil
	}

	var session models.OperatorSession
	pk := fmt.Sprintf(models.KeyPatternSession, token)
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.OperatorSession{}).
		Where("PK", "=", pk).
		Where("SK", "=", "SESSION").
		First(&session)
	if theoryErrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if !session.ExpiresAt.IsZero() && time.Now().After(session.ExpiresAt) {
		_ = s.store.DB.WithContext(ctx.Context()).
			Model(&models.OperatorSession{}).
			Where("PK", "=", session.PK).
			Where("SK", "=", session.SK).
			Delete()
		return "", nil
	}

	ctx.Set(ctxKeyOperatorRole, strings.TrimSpace(session.Role))
	ctx.Set(ctxKeyOperatorSessionID, strings.TrimSpace(session.ID))
	ctx.Set(ctxKeyOperatorMethod, strings.TrimSpace(session.Method))

	return strings.TrimSpace(session.Username), nil
}
