package trust

import (
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type linkRenderFlowTestDB struct {
	db      *ttmocks.MockExtendedDB
	qRender *ttmocks.MockQuery
	qBudget *ttmocks.MockQuery
	qLedger *ttmocks.MockQuery
}

func newLinkRenderFlowTestDB() linkRenderFlowTestDB {
	qRender := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)
	qLedger := new(ttmocks.MockQuery)

	db := newTestDBWithModelQueries(
		modelQueryPair{model: &models.RenderArtifact{}, query: qRender},
		modelQueryPair{model: &models.InstanceBudgetMonth{}, query: qBudget},
		modelQueryPair{model: &models.UsageLedgerEntry{}, query: qLedger},
	)

	return linkRenderFlowTestDB{
		db:      db,
		qRender: qRender,
		qBudget: qBudget,
		qLedger: qLedger,
	}
}

func TestRunLinkRenderJob_NilDeps_ReturnsError(t *testing.T) {
	t.Parallel()

	var s *Server
	out := s.runLinkRenderJob(nil, "inst", "job", "link_preview_render", "always", "block", 10000, []string{"https://8.8.8.8/"})
	if out.Status != statusError || out.Budget.Reason == "" || !out.Budget.Allowed {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestRunLinkRenderJob_CacheHit_WritesUsageAndReturnsOK(t *testing.T) {
	t.Parallel()

	tdb := newLinkRenderFlowTestDB()
	s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}

	normalized := "https://8.8.8.8/"
	renderID := rendering.RenderArtifactIDForInstance(rendering.RenderPolicyVersion, "inst", normalized)

	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.RenderArtifact](t, args, 0)
		*dest = models.RenderArtifact{
			ID:                 renderID,
			PolicyVersion:      rendering.RenderPolicyVersion,
			NormalizedURL:      normalized,
			ThumbnailObjectKey: "renders/" + renderID + "/thumbnail.jpg",
			RequestedBy:        "inst",
			CreatedAt:          time.Now().UTC(),
			RenderedAt:         time.Now().UTC(),
			ExpiresAt:          time.Now().UTC().Add(24 * time.Hour),
		}
	}).Once()

	tdb.qLedger.On("Create").Return(nil).Once()

	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}
	out := s.runLinkRenderJob(ctx, "inst", "job", "link_preview_render", renderPolicyAlways, overagePolicyBlock, 10000, []string{normalized})
	if out.Status != statusOK || !out.Cached || out.Budget.Reason != budgetReasonCacheHit {
		t.Fatalf("unexpected response: %#v", out)
	}

	result, ok := out.Result.(linkRenderResult)
	if !ok || result.Summary.Candidates != 1 || result.Summary.Cached != 1 || len(result.Links) != 1 {
		t.Fatalf("unexpected result: %#v", out.Result)
	}
	if result.Links[0].Status != statusOK {
		t.Fatalf("unexpected link status: %#v", result.Links[0])
	}
}

func TestRunLinkRenderJob_QueueNotConfigured_ReturnsErrorAndMarksLinks(t *testing.T) {
	t.Parallel()

	tdb := newLinkRenderFlowTestDB()
	s := &Server{cfg: config.Config{PreviewQueueURL: ""}, store: store.New(tdb.db), queues: nil}

	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}
	out := s.runLinkRenderJob(ctx, "inst", "job", "link_preview_render", renderPolicyAlways, overagePolicyBlock, 10000, []string{"https://8.8.8.8/"})
	if out.Status != statusError || out.Cached {
		t.Fatalf("unexpected response: %#v", out)
	}

	result, ok := out.Result.(linkRenderResult)
	if !ok || len(result.Links) != 1 {
		t.Fatalf("unexpected result: %#v", out.Result)
	}
	if result.Links[0].Status != statusError {
		t.Fatalf("expected link status error, got %#v", result.Links[0])
	}
}

func TestRunLinkRenderJob_BudgetNotConfigured_ReturnsNotCheckedBudget(t *testing.T) {
	t.Parallel()

	tdb := newLinkRenderFlowTestDB()
	s := &Server{
		cfg:   config.Config{PreviewQueueURL: "configured"},
		store: store.New(tdb.db),
		queues: &queueClient{
			previewQueueURL: "",
		},
	}

	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}
	out := s.runLinkRenderJob(ctx, "inst", "job", "link_preview_render", renderPolicyAlways, overagePolicyBlock, 10000, []string{"https://8.8.8.8/"})
	if out.Status != statusNotCheckedBudget || out.Budget.Allowed {
		t.Fatalf("unexpected response: %#v", out)
	}

	result, ok := out.Result.(linkRenderResult)
	if !ok || len(result.Links) != 1 {
		t.Fatalf("unexpected result: %#v", out.Result)
	}
	if result.Links[0].Status != statusNotCheckedBudget {
		t.Fatalf("expected not_checked_budget, got %#v", result.Links[0])
	}
}

func TestRunLinkRenderJob_BudgetExceeded_ReturnsNotCheckedBudget(t *testing.T) {
	t.Parallel()

	tdb := newLinkRenderFlowTestDB()
	s := &Server{
		cfg:   config.Config{PreviewQueueURL: "configured"},
		store: store.New(tdb.db),
		queues: &queueClient{
			previewQueueURL: "",
		},
	}

	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{IncludedCredits: 0, UsedCredits: 0}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}
	out := s.runLinkRenderJob(ctx, "inst", "job", "link_preview_render", renderPolicyAlways, overagePolicyBlock, 10000, []string{"https://8.8.8.8/"})
	if out.Status != statusNotCheckedBudget || out.Budget.Allowed {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.Budget.Reason != budgetReasonExceeded || !out.Budget.OverBudget {
		t.Fatalf("expected budget exceeded response, got %#v", out.Budget)
	}
}

func TestRunLinkRenderJob_BudgetDebited_QueueFailuresStillReturnOK(t *testing.T) {
	t.Parallel()

	tdb := newLinkRenderFlowTestDB()
	s := &Server{
		cfg:   config.Config{PreviewQueueURL: "configured"},
		store: store.New(tdb.db),
		queues: &queueClient{
			previewQueueURL: "",
		},
	}

	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(theoryErrors.ErrItemNotFound).Maybe()
	tdb.qRender.On("Create").Return(nil).Once()
	tdb.qRender.On("CreateOrUpdate").Return(nil).Once()

	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{IncludedCredits: 10, UsedCredits: 0}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}
	out := s.runLinkRenderJob(ctx, "inst", "job", "link_preview_render", renderPolicyAlways, overagePolicyBlock, 10000, []string{"https://8.8.8.8/"})
	if out.Status != statusOK || out.Cached {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.Budget.DebitedCredits != linkRenderCreditCost || out.Budget.Reason != budgetReasonDebited {
		t.Fatalf("unexpected budget: %#v", out.Budget)
	}

	result, ok := out.Result.(linkRenderResult)
	if !ok || len(result.Links) != 1 {
		t.Fatalf("unexpected result: %#v", out.Result)
	}
	if result.Links[0].Status != statusError {
		t.Fatalf("expected link status error due to queue failure, got %#v", result.Links[0])
	}
}

func TestRunLinkRenderJob_AllowOverage_ReportsOverageReason(t *testing.T) {
	t.Parallel()

	tdb := newLinkRenderFlowTestDB()
	s := &Server{
		cfg:   config.Config{PreviewQueueURL: "configured"},
		store: store.New(tdb.db),
		queues: &queueClient{
			previewQueueURL: "",
		},
	}

	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(theoryErrors.ErrItemNotFound).Maybe()
	tdb.qRender.On("Create").Return(nil).Once()
	tdb.qRender.On("CreateOrUpdate").Return(nil).Once()

	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{IncludedCredits: 0, UsedCredits: 0}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}
	out := s.runLinkRenderJob(ctx, "inst", "job", "link_preview_render", renderPolicyAlways, overagePolicyAllow, 10000, []string{"https://8.8.8.8/"})
	if out.Status != statusOK {
		t.Fatalf("unexpected response: %#v", out)
	}
	if !out.Budget.OverBudget || out.Budget.Reason != budgetReasonOverage {
		t.Fatalf("expected overage reason, got %#v", out.Budget)
	}
}

func TestMaybeUpgradeRenderArtifactRetention_UpgradesEvidenceRetention(t *testing.T) {
	t.Parallel()

	tdb := newLinkRenderFlowTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qRender.On("CreateOrUpdate").Return(nil).Once()

	now := time.Now().UTC()
	artifact := &models.RenderArtifact{
		ID:             "rid",
		PolicyVersion:  rendering.RenderPolicyVersion,
		RetentionClass: models.RenderRetentionClassBenign,
		CreatedAt:      now.Add(-1 * time.Hour),
		ExpiresAt:      now.Add(1 * time.Hour),
	}
	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}

	s.maybeUpgradeRenderArtifactRetention(ctx, artifact, models.RenderRetentionClassEvidence, now, "inst")

	if artifact.RetentionClass != models.RenderRetentionClassEvidence {
		t.Fatalf("expected retention upgraded, got %q", artifact.RetentionClass)
	}
	if !artifact.ExpiresAt.After(now.Add(1 * time.Hour)) {
		t.Fatalf("expected expiry extended, got %v", artifact.ExpiresAt)
	}
}
