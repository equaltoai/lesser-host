package payments

import (
	"context"
	"testing"
)

func TestNormalizeProviderName(t *testing.T) {
	t.Parallel()

	if got := normalizeProviderName(""); got != "none" {
		t.Fatalf("expected none, got %q", got)
	}
	if got := normalizeProviderName("  STRIPE "); got != "stripe" {
		t.Fatalf("expected stripe, got %q", got)
	}
}

func TestNewProviderAndNoopProvider(t *testing.T) {
	t.Parallel()

	if got := NewProvider("", nil).Name(); got != "none" {
		t.Fatalf("expected none, got %q", got)
	}
	if got := NewProvider("stripe", nil).Name(); got != "stripe" {
		t.Fatalf("expected stripe, got %q", got)
	}

	p := NewProvider("none", nil)
	if _, err := p.EnsureCustomer(context.Background(), EnsureCustomerInput{}); err != ErrNotConfigured {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
	if _, err := p.CreateCreditsCheckout(context.Background(), CreditsCheckoutInput{}); err != ErrNotConfigured {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
	if _, err := p.CreateSetupCheckout(context.Background(), SetupCheckoutInput{}); err != ErrNotConfigured {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
	if _, err := p.ParseWebhookEvent(context.Background(), nil, nil); err != ErrNotConfigured {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
	if _, err := p.ResolveSetupPaymentMethod(context.Background(), ""); err != ErrNotConfigured {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

func TestStripeHeaderValue(t *testing.T) {
	t.Parallel()

	h := map[string][]string{
		"Stripe-Signature": {" sig "},
	}
	if got := headerValue(h, "stripe-signature"); got != "sig" {
		t.Fatalf("expected sig, got %q", got)
	}
	if got := headerValue(h, "missing"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}
