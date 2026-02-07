package trust

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type previewsMoreTestDB struct {
	db      *ttmocks.MockExtendedDB
	qInst   *ttmocks.MockQuery
	qPrev   *ttmocks.MockQuery
	qRender *ttmocks.MockQuery
	qBudget *ttmocks.MockQuery
	qAudit  *ttmocks.MockQuery
}

func newPreviewsMoreTestDB() previewsMoreTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qPrev := new(ttmocks.MockQuery)
	qRender := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.LinkPreview")).Return(qPrev).Maybe()
	db.On("Model", mock.AnythingOfType("*models.RenderArtifact")).Return(qRender).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInst, qPrev, qRender, qBudget, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
	}

	return previewsMoreTestDB{
		db:      db,
		qInst:   qInst,
		qPrev:   qPrev,
		qRender: qRender,
		qBudget: qBudget,
		qAudit:  qAudit,
	}
}

func TestHandleLinkPreview_FetchBlockedSSRF_StoresErrorPreview(t *testing.T) {
	t.Parallel()

	tdb := newPreviewsMoreTestDB()

	hpe := true
	re := false
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "inst", HostedPreviewsEnabled: &hpe, RendersEnabled: &re}
	}).Once()

	// Cache miss: no preview stored yet.
	tdb.qPrev.On("First", mock.AnythingOfType("*models.LinkPreview")).Return(theoryErrors.ErrItemNotFound).Once()

	s := &Server{store: store.New(tdb.db)}

	body, _ := json.Marshal(linkPreviewRequest{URL: "http://127.0.0.1/"})
	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid", Request: apptheory.Request{Body: body}}

	resp, err := s.handleLinkPreview(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("handleLinkPreview: resp=%#v err=%v", resp, err)
	}

	var parsed linkPreviewResponse
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Status != "blocked" || parsed.ErrorCode != "blocked_ssrf" {
		t.Fatalf("unexpected preview response: %#v", parsed)
	}
}

func TestHandleGetLinkPreviewImage_InvalidAndNotFound(t *testing.T) {
	t.Parallel()

	// Invalid image id -> bad_request.
	{
		s := &Server{artifacts: artifacts.New("")}
		if _, err := s.handleGetLinkPreviewImage(&apptheory.Context{Params: map[string]string{"imageId": "bad"}}); err == nil {
			t.Fatalf("expected bad_request")
		}
	}

	// Valid id but missing bucket/object -> not_found.
	{
		imageID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		s := &Server{artifacts: artifacts.New("")}
		if _, err := s.handleGetLinkPreviewImage(&apptheory.Context{Params: map[string]string{"imageId": imageID}}); err == nil {
			t.Fatalf("expected not_found")
		}
	}
}

func TestTryStorePreviewImage_EarlyReturns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Nil artifacts or empty URL.
	if id, key := (*Server)(nil).tryStorePreviewImage(ctx, "http://127.0.0.1/"); id != "" || key != "" {
		t.Fatalf("expected empty for nil server")
	}

	s := &Server{artifacts: artifacts.New("")}
	if id, key := s.tryStorePreviewImage(ctx, " "); id != "" || key != "" {
		t.Fatalf("expected empty for blank url")
	}

	// Invalid URL.
	if id, key := s.tryStorePreviewImage(ctx, "not a url"); id != "" || key != "" {
		t.Fatalf("expected empty for invalid url")
	}

	// Blocked by SSRF checks.
	if id, key := s.tryStorePreviewImage(ctx, "http://127.0.0.1/"); id != "" || key != "" {
		t.Fatalf("expected empty for blocked url")
	}
}

func TestPreviewRenderHelpers_PolicyEligibilityAndQueueReady(t *testing.T) {
	t.Parallel()

	if got := normalizePreviewRenderPolicy(" always "); got != "always" {
		t.Fatalf("unexpected policy: %q", got)
	}
	if got := normalizePreviewRenderPolicy("bad"); got != "suspicious" {
		t.Fatalf("expected default suspicious, got %q", got)
	}

	if previewRenderEligible(nil, &apptheory.Context{}, "x", &linkPreviewResponse{Status: "ok"}) {
		t.Fatalf("expected false for nil server")
	}
	if previewRenderEligible(&Server{}, &apptheory.Context{}, "", &linkPreviewResponse{Status: "ok"}) {
		t.Fatalf("expected false for empty normalized url")
	}
	if previewRenderEligible(&Server{}, &apptheory.Context{}, "x", &linkPreviewResponse{Status: "error"}) {
		t.Fatalf("expected false for non-ok status")
	}

	if previewRenderQueueReady(&Server{}) {
		t.Fatalf("expected false when queues not configured")
	}
	if !previewRenderQueueReady(&Server{cfg: config.Config{PreviewQueueURL: "configured"}, queues: &queueClient{}}) {
		t.Fatalf("expected true for configured queue")
	}
}

func TestAuditPreviewRenderQueuedBestEffort_WritesAudit(t *testing.T) {
	t.Parallel()

	tdb := newPreviewsMoreTestDB()
	tdb.qAudit.On("Create").Return(nil).Once()

	s := &Server{store: store.New(tdb.db)}
	artifact := &models.RenderArtifact{ID: "rid"}

	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}
	now := time.Now().UTC()

	s.auditPreviewRenderQueuedBestEffort(ctx, "inst", artifact, now)
}

func TestMaybeAttachPreviewRender_QueuesOnCreateRace(t *testing.T) {
	t.Parallel()

	tdb := newPreviewsMoreTestDB()
	s := &Server{
		cfg:   config.Config{PreviewQueueURL: "configured"},
		store: store.New(tdb.db),
		queues: &queueClient{
			previewQueueURL: "",
		},
	}

	normalized := "https://8.8.8.8/"

	// attachCachedPreviewRender miss, queueRender miss, then create races and we fetch existing placeholder.
	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(theoryErrors.ErrItemNotFound).Times(2)
	tdb.qRender.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.RenderArtifact)
		*dest = models.RenderArtifact{
			ID:            "rid",
			PolicyVersion: "v1",
			NormalizedURL: normalized,
			CreatedAt:     time.Now().UTC(),
			ExpiresAt:     time.Now().UTC().Add(24 * time.Hour),
		}
	}).Once()

	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.InstanceBudgetMonth)
		*dest = models.InstanceBudgetMonth{IncludedCredits: 10, UsedCredits: 0}
	}).Once()

	resp := &linkPreviewResponse{Status: "ok"}
	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}
	s.maybeAttachPreviewRender(ctx, "inst", "always", "block", normalized, resp)

	if resp.Render == nil || resp.Render.RenderID == "" {
		t.Fatalf("expected render attached, got %#v", resp)
	}
}
