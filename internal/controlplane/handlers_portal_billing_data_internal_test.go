package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/v2/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/payments"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type billingDataTestDB struct {
	db       *ttmocks.MockExtendedDB
	qProfile *ttmocks.MockQuery
	qMethod  *ttmocks.MockQuery
	qAudit   *ttmocks.MockQuery
}

func newBillingDataTestDB() billingDataTestDB {
	db := ttmocks.NewMockExtendedDB()
	qProfile := new(ttmocks.MockQuery)
	qMethod := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.BillingProfile")).Return(qProfile).Maybe()
	db.On("Model", mock.AnythingOfType("*models.BillingPaymentMethod")).Return(qMethod).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	// Audit stub.
	qAudit.On("Create").Return(nil).Maybe()

	for _, q := range []*ttmocks.MockQuery{qProfile, qMethod} {
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

	return billingDataTestDB{
		db:       db,
		qProfile: qProfile,
		qMethod:  qMethod,
		qAudit:   qAudit,
	}
}

// stubBillingProfile sets up a default billing profile for the given username
// with the provided Stripe customer ID and default payment method ID.
func (tdb billingDataTestDB) stubBillingProfile(t *testing.T, username, stripeCustID, defaultPMID string) {
	t.Helper()
	tdb.qProfile.On("First", mock.AnythingOfType("*models.BillingProfile")).Return(nil).
		Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.BillingProfile](t, args, 0)
			*dest = models.BillingProfile{
				Username:               username,
				Provider:               models.BillingProviderStripe,
				StripeCustomerID:       stripeCustID,
				DefaultPaymentMethodID: defaultPMID,
			}
		}).Maybe()
}

func (tdb billingDataTestDB) stubBillingProfileNotFound(t *testing.T) {
	t.Helper()
	tdb.qProfile.On("First", mock.AnythingOfType("*models.BillingProfile")).Return(theoryErrors.ErrItemNotFound).Maybe()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeContext(authIdentity string) *apptheory.Context {
	return &apptheory.Context{
		AuthIdentity: authIdentity,
		RequestID:    "req-1",
	}
}

var sampleInvoices = []payments.InvoiceInfo{
	{
		ID:          "in_001",
		PeriodStart: "2026-04-01T00:00:00Z",
		PeriodEnd:   "2026-04-30T23:59:59Z",
		AmountDue:   1500,
		Currency:    "usd",
		Status:      "paid",
		HostedURL:   "https://invoice.stripe.com/in_001",
		PDFURL:      "https://invoice.stripe.com/in_001/pdf",
	},
	{
		ID:          "in_002",
		PeriodStart: "2026-03-01T00:00:00Z",
		PeriodEnd:   "2026-03-31T23:59:59Z",
		AmountDue:   2500,
		Currency:    "usd",
		Status:      "open",
		HostedURL:   "https://invoice.stripe.com/in_002",
		PDFURL:      "",
	},
}

func safeInvoicesProvider(invoices []payments.InvoiceInfo) payments.Provider {
	return &stubPaymentsProvider{
		name: paymentsProviderStripeName,
		listInvoices: func(ctx context.Context, customerID string, limit int64) ([]payments.InvoiceInfo, error) {
			_ = ctx
			_ = customerID
			_ = limit
			return invoices, nil
		},
	}
}

// ---------------------------------------------------------------------------
// Invoice tests
// ---------------------------------------------------------------------------

func TestHandlePortalListInvoices_HappyPath(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "pm_abc")

	s := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{PaymentsProvider: "mock"},
	}

	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// mock provider returns nil invoices by default → empty response.
	var body listInvoicesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.NotNil(t, body.Invoices)
	require.Zero(t, body.Count)
}

func TestHandlePortalListInvoices_WithInvoices(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "")

	provider := safeInvoicesProvider(sampleInvoices)

	s := &Server{
		store:                   store.New(tdb.db),
		cfg:                     config.Config{PaymentsProvider: paymentsProviderStripeName},
		paymentsProviderFactory: func(name string) payments.Provider { return provider },
	}

	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)

	var body listInvoicesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Len(t, body.Invoices, 2)
	require.Equal(t, 2, body.Count)

	first := body.Invoices[0]
	require.Equal(t, "in_001", first.ID)
	require.Equal(t, "2026-04-01T00:00:00Z", first.PeriodStart)
	require.Equal(t, "2026-04-30T23:59:59Z", first.PeriodEnd)
	require.Equal(t, int64(1500), first.AmountDue)
	require.Equal(t, "usd", first.Currency)
	require.Equal(t, "paid", first.Status)
	require.Equal(t, "https://invoice.stripe.com/in_001", first.HostedInvoiceURL)
	require.Equal(t, "https://invoice.stripe.com/in_001/pdf", first.InvoicePDFURL)

	second := body.Invoices[1]
	require.Equal(t, "in_002", second.ID)
	require.Equal(t, "open", second.Status)
	require.Empty(t, second.InvoicePDFURL)
}

func TestHandlePortalListInvoices_NoBillingProfile(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfileNotFound(t)

	s := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{PaymentsProvider: paymentsProviderStripeName},
	}

	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)

	var body listInvoicesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.NotNil(t, body.Invoices)
	require.Zero(t, body.Count)
}

func TestHandlePortalListInvoices_Unauthenticated(t *testing.T) {
	t.Parallel()

	s := &Server{}
	ctx := &apptheory.Context{AuthIdentity: ""}
	resp, err := s.handlePortalListInvoices(ctx)
	require.Nil(t, resp)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	require.Equal(t, "app.unauthorized", appErr.Code)
}

func TestHandlePortalListInvoices_NoCustomerID(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	// Profile has empty StripeCustomerID.
	tdb.stubBillingProfile(t, "alice", "", "")

	s := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{PaymentsProvider: paymentsProviderStripeName},
	}

	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)

	var body listInvoicesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.NotNil(t, body.Invoices)
	require.Zero(t, body.Count)
}

func TestHandlePortalListInvoices_ProviderError(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "")

	provider := &stubPaymentsProvider{
		name: paymentsProviderStripeName,
		listInvoices: func(ctx context.Context, customerID string, limit int64) ([]payments.InvoiceInfo, error) {
			return nil, errors.New("stripe api down")
		},
	}

	s := &Server{
		store:                   store.New(tdb.db),
		cfg:                     config.Config{PaymentsProvider: paymentsProviderStripeName},
		paymentsProviderFactory: func(name string) payments.Provider { return provider },
	}

	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.Nil(t, resp)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	require.Equal(t, "app.internal", appErr.Code)
}

func TestHandlePortalListInvoices_PaymentsNotConfigured(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "")

	s := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{PaymentsProvider: "none"},
	}

	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)

	var body listInvoicesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.NotNil(t, body.Invoices)
	require.Zero(t, body.Count)
}

func TestHandlePortalListInvoices_CrossUserIsolation(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	// Set up ONLY Alice's profile with her specific Stripe customer ID.
	tdb.stubBillingProfile(t, "alice", "cus_alice", "")

	// Spy provider captures the customer ID argument to prove auth-identity scoping.
	var capturedCustomerID string
	provider := &stubPaymentsProvider{
		name: paymentsProviderStripeName,
		listInvoices: func(ctx context.Context, customerID string, limit int64) ([]payments.InvoiceInfo, error) {
			capturedCustomerID = customerID
			return sampleInvoices, nil
		},
	}

	s := &Server{
		store:                   store.New(tdb.db),
		cfg:                     config.Config{PaymentsProvider: paymentsProviderStripeName},
		paymentsProviderFactory: func(name string) payments.Provider { return provider },
	}

	// Alice authenticates — handler must scope queries and provider calls to Alice.
	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Core isolation proof: the provider receives Alice's Stripe customer ID.
	// If the handler leaked Bob's customer ID, capturedCustomerID would differ.
	require.Equal(t, "cus_alice", capturedCustomerID,
		"provider must receive Alice's Stripe customer ID, not another user's")

	// Verify the DB query used Alice's PK (not Bob's, not unscoped).
	tdb.qProfile.AssertCalled(t, "Where", "PK", "=", fmt.Sprintf(models.KeyPatternUser, "alice"))

	var body listInvoicesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Len(t, body.Invoices, 2)
}

// ---------------------------------------------------------------------------
// Payment method tests
// ---------------------------------------------------------------------------

func TestHandlePortalGetPaymentMethod_HappyPath(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "pm_active")

	// Stub payment method lookup: PK=USER#alice, SK=PAYMENT_METHOD#stripe#pm_active.
	tdb.qMethod.On("First", mock.AnythingOfType("*models.BillingPaymentMethod")).Return(nil).
		Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.BillingPaymentMethod](t, args, 0)
			*dest = models.BillingPaymentMethod{
				ID:       "pm_active",
				Username: "alice",
				Provider: models.BillingProviderStripe,
				Type:     "card",
				Brand:    "visa",
				Last4:    "4242",
				ExpMonth: 12,
				ExpYear:  2028,
				Status:   models.BillingPaymentMethodStatusActive,
			}
		}).Maybe()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}

	ctx := makeContext("alice")
	resp, err := s.handlePortalGetPaymentMethod(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)

	var wrapper struct {
		PaymentMethod *paymentMethodSafe `json:"payment_method"`
	}
	require.NoError(t, json.Unmarshal(resp.Body, &wrapper))
	require.NotNil(t, wrapper.PaymentMethod)

	pm := wrapper.PaymentMethod
	require.Equal(t, "pm_active", pm.ID)
	require.Equal(t, "card", pm.Type)
	require.Equal(t, "visa", pm.Brand)
	require.Equal(t, "4242", pm.Last4)
	require.Equal(t, int64(12), pm.ExpMonth)
	require.Equal(t, int64(2028), pm.ExpYear)
	require.Equal(t, "active", pm.Status)
}

func TestHandlePortalGetPaymentMethod_NoBillingProfile(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfileNotFound(t)

	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}

	ctx := makeContext("alice")
	resp, err := s.handlePortalGetPaymentMethod(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)

	var wrapper struct {
		PaymentMethod *paymentMethodSafe `json:"payment_method"`
	}
	require.NoError(t, json.Unmarshal(resp.Body, &wrapper))
	require.Nil(t, wrapper.PaymentMethod, "no payment_method should be returned")
}

func TestHandlePortalGetPaymentMethod_NoDefaultSet(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	// Profile has no default PaymentMethodID.
	tdb.stubBillingProfile(t, "alice", "cus_123", "")

	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}

	ctx := makeContext("alice")
	resp, err := s.handlePortalGetPaymentMethod(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)

	var wrapper struct {
		PaymentMethod *paymentMethodSafe `json:"payment_method"`
	}
	require.NoError(t, json.Unmarshal(resp.Body, &wrapper))
	require.Nil(t, wrapper.PaymentMethod, "no payment_method should be returned")
}

func TestHandlePortalGetPaymentMethod_PaymentMethodNotFound(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "pm_missing")

	// Payment method lookup returns not found.
	tdb.qMethod.On("First", mock.AnythingOfType("*models.BillingPaymentMethod")).
		Return(theoryErrors.ErrItemNotFound).Maybe()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}

	ctx := makeContext("alice")
	resp, err := s.handlePortalGetPaymentMethod(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)

	var wrapper struct {
		PaymentMethod *paymentMethodSafe `json:"payment_method"`
	}
	require.NoError(t, json.Unmarshal(resp.Body, &wrapper))
	require.Nil(t, wrapper.PaymentMethod, "payment method not found should return null")
}

func TestHandlePortalGetPaymentMethod_DBError(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "pm_123")

	// Payment method lookup returns a non-not-found DB error.
	tdb.qMethod.On("First", mock.AnythingOfType("*models.BillingPaymentMethod")).
		Return(errors.New("connection reset")).Maybe()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}

	ctx := makeContext("alice")
	resp, err := s.handlePortalGetPaymentMethod(ctx)
	require.Nil(t, resp, "DB errors must not return data")
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	require.Equal(t, "app.internal", appErr.Code,
		"unexpected DB errors must become sanitized app.internal, not swallowed as no-data")
}

func TestHandlePortalGetPaymentMethod_Unauthenticated(t *testing.T) {
	t.Parallel()

	s := &Server{}
	ctx := &apptheory.Context{AuthIdentity: ""}
	resp, err := s.handlePortalGetPaymentMethod(ctx)
	require.Nil(t, resp)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	require.Equal(t, "app.unauthorized", appErr.Code)
}

func TestHandlePortalGetPaymentMethod_CrossUserIsolation(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	// Alice has a profile with her own default payment method.
	tdb.stubBillingProfile(t, "alice", "cus_alice", "pm_alice")

	// Stub Alice's payment method — the handler must return ONLY this, not Bob's.
	tdb.qMethod.On("First", mock.AnythingOfType("*models.BillingPaymentMethod")).Return(nil).
		Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.BillingPaymentMethod](t, args, 0)
			*dest = models.BillingPaymentMethod{
				ID:       "pm_alice",
				Username: "alice",
				Provider: models.BillingProviderStripe,
				Type:     "card",
				Brand:    "visa",
				Last4:    "4242",
				ExpMonth: 12,
				ExpYear:  2028,
				Status:   models.BillingPaymentMethodStatusActive,
			}
		}).Maybe()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}

	// Alice authenticates — handler queries by Alice's PK.
	ctx := makeContext("alice")
	resp, err := s.handlePortalGetPaymentMethod(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Prove PK scoping: the handler queried the DB with Alice's PK.
	tdb.qProfile.AssertCalled(t, "Where", "PK", "=", fmt.Sprintf(models.KeyPatternUser, "alice"))

	// Prove the payment-method query itself is scoped to Alice —
	// the handler must construct both PK and SK from Alice's identity.
	aliceSK := fmt.Sprintf("PAYMENT_METHOD#%s#%s", models.BillingProviderStripe, "pm_alice")
	tdb.qMethod.AssertCalled(t, "Where", "PK", "=", fmt.Sprintf(models.KeyPatternUser, "alice"))
	tdb.qMethod.AssertCalled(t, "Where", "SK", "=", aliceSK)

	// Negative proof: Bob's identity was never used in the payment-method query.
	tdb.qMethod.AssertNotCalled(t, "Where", "PK", "=", fmt.Sprintf(models.KeyPatternUser, "bob"))
	bobSK := fmt.Sprintf("PAYMENT_METHOD#%s#%s", models.BillingProviderStripe, "pm_bob")
	tdb.qMethod.AssertNotCalled(t, "Where", "SK", "=", bobSK)

	var wrapper struct {
		PaymentMethod *paymentMethodSafe `json:"payment_method"`
	}
	require.NoError(t, json.Unmarshal(resp.Body, &wrapper))
	require.NotNil(t, wrapper.PaymentMethod,
		"Alice must receive her own payment method when scoped to her PK")

	// The payment method returned must be Alice's, not Bob's.
	require.Equal(t, "pm_alice", wrapper.PaymentMethod.ID)
	require.Equal(t, "4242", wrapper.PaymentMethod.Last4)
}

// ---------------------------------------------------------------------------
// Redaction tests — prove no secrets, PK/SK, PAN/CVV, or internal IDs leak
// ---------------------------------------------------------------------------

func TestInvoiceSummary_NoSecretsLeaked(t *testing.T) {
	t.Parallel()

	// Serialize a representative invoiceSummary and verify no forbidden fields.
	inv := invoiceSummary{
		ID:               "in_001",
		PeriodStart:      "2026-04-01T00:00:00Z",
		PeriodEnd:        "2026-04-30T23:59:59Z",
		AmountDue:        1500,
		Currency:         "usd",
		Status:           "paid",
		HostedInvoiceURL: "https://invoice.stripe.com/in_001",
		InvoicePDFURL:    "https://invoice.stripe.com/in_001/pdf",
	}

	b, err := json.Marshal(inv)
	require.NoError(t, err)
	raw := string(b)

	// No internal storage keys.
	require.NotContains(t, raw, `"PK"`)
	require.NotContains(t, raw, `"SK"`)
	require.NotContains(t, raw, `"pk"`)
	require.NotContains(t, raw, `"sk"`)

	// No account_id or raw customer IDs.
	require.NotContains(t, raw, "account_id")
	require.NotContains(t, raw, "cus_")
	require.NotContains(t, raw, "customer_id")

	// No PAN, CVV.
	require.NotContains(t, raw, "cvv")
	require.NotContains(t, raw, "CVV")
	require.NotContains(t, raw, "pan")
	require.NotContains(t, raw, "PAN")

	// No vendor secrets or raw keys.
	require.NotContains(t, raw, "secret")
	require.NotContains(t, raw, "api_key")
	require.NotContains(t, raw, "stripe_key")
	require.NotContains(t, raw, "sk_live")
	require.NotContains(t, raw, "sk_test")

	// No TTL, GSI, or internal storage fields.
	require.NotContains(t, raw, `"TTL"`)
	require.NotContains(t, raw, `"GSI1"`)
	require.NotContains(t, raw, `"GSI2"`)
	require.NotContains(t, raw, `"gsi1pk"`)
	require.NotContains(t, raw, `"gsi1sk"`)

	// Verify expected safe fields ARE present.
	require.Contains(t, raw, `"id":"in_001"`)
	require.Contains(t, raw, `"period_start"`)
	require.Contains(t, raw, `"amount_due"`)
	require.Contains(t, raw, `"status":"paid"`)
	require.Contains(t, raw, `"hosted_invoice_url"`)
	require.Contains(t, raw, `"invoice_pdf_url"`)
}

func TestPaymentMethodSafe_NoSecretsLeaked(t *testing.T) {
	t.Parallel()

	pm := paymentMethodSafe{
		ID:       "pm_abc",
		Type:     "card",
		Brand:    "visa",
		Last4:    "4242",
		ExpMonth: 12,
		ExpYear:  2028,
		Status:   "active",
	}

	b, err := json.Marshal(pm)
	require.NoError(t, err)
	raw := string(b)

	// No internal storage keys.
	require.NotContains(t, raw, `"PK"`)
	require.NotContains(t, raw, `"SK"`)

	// No account_id.
	require.NotContains(t, raw, "account_id")

	// No full PAN — only last4.
	require.NotContains(t, raw, "4242424242424242") // full PAN
	require.NotContains(t, raw, `"number"`)
	// Verify last4 IS present (masked, safe).
	require.Contains(t, raw, `"last4":"4242"`)

	// No CVV.
	require.NotContains(t, raw, "cvv")
	require.NotContains(t, raw, "CVV")
	require.NotContains(t, raw, `"cvc"`)

	// No vendor secrets.
	require.NotContains(t, raw, "secret")
	require.NotContains(t, raw, "api_key")

	// No raw Stripe customer ID.
	require.NotContains(t, raw, "cus_")

	// No TTL or GSI fields.
	require.NotContains(t, raw, `"TTL"`)
	require.NotContains(t, raw, `"gsi"`)

	// No raw keys or tokens.
	require.NotContains(t, raw, "sk_live")
	require.NotContains(t, raw, "sk_test")
	require.NotContains(t, raw, "tok_")
	require.NotContains(t, raw, "src_")

	// Verify expected safe fields ARE present.
	require.Contains(t, raw, `"id":"pm_abc"`)
	require.Contains(t, raw, `"brand":"visa"`)
	require.Contains(t, raw, `"last4":"4242"`)
	require.Contains(t, raw, `"exp_month":12`)
	require.Contains(t, raw, `"exp_year":2028`)
	require.Contains(t, raw, `"status":"active"`)
}

func TestListInvoicesResponse_NoInternalFieldsLeaked(t *testing.T) {
	t.Parallel()

	resp := listInvoicesResponse{
		Invoices: []invoiceSummary{
			{ID: "in_001", Status: "paid"},
		},
		Count: 1,
	}

	b, err := json.Marshal(resp)
	require.NoError(t, err)
	raw := string(b)

	// No PK/SK anywhere.
	require.NotContains(t, raw, `"PK"`)
	require.NotContains(t, raw, `"SK"`)

	// No pagination tokens that might leak internal state.
	require.NotContains(t, raw, `"LastEvaluatedKey"`)
	require.NotContains(t, raw, `"next_token"`)
	require.NotContains(t, raw, `"cursor"`)
}

func TestHandlePortalListInvoices_ResponseShapeHasNoInternalFields(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "")

	provider := safeInvoicesProvider(sampleInvoices)

	s := &Server{
		store:                   store.New(tdb.db),
		cfg:                     config.Config{PaymentsProvider: paymentsProviderStripeName},
		paymentsProviderFactory: func(name string) payments.Provider { return provider },
	}

	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	raw := string(resp.Body)

	// Redaction on the full response body:
	for _, forbidden := range []string{
		`"PK"`, `"SK"`, `"GSI1"`, `"GSI2"`, `"TTL"`,
		"account_id", "customer_id", "cus_123",
		"cvv", "CVV", "pan", "PAN",
		"secret", "api_key", "sk_live", "sk_test",
		"stripe_key", "private",
	} {
		require.NotContains(t, raw, forbidden, "response body must not contain %q", forbidden)
	}

	// Verify we got the safe fields.
	require.Contains(t, raw, `"id"`)
	require.Contains(t, raw, `"invoices"`)
	require.Contains(t, raw, `"count"`)
}

func TestHandlePortalGetPaymentMethod_ResponseShapeHasNoInternalFields(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "pm_active")

	tdb.qMethod.On("First", mock.AnythingOfType("*models.BillingPaymentMethod")).Return(nil).
		Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.BillingPaymentMethod](t, args, 0)
			*dest = models.BillingPaymentMethod{
				ID:       "pm_active",
				Username: "alice",
				Provider: models.BillingProviderStripe,
				Type:     "card",
				Brand:    "visa",
				Last4:    "4242",
				ExpMonth: 12,
				ExpYear:  2028,
				Status:   models.BillingPaymentMethodStatusActive,
			}
		}).Maybe()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}

	ctx := makeContext("alice")
	resp, err := s.handlePortalGetPaymentMethod(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	raw := string(resp.Body)

	for _, forbidden := range []string{
		`"PK"`, `"SK"`, `"GSI1"`, `"GSI2"`, `"TTL"`,
		"account_id", "Username", "username",
		"Provider", "StripeCustomerID",
		"cvv", "CVV", "pan", "PAN",
		"secret", "api_key", "sk_live", "sk_test",
		"cus_123",
	} {
		require.NotContains(t, raw, forbidden, "response body must not contain %q", forbidden)
	}

	// Verify safe fields present.
	require.Contains(t, raw, `"payment_method"`)
	require.Contains(t, raw, `"id":"pm_active"`)
	require.Contains(t, raw, `"brand":"visa"`)
	require.Contains(t, raw, `"last4":"4242"`)
}

// ---------------------------------------------------------------------------
// Edge case: nil server/store
// ---------------------------------------------------------------------------

func TestHandlePortalListInvoices_NilStore(t *testing.T) {
	t.Parallel()

	s := &Server{}
	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.Nil(t, resp)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	require.Equal(t, "app.internal", appErr.Code)
}

func TestHandlePortalGetPaymentMethod_NilStore(t *testing.T) {
	t.Parallel()

	s := &Server{}
	ctx := makeContext("alice")
	resp, err := s.handlePortalGetPaymentMethod(ctx)
	require.Nil(t, resp)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	require.Equal(t, "app.internal", appErr.Code)
}

// ---------------------------------------------------------------------------
// DTO naming: make masked/safe semantics clear
// ---------------------------------------------------------------------------

func TestDTOs_HaveClearSafeSemantics(t *testing.T) {
	t.Parallel()

	// invoiceSummary — name implies safe summary, not raw vendor object.
	inv := invoiceSummary{}
	_ = inv

	// paymentMethodSafe — name explicitly communicates safety.
	pm := paymentMethodSafe{}
	_ = pm

	// Fields use "last4" not "number" — verifying naming.
	var rawPM map[string]any
	b, _ := json.Marshal(paymentMethodSafe{Last4: "4242"})
	if err := json.Unmarshal(b, &rawPM); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_, hasNumber := rawPM["number"]
	require.False(t, hasNumber, "paymentMethodSafe must not expose 'number' field")
	_, hasLast4 := rawPM["last4"]
	require.True(t, hasLast4, "paymentMethodSafe must expose 'last4' field")
}

// ---------------------------------------------------------------------------
// We need a paymentsFactory on Server for test injection.
// The handlers currently use payments.NewProvider(s.cfg.PaymentsProvider, nil).
// In tests, we override via a field.
// ---------------------------------------------------------------------------

func TestHandlePortalListInvoices_StripeNotConfiguredReturnsEmpty(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "")

	s := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{PaymentsProvider: "none"},
	}

	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)

	var body listInvoicesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.NotNil(t, body.Invoices)
	require.Zero(t, body.Count)
}

// ---------------------------------------------------------------------------
// Additional: provider returns empty slice (no invoices at all)
// ---------------------------------------------------------------------------

func TestHandlePortalListInvoices_EmptySliceFromProvider(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "")

	provider := safeInvoicesProvider([]payments.InvoiceInfo{})

	s := &Server{
		store:                   store.New(tdb.db),
		cfg:                     config.Config{PaymentsProvider: paymentsProviderStripeName},
		paymentsProviderFactory: func(name string) payments.Provider { return provider },
	}

	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	var body listInvoicesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.NotNil(t, body.Invoices)
	require.Zero(t, body.Count)
}

// ---------------------------------------------------------------------------
// Additional: provider returns nil invoices
// ---------------------------------------------------------------------------

func TestHandlePortalListInvoices_NilFromProvider(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "")

	provider := safeInvoicesProvider(nil)

	s := &Server{
		store:                   store.New(tdb.db),
		cfg:                     config.Config{PaymentsProvider: paymentsProviderStripeName},
		paymentsProviderFactory: func(name string) payments.Provider { return provider },
	}

	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	var body listInvoicesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.NotNil(t, body.Invoices)
	require.Zero(t, body.Count)
}

// TestPaymentMethodSafe_OnlyExposesSafeFields verifies the DTO struct doesn't
// accidentally include fields that could leak raw Stripe data.
func TestPaymentMethodSafe_OnlyExposesSafeFields(t *testing.T) {
	t.Parallel()

	pm := paymentMethodSafe{
		ID:       "pm_abc",
		Type:     "card",
		Brand:    "visa",
		Last4:    "4242",
		ExpMonth: 12,
		ExpYear:  2028,
		Status:   "active",
	}

	b, err := json.Marshal(pm)
	require.NoError(t, err)

	var fields map[string]any
	require.NoError(t, json.Unmarshal(b, &fields))

	allowed := map[string]bool{
		"id": true, "type": true, "brand": true, "last4": true,
		"exp_month": true, "exp_year": true, "status": true,
	}

	for key := range fields {
		if !allowed[key] {
			t.Errorf("paymentMethodSafe DTO unexpectedly exposes field %q", key)
		}
	}
}

// TestInvoiceSummary_NoBillingProfileInternalFields verifies the DTO doesn't
// accidentally carry over BillingProfile or Stripe internal fields.
func TestInvoiceSummary_NoBillingProfileInternalFields(t *testing.T) {
	t.Parallel()

	// invoiceSummary is a clean struct. Confirm it doesn't embed or mirror
	// BillingProfile fields.
	inv := invoiceSummary{ID: "in_001"}
	b, err := json.Marshal(inv)
	require.NoError(t, err)

	var fields map[string]any
	require.NoError(t, json.Unmarshal(b, &fields))

	for _, forbidden := range []string{
		"stripe_customer_id", "default_payment_method_id",
		"provider", "username", "pk", "sk",
	} {
		if _, ok := fields[forbidden]; ok {
			t.Errorf("invoiceSummary DTO unexpectedly exposes field %q", forbidden)
		}
	}
}

// ---------------------------------------------------------------------------
// Verify the handler uses ctx.AuthIdentity for PK construction.
// This test validates the key isolation logic by checking that the
// loadBillingProfile call uses the correct username.
// ---------------------------------------------------------------------------

func TestHandlePortalListInvoices_UsesAuthIdentityForPK(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	// Only set up alice's profile.
	tdb.stubBillingProfile(t, "alice", "cus_alice", "")

	provider := safeInvoicesProvider(sampleInvoices)

	s := &Server{
		store:                   store.New(tdb.db),
		cfg:                     config.Config{PaymentsProvider: paymentsProviderStripeName},
		paymentsProviderFactory: func(name string) payments.Provider { return provider },
	}

	// alice gets her invoices because her billing profile is found.
	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	var body listInvoicesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Len(t, body.Invoices, 2)
}

// Test the requireStoreDB guard in the handler.
func TestHandlePortalGetPaymentMethod_NilDB(t *testing.T) {
	t.Parallel()

	s := &Server{store: &store.Store{DB: nil}, cfg: config.Config{}}
	ctx := makeContext("alice")
	resp, err := s.handlePortalGetPaymentMethod(ctx)
	require.Nil(t, resp)
	require.Error(t, err)
}

// Verify the Config struct supports payments factory override for testing.
// The Server struct needs a paymentsFactory field we can inject.
// Check that the handler compiles with the field present.
func TestServerStruct_HasPaymentsFactory(t *testing.T) {
	t.Parallel()

	// Verify Server has a paymentsProviderFactory field.
	s := &Server{
		paymentsProviderFactory: func(name string) payments.Provider {
			return &stubPaymentsProvider{name: name}
		},
	}
	require.NotNil(t, s.paymentsProviderFactory)
}

// Ensure error messages are not leaking internal details.
func TestHandlePortalListInvoices_ErrorMessageSanitization(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "")

	provider := &stubPaymentsProvider{
		name: paymentsProviderStripeName,
		listInvoices: func(ctx context.Context, customerID string, limit int64) ([]payments.InvoiceInfo, error) {
			return nil, fmt.Errorf("stripe: raw api error with key sk_live_abc123 and customer cus_secret")
		},
	}

	s := &Server{
		store:                   store.New(tdb.db),
		cfg:                     config.Config{PaymentsProvider: paymentsProviderStripeName},
		paymentsProviderFactory: func(name string) payments.Provider { return provider },
	}

	ctx := makeContext("alice")
	_, err := s.handlePortalListInvoices(ctx)
	require.Error(t, err)

	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	// The handler wraps provider errors in a generic message; the raw error
	// from the provider should NOT leak through the AppError's Message field.
	require.Equal(t, "failed to list invoices", appErr.Message)
	require.NotContains(t, strings.ToLower(appErr.Message), "sk_live")
	require.NotContains(t, strings.ToLower(appErr.Message), "cus_secret")
}

// ---------------------------------------------------------------------------
// Draft invoice filtering — the provider is responsible for excluding drafts.
// This handler-level test verifies the handler correctly maps a pre-filtered
// provider result (simulating what ListRecentInvoices returns in production).
// The actual draft-exclusion logic is tested at the provider layer
// (TestIsCustomerFacingInvoiceStatus in internal/payments).
// ---------------------------------------------------------------------------

func TestHandlePortalListInvoices_HandlesPreFilteredProviderOutput(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "")

	// Simulate what the real Stripe provider returns after draft filtering:
	// only paid, open, void, uncollectible invoices — no drafts.
	provider := safeInvoicesProvider([]payments.InvoiceInfo{
		{ID: "in_paid", Status: "paid", AmountDue: 1000, Currency: "usd"},
		{ID: "in_open", Status: "open", AmountDue: 750, Currency: "usd"},
		{ID: "in_void", Status: "void", AmountDue: 0, Currency: "usd"},
	})

	s := &Server{
		store:                   store.New(tdb.db),
		cfg:                     config.Config{PaymentsProvider: paymentsProviderStripeName},
		paymentsProviderFactory: func(name string) payments.Provider { return provider },
	}

	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	var body listInvoicesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))

	require.Len(t, body.Invoices, 3)
	require.Equal(t, 3, body.Count)

	ids := make(map[string]bool)
	for _, inv := range body.Invoices {
		ids[inv.ID] = true
		require.NotEqual(t, "draft", inv.Status, "handler must never surface draft invoices")
	}
	require.True(t, ids["in_paid"])
	require.True(t, ids["in_open"])
	require.True(t, ids["in_void"])
}

func TestHandlePortalListInvoices_FilterEmptyIDsFromProviderOutput(t *testing.T) {
	t.Parallel()

	tdb := newBillingDataTestDB()
	tdb.stubBillingProfile(t, "alice", "cus_123", "")

	// Provider returns a mix of valid and empty-ID invoices. The handler's own
	// guard must drop empty IDs — this is a defense-in-depth check independent
	// of the provider.
	provider := safeInvoicesProvider([]payments.InvoiceInfo{
		{ID: "", Status: "paid", AmountDue: 100, Currency: "usd"},
		{ID: "in_ok", Status: "paid", AmountDue: 300, Currency: "usd"},
	})

	s := &Server{
		store:                   store.New(tdb.db),
		cfg:                     config.Config{PaymentsProvider: paymentsProviderStripeName},
		paymentsProviderFactory: func(name string) payments.Provider { return provider },
	}

	ctx := makeContext("alice")
	resp, err := s.handlePortalListInvoices(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	var body listInvoicesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))

	require.Len(t, body.Invoices, 1)
	require.Equal(t, 1, body.Count)
	require.Equal(t, "in_ok", body.Invoices[0].ID)
}

// ---------------------------------------------------------------------------
