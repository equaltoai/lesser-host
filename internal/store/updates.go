package store

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	return listByInstanceGSI1[models.UpdateJob](
		s,
		ctx,
		slug,
		limit,
		&models.UpdateJob{},
		"UPDATE_INSTANCE#%s",
		func(item *models.UpdateJob) time.Time { return item.CreatedAt },
	)
}
