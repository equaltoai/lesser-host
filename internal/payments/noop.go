package payments

import "context"

type noopProvider struct{}

func (noopProvider) Name() string { return providerNameNone }

func (noopProvider) EnsureCustomer(ctx context.Context, in EnsureCustomerInput) (string, error) {
	_ = ctx
	_ = in
	return "", ErrNotConfigured
}

func (noopProvider) CreateCreditsCheckout(ctx context.Context, in CreditsCheckoutInput) (*CheckoutSession, error) {
	_ = ctx
	_ = in
	return nil, ErrNotConfigured
}

func (noopProvider) CreateSetupCheckout(ctx context.Context, in SetupCheckoutInput) (*CheckoutSession, error) {
	_ = ctx
	_ = in
	return nil, ErrNotConfigured
}

func (noopProvider) ParseWebhookEvent(ctx context.Context, headers map[string][]string, body []byte) (*WebhookEvent, error) {
	_ = ctx
	_ = headers
	_ = body
	return nil, ErrNotConfigured
}

func (noopProvider) ResolveSetupPaymentMethod(ctx context.Context, setupIntentID string) (*PaymentMethodDetails, error) {
	_ = ctx
	_ = setupIntentID
	return nil, ErrNotConfigured
}
