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
