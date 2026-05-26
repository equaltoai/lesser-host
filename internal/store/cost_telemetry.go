package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// PutCostTelemetry creates or updates a CostTelemetry record.
// Because the primary key is (COST_TELEMETRY#<slug>, <date>), re-running
// the worker for the same slug and date upserts the same record — the
// write path is naturally idempotent.
func (s *Store) PutCostTelemetry(ctx context.Context, record *models.CostTelemetry) error {
	if err := s.requireDB(); err != nil {
		return err
	}
	if record == nil {
		return fmt.Errorf("cost telemetry record is required")
	}
	return s.putModel(ctx, record)
}

// ListCostTelemetryByInstance returns cost telemetry records for the given
// instance slug over the specified date range (inclusive). Records are
// ordered by date descending (most recent first).
//
// The date range uses the SK field which is the date in YYYY-MM-DD format.
// Both dateFrom and dateTo are inclusive bounds.
func (s *Store) ListCostTelemetryByInstance(ctx context.Context, slug string, dateFrom, dateTo string, limit int) ([]*models.CostTelemetry, error) {
	if err := s.requireDB(); err != nil {
		return nil, err
	}

	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return nil, fmt.Errorf("instance slug is required")
	}
	dateFrom = strings.TrimSpace(dateFrom)
	dateTo = strings.TrimSpace(dateTo)
	if dateFrom == "" || dateTo == "" {
		return nil, fmt.Errorf("dateFrom and dateTo are required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	pk := fmt.Sprintf("COST_TELEMETRY#%s", slug)

	var items []*models.CostTelemetry
	err := s.DB.WithContext(ctx).
		Model(&models.CostTelemetry{}).
		Where("PK", "=", pk).
		Where("SK", ">=", dateFrom).
		Where("SK", "<=", dateTo).
		OrderBy("SK", "DESC").
		Limit(limit).
		All(&items)
	if err != nil {
		return nil, err
	}
	return items, nil
}
