package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Store) GetUpdateJob(ctx context.Context, id string) (*models.UpdateJob, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("job id is required")
	}

	var item models.UpdateJob
	err := s.DB.WithContext(ctx).
		Model(&models.UpdateJob{}).
		Where("PK", "=", fmt.Sprintf("UPDATE_JOB#%s", id)).
		Where("SK", "=", "JOB").
		ConsistentRead().
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) PutUpdateJob(ctx context.Context, item *models.UpdateJob) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if item == nil {
		return fmt.Errorf("job is required")
	}
	return s.DB.WithContext(ctx).Model(item).CreateOrUpdate()
}

func (s *Store) ListUpdateJobsByInstance(ctx context.Context, slug string, limit int) ([]*models.UpdateJob, error) {
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

	var items []*models.UpdateJob
	err := s.DB.WithContext(ctx).
		Model(&models.UpdateJob{}).
		Index("gsi1").
		Where("gsi1PK", "=", fmt.Sprintf("UPDATE_INSTANCE#%s", slug)).
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
