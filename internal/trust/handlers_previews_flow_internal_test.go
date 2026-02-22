package trust

import (
	"encoding/json"
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

type previewsFlowTestDB struct {
	db      *ttmocks.MockExtendedDB
	qInst   *ttmocks.MockQuery
	qPrev   *ttmocks.MockQuery
	qRender *ttmocks.MockQuery
	qBudget *ttmocks.MockQuery
	qLedger *ttmocks.MockQuery
	qAudit  *ttmocks.MockQuery
}

func newPreviewsFlowTestDB() previewsFlowTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qPrev := new(ttmocks.MockQuery)
	qRender := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)
	qLedger := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.LinkPreview")).Return(qPrev).Maybe()
	db.On("Model", mock.AnythingOfType("*models.RenderArtifact")).Return(qRender).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UsageLedgerEntry")).Return(qLedger).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInst, qPrev, qRender, qBudget, qLedger, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return previewsFlowTestDB{
		db:      db,
		qInst:   qInst,
		qPrev:   qPrev,
		qRender: qRender,
		qBudget: qBudget,
		qLedger: qLedger,
		qAudit:  qAudit,
	}
}

func TestHandleLinkPreview_CacheHitAndGetPreview(t *testing.T) {
	t.Parallel()

	tdb := newPreviewsFlowTestDB()
	s := &Server{store: store.New(tdb.db)}

	// Instance config: previews enabled, renders disabled.
	hpe := true
	re := false
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst", HostedPreviewsEnabled: &hpe, RendersEnabled: &re}
	}).Once()

	// Cached preview is fresh.
	tdb.qPrev.On("First", mock.AnythingOfType("*models.LinkPreview")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.LinkPreview](t, args, 0)
		*dest = models.LinkPreview{
			ID:            "prev1",
			PolicyVersion: linkPreviewPolicyVersion,
			NormalizedURL: "https://example.com/",
			ResolvedURL:   "https://example.com/",
			Title:         "Example",
			FetchedAt:     time.Now().UTC(),
			ExpiresAt:     time.Now().UTC().Add(1 * time.Hour),
			StoredBy:      "inst",
		}
		_ = dest.UpdateKeys()
	}).Once()

	body, _ := json.Marshal(linkPreviewRequest{URL: "https://example.com"})
	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid", Request: apptheory.Request{Body: body}}
	resp, err := s.handleLinkPreview(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("handleLinkPreview: resp=%#v err=%v", resp, err)
	}

	// Get preview: not found.
	tdb.qPrev.On("First", mock.AnythingOfType("*models.LinkPreview")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, getErr := s.handleGetLinkPreview(&apptheory.Context{AuthIdentity: "inst", Params: map[string]string{"id": "missing"}}); getErr == nil {
		t.Fatalf("expected not found")
	}

	// Get preview: success.
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst", HostedPreviewsEnabled: &hpe, RendersEnabled: &re}
	}).Once()
	tdb.qPrev.On("First", mock.AnythingOfType("*models.LinkPreview")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.LinkPreview](t, args, 0)
		*dest = models.LinkPreview{ID: "ok", PolicyVersion: linkPreviewPolicyVersion, NormalizedURL: "https://example.com/", ExpiresAt: time.Now().UTC().Add(1 * time.Hour), StoredBy: "inst"}
		_ = dest.UpdateKeys()
	}).Once()
	resp, err = s.handleGetLinkPreview(&apptheory.Context{AuthIdentity: "inst", Params: map[string]string{"id": "ok"}})
	if err != nil || resp.Status != 200 {
		t.Fatalf("handleGetLinkPreview: resp=%#v err=%v", resp, err)
	}
}

func TestAttachCachedPreviewRenderAndDebitBudget(t *testing.T) {
	t.Parallel()

	tdb := newPreviewsFlowTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.RenderArtifact](t, args, 0)
		*dest = models.RenderArtifact{
			ID:            "render1",
			PolicyVersion: rendering.RenderPolicyVersion,
			NormalizedURL: "https://example.com/",
			CreatedAt:     time.Now().UTC(),
			ExpiresAt:     time.Now().UTC().Add(24 * time.Hour),
			RequestedBy:   "inst",
		}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qLedger.On("Create").Return(nil).Once()

	resp := linkPreviewResponse{Status: "ok"}
	ok := s.attachCachedPreviewRender(&apptheory.Context{RequestID: "rid"}, "inst", "render1", &resp)
	if !ok || resp.Render == nil {
		t.Fatalf("expected cached render attached, got %#v", resp)
	}

	// Debit preview render budget (allow overage=false).
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{IncludedCredits: 10, UsedCredits: 0}
		_ = dest.UpdateKeys()
	}).Once()
	now := time.Now().UTC()
	if ok := s.debitBudgetForPreviewRender(&apptheory.Context{RequestID: "rid"}, "inst", "block", "render1", now); !ok {
		t.Fatalf("expected debit ok")
	}
}

func TestHandleGetLinkPreview_RejectsCrossInstancePreview(t *testing.T) {
	t.Parallel()

	tdb := newPreviewsFlowTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qPrev.On("First", mock.AnythingOfType("*models.LinkPreview")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.LinkPreview](t, args, 0)
		*dest = models.LinkPreview{
			ID:            "p1",
			PolicyVersion: linkPreviewPolicyVersion,
			NormalizedURL: "https://example.com/",
			StoredBy:      "other",
			ExpiresAt:     time.Now().UTC().Add(1 * time.Hour),
		}
		_ = dest.UpdateKeys()
	}).Once()

	if _, err := s.handleGetLinkPreview(&apptheory.Context{AuthIdentity: "inst", Params: map[string]string{"id": "p1"}}); err == nil {
		t.Fatalf("expected not found for cross-instance preview")
	}
}
