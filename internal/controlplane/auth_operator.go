package controlplane

import (
	"fmt"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
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

func operatorMethodFromContext(ctx *apptheory.Context) string {
	if ctx == nil {
		return ""
	}
	method, _ := ctx.Get(ctxKeyOperatorMethod).(string)
	return strings.TrimSpace(method)
}

// OperatorAuthHook authenticates an operator request using a bearer token.
func (s *Server) OperatorAuthHook(ctx *apptheory.Context) (string, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	token := httpx.BearerToken(ctx.Request.Headers)
	if token == "" {
		return "", nil
	}

	var session models.OperatorSession
	var err error
	candidates := []string{sha256HexTrimmed(token), token} // hash lookup first; fallback to legacy plaintext.
	found := false
	for _, id := range candidates {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		pk := fmt.Sprintf(models.KeyPatternSession, id)
		err = s.store.DB.WithContext(ctx.Context()).
			Model(&models.OperatorSession{}).
			Where("PK", "=", pk).
			Where("SK", "=", "SESSION").
			First(&session)
		if theoryErrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
		}
		found = true
		break
	}
	if !found {
		return "", nil
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
