package controlplane

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/payments"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type billingTestDB struct {
	db        *ttmocks.MockExtendedDB
	qUser     *ttmocks.MockQuery
	qProfile  *ttmocks.MockQuery
	qPurchase *ttmocks.MockQuery
	qMethod   *ttmocks.MockQuery
	qBudget   *ttmocks.MockQuery
	qAudit    *ttmocks.MockQuery
	qInst     *ttmocks.MockQuery
	qKey      *ttmocks.MockQuery
}

func newBillingTestDB() billingTestDB {
	db := ttmocks.NewMockExtendedDB()
	qUser := new(ttmocks.MockQuery)
	qProfile := new(ttmocks.MockQuery)
	qPurchase := new(ttmocks.MockQuery)
	qMethod := new(ttmocks.MockQuery)
	qBudget := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	qInst := new(ttmocks.MockQuery)
	qKey := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.User")).Return(qUser).Maybe()
	db.On("Model", mock.AnythingOfType("*models.BillingProfile")).Return(qProfile).Maybe()
	db.On("Model", mock.AnythingOfType("*models.CreditPurchase")).Return(qPurchase).Maybe()
	db.On("Model", mock.AnythingOfType("*models.BillingPaymentMethod")).Return(qMethod).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceBudgetMonth")).Return(qBudget).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()

	for _, q := range []*ttmocks.MockQuery{qUser, qProfile, qPurchase, qMethod, qBudget, qAudit, qInst, qKey} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return billingTestDB{
		db:        db,
		qUser:     qUser,
		qProfile:  qProfile,
		qPurchase: qPurchase,
		qMethod:   qMethod,
		qBudget:   qBudget,
		qAudit:    qAudit,
		qInst:     qInst,
		qKey:      qKey,
	}
}

type stubPaymentsProvider struct {
	name               string
	ensureCustomer     func(ctx context.Context, in payments.EnsureCustomerInput) (string, error)
	resolveSetupMethod func(ctx context.Context, setupIntentID string) (*payments.PaymentMethodDetails, error)
}

func (s stubPaymentsProvider) Name() string { return s.name }

func (s stubPaymentsProvider) EnsureCustomer(ctx context.Context, in payments.EnsureCustomerInput) (string, error) {
	if s.ensureCustomer == nil {
		return "", nil
	}
	return s.ensureCustomer(ctx, in)
}

func (s stubPaymentsProvider) CreateCreditsCheckout(_ context.Context, _ payments.CreditsCheckoutInput) (*payments.CheckoutSession, error) {
	return nil, nil
}

func (s stubPaymentsProvider) CreateSetupCheckout(_ context.Context, _ payments.SetupCheckoutInput) (*payments.CheckoutSession, error) {
	return nil, nil
}

func (s stubPaymentsProvider) ParseWebhookEvent(_ context.Context, _ map[string][]string, _ []byte) (*payments.WebhookEvent, error) {
	return nil, nil
}

func (s stubPaymentsProvider) ResolveSetupPaymentMethod(ctx context.Context, setupIntentID string) (*payments.PaymentMethodDetails, error) {
	if s.resolveSetupMethod == nil {
		return nil, nil
	}
	return s.resolveSetupMethod(ctx, setupIntentID)
}

func TestLoadBillingProfile_NotFoundAndSuccess(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qProfile.On("First", mock.AnythingOfType("*models.BillingProfile")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, ok, err := s.loadBillingProfile(&apptheory.Context{}, "alice"); err != nil || ok {
		t.Fatalf("expected not found, ok=%v err=%v", ok, err)
	}

	tdb.qProfile.On("First", mock.AnythingOfType("*models.BillingProfile")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.BillingProfile)
		*dest = models.BillingProfile{Username: "alice", StripeCustomerID: "cus_123"}
	}).Once()
	profile, ok, err := s.loadBillingProfile(&apptheory.Context{}, "alice")
	if err != nil || !ok || profile == nil || profile.StripeCustomerID != "cus_123" {
		t.Fatalf("unexpected result: profile=%#v ok=%v err=%v", profile, ok, err)
	}
}

func TestPutBillingProfile_ValidatesAndUpserts(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	if err := s.putBillingProfile(&apptheory.Context{}, nil); err == nil {
		t.Fatalf("expected error")
	}

	tdb.qProfile.On("CreateOrUpdate").Return(nil).Once()
	if err := s.putBillingProfile(&apptheory.Context{}, &models.BillingProfile{Username: "alice"}); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestEnsureStripeCustomerProfile_CreatesAndStoresWhenMissing(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qProfile.On("First", mock.AnythingOfType("*models.BillingProfile")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qProfile.On("CreateOrUpdate").Return(nil).Once()

	provider := stubPaymentsProvider{
		name: "stripe",
		ensureCustomer: func(_ context.Context, in payments.EnsureCustomerInput) (string, error) {
			if in.Username != "alice" {
				t.Fatalf("unexpected username: %#v", in)
			}
			return "cus_new", nil
		},
	}

	profile, appErr := s.ensureStripeCustomerProfile(&apptheory.Context{}, provider, "alice", "a@example.com")
	if appErr != nil || profile == nil || profile.StripeCustomerID != "cus_new" {
		t.Fatalf("unexpected result: profile=%#v err=%v", profile, appErr)
	}
}

func TestHandlePortalListCreditPurchases_ListsAndFiltersNil(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qPurchase.On("All", mock.AnythingOfType("*[]*models.CreditPurchase")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.CreditPurchase)
		*dest = []*models.CreditPurchase{
			nil,
			{ID: "p1", Status: models.CreditPurchaseStatusPending},
		}
	}).Once()

	resp, err := s.handlePortalListCreditPurchases(&apptheory.Context{AuthIdentity: "alice"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
}

func TestHandlePortalCreateInstanceKey_CreatesKeyAndAudits(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice"}
	}).Once()
	tdb.qKey.On("Create").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	resp, err := s.handlePortalCreateInstanceKey(&apptheory.Context{
		AuthIdentity: "alice",
		RequestID:    "r1",
		Params:       map[string]string{"slug": "demo"},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 201 {
		t.Fatalf("expected 201, got %d (body=%q)", resp.Status, string(resp.Body))
	}
}

func TestStripeWebhookHandlers_PaymentAndSetupAndExpired(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	now := time.Unix(100, 0).UTC()

	// Payment completed (pending -> paid).
	tdb.qPurchase.On("First", mock.AnythingOfType("*models.CreditPurchase")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.CreditPurchase)
		*dest = models.CreditPurchase{
			ID:           "p1",
			InstanceSlug: "demo",
			Month:        "2026-02",
			Credits:      1000,
			Status:       models.CreditPurchaseStatusPending,
		}
	}).Once()
	resp, err := s.handleStripePaymentCheckoutCompleted(&apptheory.Context{RequestID: "r1"}, &payments.WebhookEvent{
		Session: payments.CheckoutSession{
			Metadata: map[string]string{"purchase_id": "p1"},
			ID:       "cs_1",
			Currency: "usd",
		},
	}, now)
	if err != nil || resp.Status != 200 {
		t.Fatalf("payment completed: resp=%#v err=%v", resp, err)
	}

	// Setup completed.
	tdb.qProfile.On("Create").Return(nil).Maybe()
	tdb.qMethod.On("Create").Return(nil).Maybe()
	tdb.qAudit.On("Create").Return(nil).Maybe()

	provider := stubPaymentsProvider{
		name: "stripe",
		resolveSetupMethod: func(_ context.Context, setupIntentID string) (*payments.PaymentMethodDetails, error) {
			if setupIntentID != "seti_1" {
				t.Fatalf("unexpected setup intent: %q", setupIntentID)
			}
			return &payments.PaymentMethodDetails{ID: "pm_1", Type: "card", Brand: "visa", Last4: "4242", ExpMonth: 1, ExpYear: 2030}, nil
		},
	}
	resp, err = s.handleStripeSetupCheckoutCompleted(&apptheory.Context{RequestID: "r2"}, provider, &payments.WebhookEvent{
		Session: payments.CheckoutSession{
			Metadata:      map[string]string{"username": "alice"},
			CustomerID:    "cus_1",
			SetupIntentID: "seti_1",
		},
	}, now)
	if err != nil || resp.Status != 200 {
		t.Fatalf("setup completed: resp=%#v err=%v", resp, err)
	}

	// Checkout expired marks pending purchase expired.
	tdb.qPurchase.On("First", mock.AnythingOfType("*models.CreditPurchase")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.CreditPurchase)
		*dest = models.CreditPurchase{ID: "p2", Status: models.CreditPurchaseStatusPending}
	}).Once()
	tdb.qPurchase.On("Update", mock.Anything).Return(nil).Once()
	resp, err = s.handleStripeCheckoutSessionExpired(&apptheory.Context{}, &payments.WebhookEvent{
		Session: payments.CheckoutSession{Metadata: map[string]string{"purchase_id": "p2"}},
	})
	if err != nil || resp.Status != 200 {
		t.Fatalf("expired: resp=%#v err=%v", resp, err)
	}
}

func TestHandleStripeWebhook_IgnoresWhenProviderDisabled(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	resp, err := s.handleStripeWebhook(&apptheory.Context{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
}

func TestHandleStripeCheckoutSessionCompleted_Dispatches(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	resp, err := s.handleStripeCheckoutSessionCompleted(&apptheory.Context{}, stubPaymentsProvider{name: "stripe"}, &payments.WebhookEvent{
		Session: payments.CheckoutSession{Mode: "unknown"},
	})
	if err != nil || resp.Status != 200 {
		t.Fatalf("unexpected: resp=%#v err=%v", resp, err)
	}
}

func TestCreatePendingCreditPurchase_ValidatesAndCreates(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	if _, err := s.createPendingCreditPurchase(nil, "alice", "demo", "2026-02", 1, 1, "stripe"); err == nil {
		t.Fatalf("expected error")
	}

	tdb.qPurchase.On("Create").Return(nil).Once()
	p, appErr := s.createPendingCreditPurchase(&apptheory.Context{}, "alice", "demo", "2026-02", 1, 1, "stripe")
	if appErr != nil || p == nil || p.ID == "" {
		t.Fatalf("unexpected: p=%#v err=%v", p, appErr)
	}
}

func TestPortalUserEmailBestEffort(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	if got := s.portalUserEmailBestEffort(nil, "alice"); got != "" {
		t.Fatalf("expected empty")
	}

	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.User)
		*dest = models.User{Email: " a@example.com "}
	}).Once()
	if got := s.portalUserEmailBestEffort(&apptheory.Context{}, "alice"); got != "a@example.com" {
		t.Fatalf("unexpected email: %q", got)
	}
}

func TestMarkAndUpdateCreditPurchase_BestEffortNoPanic(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	s.markCreditPurchaseFailedBestEffort(&apptheory.Context{}, "p1")
	s.updateCreditPurchaseWithCheckoutSessionBestEffort(&apptheory.Context{}, "p1", &payments.CheckoutSession{ID: "cs_1"})
}

func TestHandlePortalCreateCreditsCheckout_RejectsWhenPricingNotConfigured(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	body, _ := json.Marshal(portalCreditsCheckoutRequest{InstanceSlug: "demo", Credits: 1})
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice"}
	}).Once()

	if _, err := s.handlePortalCreateCreditsCheckout(&apptheory.Context{
		AuthIdentity: "alice",
		Request:      apptheory.Request{Body: body},
	}); err == nil {
		t.Fatalf("expected conflict when pricing is not configured")
	}
}

func TestHandlePortalListPaymentMethods_ReturnsDefaultAndMethods(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qMethod.On("All", mock.AnythingOfType("*[]*models.BillingPaymentMethod")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*[]*models.BillingPaymentMethod)
		*dest = []*models.BillingPaymentMethod{
			nil,
			{ID: "pm_1", Type: "card", Brand: "visa", Last4: "4242"},
		}
	}).Once()
	tdb.qProfile.On("First", mock.AnythingOfType("*models.BillingProfile")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.BillingProfile)
		*dest = models.BillingProfile{Username: "alice", DefaultPaymentMethodID: "pm_1"}
		_ = dest.UpdateKeys()
	}).Once()

	resp, err := s.handlePortalListPaymentMethods(&apptheory.Context{AuthIdentity: "alice"})
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resp=%#v err=%v", resp, err)
	}

	var parsed portalListPaymentMethodsResponse
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.DefaultPaymentMethodID != "pm_1" || parsed.Count != 1 || len(parsed.Methods) != 1 {
		t.Fatalf("unexpected output: %#v", parsed)
	}
}

func TestHandlePortalCreatePaymentMethodCheckout_ProviderNotConfigured(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	if _, err := s.handlePortalCreatePaymentMethodCheckout(&apptheory.Context{AuthIdentity: "alice"}); err == nil {
		t.Fatalf("expected conflict when payments provider is not configured")
	}
}
