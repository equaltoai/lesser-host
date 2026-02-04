package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Store) GetLinkSafetyBasicResult(ctx context.Context, id string) (*models.LinkSafetyBasicResult, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("result id is required")
	}

	var item models.LinkSafetyBasicResult
	err := s.DB.WithContext(ctx).
		Model(&models.LinkSafetyBasicResult{}).
		Where("PK", "=", fmt.Sprintf("LINK_SAFETY_BASIC#%s", id)).
		Where("SK", "=", "RESULT").
		ConsistentRead().
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) PutLinkSafetyBasicResult(ctx context.Context, item *models.LinkSafetyBasicResult) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if item == nil {
		return fmt.Errorf("result is required")
	}
	return s.DB.WithContext(ctx).Model(item).CreateOrUpdate()
}
