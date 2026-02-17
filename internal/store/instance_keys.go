package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

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
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return nil, fmt.Errorf("instance slug is required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var items []*models.InstanceKey
	err := s.DB.WithContext(ctx).
		Model(&models.InstanceKey{}).
		Index("gsi1").
		Where("gsi1PK", "=", fmt.Sprintf("INSTANCE_KEYS#%s", slug)).
		Limit(limit).
		All(&items)
	if err != nil {
		return nil, err
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i] == nil {
			return false
		}
		if items[j] == nil {
			return true
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	return items, nil
}
