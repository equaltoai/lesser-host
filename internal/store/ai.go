package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// GetAIJob loads an AIJob by ID.
func (s *Store) GetAIJob(ctx context.Context, id string) (*models.AIJob, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("job id is required")
	}

	var item models.AIJob
	err := s.DB.WithContext(ctx).
		Model(&models.AIJob{}).
		Where("PK", "=", fmt.Sprintf("AIJOB#%s", id)).
		Where("SK", "=", "JOB").
		ConsistentRead().
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// PutAIJob creates or updates an AIJob.
func (s *Store) PutAIJob(ctx context.Context, item *models.AIJob) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if item == nil {
		return fmt.Errorf("job is required")
	}
	return s.DB.WithContext(ctx).Model(item).CreateOrUpdate()
}

// GetAIResult loads an AIResult by ID.
func (s *Store) GetAIResult(ctx context.Context, id string) (*models.AIResult, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("result id is required")
	}

	var item models.AIResult
	err := s.DB.WithContext(ctx).
		Model(&models.AIResult{}).
		Where("PK", "=", fmt.Sprintf("AIRESULT#%s", id)).
		Where("SK", "=", "RESULT").
		ConsistentRead().
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// PutAIResult creates or updates an AIResult.
func (s *Store) PutAIResult(ctx context.Context, item *models.AIResult) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if item == nil {
		return fmt.Errorf("result is required")
	}
	return s.DB.WithContext(ctx).Model(item).CreateOrUpdate()
}

// CountQueuedAIJobsByInstance returns the number of queued AI jobs for an instance, up to limit.
func (s *Store) CountQueuedAIJobsByInstance(ctx context.Context, instanceSlug string, limit int) (int, error) {
	if s == nil || s.DB == nil {
		return 0, fmt.Errorf("store not initialized")
	}

	instanceSlug = strings.TrimSpace(instanceSlug)
	if instanceSlug == "" {
		return 0, fmt.Errorf("instance slug is required")
	}
	if limit <= 0 {
		limit = 100
	}

	pk := fmt.Sprintf("AIJOB_STATUS#%s#queued", instanceSlug)

	var items []*models.AIJob
	err := s.DB.WithContext(ctx).
		Model(&models.AIJob{}).
		Index("gsi1").
		Where("gsi1PK", "=", pk).
		Limit(limit).
		All(&items)
	if err != nil {
		return 0, err
	}
	return len(items), nil
}
