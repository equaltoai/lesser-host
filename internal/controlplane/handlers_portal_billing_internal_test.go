package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/payments"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
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
	if _, ok, err := s.loadBillingProfile(&apptheory.Context{}, testUsernameAlice); err != nil || ok {
		t.Fatalf("expected not found, ok=%v err=%v", ok, err)
	}

	tdb.qProfile.On("First", mock.AnythingOfType("*models.BillingProfile")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.BillingProfile)
		if !ok {
			t.Fatalf("expected *models.BillingProfile, got %T", destAny)
		}
		*dest = models.BillingProfile{Username: testUsernameAlice, StripeCustomerID: "cus_123"}
	}).Once()
	profile, ok, err := s.loadBillingProfile(&apptheory.Context{}, testUsernameAlice)
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
	if err := s.putBillingProfile(&apptheory.Context{}, &models.BillingProfile{Username: testUsernameAlice}); err != nil {
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
			if in.Username != testUsernameAlice {
				t.Fatalf("unexpected username: %#v", in)
			}
			return "cus_new", nil
		},
	}

	profile, appErr := s.ensureStripeCustomerProfile(&apptheory.Context{}, provider, testUsernameAlice, "a@example.com")
	if appErr != nil || profile == nil || profile.StripeCustomerID != "cus_new" {
		t.Fatalf("unexpected result: profile=%#v err=%v", profile, appErr)
	}
}

func TestHandlePortalListCreditPurchases_ListsAndFiltersNil(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qPurchase.On("All", mock.AnythingOfType("*[]*models.CreditPurchase")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*[]*models.CreditPurchase)
		if !ok {
			t.Fatalf("expected *[]*models.CreditPurchase, got %T", destAny)
		}
		*dest = []*models.CreditPurchase{
			nil,
			{ID: "p1", Status: models.CreditPurchaseStatusPending},
		}
	}).Once()

	resp, err := s.handlePortalListCreditPurchases(&apptheory.Context{AuthIdentity: testUsernameAlice})
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{Slug: "demo", Owner: testUsernameAlice}
	}).Once()
	tdb.qKey.On("Create").Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()

	resp, err := s.handlePortalCreateInstanceKey(&apptheory.Context{
		AuthIdentity: testUsernameAlice,
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.CreditPurchase)
		if !ok {
			t.Fatalf("expected *models.CreditPurchase, got %T", destAny)
		}
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
			Metadata:      map[string]string{"username": testUsernameAlice},
			CustomerID:    "cus_1",
			SetupIntentID: "seti_1",
		},
	}, now)
	if err != nil || resp.Status != 200 {
		t.Fatalf("setup completed: resp=%#v err=%v", resp, err)
	}

	// Checkout expired marks pending purchase expired.
	tdb.qPurchase.On("First", mock.AnythingOfType("*models.CreditPurchase")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.CreditPurchase)
		if !ok {
			t.Fatalf("expected *models.CreditPurchase, got %T", destAny)
		}
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

	if _, err := s.createPendingCreditPurchase(nil, testUsernameAlice, "demo", "2026-02", 1, 1, "stripe"); err == nil {
		t.Fatalf("expected error")
	}

	tdb.qPurchase.On("Create").Return(nil).Once()
	p, appErr := s.createPendingCreditPurchase(&apptheory.Context{}, testUsernameAlice, "demo", "2026-02", 1, 1, "stripe")
	if appErr != nil || p == nil || p.ID == "" {
		t.Fatalf("unexpected: p=%#v err=%v", p, appErr)
	}
}

func TestPortalUserEmailBestEffort(t *testing.T) {
	t.Parallel()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	if got := s.portalUserEmailBestEffort(nil, testUsernameAlice); got != "" {
		t.Fatalf("expected empty")
	}

	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.User)
		if !ok {
			t.Fatalf("expected *models.User, got %T", destAny)
		}
		*dest = models.User{Email: " a@example.com "}
	}).Once()
	if got := s.portalUserEmailBestEffort(&apptheory.Context{}, testUsernameAlice); got != "a@example.com" {
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
		destAny := args.Get(0)
		dest, ok := destAny.(*models.Instance)
		if !ok {
			t.Fatalf("expected *models.Instance, got %T", destAny)
		}
		*dest = models.Instance{Slug: "demo", Owner: testUsernameAlice}
	}).Once()

	if _, err := s.handlePortalCreateCreditsCheckout(&apptheory.Context{
		AuthIdentity: testUsernameAlice,
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
		destAny := args.Get(0)
		dest, ok := destAny.(*[]*models.BillingPaymentMethod)
		if !ok {
			t.Fatalf("expected *[]*models.BillingPaymentMethod, got %T", destAny)
		}
		*dest = []*models.BillingPaymentMethod{
			nil,
			{ID: "pm_1", Type: "card", Brand: "visa", Last4: "4242"},
		}
	}).Once()
	tdb.qProfile.On("First", mock.AnythingOfType("*models.BillingProfile")).Return(nil).Run(func(args mock.Arguments) {
		destAny := args.Get(0)
		dest, ok := destAny.(*models.BillingProfile)
		if !ok {
			t.Fatalf("expected *models.BillingProfile, got %T", destAny)
		}
		*dest = models.BillingProfile{Username: testUsernameAlice, DefaultPaymentMethodID: "pm_1"}
		_ = dest.UpdateKeys()
	}).Once()

	resp, err := s.handlePortalListPaymentMethods(&apptheory.Context{AuthIdentity: testUsernameAlice})
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

	if _, err := s.handlePortalCreatePaymentMethodCheckout(&apptheory.Context{AuthIdentity: testUsernameAlice}); err == nil {
		t.Fatalf("expected conflict when payments provider is not configured")
	}
}

func TestCreditsAmountCents_ErrorsAndCeil(t *testing.T) {
	t.Parallel()

	if _, err := creditsAmountCents(0, 100); err == nil {
		t.Fatalf("expected error for credits<=0")
	}
	if _, err := creditsAmountCents(1, 0); err == nil {
		t.Fatalf("expected error for centsPer1000<=0")
	}
	if _, err := creditsAmountCents(1_000_000_001, 100); err == nil {
		t.Fatalf("expected error for credits too large")
	}

	// 100 cents / 1000 credits = 0.1 cents per credit; ceil -> 1 cent.
	if got, err := creditsAmountCents(1, 100); err != nil || got != 1 {
		t.Fatalf("expected 1 cent, got %d err=%v", got, err)
	}
	if got, err := creditsAmountCents(1000, 100); err != nil || got != 100 {
		t.Fatalf("expected 100 cents, got %d err=%v", got, err)
	}
}

func TestNormalizeCreditsCheckoutMonth_DefaultAndValidates(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()

	if got, appErr := normalizeCreditsCheckoutMonth("", now); appErr != nil || got != "1970-01" {
		t.Fatalf("unexpected default month: %q err=%v", got, appErr)
	}
	if _, appErr := normalizeCreditsCheckoutMonth("2026-13", now); appErr == nil {
		t.Fatalf("expected invalid month error")
	}
	if got, appErr := normalizeCreditsCheckoutMonth(" 2026-02 ", now); appErr != nil || got != "2026-02" {
		t.Fatalf("unexpected normalized month: %q err=%v", got, appErr)
	}
}

func TestParsePortalCreditsCheckoutRequest_ValidatesAndTrims(t *testing.T) {
	t.Parallel()

	if _, appErr := parsePortalCreditsCheckoutRequest(&apptheory.Context{Request: apptheory.Request{Body: []byte("{")}}); appErr == nil {
		t.Fatalf("expected invalid request error")
	}

	body, _ := json.Marshal(portalCreditsCheckoutRequest{Credits: 1})
	if _, appErr := parsePortalCreditsCheckoutRequest(&apptheory.Context{Request: apptheory.Request{Body: body}}); appErr == nil {
		t.Fatalf("expected instance_slug required error")
	}

	body, _ = json.Marshal(portalCreditsCheckoutRequest{InstanceSlug: "demo", Credits: 0})
	if _, appErr := parsePortalCreditsCheckoutRequest(&apptheory.Context{Request: apptheory.Request{Body: body}}); appErr == nil {
		t.Fatalf("expected credits validation error")
	}

	body, _ = json.Marshal(portalCreditsCheckoutRequest{InstanceSlug: " DeMo ", Credits: 1, Month: " 2026-02 "})
	got, appErr := parsePortalCreditsCheckoutRequest(&apptheory.Context{Request: apptheory.Request{Body: body}})
	if appErr != nil || got.InstanceSlug != "demo" || got.Month != "2026-02" {
		t.Fatalf("unexpected parsed request: %#v err=%v", got, appErr)
	}
}

func TestHandleStripePaymentCheckoutCompleted_Branches(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()

	t.Run("missing_purchase_id", func(t *testing.T) {
		tdb := newBillingTestDB()
		s := &Server{store: store.New(tdb.db)}

		resp, err := s.handleStripePaymentCheckoutCompleted(&apptheory.Context{}, &payments.WebhookEvent{
			Session: payments.CheckoutSession{Metadata: map[string]string{}},
		}, now)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 200, resp.Status)
	})

	t.Run("purchase_not_found", func(t *testing.T) {
		tdb := newBillingTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qPurchase.On("First", mock.AnythingOfType("*models.CreditPurchase")).Return(theoryErrors.ErrItemNotFound).Once()

		resp, err := s.handleStripePaymentCheckoutCompleted(&apptheory.Context{}, &payments.WebhookEvent{
			Session: payments.CheckoutSession{Metadata: map[string]string{"purchase_id": "p1"}},
		}, now)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 200, resp.Status)
	})

	t.Run("already_paid", func(t *testing.T) {
		tdb := newBillingTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qPurchase.On("First", mock.AnythingOfType("*models.CreditPurchase")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.CreditPurchase](t, args, 0)
			*dest = models.CreditPurchase{ID: "p1", Status: models.CreditPurchaseStatusPaid}
		}).Once()

		resp, err := s.handleStripePaymentCheckoutCompleted(&apptheory.Context{}, &payments.WebhookEvent{
			Session: payments.CheckoutSession{Metadata: map[string]string{"purchase_id": "p1"}},
		}, now)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 200, resp.Status)
	})

	t.Run("condition_failed_returns_ok", func(t *testing.T) {
		db := ttmocks.NewMockExtendedDBStrict()
		qPurchase := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db)
		db.On("Model", mock.AnythingOfType("*models.CreditPurchase")).Return(qPurchase)
		qPurchase.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qPurchase)
		qPurchase.On("First", mock.AnythingOfType("*models.CreditPurchase")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.CreditPurchase](t, args, 0)
			*dest = models.CreditPurchase{
				ID:           "p1",
				InstanceSlug: "demo",
				Month:        "2026-02",
				Credits:      1000,
				Status:       models.CreditPurchaseStatusPending,
			}
		}).Once()
		db.On("TransactWrite", mock.Anything, mock.Anything).Return(theoryErrors.ErrConditionFailed).Once()

		s := &Server{store: store.New(db)}
		resp, err := s.handleStripePaymentCheckoutCompleted(&apptheory.Context{}, &payments.WebhookEvent{
			Session: payments.CheckoutSession{Metadata: map[string]string{"purchase_id": "p1"}},
		}, now)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 200, resp.Status)
	})

	t.Run("transact_error_returns_ok_with_error_field", func(t *testing.T) {
		db := ttmocks.NewMockExtendedDBStrict()
		qPurchase := new(ttmocks.MockQuery)

		db.On("WithContext", mock.Anything).Return(db)
		db.On("Model", mock.AnythingOfType("*models.CreditPurchase")).Return(qPurchase)
		qPurchase.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qPurchase)
		qPurchase.On("First", mock.AnythingOfType("*models.CreditPurchase")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.CreditPurchase](t, args, 0)
			*dest = models.CreditPurchase{
				ID:           "p1",
				InstanceSlug: "demo",
				Month:        "2026-02",
				Credits:      1000,
				Status:       models.CreditPurchaseStatusPending,
			}
		}).Once()
		db.On("TransactWrite", mock.Anything, mock.Anything).Return(errors.New("boom")).Once()

		s := &Server{store: store.New(db)}
		resp, err := s.handleStripePaymentCheckoutCompleted(&apptheory.Context{}, &payments.WebhookEvent{
			Session: payments.CheckoutSession{Metadata: map[string]string{"purchase_id": "p1"}},
		}, now)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, 200, resp.Status)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(resp.Body, &parsed))
		require.Equal(t, "failed to apply credits", parsed["error"])
	})
}

func TestHandleStripeSetupCheckoutCompleted_Branches(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()

	tdb := newBillingTestDB()
	s := &Server{store: store.New(tdb.db)}

	// Missing username.
	resp, err := s.handleStripeSetupCheckoutCompleted(&apptheory.Context{}, stubPaymentsProvider{name: "stripe"}, &payments.WebhookEvent{
		Session: payments.CheckoutSession{Metadata: map[string]string{}},
	}, now)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("missing username resp=%#v err=%v", resp, err)
	}

	// Missing setup intent.
	resp, err = s.handleStripeSetupCheckoutCompleted(&apptheory.Context{}, stubPaymentsProvider{name: "stripe"}, &payments.WebhookEvent{
		Session: payments.CheckoutSession{Metadata: map[string]string{"username": testUsernameAlice}},
	}, now)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("missing setup intent resp=%#v err=%v", resp, err)
	}

	// Resolve payment method failure.
	resp, err = s.handleStripeSetupCheckoutCompleted(&apptheory.Context{}, stubPaymentsProvider{
		name: "stripe",
		resolveSetupMethod: func(context.Context, string) (*payments.PaymentMethodDetails, error) {
			return nil, errors.New("boom")
		},
	}, &payments.WebhookEvent{
		Session: payments.CheckoutSession{
			Metadata:      map[string]string{"username": testUsernameAlice},
			SetupIntentID: "seti_1",
		},
	}, now)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("resolve failure resp=%#v err=%v", resp, err)
	}

	// Transact write error.
	db := ttmocks.NewMockExtendedDBStrict()
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(errors.New(testNope)).Once()

	s2 := &Server{store: store.New(db)}
	resp, err = s2.handleStripeSetupCheckoutCompleted(&apptheory.Context{}, stubPaymentsProvider{
		name: "stripe",
		resolveSetupMethod: func(context.Context, string) (*payments.PaymentMethodDetails, error) {
			return &payments.PaymentMethodDetails{ID: "pm_1", Type: "card"}, nil
		},
	}, &payments.WebhookEvent{
		Session: payments.CheckoutSession{
			Metadata:      map[string]string{"username": testUsernameAlice},
			CustomerID:    "cus_1",
			SetupIntentID: "seti_1",
		},
	}, now)
	if err != nil || resp == nil || resp.Status != 200 {
		t.Fatalf("transact error resp=%#v err=%v", resp, err)
	}
	var parsed map[string]any
	_ = json.Unmarshal(resp.Body, &parsed)
	if parsed["error"] != "failed to store payment method" {
		t.Fatalf("expected error field, got body=%q", string(resp.Body))
	}
}

func TestHandleStripeCheckoutSessionExpired_Branches(t *testing.T) {
	t.Parallel()

	t.Run("missing_purchase_id", func(t *testing.T) {
		tdb := newBillingTestDB()
		s := &Server{store: store.New(tdb.db)}

		resp, err := s.handleStripeCheckoutSessionExpired(&apptheory.Context{}, &payments.WebhookEvent{
			Session: payments.CheckoutSession{Metadata: map[string]string{}},
		})
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}
	})

	t.Run("purchase_not_pending_noop", func(t *testing.T) {
		tdb := newBillingTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qPurchase.On("First", mock.AnythingOfType("*models.CreditPurchase")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.CreditPurchase](t, args, 0)
			*dest = models.CreditPurchase{ID: "p1", Status: models.CreditPurchaseStatusPaid}
		}).Once()

		resp, err := s.handleStripeCheckoutSessionExpired(&apptheory.Context{}, &payments.WebhookEvent{
			Session: payments.CheckoutSession{Metadata: map[string]string{"purchase_id": "p1"}},
		})
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}
	})
}
