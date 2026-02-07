package trust

import (
	"encoding/json"
	"testing"
	"time"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type rendersFlowTestDB struct {
	db      *ttmocks.MockExtendedDB
	qInst   *ttmocks.MockQuery
	qRender *ttmocks.MockQuery
	qBudget *ttmocks.MockQuery
	qLedger *ttmocks.MockQuery
	qAudit  *ttmocks.MockQuery
}

func newRendersFlowTestDB() rendersFlowTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qRender := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)
	qLedger := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.RenderArtifact")).Return(qRender).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UsageLedgerEntry")).Return(qLedger).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInst, qRender, qBudget, qLedger, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return rendersFlowTestDB{
		db:      db,
		qInst:   qInst,
		qRender: qRender,
		qBudget: qBudget,
		qLedger: qLedger,
		qAudit:  qAudit,
	}
}

func TestHandleCreateRender_DisabledAndCacheHit(t *testing.T) {
	t.Parallel()

	tdb := newRendersFlowTestDB()
	s := &Server{store: store.New(tdb.db)}

	// Disabled by instance config.
	re := false
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "inst", RendersEnabled: &re}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid", Request: apptheory.Request{Body: []byte(`{"url":"https://example.com"}`)}}
	resp, err := s.handleCreateRender(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var out renderArtifactResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ErrorCode != "disabled" {
		t.Fatalf("unexpected response: %#v", out)
	}

	// Cache hit path.
	re = true
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "inst", RendersEnabled: &re}
		_ = dest.UpdateKeys()
	}).Once()

	normalized := "https://example.com/"
	renderID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, normalized)
	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.RenderArtifact)
		*dest = models.RenderArtifact{
			ID:             renderID,
			PolicyVersion:  rendering.RenderPolicyVersion,
			NormalizedURL:  normalized,
			RetentionClass: models.RenderRetentionClassBenign,
			CreatedAt:      time.Now().UTC(),
			ExpiresAt:      time.Now().UTC().Add(1 * time.Hour),
		}
		_ = dest.UpdateKeys()
	}).Once()

	ctx = &apptheory.Context{AuthIdentity: "inst", RequestID: "rid", Request: apptheory.Request{Body: []byte(`{"url":"https://example.com"}`)}}
	resp, err = s.handleCreateRender(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
	_ = json.Unmarshal(resp.Body, &out)
	if !out.Cached || out.RenderID == "" {
		t.Fatalf("expected cached render, got %#v", out)
	}
}

func TestHandleCreateRender_QueueAndBudgetErrors(t *testing.T) {
	t.Parallel()

	tdb := newRendersFlowTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{},
	}

	re := true
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "inst", RendersEnabled: &re}
		_ = dest.UpdateKeys()
	}).Maybe()

	// Cache miss always for these cases.
	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(theoryErrors.ErrItemNotFound).Maybe()

	// Queue not configured.
	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid", Request: apptheory.Request{Body: []byte(`{"url":"https://example.com"}`)}}
	resp, err := s.handleCreateRender(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
	var out renderArtifactResponse
	_ = json.Unmarshal(resp.Body, &out)
	if out.ErrorCode != "queue_not_configured" {
		t.Fatalf("unexpected: %#v", out)
	}

	// Budget not configured.
	s.cfg.PreviewQueueURL = "url"
	s.queues = &queueClient{} // configured enough for handler; enqueue will be ignored until queueRender

	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()
	resp, err = s.handleCreateRender(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
	_ = json.Unmarshal(resp.Body, &out)
	if out.ErrorCode != "not_checked_budget" || out.ErrorMessage == "" {
		t.Fatalf("unexpected: %#v", out)
	}

	// Budget exceeded.
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.InstanceBudgetMonth)
		*dest = models.InstanceBudgetMonth{IncludedCredits: 0, UsedCredits: 0}
		_ = dest.UpdateKeys()
	}).Once()
	resp, err = s.handleCreateRender(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
	_ = json.Unmarshal(resp.Body, &out)
	if out.ErrorCode != "not_checked_budget" || out.ErrorMessage != "budget exceeded" {
		t.Fatalf("unexpected: %#v", out)
	}
}

func TestHandleGetRender_AndArtifactsNotFoundPaths(t *testing.T) {
	t.Parallel()

	tdb := newRendersFlowTestDB()
	s := &Server{store: store.New(tdb.db), artifacts: artifacts.New("")}

	// Get render validations.
	if _, err := s.handleGetRender(&apptheory.Context{AuthIdentity: "inst", Params: map[string]string{"renderId": "nope"}}); err == nil {
		t.Fatalf("expected bad_request for invalid id")
	}

	renderID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, "https://example.com/")
	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, err := s.handleGetRender(&apptheory.Context{AuthIdentity: "inst", Params: map[string]string{"renderId": renderID}}); err == nil {
		t.Fatalf("expected not found")
	}

	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.RenderArtifact)
		*dest = models.RenderArtifact{ID: renderID, PolicyVersion: rendering.RenderPolicyVersion, NormalizedURL: "https://example.com/"}
		_ = dest.UpdateKeys()
	}).Once()
	resp, err := s.handleGetRender(&apptheory.Context{AuthIdentity: "inst", Request: apptheory.Request{Headers: map[string][]string{"host": {"example.com"}}}, Params: map[string]string{"renderId": renderID}})
	if err != nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	// Thumbnail missing key path.
	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.RenderArtifact)
		*dest = models.RenderArtifact{ID: renderID, ThumbnailObjectKey: ""}
		_ = dest.UpdateKeys()
	}).Once()
	if _, err := s.handleGetRenderThumbnail(&apptheory.Context{Params: map[string]string{"renderId": renderID}}); err == nil {
		t.Fatalf("expected not found for missing key")
	}

	// Thumbnail GetObject failure returns not found.
	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.RenderArtifact)
		*dest = models.RenderArtifact{ID: renderID, ThumbnailObjectKey: "thumb"}
		_ = dest.UpdateKeys()
	}).Once()
	if _, err := s.handleGetRenderThumbnail(&apptheory.Context{Params: map[string]string{"renderId": renderID}}); err == nil {
		t.Fatalf("expected not found for missing object")
	}

	// Snapshot missing key path.
	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.RenderArtifact)
		*dest = models.RenderArtifact{ID: renderID, SnapshotObjectKey: ""}
		_ = dest.UpdateKeys()
	}).Once()
	if _, err := s.handleGetRenderSnapshot(&apptheory.Context{AuthIdentity: "inst", Params: map[string]string{"renderId": renderID}}); err == nil {
		t.Fatalf("expected not found for missing key")
	}
}

