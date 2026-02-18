package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Store) GetInstanceKey(ctx context.Context, id string) (*models.InstanceKey, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("key id is required")
	}

	var item models.InstanceKey
	err := s.DB.WithContext(ctx).
		Model(&models.InstanceKey{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE_KEY#%s", id)).
		Where("SK", "=", "KEY").
		ConsistentRead().
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) ListInstanceKeysByInstance(ctx context.Context, slug string, limit int) ([]*models.InstanceKey, error) {
	return listByInstanceGSI1[models.InstanceKey](
		s,
		ctx,
		slug,
		limit,
		&models.InstanceKey{},
		"INSTANCE_KEYS#%s",
		func(item *models.InstanceKey) time.Time { return item.CreatedAt },
	)
}
