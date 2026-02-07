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
	"github.com/stretchr/testify/require"
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

type stripeTestHandler struct {
	t *testing.T

	gotCustomer        *url.Values
	gotCreditsCheckout *url.Values
	gotSetupCheckout   *url.Values
}

func (h *stripeTestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/customers":
		h.handleCustomers(w, r)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
		h.handleCheckoutSessions(w, r)
		return
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/setup_intents/"):
		h.writeJSON(w, `{"id":"seti_123","object":"setup_intent","payment_method":{"id":"pm_123","object":"payment_method"}}`)
		return
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/payment_methods/"):
		h.writeJSON(w, `{"id":"pm_123","object":"payment_method","type":"card","card":{"brand":"visa","last4":"4242","exp_month":12,"exp_year":2030}}`)
		return
	default:
		http.NotFound(w, r)
		return
	}
}

func (h *stripeTestHandler) handleCustomers(w http.ResponseWriter, r *http.Request) {
	*h.gotCustomer = parseFormBody(h.t, r)
	h.writeJSON(w, `{"id":"cus_123","object":"customer"}`)
}

func (h *stripeTestHandler) handleCheckoutSessions(w http.ResponseWriter, r *http.Request) {
	vals := parseFormBody(h.t, r)
	mode := vals.Get("mode")

	switch mode {
	case "payment":
		*h.gotCreditsCheckout = vals
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
		*h.gotSetupCheckout = vals
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
}

func (h *stripeTestHandler) writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
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

	handler := &stripeTestHandler{
		t:                  t,
		gotCustomer:        &gotCustomer,
		gotCreditsCheckout: &gotCreditsCheckout,
		gotSetupCheckout:   &gotSetupCheckout,
	}
	srv := httptest.NewServer(handler)
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
	require.NoError(t, err)
	require.Equal(t, "cus_123", custID)
	require.Equal(t, "e@example.com", gotCustomer.Get("email"))
	require.Equal(t, "u", gotCustomer.Get("metadata[username]"))

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
	require.NoError(t, err)
	require.NotNil(t, creditsSess)
	require.Equal(t, "cs_credits", creditsSess.ID)
	require.Equal(t, custID, creditsSess.CustomerID)
	require.Equal(t, "pi_123", creditsSess.PaymentIntentID)
	require.Equal(t, "payment", gotCreditsCheckout.Get("mode"))
	require.Equal(t, "usd", gotCreditsCheckout.Get("line_items[0][price_data][currency]"))
	require.Equal(t, "inst", gotCreditsCheckout.Get("metadata[instance_slug]"))
	require.Equal(t, "10", gotCreditsCheckout.Get("metadata[credits]"))

	setupSess, err := p.CreateSetupCheckout(ctx, SetupCheckoutInput{
		CustomerID: custID,
		Username:   " u ",
		SuccessURL: "https://example.com/success",
		CancelURL:  "https://example.com/cancel",
	})
	require.NoError(t, err)
	require.NotNil(t, setupSess)
	require.Equal(t, "cs_setup", setupSess.ID)
	require.Equal(t, custID, setupSess.CustomerID)
	require.Equal(t, "seti_123", setupSess.SetupIntentID)
	require.Equal(t, "overage_payment_method", gotSetupCheckout.Get("metadata[purpose]"))

	pm, err := p.ResolveSetupPaymentMethod(ctx, "seti_123")
	require.NoError(t, err)
	require.NotNil(t, pm)
	require.Equal(t, "pm_123", pm.ID)
	require.Equal(t, "card", pm.Type)
	require.Equal(t, "visa", pm.Brand)
	require.Equal(t, "4242", pm.Last4)
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
		"id":          "evt_1",
		"object":      "event",
		"api_version": stripe.APIVersion,
		"type":        "checkout.session.completed",
		"created":     time.Now().Unix(),
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
		"id":          "evt_2",
		"object":      "event",
		"api_version": stripe.APIVersion,
		"type":        "customer.created",
		"created":     time.Now().Unix(),
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
		secretParam:        "sk_test",
		webhookParam:       "   ",
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
		secretParam:       "   ",
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
