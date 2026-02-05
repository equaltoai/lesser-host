package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// GetLinkSafetyBasicResult loads a LinkSafetyBasicResult by ID.
func (s *Store) GetLinkSafetyBasicResult(ctx context.Context, id string) (*models.LinkSafetyBasicResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("result id is required")
	}

	var item models.LinkSafetyBasicResult
	err := s.getByPKSK(ctx, &models.LinkSafetyBasicResult{}, fmt.Sprintf("LINK_SAFETY_BASIC#%s", id), "RESULT", &item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// PutLinkSafetyBasicResult creates or updates a LinkSafetyBasicResult.
func (s *Store) PutLinkSafetyBasicResult(ctx context.Context, item *models.LinkSafetyBasicResult) error {
	if item == nil {
		return fmt.Errorf("result is required")
	}
	return s.putModel(ctx, item)
}
