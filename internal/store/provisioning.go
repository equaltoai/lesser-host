package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// GetProvisionJob loads a ProvisionJob by ID.
func (s *Store) GetProvisionJob(ctx context.Context, id string) (*models.ProvisionJob, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("job id is required")
	}

	var item models.ProvisionJob
	err := s.DB.WithContext(ctx).
		Model(&models.ProvisionJob{}).
		Where("PK", "=", fmt.Sprintf("PROVISION_JOB#%s", id)).
		Where("SK", "=", "JOB").
		ConsistentRead().
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// PutProvisionJob creates or updates a ProvisionJob.
func (s *Store) PutProvisionJob(ctx context.Context, item *models.ProvisionJob) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if item == nil {
		return fmt.Errorf("job is required")
	}
	return s.DB.WithContext(ctx).Model(item).CreateOrUpdate()
}
