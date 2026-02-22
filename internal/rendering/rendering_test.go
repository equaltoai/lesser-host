package rendering

import (
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestRenderArtifactID_IsStableWithWhitespace(t *testing.T) {
	t.Parallel()

	a := RenderArtifactID("v1", " https://example.com ")
	b := RenderArtifactID(" v1 ", "https://example.com")
	if a == "" || a != b {
		t.Fatalf("expected deterministic id, got %q vs %q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("expected sha256 hex length 64, got %d", len(a))
	}
}

func TestRenderArtifactIDForInstance_IsStableAndScoped(t *testing.T) {
	t.Parallel()

	a := RenderArtifactIDForInstance("v1", " inst ", " https://example.com ")
	b := RenderArtifactIDForInstance(" v1 ", "inst", "https://example.com")
	if a == "" || a != b {
		t.Fatalf("expected deterministic instance-scoped id, got %q vs %q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("expected sha256 hex length 64, got %d", len(a))
	}

	unscoped := RenderArtifactID("v1", "https://example.com")
	if unscoped == a {
		t.Fatalf("expected scoped id to differ from unscoped id")
	}
}

func TestRetentionAndExpiresAt(t *testing.T) {
	t.Parallel()

	days, class := RetentionForClass(" evidence ")
	if days != 180 || class != models.RenderRetentionClassEvidence {
		t.Fatalf("unexpected evidence retention: days=%d class=%q", days, class)
	}
	days, class = RetentionForClass("nope")
	if days != 30 || class != models.RenderRetentionClassBenign {
		t.Fatalf("unexpected default retention: days=%d class=%q", days, class)
	}

	now := time.Unix(100, 0).UTC()
	if got := ExpiresAtForRetention(now, 0); !got.Equal(now.Add(30 * 24 * time.Hour)) {
		t.Fatalf("unexpected default expiry: %v", got)
	}
	if got := ExpiresAtForRetention(now, 2); !got.Equal(now.Add(2 * 24 * time.Hour)) {
		t.Fatalf("unexpected expiry: %v", got)
	}
}

func TestRenderObjectKeys(t *testing.T) {
	t.Parallel()

	if got := ThumbnailObjectKey(""); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := SnapshotObjectKey(" id "); got != "renders/id/snapshot.txt" {
		t.Fatalf("unexpected snapshot key: %q", got)
	}
}
