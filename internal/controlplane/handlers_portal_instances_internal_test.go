package controlplane

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

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type portalTestDB struct {
	db        *ttmocks.MockExtendedDB
	qInstance *ttmocks.MockQuery
	qBudget   *ttmocks.MockQuery
	qUsage    *ttmocks.MockQuery
	qDomain   *ttmocks.MockQuery
	qAudit    *ttmocks.MockQuery
	qJob      *ttmocks.MockQuery
}

func newPortalTestDB() portalTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInstance := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)
	qUsage := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	qJob := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UsageLedgerEntry")).Return(qUsage).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInstance, qBudget, qUsage, qDomain, qAudit, qJob} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return portalTestDB{
		db:        db,
		qInstance: qInstance,
		qBudget:   qBudget,
		qUsage:    qUsage,
		qDomain:   qDomain,
		qAudit:    qAudit,
		qJob:      qJob,
	}
}

func TestRequireInstanceAccess(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	ctx := &apptheory.Context{AuthIdentity: "alice"}
	if _, err := s.requireInstanceAccess(ctx, " "); err == nil {
		t.Fatalf("expected error for empty slug")
	}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, err := s.requireInstanceAccess(ctx, "demo"); err == nil {
		t.Fatalf("expected not_found for missing instance")
	}

	// Operator can access regardless of owner.
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "someone-else"}
	}).Once()
	inst, err := s.requireInstanceAccess(ctx, "demo")
	if err != nil || inst == nil || inst.Slug != "demo" {
		t.Fatalf("unexpected result: inst=%#v err=%v", inst, err)
	}

	// Non-operator owner mismatch => forbidden.
	ctx = &apptheory.Context{AuthIdentity: "alice"}
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "bob"}
	}).Once()
	if _, err := s.requireInstanceAccess(ctx, "demo"); err == nil {
		t.Fatalf("expected forbidden for owner mismatch")
	}
}

func TestHandlePortalCreateInstance_ReturnsExistingWhenOwned(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	body, _ := json.Marshal(createInstanceRequest{Slug: "demo"})
	ctx := &apptheory.Context{AuthIdentity: "alice", Request: apptheory.Request{Body: body}}
	resp, err := s.handlePortalCreateInstance(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil || resp.Status != 200 {
		t.Fatalf("expected 200, got %#v", resp)
	}
}

func TestHandlePortalCreateInstance_CreatesNewInstance(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()

	body, _ := json.Marshal(createInstanceRequest{Slug: "demo"})
	ctx := &apptheory.Context{AuthIdentity: "alice", Request: apptheory.Request{Body: body}}
	resp, err := s.handlePortalCreateInstance(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil || resp.Status != 201 {
		t.Fatalf("expected 201, got %#v", resp)
	}
}

func TestHandlePortalListInstances(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.Instance)
		*dest = []*models.Instance{
			{Slug: "a", Owner: "alice", Status: models.InstanceStatusActive},
			{Slug: "b", Owner: "alice", Status: models.InstanceStatusActive},
		}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice"}
	resp, err := s.handlePortalListInstances(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil || resp.Status != 200 {
		t.Fatalf("expected 200, got %#v", resp)
	}
}

func TestHandlePortalUpdateInstanceConfig(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	// requireInstanceAccess -> getInstance
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{
			Slug:              "demo",
			Owner:             "alice",
			Status:            models.InstanceStatusActive,
			LinkSafetyEnabled: func() *bool { v := true; return &v }(),
		}
	}).Once()

	// Update then reload instance.
	tdb.qInstance.On("Update", mock.Anything).Return(nil).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{
			Slug:              "demo",
			Owner:             "alice",
			Status:            models.InstanceStatusActive,
			LinkSafetyEnabled: func() *bool { v := false; return &v }(),
		}
	}).Once()

	tdb.qAudit.On("Create").Return(nil).Maybe()

	disable := false
	body, _ := json.Marshal(updateInstanceConfigRequest{LinkSafetyEnabled: &disable})
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
		Request:      apptheory.Request{Body: body},
	}
	resp, err := s.handlePortalUpdateInstanceConfig(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil || resp.Status != 200 {
		t.Fatalf("expected 200, got %#v", resp)
	}
}

func TestPortalBudgetsAndUsageHandlers(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	// requireInstanceAccess -> getInstance
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Maybe()

	// List budgets.
	tdb.qBudget.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.InstanceBudgetMonth)
		*dest = []*models.InstanceBudgetMonth{
			{InstanceSlug: "demo", Month: "2026-01", IncludedCredits: 100, UsedCredits: 5},
		}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	resp, err := s.handlePortalListInstanceBudgets(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected budgets resp: %#v err=%v", resp, err)
	}

	// Get budget month not found.
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()
	ctx = &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "month": "2026-01"}}
	resp, err = s.handlePortalGetInstanceBudgetMonth(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected get budget resp: %#v err=%v", resp, err)
	}

	// Set budget month success (existing preserves used).
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.InstanceBudgetMonth)
		*dest = models.InstanceBudgetMonth{InstanceSlug: "demo", Month: "2026-01", UsedCredits: 7}
	}).Once()
	tdb.qBudget.On("CreateOrUpdate").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Maybe()

	body, _ := json.Marshal(setBudgetMonthRequest{IncludedCredits: 10})
	ctx = &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "month": "2026-01"}, Request: apptheory.Request{Body: body}}
	resp, err = s.handlePortalSetInstanceBudgetMonth(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected set budget resp: %#v err=%v", resp, err)
	}

	// Usage summary computes cached counts and includes budget (best-effort).
	tdb.qUsage.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.UsageLedgerEntry)
		*dest = []*models.UsageLedgerEntry{
			{Cached: true, ListCredits: 10, RequestedCredits: 5, DebitedCredits: 5},
			{Cached: false, ListCredits: 2, RequestedCredits: 2, DebitedCredits: 2},
		}
	}).Once()
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.InstanceBudgetMonth)
		*dest = models.InstanceBudgetMonth{InstanceSlug: "demo", Month: "2026-01", IncludedCredits: 100, UsedCredits: 9}
	}).Once()

	ctx = &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "month": "2026-01"}}
	resp, err = s.handlePortalGetInstanceUsageSummary(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected usage summary resp: %#v err=%v", resp, err)
	}

	var parsed portalUsageSummaryResponse
	if unmarshalErr := json.Unmarshal(resp.Body, &parsed); unmarshalErr != nil {
		t.Fatalf("unmarshal usage summary: %v", unmarshalErr)
	}
	if parsed.Requests != 2 || parsed.CacheHits != 1 || parsed.CacheMisses != 1 {
		t.Fatalf("unexpected summary counts: %#v", parsed)
	}
	if parsed.IncludedCredits != 100 || parsed.UsedCredits != 9 {
		t.Fatalf("expected budget included, got %#v", parsed)
	}
	if parsed.CacheHitRate <= 0 || parsed.CacheHitRate >= 1 {
		t.Fatalf("unexpected cache hit rate: %v", parsed.CacheHitRate)
	}
}

func TestHandlePortalListInstanceUsage(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	// requireInstanceAccess -> getInstance
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qUsage.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.UsageLedgerEntry)
		*dest = []*models.UsageLedgerEntry{{ID: "e1"}, {ID: "e2"}}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "month": "2026-01"}}
	resp, err := s.handlePortalListInstanceUsage(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil || resp.Status != 200 {
		t.Fatalf("expected 200, got %#v", resp)
	}
}

func TestDomainIsVerifiedOrActive(t *testing.T) {
	t.Parallel()

	if !domainIsVerifiedOrActive(models.DomainStatusVerified) {
		t.Fatalf("expected verified true")
	}
	if !domainIsVerifiedOrActive(models.DomainStatusActive) {
		t.Fatalf("expected active true")
	}
	if domainIsVerifiedOrActive("pending") {
		t.Fatalf("expected pending false")
	}
}

func TestLoadInstanceDomain_NotFoundAndSlugMismatch(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}
	ctx := &apptheory.Context{}

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, err := s.loadInstanceDomain(ctx, "example.com", "demo"); err == nil {
		t.Fatalf("expected not found")
	}

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Domain)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "other"}
	}).Once()
	if _, err := s.loadInstanceDomain(ctx, "example.com", "demo"); err == nil {
		t.Fatalf("expected not found for slug mismatch")
	}
}

func TestHandlePortalGetInstanceBudgetMonth_ValidatesMonth(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "month": "bad"}}
	if _, err := s.handlePortalGetInstanceBudgetMonth(ctx); err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestHandlePortalSetInstanceBudgetMonth_RejectsIncludedLessThanUsed(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.InstanceBudgetMonth)
		*dest = models.InstanceBudgetMonth{InstanceSlug: "demo", Month: "2026-01", UsedCredits: 9}
	}).Once()

	body, _ := json.Marshal(setBudgetMonthRequest{IncludedCredits: 3})
	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "month": "2026-01"}, Request: apptheory.Request{Body: body}}
	if _, err := s.handlePortalSetInstanceBudgetMonth(ctx); err == nil {
		t.Fatalf("expected conflict error")
	}
}

func TestHandlePortalAddInstanceDomain_PrimaryConflict(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	body, _ := json.Marshal(addDomainRequest{Domain: "demo.greater.website"})
	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}, Request: apptheory.Request{Body: body}}
	if _, err := s.handlePortalAddInstanceDomain(ctx); err == nil {
		t.Fatalf("expected conflict for primary domain")
	}
}

func TestHandlePortalAddInstanceDomain_Success(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qDomain.On("IfNotExists").Return(tdb.qDomain).Maybe()
	tdb.qDomain.On("Create").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Maybe()

	body, _ := json.Marshal(addDomainRequest{Domain: "Example.com"})
	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}, Request: apptheory.Request{Body: body}}
	resp, err := s.handlePortalAddInstanceDomain(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil || resp.Status != 201 {
		t.Fatalf("expected 201, got %#v", resp)
	}
}

func TestHandlePortalVerifyInstanceDomain_AlreadyVerifiedReturnsOK(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Domain)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "domain": "example.com"}}
	resp, err := s.handlePortalVerifyInstanceDomain(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp == nil || resp.Status != 200 {
		t.Fatalf("expected 200, got %#v", resp)
	}
}

func TestHandlePortalRotateInstanceDomain_NotFound(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "domain": "example.com"}}
	if _, err := s.handlePortalRotateInstanceDomain(ctx); err == nil {
		t.Fatalf("expected not found")
	}
}

func TestHandlePortalDisableInstanceDomain_NotFound(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "domain": "example.com"}}
	if _, err := s.handlePortalDisableInstanceDomain(ctx); err == nil {
		t.Fatalf("expected not found")
	}
}

func TestHandlePortalRotateAndDisableAndDeleteInstanceDomain_Success(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Maybe()

	t.Run("rotate_success", func(t *testing.T) {
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.Domain)
			*dest = models.Domain{
				Domain:       "example.com",
				DomainRaw:    "Example.COM",
				InstanceSlug: "demo",
				Type:         models.DomainTypeVanity,
				Status:       models.DomainStatusVerified,
			}
			_ = dest.UpdateKeys()
		}).Once()

		tdb.qDomain.On("Update", mock.Anything).Return(nil).Once()
		tdb.qAudit.On("Create").Return(nil).Maybe()

		ctx := &apptheory.Context{
			AuthIdentity: "alice",
			RequestID:    "rid",
			Params:       map[string]string{"slug": "demo", "domain": "example.com"},
		}
		resp, err := s.handlePortalRotateInstanceDomain(ctx)
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}

		var out addDomainResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if strings.TrimSpace(out.Domain.Status) != models.DomainStatusPending || strings.TrimSpace(out.Verification.TXTValue) == "" {
			t.Fatalf("unexpected rotate response: %#v", out)
		}
	})

	t.Run("disable_success", func(t *testing.T) {
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.Domain)
			*dest = models.Domain{
				Domain:       "example.com",
				InstanceSlug: "demo",
				Type:         models.DomainTypeVanity,
				Status:       models.DomainStatusVerified,
			}
			_ = dest.UpdateKeys()
		}).Once()

		tdb.qDomain.On("Update", mock.Anything).Return(nil).Once()
		tdb.qAudit.On("Create").Return(nil).Maybe()

		ctx := &apptheory.Context{
			AuthIdentity: "alice",
			RequestID:    "rid",
			Params:       map[string]string{"slug": "demo", "domain": "example.com"},
		}
		resp, err := s.handlePortalDisableInstanceDomain(ctx)
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}

		var out verifyDomainResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if strings.TrimSpace(out.Domain.Status) != models.DomainStatusDisabled {
			t.Fatalf("expected disabled domain, got %#v", out.Domain)
		}
	})

	t.Run("delete_success", func(t *testing.T) {
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.Domain)
			*dest = models.Domain{
				Domain:       "example.com",
				InstanceSlug: "demo",
				Type:         models.DomainTypeVanity,
				Status:       models.DomainStatusDisabled,
			}
			_ = dest.UpdateKeys()
		}).Once()

		tdb.qAudit.On("Create").Return(nil).Maybe()

		ctx := &apptheory.Context{
			AuthIdentity: "alice",
			RequestID:    "rid",
			Params:       map[string]string{"slug": "demo", "domain": "example.com"},
		}
		resp, err := s.handlePortalDeleteInstanceDomain(ctx)
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}

		var out map[string]any
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if deleted, ok := out["deleted"].(bool); !ok || !deleted {
			t.Fatalf("expected deleted true, got %#v", out)
		}
	})
}

func TestHandlePortalDeleteInstanceDomain_NotFound(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "domain": "example.com"}}
	if _, err := s.handlePortalDeleteInstanceDomain(ctx); err == nil {
		t.Fatalf("expected not found")
	}
}

func TestVerifyDomainTXT_InvalidLookupReturnsBadRequest(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	if err := verifyDomainTXT(canceled, "example.com", "want"); err == nil {
		t.Fatalf("expected error")
	}
}

func TestMaybeReturnExistingPortalInstance_ValidatesNilServer(t *testing.T) {
	t.Parallel()

	if _, _, err := (*Server)(nil).maybeReturnExistingPortalInstance(nil, "demo", "alice"); err == nil {
		t.Fatalf("expected internal error")
	}
}

func TestEffectivePortalInstanceDefaults_Regression(t *testing.T) {
	t.Parallel()

	// Ensure defaults remain stable for portal-created instances.
	inst := &models.Instance{Slug: "demo", Status: models.InstanceStatusActive, CreatedAt: time.Now().UTC()}
	out := instanceResponseFromModel(inst)
	if !out.HostedPreviewsEnabled || !out.LinkSafetyEnabled || !out.RendersEnabled {
		t.Fatalf("expected defaults enabled: %#v", out)
	}
}

func TestHandlePortalGetInstance(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	resp, err := s.handlePortalGetInstance(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
}

func TestPortalProvisioningHandlers_ReturnExistingAndNewJob(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	// Existing queued job branch.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{
			Slug:           "demo",
			Owner:          "alice",
			Status:         models.InstanceStatusActive,
			ProvisionJobID: "job1",
			ProvisionStatus: models.ProvisionJobStatusQueued,
		}
	}).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.ProvisionJob)
		*dest = models.ProvisionJob{ID: "job1", InstanceSlug: "demo", Status: models.ProvisionJobStatusQueued}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	resp, err := s.handlePortalStartInstanceProvisioning(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("existing job resp=%#v err=%v", resp, err)
	}

	// New job branch.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	ctx2 := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	resp, err = s.handlePortalStartInstanceProvisioning(ctx2)
	if err != nil || resp == nil || resp.Status != 202 {
		t.Fatalf("new job resp=%#v err=%v", resp, err)
	}
}

func TestHandlePortalGetInstanceProvisioning(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive, ProvisionJobID: "job1"}
	}).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.ProvisionJob)
		*dest = models.ProvisionJob{ID: "job1", InstanceSlug: "demo", Status: models.ProvisionJobStatusQueued}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	resp, err := s.handlePortalGetInstanceProvisioning(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
}

func TestHandlePortalListInstanceDomains(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()
	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.Domain)
		*dest = []*models.Domain{
			{Domain: "demo.example", InstanceSlug: "demo", Type: models.DomainTypePrimary, Status: models.DomainStatusVerified},
		}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	resp, err := s.handlePortalListInstanceDomains(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
}

func TestHandlePortalRotateInstanceDomain_PrimaryConflict(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Domain)
		*dest = models.Domain{Domain: "demo.example", InstanceSlug: "demo", Type: models.DomainTypePrimary, Status: models.DomainStatusVerified}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "domain": "demo.example"}}
	if _, err := s.handlePortalRotateInstanceDomain(ctx); err == nil {
		t.Fatalf("expected conflict")
	}
}

func TestHandlePortalRotateInstanceDomain_Success(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Domain)
		*dest = models.Domain{Domain: "vanity.example", InstanceSlug: "demo", Type: models.DomainTypeVanity, Status: models.DomainStatusVerified}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid", Params: map[string]string{"slug": "demo", "domain": "vanity.example"}}
	resp, err := s.handlePortalRotateInstanceDomain(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var parsed addDomainResponse
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if strings.TrimSpace(parsed.Domain.Status) != models.DomainStatusPending {
		t.Fatalf("expected pending, got %#v", parsed)
	}
	if strings.TrimSpace(parsed.Verification.TXTName) == "" || !strings.HasPrefix(parsed.Verification.TXTName, domainVerificationRecordPrefix) {
		t.Fatalf("expected txt name with prefix, got %#v", parsed.Verification)
	}
	if strings.TrimSpace(parsed.Verification.TXTValue) == "" || !strings.HasPrefix(parsed.Verification.TXTValue, domainVerificationValuePrefix) {
		t.Fatalf("expected txt value with prefix, got %#v", parsed.Verification)
	}
}

func TestHandlePortalDisableInstanceDomain_PrimaryConflict(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Domain)
		*dest = models.Domain{Domain: "demo.example", InstanceSlug: "demo", Type: models.DomainTypePrimary, Status: models.DomainStatusVerified}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "domain": "demo.example"}}
	if _, err := s.handlePortalDisableInstanceDomain(ctx); err == nil {
		t.Fatalf("expected conflict")
	}
}

func TestHandlePortalDisableInstanceDomain_Success(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Domain)
		*dest = models.Domain{Domain: "vanity.example", InstanceSlug: "demo", Type: models.DomainTypeVanity, Status: models.DomainStatusActive}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid", Params: map[string]string{"slug": "demo", "domain": "vanity.example"}}
	resp, err := s.handlePortalDisableInstanceDomain(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var parsed verifyDomainResponse
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if strings.TrimSpace(parsed.Domain.Status) != models.DomainStatusDisabled {
		t.Fatalf("expected disabled, got %#v", parsed)
	}
}
