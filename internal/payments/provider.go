package payments

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	providerNameNone   = "none"
	providerNameStripe = "stripe"
)

// Provider is a minimal provider-agnostic payments interface used by the portal.
type Provider interface {
	Name() string

	EnsureCustomer(ctx context.Context, in EnsureCustomerInput) (customerID string, err error)
	CreateCreditsCheckout(ctx context.Context, in CreditsCheckoutInput) (*CheckoutSession, error)
	CreateSetupCheckout(ctx context.Context, in SetupCheckoutInput) (*CheckoutSession, error)

	ParseWebhookEvent(ctx context.Context, headers map[string][]string, body []byte) (*WebhookEvent, error)
	ResolveSetupPaymentMethod(ctx context.Context, setupIntentID string) (*PaymentMethodDetails, error)

	// ListRecentInvoices returns the most recent invoices for a customer.
	ListRecentInvoices(ctx context.Context, customerID string, limit int64) ([]InvoiceInfo, error)
}

// EnsureCustomerInput identifies the user/customer to ensure in the provider.
type EnsureCustomerInput struct {
	Username string
	Email    string
}

// CreditsCheckoutInput configures a one-time payment checkout session for purchasing credits.
type CreditsCheckoutInput struct {
	CustomerID string

	PurchaseID   string
	Username     string
	InstanceSlug string
	Month        string
	Credits      int64

	AmountCents int64
	Currency    string

	SuccessURL string
	CancelURL  string
}

// SetupCheckoutInput configures a setup checkout session for collecting an overage payment method.
type SetupCheckoutInput struct {
	CustomerID string
	Username   string

	SuccessURL string
	CancelURL  string
}

// CheckoutSession is a provider-agnostic representation of a checkout session.
type CheckoutSession struct {
	ID   string
	URL  string
	Mode string // payment|setup

	// Stripe checkout session lifecycle status (e.g. open|complete|expired).
	Status string
	// Stripe payment status (e.g. paid|unpaid|no_payment_required).
	PaymentStatus string

	CustomerID      string
	PaymentIntentID string
	SetupIntentID   string

	AmountTotal int64
	Currency    string

	Metadata  map[string]string
	ExpiresAt time.Time
}

// WebhookEvent is a provider-agnostic representation of a webhook event carrying a checkout session payload.
type WebhookEvent struct {
	Type    string
	Session CheckoutSession
}

// PaymentMethodDetails is a normalized snapshot of a payment method attached to a customer.
type PaymentMethodDetails struct {
	ID    string
	Type  string
	Brand string
	Last4 string

	ExpMonth int64
	ExpYear  int64
}

// InvoiceInfo is a provider-agnostic invoice summary. No raw vendor objects or secrets.
type InvoiceInfo struct {
	ID          string `json:"id"`
	PeriodStart string `json:"period_start"` // ISO 8601 date (YYYY-MM-DD or full timestamp)
	PeriodEnd   string `json:"period_end"`   // ISO 8601 date
	AmountDue   int64  `json:"amount_due"`
	Currency    string `json:"currency"`
	Status      string `json:"status"` // e.g. "paid", "open", "void"
	HostedURL   string `json:"hosted_invoice_url,omitempty"`
	PDFURL      string `json:"invoice_pdf_url,omitempty"`
}

// ErrNotConfigured is returned when payments are not configured.
var ErrNotConfigured = fmt.Errorf("payments not configured")

func normalizeProviderName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return providerNameNone
	}
	return name
}
