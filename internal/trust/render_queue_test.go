package trust

import (
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
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

func TestQueueRender_ExistingAndCreateRaceAndEnqueueFailure(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qRender := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.RenderArtifact")).Return(qRender).Maybe()

	qRender.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qRender).Maybe()
	qRender.On("ConsistentRead").Return(qRender).Maybe()
	qRender.On("IfNotExists").Return(qRender).Maybe()
	qRender.On("CreateOrUpdate").Return(nil).Maybe()

	st := store.New(db)
	s := &Server{store: st, queues: &queueClient{previewQueueURL: ""}}

	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}
	normalized := "https://example.com/"
	renderID := rendering.RenderArtifactIDForInstance(rendering.RenderPolicyVersion, "inst", normalized)

	// Existing artifact: returned without enqueue.
	qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.RenderArtifact](t, args, 0)
		*dest = models.RenderArtifact{ID: renderID, PolicyVersion: rendering.RenderPolicyVersion, NormalizedURL: normalized, RequestedBy: "inst", ExpiresAt: time.Now().UTC().Add(1 * time.Hour)}
		_ = dest.UpdateKeys()
	}).Once()
	got, queued, err := s.queueRender(ctx, normalized, models.RenderRetentionClassBenign, 1)
	if err != nil || got == nil || queued {
		t.Fatalf("unexpected existing: got=%#v queued=%v err=%v", got, queued, err)
	}

	// Create race (condition failed): treat as existing.
	qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(theoryErrors.ErrItemNotFound).Once()
	qRender.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.RenderArtifact](t, args, 0)
		*dest = models.RenderArtifact{ID: renderID, PolicyVersion: rendering.RenderPolicyVersion, NormalizedURL: normalized, RequestedBy: "inst"}
		_ = dest.UpdateKeys()
	}).Once()
	got, queued, err = s.queueRender(ctx, normalized, models.RenderRetentionClassBenign, 1)
	if err != nil || got == nil || queued {
		t.Fatalf("unexpected race: got=%#v queued=%v err=%v", got, queued, err)
	}

	// Enqueue failure records error on placeholder.
	qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(theoryErrors.ErrItemNotFound).Once()
	qRender.On("Create").Return(nil).Once()
	got, queued, err = s.queueRender(ctx, normalized, models.RenderRetentionClassBenign, 1)
	if err == nil || got == nil || queued {
		t.Fatalf("expected enqueue failure, got=%#v queued=%v err=%v", got, queued, err)
	}
	if strings.TrimSpace(got.ErrorCode) != "queue_failed" {
		t.Fatalf("expected placeholder error code set, got %#v", got)
	}
}
