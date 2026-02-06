package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type authLogoutResponse struct {
	OK bool `json:"ok"`
}

func (s *Server) handleAuthLogout(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	sessionID, _ := ctx.Get(ctxKeyOperatorSessionID).(string)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		pk := fmt.Sprintf(models.KeyPatternSession, sessionID)
		if err := s.store.DB.WithContext(ctx.Context()).
			Model(&models.OperatorSession{}).
			Where("PK", "=", pk).
			Where("SK", "=", "SESSION").
			Delete(); err != nil && !theoryErrors.IsNotFound(err) {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to revoke session"}
		}
	}

	now := time.Now().UTC()
	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "auth.logout",
		Target:    "operator_session",
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, authLogoutResponse{OK: true})
}

