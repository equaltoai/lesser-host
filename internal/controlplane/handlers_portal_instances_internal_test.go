package controlplane

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const testPortalInstanceSlugDemo = "demo"

type portalTestDB struct {
	db        *ttmocks.MockExtendedDB
	qUser     *ttmocks.MockQuery
	qCred     *ttmocks.MockQuery
	qInstance *ttmocks.MockQuery
	qBudget   *ttmocks.MockQuery
	qUsage    *ttmocks.MockQuery
	qDomain   *ttmocks.MockQuery
	qAudit    *ttmocks.MockQuery
	qJob      *ttmocks.MockQuery
	qConsent  *ttmocks.MockQuery

	stubUser *models.User
}

func newPortalTestDB() portalTestDB {
	db := ttmocks.NewMockExtendedDB()
	qUser := new(ttmocks.MockQuery)
	qCred := new(ttmocks.MockQuery)
	qInstance := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)
	qUsage := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	qJob := new(ttmocks.MockQuery)
	qConsent := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.User")).Return(qUser).Maybe()
	db.On("Model", mock.AnythingOfType("*models.WalletCredential")).Return(qCred).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UsageLedgerEntry")).Return(qUsage).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionConsentChallenge")).Return(qConsent).Maybe()

	for _, q := range []*ttmocks.MockQuery{qUser, qCred, qInstance, qBudget, qUsage, qDomain, qAudit, qJob, qConsent} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("WithConditionExpression", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("WithCondition", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	stubUser := &models.User{Username: "alice", Role: models.RoleCustomer, Approved: true}
	qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.User)
		if !ok {
			return
		}
		*dest = *stubUser
		_ = dest.UpdateKeys()
	}).Maybe()

	return portalTestDB{
		db:        db,
		qUser:     qUser,
		qCred:     qCred,
		qInstance: qInstance,
		qBudget:   qBudget,
		qUsage:    qUsage,
		qDomain:   qDomain,
		qAudit:    qAudit,
		qJob:      qJob,
		qConsent:  qConsent,
		stubUser:  stubUser,
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
	if _, err := s.requireInstanceAccess(ctx, testPortalInstanceSlugDemo); err == nil {
		t.Fatalf("expected not_found for missing instance")
	}

	// Operator can access regardless of owner.
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{Slug: testPortalInstanceSlugDemo, Owner: "someone-else"}
	}).Once()
	inst, err := s.requireInstanceAccess(ctx, testPortalInstanceSlugDemo)
	if err != nil || inst == nil || inst.Slug != testPortalInstanceSlugDemo {
		t.Fatalf("unexpected result: inst=%#v err=%v", inst, err)
	}

	// Non-operator owner mismatch => forbidden.
	ctx = &apptheory.Context{AuthIdentity: "alice"}
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{Slug: testPortalInstanceSlugDemo, Owner: "bob"}
	}).Once()
	if _, err := s.requireInstanceAccess(ctx, testPortalInstanceSlugDemo); err == nil {
		t.Fatalf("expected forbidden for owner mismatch")
	}
}

func TestHandlePortalCreateInstance_ReturnsExistingWhenOwned(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}

	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.User)
		if !ok {
			t.Fatalf("expected *models.User, got %T", destAny)
		}
		*dest = models.User{
			Username:       "alice",
			Role:           models.RoleCustomer,
			Approved:       true,
			ApprovalStatus: models.UserApprovalStatusApproved,
			CreatedAt:      time.Now().UTC(),
		}
		_ = dest.UpdateKeys()
	}).Once()

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
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

	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.User)
		if !ok {
			t.Fatalf("expected *models.User, got %T", destAny)
		}
		*dest = models.User{
			Username:       "alice",
			Role:           models.RoleCustomer,
			Approved:       true,
			ApprovalStatus: models.UserApprovalStatusApproved,
			CreatedAt:      time.Now().UTC(),
		}
		_ = dest.UpdateKeys()
	}).Once()

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

func TestHandlePortalCreateInstance_RequiresApproval(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	tdb.stubUser.Approved = false
	tdb.stubUser.ApprovalStatus = models.UserApprovalStatusPending

	s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}

	body, _ := json.Marshal(createInstanceRequest{Slug: "demo"})
	ctx := &apptheory.Context{AuthIdentity: "alice", Request: apptheory.Request{Body: body}}
	if _, err := s.handlePortalCreateInstance(ctx); err == nil {
		t.Fatalf("expected forbidden for unapproved user")
	}
}

func TestParsePortalCreateInstanceSlug_Invalid(t *testing.T) {
	t.Parallel()

	ctxMissing := &apptheory.Context{Request: apptheory.Request{Body: []byte(`{}`)}}
	if _, err := parsePortalCreateInstanceSlug(ctxMissing); err == nil {
		t.Fatalf("expected error for missing slug")
	}

	body, _ := json.Marshal(createInstanceRequest{Slug: "-bad"})
	ctxInvalid := &apptheory.Context{Request: apptheory.Request{Body: body}}
	if _, err := parsePortalCreateInstanceSlug(ctxInvalid); err == nil {
		t.Fatalf("expected error for invalid slug")
	}

	ctxBadJSON := &apptheory.Context{Request: apptheory.Request{Body: []byte(`{not json}`)}}
	if _, err := parsePortalCreateInstanceSlug(ctxBadJSON); err == nil {
		t.Fatalf("expected error for invalid json")
	}
}

func TestHandlePortalListInstances(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*[]*models.Instance)
		if !ok {
			t.Fatalf("expected *[]*models.Instance, got %T", destAny)
		}
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
	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Maybe()

	// requireInstanceAccess -> getInstance
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
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
		Params:       map[string]string{"slug": testPortalInstanceSlugDemo},
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

func TestHandlePortalListInstanceBudgets_Success(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}
	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Maybe()

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qBudget.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.InstanceBudgetMonth](t, args, 0)
		*dest = []*models.InstanceBudgetMonth{
			{InstanceSlug: "demo", Month: "2026-01", IncludedCredits: 100, UsedCredits: 5},
		}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	resp, err := s.handlePortalListInstanceBudgets(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected budgets resp: %#v err=%v", resp, err)
	}
}

func TestHandlePortalGetInstanceBudgetMonth_NotFound(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}
	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Maybe()

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "month": "2026-01"}}
	resp, err := s.handlePortalGetInstanceBudgetMonth(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected get budget resp: %#v err=%v", resp, err)
	}
}

func TestHandlePortalSetInstanceBudgetMonth_SuccessPreservesUsedCredits(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{InstanceSlug: "demo", Month: "2026-01", UsedCredits: 7}
	}).Once()
	tdb.qBudget.On("CreateOrUpdate").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Maybe()

	body, _ := json.Marshal(setBudgetMonthRequest{IncludedCredits: 10})
	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "month": "2026-01"}, Request: apptheory.Request{Body: body}}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	resp, err := s.handlePortalSetInstanceBudgetMonth(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("unexpected set budget resp: %#v err=%v", resp, err)
	}
}

func TestHandlePortalSetInstanceBudgetMonth_RejectsCustomerSelfGrant(t *testing.T) {
	t.Parallel()

	s := &Server{}
	body, _ := json.Marshal(setBudgetMonthRequest{IncludedCredits: 10})
	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "month": "2026-01"}, Request: apptheory.Request{Body: body}}

	if _, err := s.handlePortalSetInstanceBudgetMonth(ctx); err == nil {
		t.Fatalf("expected forbidden for customer budget grant")
	}
}

func TestHandlePortalGetInstanceUsageSummary_ComputesCachedCountsAndBudget(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qUsage.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UsageLedgerEntry](t, args, 0)
		*dest = []*models.UsageLedgerEntry{
			{Cached: true, ListCredits: 10, RequestedCredits: 5, DebitedCredits: 5},
			{Cached: false, ListCredits: 2, RequestedCredits: 2, DebitedCredits: 2},
		}
	}).Once()
	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceBudgetMonth](t, args, 0)
		*dest = models.InstanceBudgetMonth{InstanceSlug: "demo", Month: "2026-01", IncludedCredits: 100, UsedCredits: 9}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "month": "2026-01"}}
	resp, err := s.handlePortalGetInstanceUsageSummary(ctx)
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qUsage.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*[]*models.UsageLedgerEntry)
		if !ok {
			t.Fatalf("expected *[]*models.UsageLedgerEntry, got %T", destAny)
		}
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Domain)
		if !ok {
			t.Fatalf("expected *models.Domain, got %T", destAny)
		}
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
	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Maybe()

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qBudget.On("First", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.InstanceBudgetMonth)
		if !ok {
			t.Fatalf("expected *models.InstanceBudgetMonth, got %T", destAny)
		}
		*dest = models.InstanceBudgetMonth{InstanceSlug: "demo", Month: "2026-01", UsedCredits: 9}
	}).Once()

	body, _ := json.Marshal(setBudgetMonthRequest{IncludedCredits: 3})
	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "month": "2026-01"}, Request: apptheory.Request{Body: body}}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	if _, err := s.handlePortalSetInstanceBudgetMonth(ctx); err == nil {
		t.Fatalf("expected conflict error")
	}
}

func TestHandlePortalAddInstanceDomain_PrimaryConflict(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{cfg: config.Config{}, store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Domain)
		if !ok {
			t.Fatalf("expected *models.Domain, got %T", destAny)
		}
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

func TestHandlePortalInstanceDomainOps_NotFound(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		call func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error)
	}{
		{
			name: "rotate",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handlePortalRotateInstanceDomain(ctx)
			},
		},
		{
			name: "disable",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handlePortalDisableInstanceDomain(ctx)
			},
		},
		{
			name: "delete",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handlePortalDeleteInstanceDomain(ctx)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tdb := newPortalTestDB()
			s := &Server{store: store.New(tdb.db)}

			tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
				destAny := args.Get(0)
				dest, ok := destAny.(*models.Instance)
				if !ok {
					t.Fatalf("expected *models.Instance, got %T", destAny)
				}
				*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
			}).Once()

			tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()

			ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "domain": "example.com"}}
			if _, err := tc.call(s, ctx); err == nil {
				t.Fatalf("expected not found")
			}
		})
	}
}

func TestHandlePortalRotateInstanceDomain_Success(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
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
}

func TestHandlePortalDisableInstanceDomain_Success(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
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
}

func TestHandlePortalDeleteInstanceDomain_Success(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
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
	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Maybe()

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
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
	s := &Server{
		cfg: config.Config{
			ManagedLesserDefaultVersion:             "v1.2.6",
			ManagedProvisionConsentEncryptionKeyHex: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
		},
		store: store.New(tdb.db),
	}

	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.User)
		if !ok {
			t.Fatalf("expected *models.User, got %T", destAny)
		}
		*dest = models.User{
			Username:       "alice",
			Role:           models.RoleCustomer,
			Approved:       true,
			ApprovalStatus: models.UserApprovalStatusApproved,
			CreatedAt:      time.Now().UTC(),
		}
		_ = dest.UpdateKeys()
	}).Twice()

	// Existing queued job branch.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{
			Slug:            "demo",
			Owner:           "alice",
			Status:          models.InstanceStatusActive,
			ProvisionJobID:  "job1",
			ProvisionStatus: models.ProvisionJobStatusQueued,
		}
	}).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.ProvisionJob)
		if !ok {
			t.Fatalf("expected *models.ProvisionJob, got %T", destAny)
		}
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	challengeID := "c1"
	privKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	walletAddr := strings.ToLower(crypto.PubkeyToAddress(privKey.PublicKey).Hex())

	issuedAt := time.Now().UTC()
	expiresAt := issuedAt.Add(5 * time.Minute)
	msg := buildProvisionConsentMessage(testProvisionConsentStageLab, testProvisionConsentBaseDomainDemoGreater, testPortalInstanceSlugDemo, testProvisionConsentNonce16, expiresAt)
	sigBytes, err := crypto.Sign(accounts.TextHash([]byte(msg)), privKey)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	signature := hexutil.Encode(sigBytes)

	tdb.qConsent.On("First", mock.AnythingOfType("*models.ProvisionConsentChallenge")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.ProvisionConsentChallenge)
		if !ok {
			t.Fatalf("expected *models.ProvisionConsentChallenge, got %T", destAny)
		}
		*dest = models.ProvisionConsentChallenge{
			ID:            challengeID,
			Username:      "alice",
			InstanceSlug:  testPortalInstanceSlugDemo,
			Stage:         testProvisionConsentStageLab,
			AdminUsername: testPortalInstanceSlugDemo,
			WalletType:    "ethereum",
			WalletAddr:    walletAddr,
			ChainID:       1,
			Nonce:         "nonce",
			Message:       msg,
			IssuedAt:      issuedAt,
			ExpiresAt:     expiresAt,
		}
		_ = dest.UpdateKeys()
	}).Once()

	body := map[string]any{
		"consent_challenge_id": challengeID,
		"consent_message":      msg,
		"consent_signature":    signature,
	}
	bodyJSON, _ := json.Marshal(body)

	ctx2 := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": testPortalInstanceSlugDemo},
		Request:      apptheory.Request{Body: bodyJSON},
	}
	resp, err = s.handlePortalStartInstanceProvisioning(ctx2)
	if err != nil || resp == nil || resp.Status != 202 {
		t.Fatalf("new job resp=%#v err=%v", resp, err)
	}
}

func TestHandlePortalStartInstanceProvisioning_RequiresApproval(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	tdb.stubUser.Approved = false

	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	if _, err := s.handlePortalStartInstanceProvisioning(ctx); err == nil {
		t.Fatalf("expected forbidden for unapproved user")
	}
}

func TestHandlePortalStartInstanceProvisioning_BlocksReservedWallet(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	issuedAt := time.Now().UTC()
	expiresAt := issuedAt.Add(5 * time.Minute)
	msg := buildProvisionConsentMessage(testProvisionConsentStageLab, testProvisionConsentBaseDomainDemoGreater, testPortalInstanceSlugDemo, testProvisionConsentNonce16, expiresAt)

	tdb.qConsent.On("First", mock.AnythingOfType("*models.ProvisionConsentChallenge")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.ProvisionConsentChallenge)
		if !ok {
			t.Fatalf("expected *models.ProvisionConsentChallenge, got %T", destAny)
		}
		*dest = models.ProvisionConsentChallenge{
			ID:            "c1",
			Username:      "alice",
			InstanceSlug:  testPortalInstanceSlugDemo,
			Stage:         testProvisionConsentStageLab,
			AdminUsername: testPortalInstanceSlugDemo,
			WalletType:    "ethereum",
			WalletAddr:    reservedWalletLesserHostAdmin,
			ChainID:       1,
			Nonce:         "nonce",
			Message:       msg,
			IssuedAt:      issuedAt,
			ExpiresAt:     expiresAt,
		}
		_ = dest.UpdateKeys()
	}).Once()

	body := map[string]any{
		"consent_challenge_id": "c1",
		"consent_message":      msg,
		"consent_signature":    "0xdead",
	}
	bodyJSON, _ := json.Marshal(body)

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": testPortalInstanceSlugDemo},
		Request:      apptheory.Request{Body: bodyJSON},
	}
	if _, err := s.handlePortalStartInstanceProvisioning(ctx); err == nil {
		t.Fatalf("expected bad_request for reserved wallet")
	}
}

func TestHandlePortalStartInstanceProvisioning_FailsOnInvalidSignature(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	walletAddr := "0x00000000000000000000000000000000000000aa"
	issuedAt := time.Now().UTC()
	expiresAt := issuedAt.Add(5 * time.Minute)
	msg := buildProvisionConsentMessage(testProvisionConsentStageLab, testProvisionConsentBaseDomainDemoGreater, testPortalInstanceSlugDemo, testProvisionConsentNonce16, expiresAt)

	tdb.qConsent.On("First", mock.AnythingOfType("*models.ProvisionConsentChallenge")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.ProvisionConsentChallenge)
		if !ok {
			t.Fatalf("expected *models.ProvisionConsentChallenge, got %T", destAny)
		}
		*dest = models.ProvisionConsentChallenge{
			ID:            "c1",
			Username:      "alice",
			InstanceSlug:  testPortalInstanceSlugDemo,
			Stage:         testProvisionConsentStageLab,
			AdminUsername: testPortalInstanceSlugDemo,
			WalletType:    "ethereum",
			WalletAddr:    walletAddr,
			ChainID:       1,
			Nonce:         "nonce",
			Message:       msg,
			IssuedAt:      issuedAt,
			ExpiresAt:     expiresAt,
		}
		_ = dest.UpdateKeys()
	}).Once()

	body := map[string]any{
		"consent_challenge_id": "c1",
		"consent_message":      msg,
		"consent_signature":    "0xdead",
	}
	bodyJSON, _ := json.Marshal(body)

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": testPortalInstanceSlugDemo},
		Request:      apptheory.Request{Body: bodyJSON},
	}
	if _, err := s.handlePortalStartInstanceProvisioning(ctx); err == nil {
		t.Fatalf("expected forbidden for invalid signature")
	}
}

func TestHandlePortalStartInstanceProvisioning_FailsOnExpiredConsentChallenge(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	walletAddr := "0x00000000000000000000000000000000000000aa"
	expiresAt := time.Now().UTC().Add(-5 * time.Minute)
	issuedAt := expiresAt.Add(-5 * time.Minute)
	msg := buildProvisionConsentMessage(testProvisionConsentStageLab, testProvisionConsentBaseDomainDemoGreater, testPortalInstanceSlugDemo, testProvisionConsentNonce16, expiresAt)

	tdb.qConsent.On("First", mock.AnythingOfType("*models.ProvisionConsentChallenge")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.ProvisionConsentChallenge)
		if !ok {
			t.Fatalf("expected *models.ProvisionConsentChallenge, got %T", destAny)
		}
		*dest = models.ProvisionConsentChallenge{
			ID:            "c1",
			Username:      "alice",
			InstanceSlug:  testPortalInstanceSlugDemo,
			Stage:         testProvisionConsentStageLab,
			AdminUsername: testPortalInstanceSlugDemo,
			WalletType:    "ethereum",
			WalletAddr:    walletAddr,
			ChainID:       1,
			Nonce:         "nonce",
			Message:       msg,
			IssuedAt:      issuedAt,
			ExpiresAt:     expiresAt,
		}
		_ = dest.UpdateKeys()
	}).Once()

	body := map[string]any{
		"consent_challenge_id": "c1",
		"consent_message":      msg,
		"consent_signature":    "0xdead",
	}
	bodyJSON, _ := json.Marshal(body)

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": testPortalInstanceSlugDemo},
		Request:      apptheory.Request{Body: bodyJSON},
	}
	if _, err := s.handlePortalStartInstanceProvisioning(ctx); err == nil {
		t.Fatalf("expected bad_request for expired consent challenge")
	}
}

func TestHandlePortalGetInstanceProvisioning(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive, ProvisionJobID: "job1"}
	}).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.ProvisionJob)
		if !ok {
			t.Fatalf("expected *models.ProvisionJob, got %T", destAny)
		}
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()
	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*[]*models.Domain)
		if !ok {
			t.Fatalf("expected *[]*models.Domain, got %T", destAny)
		}
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

func TestHandlePortalInstanceDomainOps_PrimaryConflict(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		call func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error)
	}{
		{
			name: "rotate",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handlePortalRotateInstanceDomain(ctx)
			},
		},
		{
			name: "disable",
			call: func(s *Server, ctx *apptheory.Context) (*apptheory.Response, error) {
				return s.handlePortalDisableInstanceDomain(ctx)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tdb := newPortalTestDB()
			s := &Server{store: store.New(tdb.db)}

			tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
				destAny := args.Get(0)
				dest, ok := destAny.(*models.Instance)
				if !ok {
					t.Fatalf("expected *models.Instance, got %T", destAny)
				}
				*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
			}).Once()
			tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
				destAny := args.Get(0)
				dest, ok := destAny.(*models.Domain)
				if !ok {
					t.Fatalf("expected *models.Domain, got %T", destAny)
				}
				*dest = models.Domain{Domain: "demo.example", InstanceSlug: "demo", Type: models.DomainTypePrimary, Status: models.DomainStatusVerified}
				_ = dest.UpdateKeys()
			}).Once()

			ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo", "domain": "demo.example"}}
			if _, err := tc.call(s, ctx); err == nil {
				t.Fatalf("expected conflict")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// M7 — Instance Overview data prerequisites (Project 42)
// ---------------------------------------------------------------------------

func TestDeriveSoulAnchorState(t *testing.T) {
	t.Parallel()

	t.Run("nil instance returns empty", func(t *testing.T) {
		if got := deriveSoulAnchorState(nil); got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
	})

	t.Run("soul not enabled returns empty", func(t *testing.T) {
		f := false
		inst := &models.Instance{SoulEnabled: &f}
		if got := deriveSoulAnchorState(inst); got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
	})

	t.Run("soul nil returns empty", func(t *testing.T) {
		inst := &models.Instance{SoulEnabled: nil}
		if got := deriveSoulAnchorState(inst); got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
	})

	t.Run("soul enabled but not provisioned returns empty", func(t *testing.T) {
		tru := true
		inst := &models.Instance{SoulEnabled: &tru}
		if got := deriveSoulAnchorState(inst); got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
	})

	t.Run("soul enabled and provisioned returns anchored", func(t *testing.T) {
		tru := true
		inst := &models.Instance{
			SoulEnabled:       &tru,
			SoulProvisionedAt: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		}
		if got := deriveSoulAnchorState(inst); got != "anchored" {
			t.Fatalf("expected anchored, got %q", got)
		}
	})
}

func TestHandlePortalGetInstance_OwnerEnrichment(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Maybe()

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	// Clear the .Maybe() User stub from newPortalTestDB before adding a .Once()
	// override. test-assert mock matches expected calls in order, so our .Once()
	// would never be reached if we left the .Maybe() ahead of it.
	tdb.qUser.ExpectedCalls = nil
	addStandardMockQueryStubs(tdb.qUser)

	// Override with a .Once() returning a richer owner profile.
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{Username: "alice", DisplayName: "Alice Example", Role: models.RoleCustomer}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	resp, err := s.handlePortalGetInstance(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var body instanceResponse
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.OwnerHandle != "Alice Example" {
		t.Fatalf("OwnerHandle = %q, want \"Alice Example\"", body.OwnerHandle)
	}
	if body.OwnerRole != models.RoleCustomer {
		t.Fatalf("OwnerRole = %q, want %q", body.OwnerRole, models.RoleCustomer)
	}
	if body.OwnerAvatarHash != "" {
		t.Fatalf("OwnerAvatarHash = %q, want empty (no avatar storage)", body.OwnerAvatarHash)
	}
}

func TestHandlePortalGetInstance_OwnerEnrichmentFallbackToUsername(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Maybe()

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	// User exists but has no DisplayName — OwnerHandle should fall back to Username.
	tdb.qUser.ExpectedCalls = nil
	addStandardMockQueryStubs(tdb.qUser)
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{Username: "alice", DisplayName: "", Role: models.RoleCustomer}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	resp, err := s.handlePortalGetInstance(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var body instanceResponse
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.OwnerHandle != testUsernameAlice {
		t.Fatalf("OwnerHandle = %q, want %q (username fallback)", body.OwnerHandle, testUsernameAlice)
	}
}

func TestHandlePortalGetInstance_OwnerUserNotFoundGraceful(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Maybe()

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "wallet-abc123", Status: models.InstanceStatusActive}
	}).Once()

	// Override with a .Once() returning not-found.
	tdb.qUser.ExpectedCalls = nil
	addStandardMockQueryStubs(tdb.qUser)
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{AuthIdentity: "wallet-abc123", Params: map[string]string{"slug": "demo"}}
	resp, err := s.handlePortalGetInstance(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var body instanceResponse
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.OwnerHandle != "" {
		t.Fatalf("OwnerHandle = %q, want empty (user not found)", body.OwnerHandle)
	}
	if body.OwnerRole != "" {
		t.Fatalf("OwnerRole = %q, want empty", body.OwnerRole)
	}
}

func TestHandlePortalGetInstance_CrossTenantIsolation(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "bob", Status: models.InstanceStatusActive}
	}).Once()

	// Alice requests Bob's instance — must be forbidden.
	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	_, err := s.handlePortalGetInstance(ctx)
	if err == nil {
		t.Fatalf("expected forbidden error")
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Code != appErrCodeForbidden {
		t.Fatalf("expected %s, got %#v", appErrCodeForbidden, err)
	}
}

func TestHandlePortalGetInstance_DriftFields(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{ID: "lesser-1", InstanceSlug: "demo", Status: models.UpdateJobStatusOK, LesserVersion: "v1.2.7", UpdatedAt: now},
			{ID: "body-1", InstanceSlug: "demo", Status: models.UpdateJobStatusOK, BodyOnly: true, LesserBodyVersion: "v0.3.0", UpdatedAt: now},
		}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	resp, err := s.handlePortalGetInstance(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var body instanceResponse
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if body.LesserDrift != stackDriftOK {
		t.Fatalf("LesserDrift = %q, want %q", body.LesserDrift, stackDriftOK)
	}
	if body.LesserBodyDrift != stackDriftOK {
		t.Fatalf("LesserBodyDrift = %q, want %q", body.LesserBodyDrift, stackDriftOK)
	}
	if body.MCPDrift != stackDriftUnknown {
		t.Fatalf("MCPDrift = %q, want %q (no MCP job)", body.MCPDrift, stackDriftUnknown)
	}
	if body.DriftSummary == "" {
		t.Fatalf("DriftSummary is empty")
	}
}

// TestHandlePortalGetInstance_DriftOkFallback ensures that when a newer
// non-ok (e.g. error) update job exists alongside an older ok job for the
// same component kind, the detail endpoint drift categorisation matches the
// /stack endpoint: it skips the non-ok job and reports the latest ok job's
// drift (ok), rather than picking the newest job regardless of status and
// reporting "unknown".
func TestHandlePortalGetInstance_DriftOkFallback(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	// Newer error job + older ok job for lesser.  ListUpdateJobsByInstance
	// returns newest-first, so the error job comes first.
	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{ID: "lesser-err", InstanceSlug: "demo", Status: models.UpdateJobStatusError, LesserVersion: "v1.2.8", UpdatedAt: now},
			{ID: "lesser-ok", InstanceSlug: "demo", Status: models.UpdateJobStatusOK, LesserVersion: "v1.2.7", UpdatedAt: now.Add(-1 * time.Hour)},
		}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	resp, err := s.handlePortalGetInstance(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var body instanceResponse
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Drift must be "ok" (from the successful job), matching /stack.
	if body.LesserDrift != stackDriftOK {
		t.Fatalf("LesserDrift = %q, want %q (should fall back to older ok job, not newer error job)", body.LesserDrift, stackDriftOK)
	}

	// Managed-update status must reflect the newest job (error).
	if body.LesserUpdateStatus != models.UpdateJobStatusError {
		t.Fatalf("LesserUpdateStatus = %q, want %q (should reflect newest job)", body.LesserUpdateStatus, models.UpdateJobStatusError)
	}

	// Soul anchor absence regression: zero-time must not appear in JSON.
	if body.SoulAnchorAt != nil {
		t.Fatalf("SoulAnchorAt = %v, want nil (zero-time absence regression)", body.SoulAnchorAt)
	}
}

func TestHandlePortalGetInstance_DriftWireStale(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive}
	}).Once()

	// MCP wired against old body version; body has a newer version.
	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{ID: "mcp-1", InstanceSlug: "demo", Status: models.UpdateJobStatusOK, MCPOnly: true, LesserBodyVersion: "v0.2.0", UpdatedAt: now.Add(-1 * time.Hour)},
			{ID: "body-1", InstanceSlug: "demo", Status: models.UpdateJobStatusOK, BodyOnly: true, LesserBodyVersion: "v0.3.0", UpdatedAt: now},
		}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	resp, err := s.handlePortalGetInstance(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var body instanceResponse
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if body.MCPDrift != stackDriftWireStale {
		t.Fatalf("MCPDrift = %q, want %q", body.MCPDrift, stackDriftWireStale)
	}
}

func TestHandlePortalGetInstance_SoulAnchorFields(t *testing.T) {
	t.Parallel()

	t.Run("soul disabled returns empty anchor state", func(t *testing.T) {
		tdb := newPortalTestDB()
		s := &Server{store: store.New(tdb.db)}

		qUpdate := new(ttmocks.MockQuery)
		tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
		addStandardMockQueryStubs(qUpdate)
		qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
			*dest = []*models.UpdateJob{}
		}).Maybe()

		f := false
		tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
			*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive, SoulEnabled: &f}
		}).Once()

		ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
		resp, err := s.handlePortalGetInstance(ctx)
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}

		var body instanceResponse
		if err := json.Unmarshal(resp.Body, &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if body.SoulAnchorState != "" {
			t.Fatalf("SoulAnchorState = %q, want empty", body.SoulAnchorState)
		}
		if body.SoulAnchorAt != nil {
			t.Fatalf("SoulAnchorAt = %v, want nil pointer", body.SoulAnchorAt)
		}
	})

	t.Run("soul enabled and provisioned returns anchored", func(t *testing.T) {
		tdb := newPortalTestDB()
		s := &Server{store: store.New(tdb.db)}

		qUpdate := new(ttmocks.MockQuery)
		tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
		addStandardMockQueryStubs(qUpdate)
		qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
			*dest = []*models.UpdateJob{}
		}).Maybe()

		now := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
		tru := true
		tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
			*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive, SoulEnabled: &tru, SoulProvisionedAt: now}
		}).Once()

		ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
		resp, err := s.handlePortalGetInstance(ctx)
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}

		var body instanceResponse
		if err := json.Unmarshal(resp.Body, &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if body.SoulAnchorState != "anchored" {
			t.Fatalf("SoulAnchorState = %q, want \"anchored\"", body.SoulAnchorState)
		}
		assertSoulAnchorAtEqual(t, body.SoulAnchorAt, now)
	})
}

// TestHandlePortalGetInstance_SoulAnchorFields_JSONAbsence verifies that
// soul_anchor_at is absent from JSON (not "0001-01-01T00:00:00Z") when
// the soul is not provisioned. This guards against Go's time.Time omitempty
// behavior where zero time still serializes.
func TestHandlePortalGetInstance_SoulAnchorFields_JSONAbsence(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	s := &Server{store: store.New(tdb.db)}

	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Maybe()

	f := false
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive, SoulEnabled: &f}
	}).Once()

	ctx := &apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}}
	resp, err := s.handlePortalGetInstance(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	assertJSONFieldAbsent(t, resp.Body, "soul_anchor_at")
}

func TestInstanceResponseDTO_RedactionProof(t *testing.T) {
	t.Parallel()

	resp := instanceResponse{
		Slug:            "demo",
		Owner:           "alice",
		Status:          "active",
		OwnerHandle:     "Alice Example",
		OwnerRole:       "customer",
		SoulAnchorState: "anchored",
		SoulAnchorAt:    soulAnchorTestTime(),
		LesserDrift:     stackDriftOK,
		LesserBodyDrift: stackDriftOK,
		MCPDrift:        stackDriftUnknown,
		DriftSummary:    "partial telemetry",
	}

	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	raw := string(b)

	// Forbidden: internal storage keys.
	for _, forbidden := range []string{
		`"pk"`, `"PK"`,
		`"sk"`, `"SK"`,
		`"ttl"`, `"TTL"`,
		`"gsi1PK"`, `"gsi1pk"`,
		`"gsi1SK"`, `"gsi1sk"`,
		`"gsi2PK"`, `"gsi2pk"`,
		`"gsi2SK"`, `"gsi2sk"`,
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("leaked internal key %q in JSON: %s", forbidden, raw)
		}
	}

	// Forbidden: account identifiers and secrets.
	for _, forbidden := range []string{
		`"account_id"`,
		`"accountId"`,
		`"instance_key_secret"`,
		`"secret_arn"`,
		`"raw_key"`,
		`"raw_secret"`,
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("leaked sensitive field %q in JSON: %s", forbidden, raw)
		}
	}

	// Confirm the new M7 fields are present.
	for _, want := range []string{
		`"owner_handle"`,
		`"owner_role"`,
		`"soul_anchor_state"`,
		`"lesser_drift"`,
		`"lesser_body_drift"`,
		`"mcp_drift"`,
		`"drift_summary"`,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("missing expected field %q in JSON: %s", want, raw)
		}
	}
}

func TestEnrichDerivedDrift_NilResponse(t *testing.T) {
	t.Parallel()
	enrichDerivedDrift(nil, nil, nil, nil)
}

func TestEnrichDerivedDrift_EmptyJobs(t *testing.T) {
	t.Parallel()

	resp := &instanceResponse{}
	enrichDerivedDrift(resp, nil, nil, nil)

	if resp.LesserDrift != stackDriftUnknown {
		t.Fatalf("LesserDrift = %q, want %q", resp.LesserDrift, stackDriftUnknown)
	}
	if resp.LesserBodyDrift != stackDriftUnknown {
		t.Fatalf("LesserBodyDrift = %q, want %q", resp.LesserBodyDrift, stackDriftUnknown)
	}
	if resp.MCPDrift != stackDriftUnknown {
		t.Fatalf("MCPDrift = %q, want %q", resp.MCPDrift, stackDriftUnknown)
	}
	if resp.DriftSummary == "" {
		t.Fatalf("DriftSummary is empty")
	}
}

func TestComputeMCPDriftForDetail_NilMCPJob(t *testing.T) {
	t.Parallel()
	if got := computeMCPDriftForDetail(nil, nil); got != stackDriftUnknown {
		t.Fatalf("expected unknown, got %q", got)
	}
}

func TestComputeMCPDriftForDetail_NonOKMCPJob(t *testing.T) {
	t.Parallel()
	job := &models.UpdateJob{Status: models.UpdateJobStatusRunning}
	if got := computeMCPDriftForDetail(job, nil); got != stackDriftUnknown {
		t.Fatalf("expected unknown, got %q", got)
	}
}

func TestComputeDriftForKind_NilJob(t *testing.T) {
	t.Parallel()
	if got := computeDriftForKind(nil); got != stackDriftUnknown {
		t.Fatalf("expected unknown, got %q", got)
	}
}

func TestComputeDriftForKind_NonOKJob(t *testing.T) {
	t.Parallel()
	job := &models.UpdateJob{Status: models.UpdateJobStatusError}
	if got := computeDriftForKind(job); got != stackDriftUnknown {
		t.Fatalf("expected unknown, got %q", got)
	}
}

func TestEnrichOwnerIdentity_NilArgs(t *testing.T) {
	t.Parallel()
	enrichOwnerIdentity(nil, nil, nil, nil)
	enrichOwnerIdentity(&apptheory.Context{}, nil, &models.Instance{Owner: "alice"}, &instanceResponse{})
	enrichOwnerIdentity(&apptheory.Context{}, &Server{}, nil, &instanceResponse{})
}

// soulAnchorTestTime returns a pointer to a deterministic test timestamp.
// Used by DTO tests that need a non-nil *time.Time for the SoulAnchorAt field.
func soulAnchorTestTime() *time.Time {
	t := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	return &t
}

// assertSoulAnchorAtEqual fails t if at is nil or does not equal want.
func assertSoulAnchorAtEqual(t *testing.T, at *time.Time, want time.Time) {
	t.Helper()
	if at == nil || !at.Equal(want) {
		t.Fatalf("SoulAnchorAt = %v, want %v", at, want)
	}
}

// assertJSONFieldAbsent fails t if raw contains a JSON key for field.
func assertJSONFieldAbsent(t *testing.T, raw []byte, field string) {
	t.Helper()
	needle := "\"" + field + "\""
	if strings.Contains(string(raw), needle) {
		t.Fatalf("JSON contains %s, want absent: %s", needle, string(raw))
	}
}
