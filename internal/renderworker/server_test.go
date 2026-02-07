package renderworker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/apptheory/testkit"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/rendering"
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

func TestHandlePreviewQueueMessage_DropsInvalidAndUnknown(t *testing.T) {
	t.Parallel()

	s := &Server{store: &fakeRenderStore{}}

	if err := s.handlePreviewQueueMessage(nil, events.SQSMessage{}); err == nil {
		t.Fatalf("expected error for nil ctx")
	}

	ctx := &apptheory.EventContext{RequestID: "r1"}

	// Invalid JSON is dropped.
	if err := s.handlePreviewQueueMessage(ctx, events.SQSMessage{Body: "{"}); err != nil {
		t.Fatalf("expected nil for invalid json, got %v", err)
	}

	// Unknown kind is dropped.
	body, _ := json.Marshal(rendering.RenderJobMessage{Kind: "other"})
	if err := s.handlePreviewQueueMessage(ctx, events.SQSMessage{Body: string(body)}); err != nil {
		t.Fatalf("expected nil for unknown kind, got %v", err)
	}
}

func TestSQSQueueNameFromURL(t *testing.T) {
	t.Parallel()

	if got := sqsQueueNameFromURL(""); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := sqsQueueNameFromURL("http://%"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := sqsQueueNameFromURL("not a url"); got != "not a url" {
		t.Fatalf("expected last path segment, got %q", got)
	}
	if got := sqsQueueNameFromURL("https://sqs.us-east-1.amazonaws.com/123/q"); got != "q" {
		t.Fatalf("expected q, got %q", got)
	}
}

func TestRenderJobHelpers(t *testing.T) {
	t.Parallel()

	normalized := "https://8.8.8.8/"
	wantID := normalizeRenderJobID(normalized, "")
	if wantID == "" {
		t.Fatalf("expected render id")
	}
	if got := normalizeRenderJobID(normalized, "wrong"); got != wantID {
		t.Fatalf("expected computed id, got %q", got)
	}
	if got := normalizeRenderJobID(normalized, wantID); got != wantID {
		t.Fatalf("expected preserved id, got %q", got)
	}

	now := time.Date(2026, 2, 7, 0, 0, 0, 0, time.UTC)
	days, classOut, expiresAt := desiredRenderExpiration(now, models.RenderRetentionClassEvidence, 0)
	if days <= 0 || classOut == "" || expiresAt.IsZero() {
		t.Fatalf("unexpected desiredRenderExpiration: days=%d class=%q expiresAt=%v", days, classOut, expiresAt)
	}

	days2, _, _ := desiredRenderExpiration(now, models.RenderRetentionClassBenign, 3)
	if days2 != 3 {
		t.Fatalf("expected override retention days, got %d", days2)
	}

	if maxTime(now, now.Add(1*time.Minute)) != now.Add(1*time.Minute) {
		t.Fatalf("expected maxTime to pick later time")
	}

	if got := classOutOrExisting("", models.RenderRetentionClassEvidence); got != models.RenderRetentionClassEvidence {
		t.Fatalf("expected evidence preference, got %q", got)
	}
	if got := classOutOrExisting("custom", ""); got != "custom" {
		t.Fatalf("expected preserve existing class, got %q", got)
	}
	if got := safeURLString(nil); got != "" {
		t.Fatalf("expected empty")
	}
	u, _ := url.Parse("https://example.com/")
	if got := safeURLString(u); got != "https://example.com/" {
		t.Fatalf("unexpected url string: %q", got)
	}
}

func TestMaybeShortCircuitExistingRender_UpdatesRetentionAndReturnsDone(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	normalized := "https://8.8.8.8/"
	renderID := normalizeRenderJobID(normalized, "")

	st := &fakeRenderStore{
		items: map[string]*models.RenderArtifact{
			renderID: {
				ID:                 renderID,
				PolicyVersion:      "v1",
				NormalizedURL:      normalized,
				RetentionClass:     models.RenderRetentionClassBenign,
				ThumbnailObjectKey: "renders/" + renderID + "/thumbnail.jpg",
				RenderedAt:         now.Add(-1 * time.Minute),
				ExpiresAt:          now.Add(1 * time.Hour),
			},
		},
	}

	srv := NewServer(config.Config{}, st, &fakeArtifactStore{})

	done, err := srv.maybeShortCircuitExistingRender(context.Background(), renderID, now.Add(48*time.Hour), models.RenderRetentionClassEvidence, "req", "inst")
	if err != nil || !done {
		t.Fatalf("expected done=true, got done=%v err=%v", done, err)
	}

	st.mu.Lock()
	got := st.items[renderID]
	st.mu.Unlock()
	if got == nil || got.RetentionClass != models.RenderRetentionClassEvidence {
		t.Fatalf("expected retention upgraded, got %#v", got)
	}
	if !got.ExpiresAt.After(now.Add(1 * time.Hour)) {
		t.Fatalf("expected expiry extended, got %v", got.ExpiresAt)
	}
}

func TestProcessRenderJob_StoresInvalidAndBlockedErrors(t *testing.T) {
	t.Parallel()

	st := &fakeRenderStore{}
	srv := NewServer(config.Config{}, st, &fakeArtifactStore{})

	ctx := context.Background()

	invalid := rendering.RenderJobMessage{Kind: "render", NormalizedURL: "not a url", RequestedBy: "inst"}
	if err := srv.processRenderJob(ctx, "req1", invalid); err != nil {
		t.Fatalf("invalid url job: %v", err)
	}

	blocked := rendering.RenderJobMessage{Kind: "render", NormalizedURL: "http://127.0.0.1/", RequestedBy: "inst"}
	if err := srv.processRenderJob(ctx, "req2", blocked); err != nil {
		t.Fatalf("blocked url job: %v", err)
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.items) != 2 {
		t.Fatalf("expected 2 artifacts stored, got %d", len(st.items))
	}
	foundInvalid := false
	foundBlocked := false
	for _, it := range st.items {
		if it == nil {
			continue
		}
		switch strings.TrimSpace(it.ErrorCode) {
		case "invalid_url":
			foundInvalid = true
		case "blocked_ssrf":
			foundBlocked = true
		}
	}
	if !foundInvalid || !foundBlocked {
		t.Fatalf("expected both invalid_url and blocked_ssrf stored, got %#v", st.items)
	}
}

func TestStoreNonHTMLArtifact_StoresNotHTML(t *testing.T) {
	t.Parallel()

	st := &fakeRenderStore{}
	srv := NewServer(config.Config{}, st, &fakeArtifactStore{})

	now := time.Now().UTC()
	finalURL, _ := url.Parse("https://example.com/")
	if err := srv.storeNonHTMLArtifact(context.Background(), "rid", "https://example.com/", finalURL, []string{"https://example.com/"}, models.RenderRetentionClassBenign, now.Add(1*time.Hour), "req", "inst", now); err != nil {
		t.Fatalf("storeNonHTMLArtifact: %v", err)
	}

	st.mu.Lock()
	got := st.items["rid"]
	st.mu.Unlock()
	if got == nil || got.ErrorCode != "not_html" {
		t.Fatalf("expected not_html artifact, got %#v", got)
	}
}
