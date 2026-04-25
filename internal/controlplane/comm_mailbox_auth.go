package controlplane

import (
	"net/http"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// requireMailboxInstanceKey authenticates Body/instance mailbox calls using
// only sha256(raw bearer) instance-key lookup. It intentionally does not allow
// the legacy plaintext key-id fallback that older comm send/status paths still
// accept for backward compatibility.
func (s *Server) requireMailboxInstanceKey(ctx *apptheory.Context) (*models.InstanceKey, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	token := httpx.BearerToken(ctx.Request.Headers)
	if token == "" {
		return nil, apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}

	key, err := s.store.GetInstanceKey(ctx.Context(), sha256HexTrimmed(token))
	if theoryErrors.IsNotFound(err) || key == nil || !key.RevokedAt.IsZero() {
		return nil, apptheory.NewAppTheoryError("comm.unauthorized", "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	if err != nil {
		return nil, apptheory.NewAppTheoryError("comm.internal", "internal error").WithStatusCode(http.StatusInternalServerError)
	}

	key.LastUsedAt = time.Now().UTC()
	_ = key.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(key).IfExists().Update("LastUsedAt")
	return key, nil
}
