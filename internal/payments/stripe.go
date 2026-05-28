package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v79"
	checkoutsession "github.com/stripe/stripe-go/v79/checkout/session"
	"github.com/stripe/stripe-go/v79/customer"
	"github.com/stripe/stripe-go/v79/invoice"
	"github.com/stripe/stripe-go/v79/paymentmethod"
	"github.com/stripe/stripe-go/v79/setupintent"
	"github.com/stripe/stripe-go/v79/webhook"

	"github.com/equaltoai/lesser-host/internal/secrets"
)

type stripeProvider struct {
	ssmClient secrets.SSMAPI
}

func (stripeProvider) Name() string { return providerNameStripe }

func (p stripeProvider) ensureKey(ctx context.Context) error {
	key, err := secrets.StripeSecretKey(ctx, p.ssmClient)
	if err != nil {
		return err
	}
	stripe.Key = strings.TrimSpace(key)
	if stripe.Key == "" {
		return ErrNotConfigured
	}
	return nil
}

func (p stripeProvider) webhookSecret(ctx context.Context) (string, error) {
	secret, err := secrets.StripeWebhookSecret(ctx, p.ssmClient)
	if err != nil {
		return "", err
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", ErrNotConfigured
	}
	return secret, nil
}

func (p stripeProvider) EnsureCustomer(ctx context.Context, in EnsureCustomerInput) (string, error) {
	if err := p.ensureKey(ctx); err != nil {
		return "", err
	}

	params := &stripe.CustomerParams{
		Email: stripe.String(strings.TrimSpace(in.Email)),
		Metadata: map[string]string{
			"username": strings.TrimSpace(in.Username),
		},
	}
	c, err := customer.New(params)
	if err != nil {
		return "", err
	}
	if c == nil || strings.TrimSpace(c.ID) == "" {
		return "", fmt.Errorf("stripe: missing customer id")
	}
	return strings.TrimSpace(c.ID), nil
}

func (p stripeProvider) CreateCreditsCheckout(ctx context.Context, in CreditsCheckoutInput) (*CheckoutSession, error) {
	if err := p.ensureKey(ctx); err != nil {
		return nil, err
	}

	currency := strings.ToLower(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "usd"
	}

	meta := map[string]string{
		"purchase_id":   strings.TrimSpace(in.PurchaseID),
		"username":      strings.TrimSpace(in.Username),
		"instance_slug": strings.TrimSpace(in.InstanceSlug),
		"month":         strings.TrimSpace(in.Month),
		"credits":       strconv.FormatInt(in.Credits, 10),
	}

	params := &stripe.CheckoutSessionParams{
		Mode:              stripe.String(string(stripe.CheckoutSessionModePayment)),
		Customer:          stripe.String(strings.TrimSpace(in.CustomerID)),
		SuccessURL:        stripe.String(strings.TrimSpace(in.SuccessURL)),
		CancelURL:         stripe.String(strings.TrimSpace(in.CancelURL)),
		Metadata:          meta,
		ClientReferenceID: stripe.String(strings.TrimSpace(in.Username)),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Quantity: stripe.Int64(1),
				PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
					Currency:   stripe.String(currency),
					UnitAmount: stripe.Int64(in.AmountCents),
					ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
						Name: stripe.String(fmt.Sprintf("%d credits", in.Credits)),
					},
				},
			},
		},
	}

	sess, err := checkoutsession.New(params)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, fmt.Errorf("stripe: empty session response")
	}

	out := &CheckoutSession{
		ID:              strings.TrimSpace(sess.ID),
		URL:             strings.TrimSpace(sess.URL),
		Mode:            string(sess.Mode),
		Status:          strings.TrimSpace(string(sess.Status)),
		PaymentStatus:   strings.TrimSpace(string(sess.PaymentStatus)),
		CustomerID:      strings.TrimSpace(sess.Customer.ID),
		PaymentIntentID: strings.TrimSpace(sess.PaymentIntent.ID),
		AmountTotal:     sess.AmountTotal,
		Currency:        strings.TrimSpace(string(sess.Currency)),
		Metadata:        sess.Metadata,
	}
	if sess.ExpiresAt > 0 {
		out.ExpiresAt = time.Unix(sess.ExpiresAt, 0).UTC()
	}

	return out, nil
}

func (p stripeProvider) CreateSetupCheckout(ctx context.Context, in SetupCheckoutInput) (*CheckoutSession, error) {
	if err := p.ensureKey(ctx); err != nil {
		return nil, err
	}

	params := &stripe.CheckoutSessionParams{
		Mode:       stripe.String(string(stripe.CheckoutSessionModeSetup)),
		Customer:   stripe.String(strings.TrimSpace(in.CustomerID)),
		SuccessURL: stripe.String(strings.TrimSpace(in.SuccessURL)),
		CancelURL:  stripe.String(strings.TrimSpace(in.CancelURL)),
		Metadata: map[string]string{
			"username": strings.TrimSpace(in.Username),
			"purpose":  "overage_payment_method",
		},
		ClientReferenceID: stripe.String(strings.TrimSpace(in.Username)),
	}

	sess, err := checkoutsession.New(params)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, fmt.Errorf("stripe: empty session response")
	}

	out := &CheckoutSession{
		ID:            strings.TrimSpace(sess.ID),
		URL:           strings.TrimSpace(sess.URL),
		Mode:          string(sess.Mode),
		Status:        strings.TrimSpace(string(sess.Status)),
		PaymentStatus: strings.TrimSpace(string(sess.PaymentStatus)),
		CustomerID:    strings.TrimSpace(sess.Customer.ID),
		SetupIntentID: strings.TrimSpace(sess.SetupIntent.ID),
		Metadata:      sess.Metadata,
	}
	if sess.ExpiresAt > 0 {
		out.ExpiresAt = time.Unix(sess.ExpiresAt, 0).UTC()
	}
	return out, nil
}

func headerValue(headers map[string][]string, key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	for k, v := range headers {
		if strings.ToLower(strings.TrimSpace(k)) != key {
			continue
		}
		if len(v) == 0 {
			return ""
		}
		return strings.TrimSpace(v[0])
	}
	return ""
}

func (p stripeProvider) ParseWebhookEvent(ctx context.Context, headers map[string][]string, body []byte) (*WebhookEvent, error) {
	if err := p.ensureKey(ctx); err != nil {
		return nil, err
	}

	secret, err := p.webhookSecret(ctx)
	if err != nil {
		return nil, err
	}

	sig := headerValue(headers, "stripe-signature")
	if sig == "" {
		return nil, fmt.Errorf("missing stripe-signature header")
	}

	event, err := webhook.ConstructEvent(body, sig, secret)
	if err != nil {
		return nil, err
	}

	switch event.Type {
	case "checkout.session.completed", "checkout.session.expired", "checkout.session.async_payment_succeeded", "checkout.session.async_payment_failed":
	default:
		// Ignore unsupported event types.
		return nil, nil
	}

	var sess stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
		return nil, err
	}

	customerID := ""
	if sess.Customer != nil {
		customerID = strings.TrimSpace(sess.Customer.ID)
	}
	paymentIntentID := ""
	if sess.PaymentIntent != nil {
		paymentIntentID = strings.TrimSpace(sess.PaymentIntent.ID)
	}
	setupIntentID := ""
	if sess.SetupIntent != nil {
		setupIntentID = strings.TrimSpace(sess.SetupIntent.ID)
	}

	out := WebhookEvent{
		Type: string(event.Type),
		Session: CheckoutSession{
			ID:              strings.TrimSpace(sess.ID),
			URL:             strings.TrimSpace(sess.URL),
			Mode:            string(sess.Mode),
			Status:          strings.TrimSpace(string(sess.Status)),
			PaymentStatus:   strings.TrimSpace(string(sess.PaymentStatus)),
			CustomerID:      customerID,
			PaymentIntentID: paymentIntentID,
			SetupIntentID:   setupIntentID,
			AmountTotal:     sess.AmountTotal,
			Currency:        strings.TrimSpace(string(sess.Currency)),
			Metadata:        sess.Metadata,
		},
	}
	if sess.ExpiresAt > 0 {
		out.Session.ExpiresAt = time.Unix(sess.ExpiresAt, 0).UTC()
	}
	return &out, nil
}

func (p stripeProvider) ResolveSetupPaymentMethod(ctx context.Context, setupIntentID string) (*PaymentMethodDetails, error) {
	if err := p.ensureKey(ctx); err != nil {
		return nil, err
	}

	setupIntentID = strings.TrimSpace(setupIntentID)
	if setupIntentID == "" {
		return nil, fmt.Errorf("setup intent id is required")
	}

	params := &stripe.SetupIntentParams{}
	params.AddExpand("payment_method")
	si, err := setupintent.Get(setupIntentID, params)
	if err != nil {
		return nil, err
	}
	if si == nil || si.PaymentMethod == nil || strings.TrimSpace(si.PaymentMethod.ID) == "" {
		return nil, fmt.Errorf("stripe: missing payment method on setup intent")
	}

	pm, err := paymentmethod.Get(si.PaymentMethod.ID, nil)
	if err != nil {
		return nil, err
	}
	if pm == nil {
		return nil, fmt.Errorf("stripe: missing payment method")
	}

	out := &PaymentMethodDetails{
		ID:   strings.TrimSpace(pm.ID),
		Type: strings.TrimSpace(string(pm.Type)),
	}

	if pm.Card != nil {
		out.Brand = strings.TrimSpace(string(pm.Card.Brand))
		out.Last4 = strings.TrimSpace(pm.Card.Last4)
		out.ExpMonth = int64(pm.Card.ExpMonth)
		out.ExpYear = int64(pm.Card.ExpYear)
	}

	return out, nil
}

func dateStringFromUnix(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).UTC().Format(time.RFC3339)
}

// buildInvoiceListParams constructs a Stripe invoice list params object for a
// single page. Single=true prevents the iterator from auto-paginating through
// the entire customer history.
func buildInvoiceListParams(customerID string, limit int64) *stripe.InvoiceListParams {
	return &stripe.InvoiceListParams{
		ListParams: stripe.ListParams{
			Limit:  stripe.Int64(limit),
			Single: true,
		},
		Customer: stripe.String(customerID),
	}
}

// isCustomerFacingInvoiceStatus returns true for finalized invoice statuses
// (open, paid, void, uncollectible) and false for drafts and unknowns.
func isCustomerFacingInvoiceStatus(status stripe.InvoiceStatus) bool {
	switch status {
	case stripe.InvoiceStatusOpen, stripe.InvoiceStatusPaid,
		stripe.InvoiceStatusVoid, stripe.InvoiceStatusUncollectible:
		return true
	default:
		return false
	}
}

func (p stripeProvider) ListRecentInvoices(ctx context.Context, customerID string, limit int64) ([]InvoiceInfo, error) {
	if err := p.ensureKey(ctx); err != nil {
		return nil, err
	}

	customerID = strings.TrimSpace(customerID)
	if customerID == "" {
		return nil, fmt.Errorf("customer id is required")
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}

	params := buildInvoiceListParams(customerID, limit)

	iter := invoice.List(params)
	var out []InvoiceInfo
	for iter.Next() {
		inv := iter.Invoice()
		if inv == nil || strings.TrimSpace(inv.ID) == "" {
			continue
		}
		// Exclude draft invoices — they are not customer-facing.
		if !isCustomerFacingInvoiceStatus(inv.Status) {
			continue
		}
		out = append(out, InvoiceInfo{
			ID:          strings.TrimSpace(inv.ID),
			PeriodStart: dateStringFromUnix(inv.PeriodStart),
			PeriodEnd:   dateStringFromUnix(inv.PeriodEnd),
			AmountDue:   inv.AmountDue,
			Currency:    strings.TrimSpace(string(inv.Currency)),
			Status:      strings.TrimSpace(string(inv.Status)),
			HostedURL:   strings.TrimSpace(inv.HostedInvoiceURL),
			PDFURL:      strings.TrimSpace(inv.InvoicePDF),
		})
		// Defensive break: never exceed the requested limit even if filtering
		// changes the effective count from a page.
		if len(out) >= int(limit) {
			break
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}

	return out, nil
}
