package trust

import (
	"errors"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/artifacts"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type summariesFlowTestDB struct {
	db      *ttmocks.MockExtendedDB
	qRender *ttmocks.MockQuery
	qAIJob  *ttmocks.MockQuery
	qAIRes  *ttmocks.MockQuery
}

func newSummariesFlowTestDB() summariesFlowTestDB {
	qRender := new(ttmocks.MockQuery)
	qAIJob := new(ttmocks.MockQuery)
	qAIRes := new(ttmocks.MockQuery)

	db := newTestDBWithModelQueries(
		modelQueryPair{model: &models.RenderArtifact{}, query: qRender},
		modelQueryPair{model: &models.AIJob{}, query: qAIJob},
		modelQueryPair{model: &models.AIResult{}, query: qAIRes},
	)

	return summariesFlowTestDB{
		db:      db,
		qRender: qRender,
		qAIJob:  qAIJob,
		qAIRes:  qAIRes,
	}
}

func TestRunLinkRenderSummaryJob_QueuedAndSkipped(t *testing.T) {
	t.Parallel()

	tdb := newSummariesFlowTestDB()

	// The only candidate (risk high) should attempt to load a render artifact and get queued.
	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(theoryErrors.ErrItemNotFound).Once()

	s := &Server{
		store:     store.New(tdb.db),
		artifacts: artifacts.New(""),
	}

	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}
	out := s.runLinkRenderSummaryJob(ctx, "inst", renderPolicySuspicious, instanceTrustConfig{}, 10000, []string{
		"not a url",
		"https://8.8.8.8/",
		"ftp://user:pass@8.8.8.8/",
	})

	if out.Status != statusOK || out.Cached {
		t.Fatalf("unexpected response: %#v", out)
	}

	res, ok := out.Result.(linkRenderSummaryResult)
	if !ok {
		t.Fatalf("expected linkRenderSummaryResult, got %T", out.Result)
	}
	if res.Summary.TotalLinks != 3 || res.Summary.Invalid != 1 || res.Summary.Skipped != 1 || res.Summary.Queued != 1 {
		t.Fatalf("unexpected summary: %#v", res.Summary)
	}
	if out.Budget.Reason != statusQueued {
		t.Fatalf("expected queued reason, got %#v", out.Budget)
	}
}

func TestLoadRenderArtifactForSummary_Statuses(t *testing.T) {
	t.Parallel()

	tdb := newSummariesFlowTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(theoryErrors.ErrItemNotFound).Once()
	if art, status := s.loadRenderArtifactForSummary(&apptheory.Context{}, "rid"); art != nil || status != statusQueued {
		t.Fatalf("expected queued for not found, got art=%#v status=%q", art, status)
	}

	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(errors.New("boom")).Once()
	if art, status := s.loadRenderArtifactForSummary(&apptheory.Context{}, "rid"); art != nil || status != statusError {
		t.Fatalf("expected error for internal failure, got art=%#v status=%q", art, status)
	}

	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.RenderArtifact](t, args, 0)
		*dest = models.RenderArtifact{ID: "rid", ErrorCode: "x"}
	}).Once()
	if art, status := s.loadRenderArtifactForSummary(&apptheory.Context{}, "rid"); art != nil || status != statusError {
		t.Fatalf("expected error for errored artifact, got art=%#v status=%q", art, status)
	}

	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.RenderArtifact](t, args, 0)
		*dest = models.RenderArtifact{ID: "rid", ErrorCode: "", SnapshotObjectKey: ""}
	}).Once()
	if art, status := s.loadRenderArtifactForSummary(&apptheory.Context{}, "rid"); art != nil || status != statusQueued {
		t.Fatalf("expected queued for missing snapshot, got art=%#v status=%q", art, status)
	}

	tdb.qRender.On("First", mock.AnythingOfType("*models.RenderArtifact")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.RenderArtifact](t, args, 0)
		*dest = models.RenderArtifact{ID: "rid", ErrorCode: "", SnapshotObjectKey: "snap"}
	}).Once()
	if art, status := s.loadRenderArtifactForSummary(&apptheory.Context{}, "rid"); art == nil || status != "" {
		t.Fatalf("expected ok artifact, got art=%#v status=%q", art, status)
	}
}

func TestPersistQueuedSummaryResult_SuccessAndFailure(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	ctx := &apptheory.Context{AuthIdentity: "inst", RequestID: "rid"}

	{
		tdb := newSummariesFlowTestDB()

		tdb.qAIRes.On("CreateOrUpdate").Return(nil).Once()

		tdb.qAIJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.AIJob](t, args, 0)
			*dest = models.AIJob{ID: "job1"}
		}).Once()
		tdb.qAIJob.On("CreateOrUpdate").Return(nil).Once()

		tdb.qRender.On("CreateOrUpdate").Return(nil).Once()

		s := &Server{store: store.New(tdb.db)}

		out := &linkRenderSummaryResult{
			Summary: linkRenderSummarySummary{Queued: 1},
			Links:   []linkRenderSummaryLinkResult{{Status: statusQueued}},
		}
		artifact := &models.RenderArtifact{ID: "rid"}
		q := queuedRenderSummary{
			Index:    0,
			JobID:    "job1",
			Inputs:   ai.RenderSummaryInputsV1{NormalizedURL: "https://8.8.8.8/"},
			Artifact: artifact,
		}
		s.persistQueuedSummaryResult(ctx, "inst", now, modelSetDeterministic, models.AIUsage{Provider: modelSetDeterministic}, nil, q, ai.RenderSummaryResultV1{ShortSummary: " ok "}, out)

		if out.Links[0].Status != statusOK || out.Links[0].Summary != "ok" {
			t.Fatalf("expected queued summary applied, got %#v", out.Links[0])
		}
		if out.Summary.Generated != 1 || out.Summary.Queued != 0 {
			t.Fatalf("unexpected summary counts: %#v", out.Summary)
		}
		if artifact.Summary != "ok" || artifact.SummaryPolicyVersion != linkRenderSummaryPolicyVersion {
			t.Fatalf("expected artifact mirrored, got %#v", artifact)
		}
	}

	{
		tdb := newSummariesFlowTestDB()
		tdb.qAIRes.On("CreateOrUpdate").Return(errors.New("boom")).Once()

		s := &Server{store: store.New(tdb.db)}
		out := &linkRenderSummaryResult{
			Summary: linkRenderSummarySummary{Queued: 1},
			Links:   []linkRenderSummaryLinkResult{{Status: statusQueued}},
		}
		q := queuedRenderSummary{
			Index:  0,
			JobID:  "job1",
			Inputs: ai.RenderSummaryInputsV1{NormalizedURL: "https://8.8.8.8/"},
		}

		s.persistQueuedSummaryResult(ctx, "inst", now, modelSetDeterministic, models.AIUsage{}, nil, q, ai.RenderSummaryResultV1{ShortSummary: "ok"}, out)
		if out.Links[0].Status != statusError || out.Summary.Errors != 1 || out.Summary.Queued != 0 {
			t.Fatalf("expected error recorded, got out=%#v", out)
		}
	}
}
