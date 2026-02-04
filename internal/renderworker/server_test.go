package renderworker

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type fakeRenderStore struct {
	mu    sync.Mutex
	items map[string]*models.RenderArtifact
}

func (f *fakeRenderStore) GetRenderArtifact(_ context.Context, id string) (*models.RenderArtifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[strings.TrimSpace(id)]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return item, nil
}

func (f *fakeRenderStore) PutRenderArtifact(_ context.Context, item *models.RenderArtifact) error {
	if item == nil {
		return fmt.Errorf("item is nil")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.items == nil {
		f.items = map[string]*models.RenderArtifact{}
	}
	f.items[strings.TrimSpace(item.ID)] = item
	return nil
}

func (f *fakeRenderStore) DeleteRenderArtifact(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, strings.TrimSpace(id))
	return nil
}

func (f *fakeRenderStore) ListExpiredRenderArtifacts(_ context.Context, now time.Time, limit int) ([]*models.RenderArtifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if limit <= 0 {
		limit = 50
	}

	var out []*models.RenderArtifact
	for _, item := range f.items {
		if item == nil || item.ExpiresAt.IsZero() {
			continue
		}
		if !item.ExpiresAt.After(now) {
			out = append(out, item)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return strings.TrimSpace(out[i].ID) < strings.TrimSpace(out[j].ID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type fakeArtifactStore struct {
	mu      sync.Mutex
	deleted []string
}

func (f *fakeArtifactStore) PutObject(_ context.Context, _ string, _ []byte, _ string, _ string) error {
	return nil
}

func (f *fakeArtifactStore) DeleteObject(_ context.Context, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	f.mu.Lock()
	f.deleted = append(f.deleted, key)
	f.mu.Unlock()
	return nil
}

func TestRetentionSweepDeletesExpiredArtifacts(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	expired := &models.RenderArtifact{
		ID:                 "expired-1",
		PolicyVersion:      "v1",
		NormalizedURL:      "https://example.com",
		ThumbnailObjectKey: "renders/expired-1/thumbnail.jpg",
		SnapshotObjectKey:  "renders/expired-1/snapshot.txt",
		RetentionClass:     models.RenderRetentionClassBenign,
		CreatedAt:          now.Add(-2 * time.Hour),
		ExpiresAt:          now.Add(-1 * time.Hour),
	}
	keep := &models.RenderArtifact{
		ID:                 "keep-1",
		PolicyVersion:      "v1",
		NormalizedURL:      "https://example.net",
		ThumbnailObjectKey: "renders/keep-1/thumbnail.jpg",
		SnapshotObjectKey:  "renders/keep-1/snapshot.txt",
		RetentionClass:     models.RenderRetentionClassBenign,
		CreatedAt:          now.Add(-2 * time.Hour),
		ExpiresAt:          now.Add(24 * time.Hour),
	}

	st := &fakeRenderStore{
		items: map[string]*models.RenderArtifact{
			expired.ID: expired,
			keep.ID:    keep,
		},
	}
	art := &fakeArtifactStore{}

	cfg := config.Config{
		AppName: "lesser-host",
		Stage:   "lab",
	}
	srv := NewServer(cfg, st, art)

	env := testkit.New()
	app := env.App()
	Register(app, srv)

	ruleName := fmt.Sprintf("%s-%s-retention-sweep", cfg.AppName, cfg.Stage)
	event := testkit.EventBridgeEvent(testkit.EventBridgeEventOptions{
		Resources: []string{
			fmt.Sprintf("arn:aws:events:us-east-1:123456789012:rule/%s", ruleName),
		},
	})

	out, err := env.InvokeEventBridge(context.Background(), app, event)
	if err != nil {
		t.Fatalf("InvokeEventBridge error: %v", err)
	}

	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", out)
	}
	var deleted int
	switch v := m["deleted"].(type) {
	case int:
		deleted = v
	case int64:
		deleted = int(v)
	case float64:
		deleted = int(v)
	default:
		t.Fatalf("unexpected deleted type: %T", m["deleted"])
	}
	if deleted != 1 {
		t.Fatalf("expected deleted=1, got %d", deleted)
	}

	// Record deleted, non-expired remains.
	st.mu.Lock()
	_, expiredStillPresent := st.items[expired.ID]
	_, keepPresent := st.items[keep.ID]
	st.mu.Unlock()

	if expiredStillPresent {
		t.Fatalf("expected expired artifact to be deleted")
	}
	if !keepPresent {
		t.Fatalf("expected keep artifact to remain")
	}

	// S3 objects were deleted.
	art.mu.Lock()
	deletedKeys := append([]string(nil), art.deleted...)
	art.mu.Unlock()

	wantThumb := expired.ThumbnailObjectKey
	wantSnap := expired.SnapshotObjectKey
	if !containsString(deletedKeys, wantThumb) {
		t.Fatalf("expected deleted keys to include %q; got %v", wantThumb, deletedKeys)
	}
	if !containsString(deletedKeys, wantSnap) {
		t.Fatalf("expected deleted keys to include %q; got %v", wantSnap, deletedKeys)
	}
}

func containsString(haystack []string, needle string) bool {
	needle = strings.TrimSpace(needle)
	for _, s := range haystack {
		if strings.TrimSpace(s) == needle {
			return true
		}
	}
	return false
}
