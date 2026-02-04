package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	ctxKeyInstanceSlug = "instance.slug"
	ctxKeyInstanceKey  = "instance.key_id"
)

func (s *Server) InstanceAuthHook(ctx *apptheory.Context) (string, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	raw := strings.TrimSpace(bearerToken(ctx.Request.Headers))
	if raw == "" {
		return "", nil
	}

	sum := sha256.Sum256([]byte(raw))
	keyID := hex.EncodeToString(sum[:])

	var key models.InstanceKey
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceKey{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE_KEY#%s", keyID)).
		Where("SK", "=", "KEY").
		First(&key)
	if theoryErrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !key.RevokedAt.IsZero() {
		return "", nil
	}

	slug := strings.TrimSpace(key.InstanceSlug)
	ctx.Set(ctxKeyInstanceSlug, slug)
	ctx.Set(ctxKeyInstanceKey, strings.TrimSpace(key.ID))

	return slug, nil
}
