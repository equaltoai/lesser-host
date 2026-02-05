package payments

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Provider is a minimal provider-agnostic payments interface used by the portal.
type Provider interface {
	Name() string

	EnsureCustomer(ctx context.Context, in EnsureCustomerInput) (customerID string, err error)
	CreateCreditsCheckout(ctx context.Context, in CreditsCheckoutInput) (*CheckoutSession, error)
	CreateSetupCheckout(ctx context.Context, in SetupCheckoutInput) (*CheckoutSession, error)

	ParseWebhookEvent(ctx context.Context, headers map[string][]string, body []byte) (*WebhookEvent, error)
	ResolveSetupPaymentMethod(ctx context.Context, setupIntentID string) (*PaymentMethodDetails, error)
}

type EnsureCustomerInput struct {
	Username string
	Email    string
}

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

type SetupCheckoutInput struct {
	CustomerID string
	Username   string

	SuccessURL string
	CancelURL  string
}

type CheckoutSession struct {
	ID   string
	URL  string
	Mode string // payment|setup

	CustomerID      string
	PaymentIntentID string
	SetupIntentID   string

	AmountTotal int64
	Currency    string

	Metadata  map[string]string
	ExpiresAt time.Time
}

type WebhookEvent struct {
	Type    string
	Session CheckoutSession
}

type PaymentMethodDetails struct {
	ID    string
	Type  string
	Brand string
	Last4 string

	ExpMonth int64
	ExpYear  int64
}

// ErrNotConfigured is returned when payments are not configured.
var ErrNotConfigured = fmt.Errorf("payments not configured")

func normalizeProviderName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "none"
	}
	return name
}
