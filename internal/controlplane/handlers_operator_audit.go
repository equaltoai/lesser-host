package controlplane

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type listOperatorAuditLogResponse struct {
	Entries []models.AuditLogEntry `json:"entries"`
	Count   int                    `json:"count"`
}

func parseRFC3339Time(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

type operatorAuditLogFilters struct {
	Target    string
	Actor     string
	Action    string
	RequestID string
	Since     time.Time
	Until     time.Time
	Limit     int
}

func parseOperatorAuditLogFilters(ctx *apptheory.Context) (operatorAuditLogFilters, *apptheory.AppError) {
	filters := operatorAuditLogFilters{
		Target:    strings.TrimSpace(queryFirst(ctx, "target")),
		Actor:     strings.TrimSpace(queryFirst(ctx, "actor")),
		Action:    strings.TrimSpace(queryFirst(ctx, "action")),
		RequestID: strings.TrimSpace(queryFirst(ctx, "request_id")),
		Limit:     parseLimit(queryFirst(ctx, "limit"), 50, 1, 200),
	}

	since, err := parseRFC3339Time(queryFirst(ctx, "since"))
	if err != nil {
		return operatorAuditLogFilters{}, &apptheory.AppError{Code: "app.bad_request", Message: "since must be RFC3339"}
	}
	filters.Since = since

	until, err := parseRFC3339Time(queryFirst(ctx, "until"))
	if err != nil {
		return operatorAuditLogFilters{}, &apptheory.AppError{Code: "app.bad_request", Message: "until must be RFC3339"}
	}
	filters.Until = until

	return filters, nil
}

func (s *Server) listOperatorAuditLogEntries(ctx *apptheory.Context, filters operatorAuditLogFilters) ([]*models.AuditLogEntry, *apptheory.AppError) {
	var items []*models.AuditLogEntry

	if filters.Target != "" {
		pk := fmt.Sprintf("AUDIT#%s", filters.Target)
		q := s.store.DB.WithContext(ctx.Context()).
			Model(&models.AuditLogEntry{}).
			Where("PK", "=", pk).
			Limit(200)

		if filters.Since.IsZero() && filters.Until.IsZero() {
			q = q.Where("SK", "BEGINS_WITH", "EVENT#")
		} else {
			if !filters.Since.IsZero() {
				q = q.Where("SK", ">=", fmt.Sprintf("EVENT#%s#", filters.Since.UTC().Format(time.RFC3339Nano)))
			}
			if !filters.Until.IsZero() {
				q = q.Where("SK", "<=", fmt.Sprintf("EVENT#%s#~", filters.Until.UTC().Format(time.RFC3339Nano)))
			}
		}

		if err := q.All(&items); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list audit log"}
		}
		return items, nil
	}

	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.AuditLogEntry{}).
		Where("SK", "BEGINS_WITH", "EVENT#").
		Limit(200).
		All(&items); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list audit log"}
	}

	return items, nil
}

func filterOperatorAuditLogEntries(items []*models.AuditLogEntry, filters operatorAuditLogFilters) []models.AuditLogEntry {
	out := make([]models.AuditLogEntry, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		if filters.Actor != "" && strings.TrimSpace(it.Actor) != filters.Actor {
			continue
		}
		if filters.Action != "" && strings.TrimSpace(it.Action) != filters.Action {
			continue
		}
		if filters.RequestID != "" && strings.TrimSpace(it.RequestID) != filters.RequestID {
			continue
		}
		if !filters.Since.IsZero() && it.CreatedAt.Before(filters.Since) {
			continue
		}
		if !filters.Until.IsZero() && it.CreatedAt.After(filters.Until) {
			continue
		}
		out = append(out, *it)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})

	if len(out) > filters.Limit {
		out = out[:filters.Limit]
	}

	return out
}

func (s *Server) handleListOperatorAuditLog(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	filters, appErr := parseOperatorAuditLogFilters(ctx)
	if appErr != nil {
		return nil, appErr
	}

	items, appErr := s.listOperatorAuditLogEntries(ctx, filters)
	if appErr != nil {
		return nil, appErr
	}

	out := filterOperatorAuditLogEntries(items, filters)
	return apptheory.JSON(http.StatusOK, listOperatorAuditLogResponse{Entries: out, Count: len(out)})
}
