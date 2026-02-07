package payments

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/stripe/stripe-go/v79"
	"github.com/stripe/stripe-go/v79/webhook"
)

type stubSSM struct {
	values map[string]string
}

func (s stubSSM) GetParameter(_ context.Context, params *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if params == nil || params.Name == nil {
		return nil, errors.New("missing name")
	}
	name := aws.ToString(params.Name)
	v, ok := s.values[name]
	if !ok {
		return nil, errors.New("parameter not found")
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{Value: aws.String(v)},
	}, nil
}

func parseFormBody(t *testing.T, r *http.Request) url.Values {
	t.Helper()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	_ = r.Body.Close()

	vals, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	return vals
}

func TestStripeProvider_HTTPFlows_AreCISafe(t *testing.T) {
	t.Setenv("STAGE", "payments-test")

	secretParam := "/lesser-host/stripe/payments-test/secret"
	webhookParam := "/lesser-host/stripe/payments-test/webhook"

	ssmClient := stubSSM{
		values: map[string]string{
			secretParam:  " sk_test ",
			webhookParam: " whsec_test ",
		},
	}

	var gotCustomer url.Values
	var gotCreditsCheckout url.Values
	var gotSetupCheckout url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/customers":
			gotCustomer = parseFormBody(t, r)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"cus_123","object":"customer"}`)
			return

		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
			vals := parseFormBody(t, r)
			mode := vals.Get("mode")
			w.Header().Set("Content-Type", "application/json")

			switch mode {
			case "payment":
				gotCreditsCheckout = vals
				resp := map[string]any{
					"id":     "cs_credits",
					"object": "checkout.session",
					"url":    "https://stripe.test/checkout/credits",
					"mode":   "payment",
					"customer": map[string]any{
						"id":     vals.Get("customer"),
						"object": "customer",
					},
					"payment_intent": map[string]any{
						"id":     "pi_123",
						"object": "payment_intent",
					},
					"amount_total": 1234,
					"currency":     "usd",
					"metadata": map[string]string{
						"purchase_id":   vals.Get("metadata[purchase_id]"),
						"username":      vals.Get("metadata[username]"),
						"instance_slug": vals.Get("metadata[instance_slug]"),
						"month":         vals.Get("metadata[month]"),
						"credits":       vals.Get("metadata[credits]"),
					},
					"expires_at": time.Now().Add(10 * time.Minute).Unix(),
				}
				_ = json.NewEncoder(w).Encode(resp)
				return

			case "setup":
				gotSetupCheckout = vals
				resp := map[string]any{
					"id":     "cs_setup",
					"object": "checkout.session",
					"url":    "https://stripe.test/checkout/setup",
					"mode":   "setup",
					"customer": map[string]any{
						"id":     vals.Get("customer"),
						"object": "customer",
					},
					"setup_intent": map[string]any{
						"id":     "seti_123",
						"object": "setup_intent",
					},
					"metadata": map[string]string{
						"username": vals.Get("metadata[username]"),
						"purpose":  vals.Get("metadata[purpose]"),
					},
					"expires_at": time.Now().Add(10 * time.Minute).Unix(),
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			default:
				http.Error(w, "unexpected mode", http.StatusBadRequest)
				return
			}

		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/setup_intents/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"seti_123","object":"setup_intent","payment_method":{"id":"pm_123","object":"payment_method"}}`)
			return

		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/payment_methods/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"pm_123","object":"payment_method","type":"card","card":{"brand":"visa","last4":"4242","exp_month":12,"exp_year":2030}}`)
			return
		default:
			http.NotFound(w, r)
			return
		}
	}))
	t.Cleanup(srv.Close)

	oldBackend := stripe.GetBackend(stripe.APIBackend)
	t.Cleanup(func() { stripe.SetBackend(stripe.APIBackend, oldBackend) })
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL: stripe.String(srv.URL),
	}))

	oldKey := stripe.Key
	t.Cleanup(func() { stripe.Key = oldKey })

	p := stripeProvider{ssmClient: ssmClient}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	custID, err := p.EnsureCustomer(ctx, EnsureCustomerInput{Username: " u ", Email: " e@example.com "})
	if err != nil {
		t.Fatalf("EnsureCustomer: %v", err)
	}
	if custID != "cus_123" {
		t.Fatalf("expected customer id cus_123, got %q", custID)
	}
	if gotCustomer.Get("email") != "e@example.com" || gotCustomer.Get("metadata[username]") != "u" {
		t.Fatalf("unexpected customer params: %#v", gotCustomer)
	}

	creditsSess, err := p.CreateCreditsCheckout(ctx, CreditsCheckoutInput{
		CustomerID:   custID,
		PurchaseID:   "p1",
		Username:     " u ",
		InstanceSlug: " inst ",
		Month:        "2026-02",
		Credits:      10,
		AmountCents:  1234,
		SuccessURL:   "https://example.com/success",
		CancelURL:    "https://example.com/cancel",
	})
	if err != nil {
		t.Fatalf("CreateCreditsCheckout: %v", err)
	}
	if creditsSess == nil || creditsSess.ID != "cs_credits" || creditsSess.CustomerID != custID || creditsSess.PaymentIntentID != "pi_123" {
		t.Fatalf("unexpected credits session: %#v", creditsSess)
	}
	if gotCreditsCheckout.Get("mode") != "payment" || gotCreditsCheckout.Get("line_items[0][price_data][currency]") != "usd" {
		t.Fatalf("unexpected checkout params: %#v", gotCreditsCheckout)
	}
	if gotCreditsCheckout.Get("metadata[instance_slug]") != "inst" || gotCreditsCheckout.Get("metadata[credits]") != "10" {
		t.Fatalf("unexpected checkout metadata: %#v", gotCreditsCheckout)
	}

	setupSess, err := p.CreateSetupCheckout(ctx, SetupCheckoutInput{
		CustomerID: custID,
		Username:   " u ",
		SuccessURL: "https://example.com/success",
		CancelURL:  "https://example.com/cancel",
	})
	if err != nil {
		t.Fatalf("CreateSetupCheckout: %v", err)
	}
	if setupSess == nil || setupSess.ID != "cs_setup" || setupSess.CustomerID != custID || setupSess.SetupIntentID != "seti_123" {
		t.Fatalf("unexpected setup session: %#v", setupSess)
	}
	if gotSetupCheckout.Get("metadata[purpose]") != "overage_payment_method" {
		t.Fatalf("unexpected setup metadata: %#v", gotSetupCheckout)
	}

	pm, err := p.ResolveSetupPaymentMethod(ctx, "seti_123")
	if err != nil {
		t.Fatalf("ResolveSetupPaymentMethod: %v", err)
	}
	if pm == nil || pm.ID != "pm_123" || pm.Type != "card" || pm.Brand != "visa" || pm.Last4 != "4242" {
		t.Fatalf("unexpected payment method: %#v", pm)
	}
}

func TestStripeProvider_ParseWebhookEvent_ValidatesAndFilters(t *testing.T) {
	t.Setenv("STAGE", "payments-test-webhook")

	secretParam := "/lesser-host/stripe/payments-test-webhook/secret"
	webhookParam := "/lesser-host/stripe/payments-test-webhook/webhook"

	ssmClient := stubSSM{
		values: map[string]string{
			secretParam:  "sk_test",
			webhookParam: "whsec_test",
		},
	}

	p := stripeProvider{ssmClient: ssmClient}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sessObj := map[string]any{
		"id":     "cs_123",
		"object": "checkout.session",
		"mode":   "payment",
		"customer": map[string]any{
			"id":     "cus_123",
			"object": "customer",
		},
		"payment_intent": map[string]any{
			"id":     "pi_123",
			"object": "payment_intent",
		},
		"amount_total": 500,
		"currency":     "usd",
		"metadata":     map[string]string{"username": "u"},
		"expires_at":   time.Now().Add(10 * time.Minute).Unix(),
	}

	bodyBytes, err := json.Marshal(map[string]any{
		"id":      "evt_1",
		"object":  "event",
		"api_version": stripe.APIVersion,
		"type":    "checkout.session.completed",
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": sessObj,
		},
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   bodyBytes,
		Secret:    "whsec_test",
		Timestamp: time.Now(),
		Scheme:    "v1",
	})

	got, err := p.ParseWebhookEvent(ctx, map[string][]string{
		"Stripe-Signature": {signed.Header},
	}, bodyBytes)
	if err != nil {
		t.Fatalf("ParseWebhookEvent: %v", err)
	}
	if got == nil || got.Type != "checkout.session.completed" || got.Session.ID != "cs_123" || got.Session.PaymentIntentID != "pi_123" {
		t.Fatalf("unexpected webhook event: %#v", got)
	}

	unsupportedBody, err := json.Marshal(map[string]any{
		"id":      "evt_2",
		"object":  "event",
		"api_version": stripe.APIVersion,
		"type":    "customer.created",
		"created": time.Now().Unix(),
		"data": map[string]any{
			"object": map[string]any{"id": "cus_x"},
		},
	})
	if err != nil {
		t.Fatalf("marshal unsupported body: %v", err)
	}
	signed2 := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   unsupportedBody,
		Secret:    "whsec_test",
		Timestamp: time.Now(),
		Scheme:    "v1",
	})

	ignored, err := p.ParseWebhookEvent(ctx, map[string][]string{
		"Stripe-Signature": {signed2.Header},
	}, unsupportedBody)
	if err != nil {
		t.Fatalf("ParseWebhookEvent unsupported: %v", err)
	}
	if ignored != nil {
		t.Fatalf("expected unsupported webhook event ignored, got %#v", ignored)
	}

	if _, err := p.ParseWebhookEvent(ctx, map[string][]string{}, bodyBytes); err == nil {
		t.Fatalf("expected error for missing signature header")
	}
}

func TestStripeProvider_ParseWebhookEvent_RequiresConfig(t *testing.T) {
	t.Setenv("STAGE", "payments-test-missing")

	p := stripeProvider{ssmClient: stubSSM{values: map[string]string{}}}
	if _, err := p.ParseWebhookEvent(context.Background(), map[string][]string{}, nil); err == nil {
		t.Fatalf("expected config error")
	}
}

func TestStripeProvider_ResolveSetupPaymentMethod_ValidatesInput(t *testing.T) {
	t.Setenv("STAGE", "payments-test-validate-id")

	secretParam := "/lesser-host/stripe/payments-test-validate-id/secret"
	oldKey := stripe.Key
	t.Cleanup(func() { stripe.Key = oldKey })

	p := stripeProvider{ssmClient: stubSSM{values: map[string]string{
		secretParam: "sk_test",
	}}}
	if _, err := p.ResolveSetupPaymentMethod(context.Background(), " "); err == nil {
		t.Fatalf("expected error for empty setup intent id")
	}
}

func TestStripeProvider_ParseWebhookEvent_HeaderValueHandlesEmptyValues(t *testing.T) {
	t.Parallel()

	h := map[string][]string{
		"Stripe-Signature": {},
	}
	if got := headerValue(h, "stripe-signature"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestStripeProvider_ParseWebhookEvent_TrimsAndRejectsEmptySecret(t *testing.T) {
	t.Setenv("STAGE", "payments-test-empty-secret")

	secretParam := "/lesser-host/stripe/payments-test-empty-secret/secret"
	webhookParam := "/lesser-host/stripe/payments-test-empty-secret/webhook"
	legacyWebhookParam := "/lesser-host/api/stripe/webhook"

	p := stripeProvider{ssmClient: stubSSM{values: map[string]string{
		secretParam:  "sk_test",
		webhookParam: "   ",
		legacyWebhookParam: "   ",
	}}}

	if _, err := p.ParseWebhookEvent(context.Background(), map[string][]string{}, nil); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("expected empty-secret error, got %v", err)
	}
}

func TestStripeProvider_EnsureCustomer_ErrorsWhenNotConfigured(t *testing.T) {
	t.Setenv("STAGE", "payments-test-not-configured")

	secretParam := "/lesser-host/stripe/payments-test-not-configured/secret"
	legacySecretParam := "/lesser-host/api/stripe/secret"

	p := stripeProvider{ssmClient: stubSSM{values: map[string]string{
		secretParam: "   ",
		legacySecretParam: "   ",
	}}}

	if _, err := p.EnsureCustomer(context.Background(), EnsureCustomerInput{}); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("expected empty-secret error, got %v", err)
	}
}

func TestStripeProvider_ParseWebhookEvent_RejectsInvalidSignature(t *testing.T) {
	t.Setenv("STAGE", "payments-test-bad-sig")

	secretParam := "/lesser-host/stripe/payments-test-bad-sig/secret"
	webhookParam := "/lesser-host/stripe/payments-test-bad-sig/webhook"

	p := stripeProvider{ssmClient: stubSSM{values: map[string]string{
		secretParam:  "sk_test",
		webhookParam: "whsec_test",
	}}}

	body := []byte(`{"id":"evt_1","object":"event","type":"checkout.session.completed","data":{"object":{"id":"cs_1"}}}`)
	if _, err := p.ParseWebhookEvent(context.Background(), map[string][]string{
		"Stripe-Signature": {"t=123,v1=bad"},
	}, body); err == nil {
		t.Fatalf("expected signature validation error")
	}
}

func TestStripeProvider_ParseWebhookEvent_HandlesBadJSON(t *testing.T) {
	t.Setenv("STAGE", "payments-test-bad-json")

	secretParam := "/lesser-host/stripe/payments-test-bad-json/secret"
	webhookParam := "/lesser-host/stripe/payments-test-bad-json/webhook"

	p := stripeProvider{ssmClient: stubSSM{values: map[string]string{
		secretParam:  "sk_test",
		webhookParam: "whsec_test",
	}}}

	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload:   []byte("{"),
		Secret:    "whsec_test",
		Timestamp: time.Now(),
		Scheme:    "v1",
	})
	if _, err := p.ParseWebhookEvent(context.Background(), map[string][]string{
		"Stripe-Signature": {signed.Header},
	}, []byte("{")); err == nil {
		t.Fatalf("expected json error")
	}
}

func TestStripeProvider_HeaderValue_TrimsKeyAndValue(t *testing.T) {
	t.Parallel()

	h := map[string][]string{" Stripe-Signature ": {"  sig  "}}
	if got := headerValue(h, " stripe-signature "); got != "sig" {
		t.Fatalf("expected sig, got %q", got)
	}
}
