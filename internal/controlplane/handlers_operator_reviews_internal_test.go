package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type operatorReviewsTestDB struct {
	db      *ttmocks.MockExtendedDB
	qVReq   *ttmocks.MockQuery
	qDomain *ttmocks.MockQuery
	qReg    *ttmocks.MockQuery
	qInst   *ttmocks.MockQuery
	qAudit  *ttmocks.MockQuery
}

func newOperatorReviewsTestDB() operatorReviewsTestDB {
	db, qs := newTestDBWithModelQueries(
		"*models.VanityDomainRequest",
		"*models.Domain",
		"*models.ExternalInstanceRegistration",
		"*models.Instance",
		"*models.AuditLogEntry",
	)
	return operatorReviewsTestDB{
		db:      db,
		qVReq:   qs[0],
		qDomain: qs[1],
		qReg:    qs[2],
		qInst:   qs[3],
		qAudit:  qs[4],
	}
}

func portalCtx(username string) *apptheory.Context {
	return &apptheory.Context{AuthIdentity: username, RequestID: "rid"}
}

func TestHandleListVanityDomainRequests(t *testing.T) {
	t.Parallel()

	tdb := newOperatorReviewsTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qVReq.On("All", mock.AnythingOfType("*[]*models.VanityDomainRequest")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.VanityDomainRequest](t, args, 0)
		*dest = []*models.VanityDomainRequest{{Domain: "example.com", Status: models.VanityDomainRequestStatusPending}}
	}).Once()

	resp, err := s.handleListVanityDomainRequests(adminCtx())
	if err != nil {
		t.Fatalf("handleListVanityDomainRequests err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
}

func TestApproveAndRejectVanityDomainRequest(t *testing.T) {
	t.Parallel()

	tdb := newOperatorReviewsTestDB()
	s := &Server{store: store.New(tdb.db)}

	now := time.Now().UTC()

	// Approve path.
	tdb.qVReq.On("First", mock.AnythingOfType("*models.VanityDomainRequest")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.VanityDomainRequest](t, args, 0)
		*dest = models.VanityDomainRequest{Domain: "example.com", Status: models.VanityDomainRequestStatusPending, CreatedAt: now}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", Type: models.DomainTypeVanity, Status: models.DomainStatusVerified, InstanceSlug: "inst"}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := adminCtx()
	ctx.Params = map[string]string{"domain": "example.com"}
	ctx.Request.Body = []byte(`{"note":" ok "}`)
	resp, err := s.handleApproveVanityDomainRequest(ctx)
	if err != nil {
		t.Fatalf("handleApproveVanityDomainRequest err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}

	// Reject path.
	tdb.qVReq.On("First", mock.AnythingOfType("*models.VanityDomainRequest")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.VanityDomainRequest](t, args, 0)
		*dest = models.VanityDomainRequest{Domain: "example.net", Status: models.VanityDomainRequestStatusPending, CreatedAt: now}
		_ = dest.UpdateKeys()
	}).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.net", Type: models.DomainTypeVanity, Status: models.DomainStatusVerified, InstanceSlug: "inst"}
		_ = dest.UpdateKeys()
	}).Once()

	ctx2 := adminCtx()
	ctx2.Params = map[string]string{"domain": "example.net"}
	ctx2.Request.Body = []byte(`{"note":" no "}`)
	resp, err = s.handleRejectVanityDomainRequest(ctx2)
	if err != nil {
		t.Fatalf("handleRejectVanityDomainRequest err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
}

func TestPortalExternalInstanceRegistrationLifecycle(t *testing.T) {
	t.Parallel()

	tdb := newOperatorReviewsTestDB()
	s := &Server{store: store.New(tdb.db)}

	// Portal create: instance not found.
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qReg.On("Create").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	createBody, _ := json.Marshal(externalInstanceRegistrationRequest{Slug: "demo", Note: "hello"})
	resp, err := s.handlePortalCreateExternalInstanceRegistration(&apptheory.Context{
		AuthIdentity: "alice",
		RequestID:    "r1",
		Request:      apptheory.Request{Body: createBody},
	})
	if err != nil {
		t.Fatalf("handlePortalCreateExternalInstanceRegistration err: %v", err)
	}
	if resp.Status != 201 {
		t.Fatalf("expected 201, got %d", resp.Status)
	}

	// Portal list.
	tdb.qReg.On("All", mock.AnythingOfType("*[]*models.ExternalInstanceRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ExternalInstanceRegistration](t, args, 0)
		*dest = []*models.ExternalInstanceRegistration{{ID: "x", Username: "alice", Slug: "demo", Status: models.ExternalInstanceRegistrationStatusPending}}
	}).Once()
	resp, err = s.handlePortalListExternalInstanceRegistrations(portalCtx("alice"))
	if err != nil || resp.Status != 200 {
		t.Fatalf("portal list: resp=%#v err=%v", resp, err)
	}

	// Operator list (GSI).
	tdb.qReg.On("All", mock.AnythingOfType("*[]*models.ExternalInstanceRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ExternalInstanceRegistration](t, args, 0)
		*dest = []*models.ExternalInstanceRegistration{{ID: "x", Username: "alice", Slug: "demo", Status: models.ExternalInstanceRegistrationStatusPending}}
	}).Once()
	resp, err = s.handleListExternalInstanceRegistrations(adminCtx())
	if err != nil || resp.Status != 200 {
		t.Fatalf("operator list: resp=%#v err=%v", resp, err)
	}

	// Operator approve.
	tdb.qReg.On("First", mock.AnythingOfType("*models.ExternalInstanceRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ExternalInstanceRegistration](t, args, 0)
		*dest = models.ExternalInstanceRegistration{ID: "x", Username: "alice", Slug: "demo", Status: models.ExternalInstanceRegistrationStatusPending, CreatedAt: time.Now().UTC()}
		_ = dest.UpdateKeys()
	}).Once()
	ctx := adminCtx()
	ctx.Params = map[string]string{"username": "alice", "id": "x"}
	resp, err = s.handleApproveExternalInstanceRegistration(ctx)
	if err != nil || resp.Status != 200 {
		t.Fatalf("approve: resp=%#v err=%v", resp, err)
	}

	// Operator reject.
	tdb.qReg.On("First", mock.AnythingOfType("*models.ExternalInstanceRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ExternalInstanceRegistration](t, args, 0)
		*dest = models.ExternalInstanceRegistration{ID: "y", Username: "alice", Slug: "demo2", Status: models.ExternalInstanceRegistrationStatusPending, CreatedAt: time.Now().UTC()}
		_ = dest.UpdateKeys()
	}).Once()
	ctx2 := adminCtx()
	ctx2.Params = map[string]string{"username": "alice", "id": "y"}
	resp, err = s.handleRejectExternalInstanceRegistration(ctx2)
	if err != nil || resp.Status != 200 {
		t.Fatalf("reject: resp=%#v err=%v", resp, err)
	}
}
