package trust

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type publishJobsTestDB struct {
	db      *ttmocks.MockExtendedDB
	qInst   *ttmocks.MockQuery
	qLSB    *ttmocks.MockQuery
	qBudget *ttmocks.MockQuery
	qLedger *ttmocks.MockQuery
	qAudit  *ttmocks.MockQuery
}

func newPublishJobsTestDB() publishJobsTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qLSB := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)
	qLedger := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.LinkSafetyBasicResult")).Return(qLSB).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UsageLedgerEntry")).Return(qLedger).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInst, qLSB, qBudget, qLedger, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("All", mock.Anything).Return(nil).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return publishJobsTestDB{
		db:      db,
		qInst:   qInst,
		qLSB:    qLSB,
		qBudget: qBudget,
		qLedger: qLedger,
		qAudit:  qAudit,
	}
}

func TestCanonicalizePublishLinks_DedupAndLimit(t *testing.T) {
	t.Parallel()

	in := []string{
		" ",
		"https://example.com",
		"https://example.com/",
		"http://example.com",
	}
	out := canonicalizePublishLinks(in)
	if len(out) == 0 {
		t.Fatalf("expected non-empty output")
	}
	if len(out) > maxPublishLinks {
		t.Fatalf("expected clamped links, got %d", len(out))
	}
	// Should be trimmed + normalized, no blanks.
	for _, u := range out {
		if strings.TrimSpace(u) == "" {
			t.Fatalf("unexpected blank link in output: %#v", out)
		}
	}
}

func TestHandlePublishJob_NoModulesRequested_ReturnsNoop(t *testing.T) {
	t.Parallel()

	tdb := newPublishJobsTestDB()
	s := NewServer(config.Config{}, store.New(tdb.db))

	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()

	body, _ := json.Marshal(publishJobRequest{
		ActorURI:  "https://actor.example",
		ObjectURI: "https://obj.example",
		Modules:   []publishJobModuleRequest{{Name: " "}}, // skip all modules -> no-op
		Links:     []string{"https://example.com"},
	})
	ctx := &apptheory.Context{
		AuthIdentity: "inst",
		RequestID:    "rid",
		Request:      apptheory.Request{Body: body},
	}

	resp, err := s.handlePublishJob(ctx)
	if err != nil {
		t.Fatalf("handlePublishJob err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	var out publishJobResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.JobID == "" || out.LinksHash == "" {
		t.Fatalf("expected job id + links hash, got %#v", out)
	}
	if len(out.Modules) != 1 {
		t.Fatalf("expected 1 module response, got %#v", out.Modules)
	}
	if out.Modules[0].Status != "ok" || !out.Modules[0].Cached {
		t.Fatalf("unexpected module: %#v", out.Modules[0])
	}
	if out.Modules[0].Budget.Reason != "no modules requested" {
		t.Fatalf("unexpected budget reason: %#v", out.Modules[0].Budget)
	}
}

func TestHandlePublishJob_LinkSafetyBasic_NoLinks(t *testing.T) {
	t.Parallel()

	tdb := newPublishJobsTestDB()
	s := NewServer(config.Config{}, store.New(tdb.db))

	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qLSB.On("First", mock.AnythingOfType("*models.LinkSafetyBasicResult")).Return(theoryErrors.ErrItemNotFound).Once()

	body, _ := json.Marshal(publishJobRequest{
		ActorURI:    "https://actor.example",
		ObjectURI:   "https://obj.example",
		ContentHash: "hash",
		Modules:     []publishJobModuleRequest{{Name: "link_safety_basic"}},
		Links:       nil,
	})
	ctx := &apptheory.Context{
		AuthIdentity: "inst",
		RequestID:    "rid",
		Request:      apptheory.Request{Body: body},
	}

	resp, err := s.handlePublishJob(ctx)
	if err != nil {
		t.Fatalf("handlePublishJob err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	var out publishJobResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Modules) != 1 {
		t.Fatalf("expected 1 module, got %#v", out.Modules)
	}
	if out.Modules[0].Status != "ok" || out.Modules[0].Budget.Reason != "no_links" {
		t.Fatalf("unexpected module: %#v", out.Modules[0])
	}
}

func TestRunLinkSafetyBasicJob_BudgetPaths(t *testing.T) {
	t.Parallel()

	tdb := newPublishJobsTestDB()
	s := NewServer(config.Config{}, store.New(tdb.db))

	ctx := &apptheory.Context{RequestID: "rid"}

	// Budget not configured.
	tdb.qLSB.On("First", mock.AnythingOfType("*models.LinkSafetyBasicResult")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()

	got := s.runLinkSafetyBasicJob(ctx, "inst", strings.Repeat("a", 64), "", "", "", "lh", []string{"https://example.com"}, overagePolicyBlock, 10000)
	if got.Status != statusNotCheckedBudget || got.Budget.Reason != budgetReasonNotConfigured {
		t.Fatalf("unexpected response: %#v", got)
	}

	// Budget exceeded (no overage).
	tdb.qLSB.On("First", mock.AnythingOfType("*models.LinkSafetyBasicResult")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{IncludedCredits: 0, UsedCredits: 0}
		_ = dest.UpdateKeys()
	}).Once()

	got = s.runLinkSafetyBasicJob(ctx, "inst", strings.Repeat("b", 64), "", "", "", "lh", []string{"https://example.com"}, overagePolicyBlock, 10000)
	if got.Status != statusNotCheckedBudget || got.Budget.Reason != budgetReasonExceeded {
		t.Fatalf("unexpected response: %#v", got)
	}
}

func TestRunLinkSafetyBasicJob_DebitedSuccess(t *testing.T) {
	t.Parallel()

	tdb := newPublishJobsTestDB()
	s := NewServer(config.Config{}, store.New(tdb.db))

	ctx := &apptheory.Context{RequestID: "rid"}

	// Cache miss.
	tdb.qLSB.On("First", mock.AnythingOfType("*models.LinkSafetyBasicResult")).Return(theoryErrors.ErrItemNotFound).Once()

	// Budget precheck passes.
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{IncludedCredits: 10, UsedCredits: 0}
		_ = dest.UpdateKeys()
	}).Once()

	// Refresh budget for debited response.
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{IncludedCredits: 10, UsedCredits: 1}
		_ = dest.UpdateKeys()
	}).Once()

	jobID := strings.Repeat("c", 64)
	got := s.runLinkSafetyBasicJob(ctx, "inst", jobID, "", "", "", "lh", []string{"https://example.com"}, overagePolicyBlock, 10000)
	if got.Status != "ok" || got.Budget.Reason == "" || got.Budget.DebitedCredits <= 0 {
		t.Fatalf("unexpected response: %#v", got)
	}
}

func TestRunLinkSafetyBasicJob_CacheHit(t *testing.T) {
	t.Parallel()

	tdb := newPublishJobsTestDB()
	s := NewServer(config.Config{}, store.New(tdb.db))

	// Cache hit.
	tdb.qLSB.On("First", mock.AnythingOfType("*models.LinkSafetyBasicResult")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.LinkSafetyBasicResult](t, args, 0)
		*dest = models.LinkSafetyBasicResult{
			ID:            strings.Repeat("a", 64),
			PolicyVersion: linkSafetyBasicPolicyVersion,
			ActorURI:      "https://actor.example",
			ObjectURI:     "https://obj.example",
			ContentHash:   "hash",
			LinksHash:     "lh",
			CreatedAt:     time.Unix(1, 0).UTC(),
			ExpiresAt:     time.Unix(2, 0).UTC(),
		}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := &apptheory.Context{RequestID: "rid"}
	got := s.runLinkSafetyBasicJob(ctx, "inst", strings.Repeat("a", 64), "actor", "obj", "ch", "lh", []string{"https://example.com"}, overagePolicyBlock, 10000)
	if got.Status != "ok" || !got.Cached || got.Budget.Reason != "cache_hit" {
		t.Fatalf("unexpected response: %#v", got)
	}
}

func TestHandleLinkSafetyBasicConditionFailed_BudgetLookupBranches(t *testing.T) {
	t.Parallel()

	tdb := newPublishJobsTestDB()
	s := NewServer(config.Config{}, store.New(tdb.db))

	now := time.Unix(100, 0).UTC()
	ctx := &apptheory.Context{RequestID: "rid"}

	// No cached result, but budget record exists => "budget conflict" when remaining >= priced.
	tdb.qLSB.On("First", mock.AnythingOfType("*models.LinkSafetyBasicResult")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{IncludedCredits: 10, UsedCredits: 5}
		_ = dest.UpdateKeys()
	}).Once()

	resp := s.handleLinkSafetyBasicConditionFailed(ctx, "inst", "2026-02", "pk", "sk", "job", "actor", "obj", "ch", "lh", 1, 4, 10000, now)
	if resp.Status != statusNotCheckedBudget || resp.Budget.Reason != "budget conflict" {
		t.Fatalf("unexpected resp: %#v", resp)
	}

	// No cached result and no readable budget record => defaults to "budget exceeded".
	tdb.qLSB.On("First", mock.AnythingOfType("*models.LinkSafetyBasicResult")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()

	resp = s.handleLinkSafetyBasicConditionFailed(ctx, "inst", "2026-02", "pk", "sk", "job", "actor", "obj", "ch", "lh", 1, 10, 10000, now)
	if resp.Status != statusNotCheckedBudget || resp.Budget.Reason != budgetReasonExceeded {
		t.Fatalf("unexpected resp: %#v", resp)
	}
}

func TestHandleGetPublishJob_ValidationNotFoundAndSuccess(t *testing.T) {
	t.Parallel()

	tdb := newPublishJobsTestDB()
	s := NewServer(config.Config{}, store.New(tdb.db))

	// Invalid id.
	if _, err := s.handleGetPublishJob(&apptheory.Context{Params: map[string]string{"jobId": "nope"}}); err == nil {
		t.Fatalf("expected error")
	}

	// Not found.
	tdb.qLSB.On("First", mock.AnythingOfType("*models.LinkSafetyBasicResult")).Return(theoryErrors.ErrItemNotFound).Once()
	jobID := strings.Repeat("d", 64)
	if _, err := s.handleGetPublishJob(&apptheory.Context{Params: map[string]string{"jobId": jobID}}); err == nil {
		t.Fatalf("expected not found error")
	}

	// Success.
	tdb.qLSB.On("First", mock.AnythingOfType("*models.LinkSafetyBasicResult")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.LinkSafetyBasicResult](t, args, 0)
		*dest = models.LinkSafetyBasicResult{ID: jobID, PolicyVersion: linkSafetyBasicPolicyVersion, CreatedAt: time.Now().UTC()}
		_ = dest.UpdateKeys()
	}).Once()

	resp, err := s.handleGetPublishJob(&apptheory.Context{Params: map[string]string{"jobId": jobID}})
	if err != nil || resp.Status != 200 {
		t.Fatalf("expected 200, got resp=%#v err=%v", resp, err)
	}
}
