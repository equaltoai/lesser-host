package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// GetRenderArtifact loads a RenderArtifact by ID.
func (s *Store) GetRenderArtifact(ctx context.Context, id string) (*models.RenderArtifact, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("render artifact id is required")
	}

	var item models.RenderArtifact
	err := s.DB.WithContext(ctx).
		Model(&models.RenderArtifact{}).
		Where("PK", "=", fmt.Sprintf("RENDER#%s", id)).
		Where("SK", "=", "ARTIFACT").
		ConsistentRead().
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// PutRenderArtifact creates or updates a RenderArtifact.
func (s *Store) PutRenderArtifact(ctx context.Context, item *models.RenderArtifact) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if item == nil {
		return fmt.Errorf("render artifact is required")
	}
	return s.DB.WithContext(ctx).Model(item).CreateOrUpdate()
}

// DeleteRenderArtifact deletes a RenderArtifact by ID.
func (s *Store) DeleteRenderArtifact(ctx context.Context, id string) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("render artifact id is required")
	}

	return s.DB.WithContext(ctx).
		Model(&models.RenderArtifact{}).
		Where("PK", "=", fmt.Sprintf("RENDER#%s", id)).
		Where("SK", "=", "ARTIFACT").
		Delete()
}

// ListExpiredRenderArtifacts lists artifacts past their expiry, ordered by expiry time.
func (s *Store) ListExpiredRenderArtifacts(ctx context.Context, now time.Time, limit int) ([]*models.RenderArtifact, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if limit <= 0 {
		limit = 50
	}

	cutoff := now.UTC().Format(time.RFC3339Nano) + "#~"

	var items []*models.RenderArtifact
	err := s.DB.WithContext(ctx).
		Model(&models.RenderArtifact{}).
		Index("gsi1").
		Where("gsi1PK", "=", "RENDER_EXPIRES").
		Where("gsi1SK", "<=", cutoff).
		Limit(limit).
		All(&items)
	if err != nil {
		return nil, err
	}
	return items, nil
}
