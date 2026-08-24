package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type listUsageResponse struct {
	Entries []*models.UsageLedgerEntry `json:"entries"`
	Count   int                        `json:"count"`
}

func (s *Server) handleListInstanceUsage(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	slug := strings.ToLower(strings.TrimSpace(ctx.Param("slug")))
	if slug == "" {
		return nil, newAppTheoryError("app.bad_request", "slug is required")
	}

	month := strings.TrimSpace(ctx.Param("month"))
	if month == "" {
		return nil, newAppTheoryError("app.bad_request", "month is required")
	}
	if _, err := time.Parse("2006-01", month); err != nil {
		return nil, newAppTheoryError("app.bad_request", "month must be YYYY-MM")
	}

	// Ensure the instance exists.
	if _, err := s.getInstance(ctx, slug); theoryErrors.IsNotFound(err) {
		return nil, newAppTheoryError("app.not_found", "instance not found")
	} else if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	pk := fmt.Sprintf("USAGE#%s#%s", slug, month)

	var items []*models.UsageLedgerEntry
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.UsageLedgerEntry{}).
		Where("PK", "=", pk).
		Limit(200).
		All(&items)
	if err != nil {
		return nil, newAppTheoryError("app.internal", "failed to list usage")
	}

	return apptheory.JSON(http.StatusOK, listUsageResponse{
		Entries: items,
		Count:   len(items),
	})
}
