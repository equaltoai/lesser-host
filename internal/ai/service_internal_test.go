package ai

import (
	"context"
	"strings"
	"testing"
	"time"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type aiServiceTestDB struct {
	db      *ttmocks.MockExtendedDB
	qRes    *ttmocks.MockQuery
	qJob    *ttmocks.MockQuery
	qBudget *ttmocks.MockQuery
	qLedger *ttmocks.MockQuery
	qAudit  *ttmocks.MockQuery
}

func newAIServiceTestDB() aiServiceTestDB {
	db := ttmocks.NewMockExtendedDB()
	qRes := new(ttmocks.MockQuery)
	qJob := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)
	qLedger := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AIResult")).Return(qRes).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AIJob")).Return(qJob).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UsageLedgerEntry")).Return(qLedger).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qRes, qJob, qBudget, qLedger, qAudit} {
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

	return aiServiceTestDB{
		db:      db,
		qRes:    qRes,
		qJob:    qJob,
		qBudget: qBudget,
		qLedger: qLedger,
		qAudit:  qAudit,
	}
}

func TestServiceGetOrQueue_Validations(t *testing.T) {
	t.Parallel()

	svc := NewService(nil)
	if _, err := svc.GetOrQueue(context.Background(), Request{}); err == nil {
		t.Fatalf("expected error for nil store")
	}

	tdb := newAIServiceTestDB()
	svc = NewService(store.New(tdb.db))
	if _, err := svc.GetOrQueue(context.Background(), Request{InstanceSlug: " "}); err == nil {
		t.Fatalf("expected error for empty instance slug")
	}
	if _, err := svc.GetOrQueue(context.Background(), Request{InstanceSlug: "inst", Module: " ", PolicyVersion: "v1", ModelSet: "deterministic"}); err == nil {
		t.Fatalf("expected error for empty module")
	}
}

func TestServiceGetOrQueue_CacheHit(t *testing.T) {
	t.Parallel()

	tdb := newAIServiceTestDB()
	svc := NewService(store.New(tdb.db))

	tdb.qRes.On("First", mock.AnythingOfType("*models.AIResult")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.AIResult)
		*dest = models.AIResult{
			ID:        strings.Repeat("a", 64),
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(1 * time.Hour),
		}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qLedger.On("Create").Return(nil).Once()

	resp, err := svc.GetOrQueue(context.Background(), Request{
		InstanceSlug:  "inst",
		RequestID:     "rid",
		Module:        "moderation_text_llm",
		PolicyVersion: "v1",
		ModelSet:      "deterministic",
		Inputs:        map[string]any{"text": "hello"},
		BaseCredits:   10,
	})
	if err != nil {
		t.Fatalf("GetOrQueue err: %v", err)
	}
	if resp.Status != JobStatusOK || !resp.Cached || resp.Result == nil {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if resp.Budget.Reason != "cache_hit" || resp.Budget.DebitedCredits != 0 {
		t.Fatalf("unexpected budget: %#v", resp.Budget)
	}
}

func TestServiceGetOrQueue_AlreadyQueued(t *testing.T) {
	t.Parallel()

	tdb := newAIServiceTestDB()
	svc := NewService(store.New(tdb.db))

	// Cache miss.
	tdb.qRes.On("First", mock.AnythingOfType("*models.AIResult")).Return(theoryErrors.ErrItemNotFound).Once()
	// Job exists.
	tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.AIJob)
		*dest = models.AIJob{ID: strings.Repeat("b", 64)}
		_ = dest.UpdateKeys()
	}).Once()

	resp, err := svc.GetOrQueue(context.Background(), Request{
		InstanceSlug:  "inst",
		RequestID:     "rid",
		Module:        "moderation_text_llm",
		PolicyVersion: "v1",
		ModelSet:      "deterministic",
		Inputs:        map[string]any{"text": "hello"},
		BaseCredits:   10,
	})
	if err != nil {
		t.Fatalf("GetOrQueue err: %v", err)
	}
	if resp.Status != JobStatusQueued || resp.Budget.Reason != "already_queued" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestServiceGetOrQueue_ConcurrencyExceeded(t *testing.T) {
	t.Parallel()

	tdb := newAIServiceTestDB()
	svc := NewService(store.New(tdb.db))

	tdb.qRes.On("First", mock.AnythingOfType("*models.AIResult")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qJob.On("All", mock.AnythingOfType("*[]*models.AIJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.AIJob)
		*dest = []*models.AIJob{{ID: "x"}}
	}).Once()

	resp, err := svc.GetOrQueue(context.Background(), Request{
		InstanceSlug:    "inst",
		RequestID:       "rid",
		Module:          "moderation_text_llm",
		PolicyVersion:   "v1",
		ModelSet:        "deterministic",
		Inputs:          map[string]any{"text": "hello"},
		BaseCredits:     10,
		MaxInflightJobs: 1,
	})
	if err != nil {
		t.Fatalf("GetOrQueue err: %v", err)
	}
	if resp.Status != JobStatusNotCheckedBudget || !strings.Contains(resp.Budget.Reason, "concurrency limit exceeded") {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestServiceGetOrQueue_BudgetNotConfiguredAndExceeded(t *testing.T) {
	t.Parallel()

	tdb := newAIServiceTestDB()
	svc := NewService(store.New(tdb.db))

	// Budget not configured.
	tdb.qRes.On("First", mock.AnythingOfType("*models.AIResult")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()

	resp, err := svc.GetOrQueue(context.Background(), Request{
		InstanceSlug:  "inst",
		RequestID:     "rid",
		Module:        "moderation_text_llm",
		PolicyVersion: "v1",
		ModelSet:      "deterministic",
		Inputs:        map[string]any{"text": "hello"},
		BaseCredits:   10,
	})
	if err != nil {
		t.Fatalf("GetOrQueue err: %v", err)
	}
	if resp.Status != JobStatusNotCheckedBudget || resp.Budget.Reason != "budget not configured" {
		t.Fatalf("unexpected response: %#v", resp)
	}

	// Budget exceeded.
	tdb.qRes.On("First", mock.AnythingOfType("*models.AIResult")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.InstanceBudgetMonth)
		*dest = models.InstanceBudgetMonth{IncludedCredits: 0, UsedCredits: 0}
		_ = dest.UpdateKeys()
	}).Once()

	resp, err = svc.GetOrQueue(context.Background(), Request{
		InstanceSlug:  "inst",
		RequestID:     "rid",
		Module:        "moderation_text_llm",
		PolicyVersion: "v1",
		ModelSet:      "deterministic",
		Inputs:        map[string]any{"text": "hello"},
		BaseCredits:   10,
	})
	if err != nil {
		t.Fatalf("GetOrQueue err: %v", err)
	}
	if resp.Status != JobStatusNotCheckedBudget || resp.Budget.Reason != "budget exceeded" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestServiceGetOrQueue_QueueNoChargeAndDebit(t *testing.T) {
	t.Parallel()

	tdb := newAIServiceTestDB()
	svc := NewService(store.New(tdb.db))

	// No-charge path (BaseCredits=0).
	tdb.qRes.On("First", mock.AnythingOfType("*models.AIResult")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qJob.On("Create").Return(nil).Once()

	resp, err := svc.GetOrQueue(context.Background(), Request{
		InstanceSlug:  "inst",
		RequestID:     "rid",
		Module:        "moderation_text_llm",
		PolicyVersion: "v1",
		ModelSet:      "deterministic",
		Inputs:        map[string]any{"text": "hello"},
		BaseCredits:   0,
	})
	if err != nil {
		t.Fatalf("GetOrQueue err: %v", err)
	}
	if resp.Status != JobStatusQueued || resp.Budget.Reason != "queued_no_charge" {
		t.Fatalf("unexpected response: %#v", resp)
	}

	// Debit path.
	tdb.qRes.On("First", mock.AnythingOfType("*models.AIResult")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.InstanceBudgetMonth)
		*dest = models.InstanceBudgetMonth{IncludedCredits: 100, UsedCredits: 0}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.InstanceBudgetMonth)
		*dest = models.InstanceBudgetMonth{IncludedCredits: 100, UsedCredits: 10}
		_ = dest.UpdateKeys()
	}).Once()

	resp, err = svc.GetOrQueue(context.Background(), Request{
		InstanceSlug:  "inst",
		RequestID:     "rid",
		Module:        "moderation_text_llm",
		PolicyVersion: "v1",
		ModelSet:      "deterministic",
		Inputs:        map[string]any{"text": "hello"},
		BaseCredits:   10,
	})
	if err != nil {
		t.Fatalf("GetOrQueue debit err: %v", err)
	}
	if resp.Status != JobStatusQueued || resp.Budget.DebitedCredits <= 0 {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestServiceHandleDebitConditionFailed_CoversBranches(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	prepared := getOrQueuePrepared{
		InstanceSlug: "inst",
		JobID:        strings.Repeat("c", 64),
		ResultID:     strings.Repeat("c", 64),
		Now:          now,
		Month:        "2026-02",
	}

	t.Run("cache_hit", func(t *testing.T) {
		tdb := newAIServiceTestDB()
		svc := NewService(store.New(tdb.db))

		tdb.qRes.On("First", mock.AnythingOfType("*models.AIResult")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.AIResult)
			*dest = models.AIResult{ID: prepared.ResultID, ExpiresAt: now.Add(1 * time.Hour)}
			_ = dest.UpdateKeys()
		}).Once()

		resp, err := svc.handleDebitConditionFailed(context.Background(), prepared, "PK", "SK", 10)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != JobStatusOK || !resp.Cached || resp.Budget.Reason != "cache_hit" {
			t.Fatalf("unexpected resp: %#v", resp)
		}
	})

	t.Run("already_queued", func(t *testing.T) {
		tdb := newAIServiceTestDB()
		svc := NewService(store.New(tdb.db))

		tdb.qRes.On("First", mock.AnythingOfType("*models.AIResult")).Return(theoryErrors.ErrItemNotFound).Once()
		tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.AIJob)
			*dest = models.AIJob{ID: prepared.JobID}
			_ = dest.UpdateKeys()
		}).Once()

		resp, err := svc.handleDebitConditionFailed(context.Background(), prepared, "PK", "SK", 10)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != JobStatusQueued || resp.Budget.Reason != "already_queued" {
			t.Fatalf("unexpected resp: %#v", resp)
		}
	})

	t.Run("budget_conflict_and_exceeded", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			used   int64
			reason string
		}{
			{name: "conflict", used: 50, reason: "budget conflict"},
			{name: "exceeded", used: 95, reason: "budget exceeded"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tdb := newAIServiceTestDB()
				svc := NewService(store.New(tdb.db))

				tdb.qRes.On("First", mock.AnythingOfType("*models.AIResult")).Return(theoryErrors.ErrItemNotFound).Once()
				tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(theoryErrors.ErrItemNotFound).Once()
				tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
					dest := args.Get(0).(*models.InstanceBudgetMonth)
					*dest = models.InstanceBudgetMonth{IncludedCredits: 100, UsedCredits: tc.used}
					_ = dest.UpdateKeys()
				}).Once()

				resp, err := svc.handleDebitConditionFailed(context.Background(), prepared, "INSTANCE#inst", "BUDGET#2026-02", 10)
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
				if resp.Status != JobStatusNotCheckedBudget || resp.Budget.Allowed || !resp.Budget.OverBudget || resp.Budget.Reason != tc.reason {
					t.Fatalf("unexpected resp: %#v", resp)
				}
				if resp.Budget.IncludedCredits != 100 || resp.Budget.UsedCredits != tc.used || resp.Budget.RequestedCredits != 10 {
					t.Fatalf("unexpected budget: %#v", resp.Budget)
				}
			})
		}
	})

	t.Run("fallback_budget_exceeded_when_refresh_fails", func(t *testing.T) {
		tdb := newAIServiceTestDB()
		svc := NewService(store.New(tdb.db))

		tdb.qRes.On("First", mock.AnythingOfType("*models.AIResult")).Return(theoryErrors.ErrItemNotFound).Once()
		tdb.qJob.On("First", mock.AnythingOfType("*models.AIJob")).Return(theoryErrors.ErrItemNotFound).Once()
		tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()

		resp, err := svc.handleDebitConditionFailed(context.Background(), prepared, "INSTANCE#inst", "BUDGET#2026-02", 10)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != JobStatusNotCheckedBudget || resp.Budget.Allowed || !resp.Budget.OverBudget || resp.Budget.Reason != "budget exceeded" {
			t.Fatalf("unexpected resp: %#v", resp)
		}
		if resp.Budget.IncludedCredits != 0 || resp.Budget.UsedCredits != 0 {
			t.Fatalf("expected unknown budget totals, got %#v", resp.Budget)
		}
	})
}
