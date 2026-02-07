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

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestNormalizeBudgetMonth(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 2, 6, 0, 0, 0, 0, time.UTC)

	got, err := normalizeBudgetMonth("", now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "2026-02" {
		t.Fatalf("unexpected month: %q", got)
	}

	got, err = normalizeBudgetMonth(" 2026-01 ", now)
	if err != nil || got != "2026-01" {
		t.Fatalf("unexpected result: month=%q err=%v", got, err)
	}

	if _, err := normalizeBudgetMonth("bad", now); err == nil {
		t.Fatalf("expected error")
	}
}

func TestPrepareBudgetDebit_ErrorsAndSuccess(t *testing.T) {
	t.Parallel()

	s := &Server{}
	if _, err := s.prepareBudgetDebit(&apptheory.Context{}); err == nil {
		t.Fatalf("expected error for nil store/db")
	}

	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.Anything).Return(q).Maybe()
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Maybe()

	s = &Server{store: store.New(db)}

	if _, err := s.prepareBudgetDebit(&apptheory.Context{}); err == nil {
		t.Fatalf("expected unauthorized for empty identity")
	}

	{
		body, _ := json.Marshal(budgetDebitRequest{Credits: 0, Month: "2026-01"})
		_, err := s.prepareBudgetDebit(&apptheory.Context{AuthIdentity: "inst", Request: apptheory.Request{Body: body}})
		if err == nil {
			t.Fatalf("expected error for credits <= 0")
		}
	}

	{
		body, _ := json.Marshal(budgetDebitRequest{Credits: 1, Month: "bad"})
		_, err := s.prepareBudgetDebit(&apptheory.Context{AuthIdentity: "inst", Request: apptheory.Request{Body: body}})
		if err == nil {
			t.Fatalf("expected error for invalid month")
		}
	}

	{
		body, _ := json.Marshal(budgetDebitRequest{Credits: 5, Month: "2026-01"})
		prepared, err := s.prepareBudgetDebit(&apptheory.Context{
			AuthIdentity: "inst",
			RequestID:    "rid",
			Request:      apptheory.Request{Body: body},
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if prepared.InstanceSlug != "inst" || prepared.Month != "2026-01" || prepared.Credits != 5 {
			t.Fatalf("unexpected prepared: %#v", prepared)
		}
		if prepared.PK != "INSTANCE#inst" || prepared.SK != "BUDGET#2026-01" || prepared.RequestID != "rid" {
			t.Fatalf("unexpected prepared keys: %#v", prepared)
		}
		if prepared.AllowOverage {
			t.Fatalf("expected default overage policy to block")
		}
	}
}

func TestLoadInstanceBudgetMonth(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.Anything).Return(q).Maybe()
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("ConsistentRead").Return(q).Maybe()

	s := &Server{store: store.New(db)}

	q.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()
	_, ok, err := s.loadInstanceBudgetMonth(context.Background(), "PK", "SK")
	if err != nil || ok {
		t.Fatalf("expected not found -> ok=false err=nil, got ok=%v err=%v", ok, err)
	}

	q.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.InstanceBudgetMonth)
		*dest = models.InstanceBudgetMonth{InstanceSlug: "inst", Month: "2026-01", IncludedCredits: 10, UsedCredits: 3}
	}).Once()
	b, ok, err := s.loadInstanceBudgetMonth(context.Background(), "PK", "SK")
	if err != nil || !ok || b.IncludedCredits != 10 || b.UsedCredits != 3 {
		t.Fatalf("unexpected result: budget=%#v ok=%v err=%v", b, ok, err)
	}
}

func TestTransactBudgetDebit(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	s := &Server{store: store.New(db)}

	now := time.Date(2026, 2, 6, 0, 0, 0, 0, time.UTC)
	update := &models.InstanceBudgetMonth{InstanceSlug: "inst", Month: "2026-02"}
	_ = update.UpdateKeys()

	ledger := &models.UsageLedgerEntry{InstanceSlug: "inst", Month: "2026-02"}
	_ = ledger.UpdateKeys()

	audit := &models.AuditLogEntry{Actor: "inst", Action: "budget.debit", Target: "x"}
	_ = audit.UpdateKeys()

	if err := s.transactBudgetDebit(context.Background(), update, false, 5, now, ledger, audit); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if err := s.transactBudgetDebit(context.Background(), update, true, 5, now, ledger, audit); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestBudgetDebitResponses(t *testing.T) {
	t.Parallel()

	prepared := budgetDebitPrepared{InstanceSlug: "inst", Month: "2026-02", Credits: 5}

	notCfg := budgetDebitNotConfiguredResponse(prepared)
	if notCfg.Allowed || notCfg.DebitedCredits != 0 || notCfg.RequestedCredits != 5 {
		t.Fatalf("unexpected not configured response: %#v", notCfg)
	}

	exceeded := budgetDebitExceededResponse(prepared, models.InstanceBudgetMonth{IncludedCredits: 10, UsedCredits: 9}, 1)
	if exceeded.Allowed || exceeded.IncludedCredits != 10 || exceeded.UsedCredits != 9 {
		t.Fatalf("unexpected exceeded response: %#v", exceeded)
	}
}

func TestHandleBudgetDebit_NotConfiguredAndExceeded(t *testing.T) {
	t.Parallel()

	// Shared db mocks:
	// - loadInstanceTrustConfig -> First(models.Instance) -> not found
	// - loadInstanceBudgetMonth -> First(models.InstanceBudgetMonth) -> configured/not configured
	db := ttmocks.NewMockExtendedDB()
	q := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.Anything).Return(q).Maybe()
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("ConsistentRead").Return(q).Maybe()

	q.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Maybe()

	s := &Server{store: store.New(db)}

	body, _ := json.Marshal(budgetDebitRequest{Credits: 5, Month: "2026-01"})
	ctx := &apptheory.Context{AuthIdentity: "inst", Request: apptheory.Request{Body: body}}

	// Not configured.
	q.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()
	resp, err := s.handleBudgetDebit(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected resp: %#v err=%v", resp, err)
	}

	// Exceeded (block overage): IncludedCredits=3, UsedCredits=0, request 5.
	q.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.InstanceBudgetMonth)
		*dest = models.InstanceBudgetMonth{InstanceSlug: "inst", Month: "2026-01", IncludedCredits: 3, UsedCredits: 0}
	}).Once()
	resp, err = s.handleBudgetDebit(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected resp: %#v err=%v", resp, err)
	}
}
