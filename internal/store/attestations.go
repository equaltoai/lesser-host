package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Store) GetAttestation(ctx context.Context, id string) (*models.Attestation, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("attestation id is required")
	}

	var item models.Attestation
	err := s.DB.WithContext(ctx).
		Model(&models.Attestation{}).
		Where("PK", "=", fmt.Sprintf("ATTESTATION#%s", id)).
		Where("SK", "=", "ATTESTATION").
		ConsistentRead().
		First(&item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) PutAttestation(ctx context.Context, item *models.Attestation) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	if item == nil {
		return fmt.Errorf("attestation is required")
	}
	return s.DB.WithContext(ctx).Model(item).CreateOrUpdate()
}
