package models

import (
	"testing"
	"time"
)

func TestInstanceEnsureCoreDefaultsSetsUpdatedAtAndBodyEnabled(t *testing.T) {
	created := time.Date(2026, 3, 13, 18, 40, 0, 0, time.UTC)
	inst := &Instance{CreatedAt: created}

	inst.ensureCoreDefaults()

	if !inst.UpdatedAt.Equal(created) {
		t.Fatalf("UpdatedAt = %s, want %s", inst.UpdatedAt, created)
	}
	if inst.Status != InstanceStatusActive {
		t.Fatalf("Status = %q, want %q", inst.Status, InstanceStatusActive)
	}
	if inst.BodyEnabled == nil || !*inst.BodyEnabled {
		t.Fatalf("BodyEnabled = %v, want true", inst.BodyEnabled)
	}
}
