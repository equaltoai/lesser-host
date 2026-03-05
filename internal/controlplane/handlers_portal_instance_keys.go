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

type instanceKeyListItem struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
	RevokedAt  time.Time `json:"revoked_at,omitempty"`
}

type listInstanceKeysResponse struct {
	Keys  []instanceKeyListItem `json:"keys"`
	Count int                   `json:"count"`
}

type revokeInstanceKeyResponse struct {
	InstanceSlug string `json:"instance_slug"`
	KeyID        string `json:"key_id"`
	Revoked      bool   `json:"revoked"`
}

func instanceKeyListItemFromModel(k *models.InstanceKey) instanceKeyListItem {
	if k == nil {
		return instanceKeyListItem{}
	}
	return instanceKeyListItem{
		ID:         strings.TrimSpace(k.ID),
		CreatedAt:  k.CreatedAt,
		LastUsedAt: k.LastUsedAt,
		RevokedAt:  k.RevokedAt,
	}
}

func (s *Server) handlePortalListInstanceKeys(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}
	if s == nil || s.store == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	limit := parseLimit(queryFirst(ctx, "limit"), 50, 1, 200)
	keys, err := s.store.ListInstanceKeysByInstance(ctx.Context(), strings.TrimSpace(inst.Slug), limit)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list keys"}
	}

	out := make([]instanceKeyListItem, 0, len(keys))
	for _, k := range keys {
		out = append(out, instanceKeyListItemFromModel(k))
	}

	return apptheory.JSON(http.StatusOK, listInstanceKeysResponse{
		Keys:  out,
		Count: len(out),
	})
}

func (s *Server) handlePortalRevokeInstanceKey(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))
	keyID := strings.TrimSpace(ctx.Param("keyId"))
	if keyID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "keyId is required"}
	}

	key, err := s.store.GetInstanceKey(ctx.Context(), keyID)
	if theoryErrors.IsNotFound(err) || key == nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "key not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to load key"}
	}

	if strings.TrimSpace(key.InstanceSlug) != slug {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "key not found"}
	}

	if !key.RevokedAt.IsZero() {
		return apptheory.JSON(http.StatusOK, revokeInstanceKeyResponse{
			InstanceSlug: slug,
			KeyID:        keyID,
			Revoked:      true,
		})
	}

	now := time.Now().UTC()
	key.RevokedAt = now
	_ = key.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(key).IfExists().Update("RevokedAt", "GSI1PK", "GSI1SK"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to revoke key"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "portal.instance_key.revoke",
		Target:    fmt.Sprintf("instance:%s", slug),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	s.tryWriteAuditLog(ctx, audit)

	return apptheory.JSON(http.StatusOK, revokeInstanceKeyResponse{
		InstanceSlug: slug,
		KeyID:        keyID,
		Revoked:      true,
	})
}
