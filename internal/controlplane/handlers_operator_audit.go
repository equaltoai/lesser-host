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

func (s *Server) handleListOperatorAuditLog(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	target := strings.TrimSpace(queryFirst(ctx, "target"))
	actor := strings.TrimSpace(queryFirst(ctx, "actor"))
	action := strings.TrimSpace(queryFirst(ctx, "action"))
	requestID := strings.TrimSpace(queryFirst(ctx, "request_id"))

	since, err := parseRFC3339Time(queryFirst(ctx, "since"))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "since must be RFC3339"}
	}
	until, err := parseRFC3339Time(queryFirst(ctx, "until"))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "until must be RFC3339"}
	}

	limit := parseLimit(queryFirst(ctx, "limit"), 50, 1, 200)

	var items []*models.AuditLogEntry
	if target != "" {
		pk := fmt.Sprintf("AUDIT#%s", target)
		q := s.store.DB.WithContext(ctx.Context()).
			Model(&models.AuditLogEntry{}).
			Where("PK", "=", pk).
			Limit(200)

		if since.IsZero() && until.IsZero() {
			q = q.Where("SK", "BEGINS_WITH", "EVENT#")
		} else {
			if !since.IsZero() {
				q = q.Where("SK", ">=", fmt.Sprintf("EVENT#%s#", since.UTC().Format(time.RFC3339Nano)))
			}
			if !until.IsZero() {
				q = q.Where("SK", "<=", fmt.Sprintf("EVENT#%s#~", until.UTC().Format(time.RFC3339Nano)))
			}
		}

		if err := q.All(&items); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list audit log"}
		}
	} else {
		// Operator-friendly: scan audit log (limited) and filter/sort in-memory.
		if err := s.store.DB.WithContext(ctx.Context()).
			Model(&models.AuditLogEntry{}).
			Where("SK", "BEGINS_WITH", "EVENT#").
			Limit(200).
			All(&items); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list audit log"}
		}
	}

	out := make([]models.AuditLogEntry, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		if actor != "" && strings.TrimSpace(it.Actor) != actor {
			continue
		}
		if action != "" && strings.TrimSpace(it.Action) != action {
			continue
		}
		if requestID != "" && strings.TrimSpace(it.RequestID) != requestID {
			continue
		}
		if !since.IsZero() && it.CreatedAt.Before(since) {
			continue
		}
		if !until.IsZero() && it.CreatedAt.After(until) {
			continue
		}
		out = append(out, *it)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})

	if len(out) > limit {
		out = out[:limit]
	}

	return apptheory.JSON(http.StatusOK, listOperatorAuditLogResponse{Entries: out, Count: len(out)})
}
