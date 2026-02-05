package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// GetAttestation loads an Attestation by ID.
func (s *Store) GetAttestation(ctx context.Context, id string) (*models.Attestation, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("attestation id is required")
	}

	var item models.Attestation
	err := s.getByPKSK(ctx, &models.Attestation{}, fmt.Sprintf("ATTESTATION#%s", id), "ATTESTATION", &item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// PutAttestation creates or updates an Attestation.
func (s *Store) PutAttestation(ctx context.Context, item *models.Attestation) error {
	if item == nil {
		return fmt.Errorf("attestation is required")
	}
	return s.putModel(ctx, item)
}
