package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type domainsTestDB struct {
	db      *ttmocks.MockExtendedDB
	qInst   *ttmocks.MockQuery
	qDomain *ttmocks.MockQuery
	qAudit  *ttmocks.MockQuery
	qVReq   *ttmocks.MockQuery
}

func newDomainsTestDB() domainsTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	qVReq := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	db.On("Model", mock.AnythingOfType("*models.VanityDomainRequest")).Return(qVReq).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInst, qDomain, qAudit, qVReq} {
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

	return domainsTestDB{db: db, qInst: qInst, qDomain: qDomain, qAudit: qAudit, qVReq: qVReq}
}

func TestHandleListAddAndDeleteInstanceDomain(t *testing.T) {
	t.Parallel()

	tdb := newDomainsTestDB()
	s := &Server{cfg: config.Config{ManagedParentDomain: "lesser.host"}, store: store.New(tdb.db)}

	// Instance exists.
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Status: models.InstanceStatusActive}
		_ = dest.UpdateKeys()
	}).Maybe()

	// List domains.
	tdb.qDomain.On("AllPaginated", mock.AnythingOfType("*[]*models.Domain")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "demo.lesser.host", InstanceSlug: "demo", Type: models.DomainTypePrimary, Status: models.DomainStatusVerified}}
	}).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"slug": "demo"}
	resp, err := s.handleListInstanceDomains(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("list domains: resp=%#v err=%v", resp, err)
	}

	// Add domain: reject primary managed domain.
	addPrimaryBody, _ := json.Marshal(addDomainRequest{Domain: "demo.lesser.host"})
	ctx2 := adminCtx()
	ctx2.Params = map[string]string{"slug": "demo"}
	ctx2.Request.Body = addPrimaryBody
	if _, addErr := s.handleAddInstanceDomain(ctx2); addErr == nil {
		t.Fatalf("expected conflict for primary domain")
	}

	// Add domain success.
	tdb.qDomain.On("Create").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	addBody, _ := json.Marshal(addDomainRequest{Domain: "example.com"})
	ctx3 := adminCtx()
	ctx3.Params = map[string]string{"slug": "demo"}
	ctx3.Request.Body = addBody
	resp, err = s.handleAddInstanceDomain(ctx3)
	if err != nil || resp.Status != 201 {
		t.Fatalf("add domain: resp=%#v err=%v", resp, err)
	}

	// Delete not found.
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
	ctx4 := adminCtx()
	ctx4.Params = map[string]string{"slug": "demo", "domain": "example.com"}
	if _, delErr := s.handleDeleteInstanceDomain(ctx4); delErr == nil {
		t.Fatalf("expected not found")
	}

	// Delete primary conflict.
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "demo.lesser.host", InstanceSlug: "demo", Type: models.DomainTypePrimary}
		_ = dest.UpdateKeys()
	}).Once()
	ctx5 := adminCtx()
	ctx5.Params = map[string]string{"slug": "demo", "domain": "demo.lesser.host"}
	if _, delErr := s.handleDeleteInstanceDomain(ctx5); delErr == nil {
		t.Fatalf("expected conflict for primary domain")
	}

	// Delete success.
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.net", InstanceSlug: "demo", Type: models.DomainTypeVanity, Status: models.DomainStatusPending}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qDomain.On("Delete").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	ctx6 := adminCtx()
	ctx6.Params = map[string]string{"slug": "demo", "domain": "example.net"}
	resp, err = s.handleDeleteInstanceDomain(ctx6)
	if err != nil || resp.Status != 200 {
		t.Fatalf("delete domain: resp=%#v err=%v", resp, err)
	}
}

func TestMarkDomainVerifiedAndCreateVanityRequestBestEffort(t *testing.T) {
	t.Parallel()

	tdb := newDomainsTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qDomain.On("Update", mock.Anything).Return(nil).Once()
	ctx := adminCtx()
	now := time.Now().UTC()
	if err := s.markDomainVerified(ctx, "example.com", "demo", models.DomainTypeVanity, now); err != nil {
		t.Fatalf("markDomainVerified err: %v", err)
	}

	tdb.qVReq.On("CreateOrUpdate").Return(nil).Once()
	s.createVanityDomainRequestBestEffort(&apptheory.Context{AuthIdentity: "admin"}, &models.Domain{
		Domain:       "example.com",
		DomainRaw:    "example.com",
		InstanceSlug: "demo",
		Type:         models.DomainTypeVanity,
	}, now)
}

func TestHandleVerifyInstanceDomain_ReturnsExistingWhenAlreadyVerified(t *testing.T) {
	t.Parallel()

	tdb := newDomainsTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"slug": "demo", "domain": "example.com"}
	resp, err := s.handleVerifyInstanceDomain(ctx)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}
}

func TestHandleVerifyInstanceDomain_ConflictWhenNoVerificationToken(t *testing.T) {
	t.Parallel()

	tdb := newDomainsTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusPending, VerificationToken: " "}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"slug": "demo", "domain": "example.com"}
	if _, err := s.handleVerifyInstanceDomain(ctx); err == nil {
		t.Fatalf("expected conflict")
	} else if appErr, ok := err.(*apptheory.AppTheoryError); !ok || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected app.conflict, got %#v", err)
	}
}

func TestHandleVerifyInstanceDomain_VerifiesDNSAndMarksVerified(t *testing.T) {
	tdb := newDomainsTestDB()
	s := &Server{cfg: config.Config{TipEnabled: false}, store: store.New(tdb.db)}

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:             "example.com",
			DomainRaw:          "Example.COM",
			InstanceSlug:       "demo",
			Type:               models.DomainTypeVanity,
			Status:             models.DomainStatusPending,
			VerificationMethod: domainVerificationMethodDNSTXT,
			VerificationToken:  "tok",
		}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qDomain.On("Update", mock.Anything).Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()
	tdb.qVReq.On("CreateOrUpdate").Return(nil).Once()

	ctx := adminCtx()
	ctx.RequestID = "rid"
	ctx.Params = map[string]string{"slug": "demo", "domain": "example.com"}

	txtName := domainVerificationRecordPrefix + "example.com"
	want := domainVerificationValuePrefix + "tok"

	withDNSTXTResolver(t, txtName, want, func() {
		resp, err := s.handleVerifyInstanceDomain(ctx)
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}

		var out verifyDomainResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if strings.TrimSpace(out.Domain.Status) != models.DomainStatusVerified {
			t.Fatalf("expected verified, got %#v", out.Domain)
		}
		if strings.TrimSpace(out.Domain.VerificationMethod) != domainVerificationMethodDNSTXT {
			t.Fatalf("expected dns_txt method, got %#v", out.Domain)
		}
		if out.Domain.VerifiedAt.IsZero() {
			t.Fatalf("expected VerifiedAt set, got %#v", out.Domain)
		}
	})
}

func TestHandleVerifyInstanceDomain_ReturnsBadRequestWhenVerificationTXTDoesNotMatch(t *testing.T) {
	tdb := newDomainsTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:            "example.com",
			InstanceSlug:      "demo",
			Type:              models.DomainTypeVanity,
			Status:            models.DomainStatusPending,
			VerificationToken: "tok",
		}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"slug": "demo", "domain": "example.com"}

	txtName := domainVerificationRecordPrefix + "example.com"
	withDNSTXTResolver(t, txtName, "wrong", func() {
		if _, err := s.handleVerifyInstanceDomain(ctx); err == nil {
			t.Fatalf("expected bad_request for missing verification record")
		} else if appErr, ok := err.(*apptheory.AppTheoryError); !ok || appErr.Code != appErrCodeBadRequest {
			t.Fatalf("expected app.bad_request, got %#v", err)
		}
	})
}

// TestHandleListInstanceDomains_BoundedPagination verifies the operator domain
// list (issue #1061 part B) applies the clamped Limit, forwards the opaque
// cursor, echoes next_cursor in the response, and never issues a Scan.
func TestHandleListInstanceDomains_BoundedPagination(t *testing.T) {
	t.Parallel()

	tdb := newDomainsTestDB()
	s := &Server{cfg: config.Config{ManagedParentDomain: "lesser.host"}, store: store.New(tdb.db)}
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Status: models.InstanceStatusActive}
		_ = dest.UpdateKeys()
	}).Maybe()

	appliedLimit := 0
	filterMockQueryCalls(tdb.qDomain, "Limit")
	tdb.qDomain.On("Limit", mock.Anything).Return(tdb.qDomain).Run(func(args mock.Arguments) {
		appliedLimit = testutil.RequireMockArg[int](t, args, 0)
	})
	appliedCursor := ""
	tdb.qDomain.On("Cursor", mock.Anything).Return(tdb.qDomain).Run(func(args mock.Arguments) {
		appliedCursor = testutil.RequireMockArg[string](t, args, 0)
	})
	tdb.qDomain.On("AllPaginated", mock.AnythingOfType("*[]*models.Domain")).Return(&core.PaginatedResult{HasMore: true, NextCursor: " cursor-2 "}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "demo.lesser.host", InstanceSlug: "demo", Type: models.DomainTypePrimary, Status: models.DomainStatusVerified}}
	}).Once()

	wantCursor := "dom-tok-1"
	ctx := adminCtx()
	ctx.Params = map[string]string{"slug": "demo"}
	ctx.Request.Query = map[string][]string{"limit": {"2"}, "cursor": {wantCursor}}
	resp, err := s.handleListInstanceDomains(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("list domains: resp=%#v err=%v", resp, err)
	}

	var out listDomainsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if appliedLimit != 2 {
		t.Fatalf("expected query Limit(2), got Limit(%d)", appliedLimit)
	}
	if appliedCursor != wantCursor {
		t.Fatalf("expected cursor %q passed through, got %q", wantCursor, appliedCursor)
	}
	if out.Count != 1 || len(out.Domains) != 1 || out.Limit != 2 || out.NextCursor != "cursor-2" {
		t.Fatalf("unexpected response: %#v", out)
	}
	tdb.qDomain.AssertNotCalled(t, "Scan", mock.Anything)
}

// TestHandleListInstanceDomains_DefaultsLimitAndClamps verifies the domain
// list defaults to 50 and clamps oversized limit params (issue #1061 part B).
func TestHandleListInstanceDomains_DefaultsLimitAndClamps(t *testing.T) {
	t.Parallel()

	tdb := newDomainsTestDB()
	s := &Server{cfg: config.Config{ManagedParentDomain: "lesser.host"}, store: store.New(tdb.db)}
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Status: models.InstanceStatusActive}
		_ = dest.UpdateKeys()
	}).Maybe()

	appliedLimits := []int{}
	filterMockQueryCalls(tdb.qDomain, "Limit")
	tdb.qDomain.On("Limit", mock.Anything).Return(tdb.qDomain).Times(2).Run(func(args mock.Arguments) {
		appliedLimits = append(appliedLimits, testutil.RequireMockArg[int](t, args, 0))
	})
	tdb.qDomain.On("AllPaginated", mock.AnythingOfType("*[]*models.Domain")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = nil
	}).Times(2)

	ctxDefault := adminCtx()
	ctxDefault.Params = map[string]string{"slug": "demo"}
	if _, err := s.handleListInstanceDomains(ctxDefault); err != nil {
		t.Fatalf("default limit: %v", err)
	}
	ctxClamp := adminCtx()
	ctxClamp.Params = map[string]string{"slug": "demo"}
	ctxClamp.Request.Query = map[string][]string{"limit": {"9999"}}
	if _, err := s.handleListInstanceDomains(ctxClamp); err != nil {
		t.Fatalf("clamped limit: %v", err)
	}
	if len(appliedLimits) != 2 || appliedLimits[0] != 50 || appliedLimits[1] != 200 {
		t.Fatalf("expected default 50 and clamp 200, got %v", appliedLimits)
	}
}
