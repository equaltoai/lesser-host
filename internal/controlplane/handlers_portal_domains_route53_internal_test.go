package controlplane

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestRoute53Helpers_NormalizeAndQuoteAndZonePicking(t *testing.T) {
	t.Parallel()

	if got := normalizeRoute53ZoneName(" Example.COM. "); got != "example.com" {
		t.Fatalf("unexpected normalize: %q", got)
	}

	if domainInZone("a.b.example.com", "example.com") != true {
		t.Fatalf("expected in-zone")
	}
	if domainInZone("example.com", "example.com") != true {
		t.Fatalf("expected equal in-zone")
	}
	if domainInZone("evil.com", "example.com") != false {
		t.Fatalf("expected out-of-zone")
	}
	if domainInZone("", "example.com") != false {
		t.Fatalf("expected false for empty domain")
	}

	if got := ensureRoute53FQDN("a.b"); got != "a.b." {
		t.Fatalf("unexpected fqdn: %q", got)
	}
	if got := ensureRoute53FQDN("a.b."); got != "a.b." {
		t.Fatalf("unexpected fqdn passthrough: %q", got)
	}

	if got := quoteTXTValue(`lesser-host-verification="x"`); got != `"lesser-host-verification=\"x\""` {
		t.Fatalf("unexpected quote: %q", got)
	}

	bestID, bestLen := pickBestHostedZoneID("demo.sub.example.com", []r53types.HostedZone{
		{Id: aws.String("/hostedzone/ZPRIVATE"), Name: aws.String("example.com."), Config: &r53types.HostedZoneConfig{PrivateZone: true}},
		{Id: aws.String("/hostedzone/Z1"), Name: aws.String("example.com."), Config: &r53types.HostedZoneConfig{PrivateZone: false}},
		{Id: aws.String("/hostedzone/Z2"), Name: aws.String("sub.example.com."), Config: &r53types.HostedZoneConfig{PrivateZone: false}},
	}, "", -1)
	if bestLen <= 0 || bestID != "/hostedzone/Z2" {
		t.Fatalf("unexpected best zone: id=%q len=%d", bestID, bestLen)
	}
}

func TestFindRoute53HostedZoneID_RequiresClient(t *testing.T) {
	t.Parallel()

	s := &Server{}
	if _, err := s.findRoute53HostedZoneID(&apptheory.Context{}, "example.com"); err == nil {
		t.Fatalf("expected error")
	}
}

type route53AssistTestDB struct {
	db      *ttmocks.MockExtendedDB
	qInst   *ttmocks.MockQuery
	qDomain *ttmocks.MockQuery
}

func newRoute53AssistTestDB() route53AssistTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInst, qDomain} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
	}

	return route53AssistTestDB{
		db:      db,
		qInst:   qInst,
		qDomain: qDomain,
	}
}

func TestHandlePortalUpsertDomainVerificationRoute53_ForbiddenWhenNotOwner(t *testing.T) {
	t.Parallel()

	tdb := newRoute53AssistTestDB()
	s := &Server{store: store.New(tdb.db)} // Route53 client intentionally nil.

	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "bob"}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		RequestID:    "rid",
		Params:       map[string]string{"slug": "demo", "domain": "example.com"},
	}
	if _, err := s.handlePortalUpsertDomainVerificationRoute53(ctx); err == nil {
		t.Fatalf("expected forbidden")
	} else if appErr, ok := err.(*apptheory.AppError); !ok || appErr.Code != "app.forbidden" {
		t.Fatalf("expected app.forbidden, got %#v", err)
	}
}

func TestHandlePortalUpsertDomainVerificationRoute53_NotFoundWhenDomainMissing(t *testing.T) {
	t.Parallel()

	tdb := newRoute53AssistTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice"}
		_ = dest.UpdateKeys()
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		RequestID:    "rid",
		Params:       map[string]string{"slug": "demo", "domain": "example.com"},
	}
	if _, err := s.handlePortalUpsertDomainVerificationRoute53(ctx); err == nil {
		t.Fatalf("expected not_found")
	} else if appErr, ok := err.(*apptheory.AppError); !ok || appErr.Code != "app.not_found" {
		t.Fatalf("expected app.not_found, got %#v", err)
	}
}

func TestHandlePortalUpsertDomainVerificationRoute53_ConflictWhenNotEligible(t *testing.T) {
	t.Parallel()

	tdb := newRoute53AssistTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice"}
		_ = dest.UpdateKeys()
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Domain)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusPending}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		RequestID:    "rid",
		Params:       map[string]string{"slug": "demo", "domain": "example.com"},
	}
	if _, err := s.handlePortalUpsertDomainVerificationRoute53(ctx); err == nil {
		t.Fatalf("expected conflict")
	} else if appErr, ok := err.(*apptheory.AppError); !ok || appErr.Code != "app.conflict" {
		t.Fatalf("expected app.conflict, got %#v", err)
	}
}

func TestHandlePortalUpsertDomainVerificationRoute53_ConflictWhenRoute53ClientMissing(t *testing.T) {
	t.Parallel()

	tdb := newRoute53AssistTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice"}
		_ = dest.UpdateKeys()
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Domain)
		*dest = models.Domain{
			Domain:            "example.com",
			InstanceSlug:      "demo",
			Status:            models.DomainStatusPending,
			VerificationToken: "tok",
		}
		_ = dest.UpdateKeys()
	}).Once()

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		RequestID:    "rid",
		Params:       map[string]string{"slug": "demo", "domain": "example.com"},
	}
	if _, err := s.handlePortalUpsertDomainVerificationRoute53(ctx); err == nil {
		t.Fatalf("expected conflict")
	} else if appErr, ok := err.(*apptheory.AppError); !ok || appErr.Code != "app.conflict" {
		t.Fatalf("expected app.conflict, got %#v", err)
	}
}
