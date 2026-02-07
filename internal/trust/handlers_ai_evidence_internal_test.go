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

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type aiEvidenceTestDB struct {
	db        *ttmocks.MockExtendedDB
	qJob      *ttmocks.MockQuery
	qResult   *ttmocks.MockQuery
	qBudget   *ttmocks.MockQuery
	qInstance *ttmocks.MockQuery
}

func newAIEvidenceTestDB() aiEvidenceTestDB {
	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)
	qResult := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)
	qInstance := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AIJob")).Return(qJob).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AIResult")).Return(qResult).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()

	for _, q := range []*ttmocks.MockQuery{qJob, qResult, qBudget, qInstance} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return aiEvidenceTestDB{
		db:        db,
		qJob:      qJob,
		qResult:   qResult,
		qBudget:   qBudget,
		qInstance: qInstance,
	}
}

func TestHandleAIEvidenceText_BudgetNotConfigured(t *testing.T) {
	t.Parallel()

	tdb := newAIEvidenceTestDB()
	st := store.New(tdb.db)
	s := &Server{
		store: st,
		ai:    ai.NewService(st),
	}

	// loadInstanceTrustConfig falls back to defaults when instance not found.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()
	// No cached result, no job exists.
	tdb.qResult.On("First", mock.AnythingOfType("*models.AIResult")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(theoryErrors.ErrItemNotFound).Once()
	// Concurrency check queries queued jobs by instance.
	tdb.qJob.On("All", mock.AnythingOfType("*[]*models.AIJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.AIJob](t, args, 0)
		*dest = nil
	}).Once()
	// Budget month missing => not_checked_budget response.
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()

	body, _ := json.Marshal(aiEvidenceTextRequest{Text: "hello"})
	resp, err := s.handleAIEvidenceText(&apptheory.Context{
		AuthIdentity: "demo",
		Request:      apptheory.Request{Body: body},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out aiEvidenceResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status != string(ai.JobStatusNotCheckedBudget) || out.Budget.Allowed {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestHandleGetAIJob_ValidatesAndHidesCrossInstance(t *testing.T) {
	t.Parallel()

	tdb := newAIEvidenceTestDB()
	s := &Server{store: store.New(tdb.db)}

	if _, err := s.handleGetAIJob(&apptheory.Context{}); err == nil {
		t.Fatalf("expected unauthorized")
	}

	ctx := &apptheory.Context{AuthIdentity: "inst"}
	ctx.Params = map[string]string{"jobId": "nope"}
	if _, err := s.handleGetAIJob(ctx); err == nil {
		t.Fatalf("expected bad_request")
	}

	jobID := strings.Repeat("a", 64)
	ctx.Params["jobId"] = jobID
	tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, err := s.handleGetAIJob(ctx); err == nil {
		t.Fatalf("expected not_found")
	}

	// Cross-instance job must not be visible.
	tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.AIJob](t, args, 0)
		*dest = models.AIJob{ID: jobID, InstanceSlug: "other"}
	}).Once()
	if _, err := s.handleGetAIJob(ctx); err == nil {
		t.Fatalf("expected not_found for cross-instance")
	}
}

func TestHandleGetAIJob_IncludesResultWhenPresent(t *testing.T) {
	t.Parallel()

	tdb := newAIEvidenceTestDB()
	s := &Server{store: store.New(tdb.db)}

	jobID := strings.Repeat("b", 64)
	ctx := &apptheory.Context{AuthIdentity: "inst", Params: map[string]string{"jobId": jobID}}

	tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.AIJob](t, args, 0)
		*dest = models.AIJob{ID: jobID, InstanceSlug: "inst"}
	}).Once()

	tdb.qResult.On("First", mock.AnythingOfType("*models.AIResult")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.AIResult](t, args, 0)
		*dest = models.AIResult{
			ID:            jobID,
			Module:        aiEvidenceTextModule,
			PolicyVersion: aiEvidenceTextPolicyVersion,
			ModelSet:      aiEvidenceTextModelSet,
			InputsHash:    "h",
			ResultJSON:    `{"ok":true}`,
			CreatedAt:     time.Unix(10, 0).UTC(),
			ExpiresAt:     time.Unix(20, 0).UTC(),
		}
	}).Once()

	resp, err := s.handleGetAIJob(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	var out map[string]any
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := out["result"]; !ok {
		t.Fatalf("expected result key, got %#v", out)
	}
}

func TestPrepareAIEvidenceImage_ArtifactsRequired(t *testing.T) {
	t.Parallel()

	tdb := newAIEvidenceTestDB()
	st := store.New(tdb.db)
	s := &Server{store: st, ai: ai.NewService(st)}

	if _, err := s.prepareAIEvidenceImage(&apptheory.Context{AuthIdentity: "inst", Request: apptheory.Request{Body: []byte(`{"object_key":"k"}`)}}); err == nil {
		t.Fatalf("expected error without artifacts store")
	}
	if _, _, _, err := s.headAndValidateEvidenceImageObject(context.TODO(), "k"); err == nil {
		t.Fatalf("expected error without artifacts store")
	}
}
