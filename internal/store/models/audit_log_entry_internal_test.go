package models

import (
	"strings"
	"testing"
	"time"
)

func TestAuditLogEntryUpdateKeysRejectsOversizedTarget(t *testing.T) {
	entry := &AuditLogEntry{
		Target:    strings.Repeat("x", auditLogPartitionKeyMaxBytes),
		CreatedAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}

	err := entry.UpdateKeys()
	if err == nil {
		t.Fatalf("expected oversized target error")
	}
	if entry.PK != "" {
		t.Fatalf("oversized target must not emit a PK, got %q", entry.PK)
	}
	if strings.Contains(entry.PK, strings.Repeat("x", 64)) {
		t.Fatalf("oversized target leaked into PK: %q", entry.PK)
	}
}
