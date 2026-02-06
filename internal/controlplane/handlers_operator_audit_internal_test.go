package controlplane

import (
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestParseRFC3339Time(t *testing.T) {
	t.Parallel()

	if got, err := parseRFC3339Time(" "); err != nil || !got.IsZero() {
		t.Fatalf("expected zero time, got %v err=%v", got, err)
	}

	got, err := parseRFC3339Time("2026-02-06T00:00:00Z")
	if err != nil {
		t.Fatalf("parseRFC3339Time: %v", err)
	}
	if got.Location() != time.UTC {
		t.Fatalf("expected UTC, got %v", got.Location())
	}

	if _, err := parseRFC3339Time("nope"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestParseOperatorAuditLogFilters(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{
		Request: apptheory.Request{
			Query: map[string][]string{
				"limit": {"999"},
				"since": {"2026-02-06T00:00:00Z"},
			},
		},
	}

	f, appErr := parseOperatorAuditLogFilters(ctx)
	if appErr != nil {
		t.Fatalf("parseOperatorAuditLogFilters: %v", appErr)
	}
	if f.Limit != 200 {
		t.Fatalf("expected limit clamped to 200, got %d", f.Limit)
	}
	if f.Since.IsZero() {
		t.Fatalf("expected since parsed")
	}

	ctx.Request.Query["since"] = []string{"nope"}
	if _, appErr := parseOperatorAuditLogFilters(ctx); appErr == nil {
		t.Fatalf("expected bad_request for invalid since")
	}
}

func TestFilterOperatorAuditLogEntries_FiltersSortsAndLimits(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	items := []*models.AuditLogEntry{
		nil,
		{Actor: "alice", Action: "a", RequestID: "r1", CreatedAt: now.Add(-1 * time.Hour)},
		{Actor: "bob", Action: "a", RequestID: "r2", CreatedAt: now},
		{Actor: "alice", Action: "b", RequestID: "r3", CreatedAt: now.Add(-2 * time.Hour)},
	}

	out := filterOperatorAuditLogEntries(items, operatorAuditLogFilters{
		Actor: "alice",
		Limit: 1,
	})
	if len(out) != 1 {
		t.Fatalf("expected limited output, got %#v", out)
	}
	if out[0].Actor != "alice" {
		t.Fatalf("unexpected output: %#v", out[0])
	}
}

