package trust

import (
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestRenderQueue_Helpers(t *testing.T) {
	t.Parallel()

	if appErr := requireQueueRenderDeps(nil, &apptheory.Context{}); appErr == nil {
		t.Fatalf("expected error for nil server")
	}
	if appErr := requireQueueRenderDeps(&Server{}, nil); appErr == nil {
		t.Fatalf("expected error for nil ctx")
	}

	db := ttmocks.NewMockExtendedDB()
	s := &Server{store: store.New(db), queues: &queueClient{}}
	if appErr := requireQueueRenderDeps(s, &apptheory.Context{}); appErr != nil {
		t.Fatalf("expected deps ok, got %v", appErr)
	}

	if _, appErr := normalizeQueueRenderURL(" "); appErr == nil {
		t.Fatalf("expected url required error")
	}
	if got, appErr := normalizeQueueRenderURL(" https://x "); appErr != nil || got != "https://x" {
		t.Fatalf("unexpected normalize: %q err=%v", got, appErr)
	}

	now := time.Unix(100, 0).UTC()
	days, classOut, expires := desiredQueueRenderRetention(now, models.RenderRetentionClassEvidence, 0)
	if days != 180 || classOut != models.RenderRetentionClassEvidence {
		t.Fatalf("unexpected retention: days=%d class=%q", days, classOut)
	}
	if !expires.Equal(rendering.ExpiresAtForRetention(now, 180)) {
		t.Fatalf("unexpected expiry: got %v", expires)
	}

	existing := &models.RenderArtifact{
		ExpiresAt:      now,
		RetentionClass: models.RenderRetentionClassBenign,
	}
	if !maybeExtendRenderArtifact(existing, now.Add(24*time.Hour), models.RenderRetentionClassEvidence, " alice ", " req ") {
		t.Fatalf("expected extension")
	}
	if existing.RetentionClass != models.RenderRetentionClassEvidence || existing.RequestedBy != "alice" || existing.RequestID != "req" {
		t.Fatalf("unexpected artifact update: %#v", existing)
	}

	// No-op extension returns false.
	if maybeExtendRenderArtifact(existing, existing.ExpiresAt, existing.RetentionClass, "x", "y") {
		t.Fatalf("expected no update")
	}
}
