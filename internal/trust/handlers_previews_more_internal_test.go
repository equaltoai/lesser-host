package trust

import (
	"context"
	"encoding/json"
	"strings"
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

func TestHandleGetLinkPreviewImage_ServesAndSetsHeaders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	art, cleanup := newTestArtifactsStore(t, "bucket")
	t.Cleanup(cleanup)

	imageID := strings.Repeat("a", 64)
	key := linkPreviewImageObjectKey(imageID)

	// Store without a content-type so handler falls back to DetectContentType.
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00}
	if err := art.PutObject(ctx, key, png, "", ""); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	s := &Server{artifacts: art}
	resp, err := s.handleGetLinkPreviewImage(&apptheory.Context{Params: map[string]string{"imageId": imageID}})
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
	if ct := resp.Headers["content-type"]; len(ct) == 0 || !strings.HasPrefix(ct[0], "image/") {
		t.Fatalf("expected image content-type, got %#v", resp.Headers)
	}
	if cc := resp.Headers["cache-control"]; len(cc) == 0 || !strings.Contains(cc[0], "max-age") {
		t.Fatalf("expected cache-control max-age, got %#v", resp.Headers)
	}
	if et := resp.Headers["etag"]; len(et) == 0 || strings.TrimSpace(et[0]) == "" {
		t.Fatalf("expected etag header, got %#v", resp.Headers)
	}
	if string(resp.Body) != string(png) || !resp.IsBase64 {
		t.Fatalf("unexpected body/base64: resp=%#v", resp)
	}
}

func TestLinkPreviewResponseFromModel_StatusAndImageURL(t *testing.T) {
	t.Parallel()

	imageID := strings.Repeat("b", 64)
	item := &models.LinkPreview{
		ID:            "id",
		PolicyVersion: linkPreviewPolicyVersion,
		NormalizedURL: "https://example.com/",
		ImageID:       imageID,
	}

	ctx := &apptheory.Context{Request: apptheory.Request{Headers: map[string][]string{"host": {"example.com"}}}}
	resp := linkPreviewResponseFromModel(ctx, item, true)
	if resp.Status != "ok" || !resp.Cached || !strings.Contains(resp.ImageURL, "/api/v1/previews/images/") {
		t.Fatalf("unexpected ok response: %#v", resp)
	}

	item.ErrorCode = "blocked_ssrf"
	resp = linkPreviewResponseFromModel(ctx, item, true)
	if resp.Status != "blocked" {
		t.Fatalf("expected blocked, got %#v", resp)
	}

	item.ErrorCode = "disabled"
	resp = linkPreviewResponseFromModel(ctx, item, true)
	if resp.Status != "disabled" {
		t.Fatalf("expected disabled, got %#v", resp)
	}

	item.ErrorCode = "fetch_failed"
	resp = linkPreviewResponseFromModel(ctx, item, true)
	if resp.Status != "error" {
		t.Fatalf("expected error, got %#v", resp)
	}

	resp = linkPreviewResponseFromModel(nil, item, true)
	if !strings.HasPrefix(resp.ImageURL, "/api/v1/previews/images/") {
		t.Fatalf("expected relative image url when base empty, got %#v", resp)
	}
}

func TestRequireLinkPreviewAuth_Errors(t *testing.T) {
	t.Parallel()

	if _, err := requireLinkPreviewAuth(nil, &apptheory.Context{}); err == nil {
		t.Fatalf("expected internal error")
	}
	if _, err := requireLinkPreviewAuth(&Server{}, nil); err == nil {
		t.Fatalf("expected internal error")
	}
	if _, err := requireLinkPreviewAuth(&Server{store: store.New(ttmocks.NewMockExtendedDB())}, &apptheory.Context{}); err == nil {
		t.Fatalf("expected unauthorized")
	}
}
