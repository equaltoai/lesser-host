package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// GetLinkPreview loads a LinkPreview by ID.
func (s *Store) GetLinkPreview(ctx context.Context, id string) (*models.LinkPreview, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("link preview id is required")
	}

	var item models.LinkPreview
	err := s.DB.WithContext(ctx).
		Model(&models.LinkPreview{}).
		Where("PK", "=", fmt.Sprintf("LINK_PREVIEW#%s", id)).
		Where("SK", "=", "PREVIEW").
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// PutLinkPreview creates or updates a LinkPreview.
func (s *Store) PutLinkPreview(ctx context.Context, item *models.LinkPreview) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if item == nil {
		return fmt.Errorf("link preview is required")
	}
	return s.DB.WithContext(ctx).Model(item).CreateOrUpdate()
}
