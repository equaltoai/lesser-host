package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/stripe/stripe-go/v79"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type stubStripeSSM struct{}

func (stubStripeSSM) GetParameter(_ context.Context, params *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	name := strings.TrimSpace(aws.ToString(params.Name))
	value := "sk_test"
	if strings.Contains(name, "/webhook") {
		value = "whsec_test"
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{Value: aws.String(value)},
	}, nil
}

func seedStripeSecretsCache(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	ssmClient := stubStripeSSM{}

	if _, err := secrets.GetSSMParameterCached(ctx, ssmClient, "/lesser-host/stripe/lab/secret", 30*time.Minute); err != nil {
		t.Fatalf("seed stripe secret: %v", err)
	}
	if _, err := secrets.GetSSMParameterCached(ctx, ssmClient, "/lesser-host/stripe/lab/webhook", 30*time.Minute); err != nil {
		t.Fatalf("seed stripe webhook: %v", err)
	}
}

func withStripeMockServer(t *testing.T, fn func(baseURL string)) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/customers":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"cus_123","object":"customer"}`)
			return

		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			_ = r.ParseForm()
			mode := strings.TrimSpace(r.PostForm.Get("mode"))

			w.Header().Set("Content-Type", "application/json")
			switch mode {
			case "setup":
				_, _ = io.WriteString(w, `{"id":"cs_setup","object":"checkout.session","url":"https://stripe.test/checkout/setup","mode":"setup","customer":{"id":"cus_123","object":"customer"},"setup_intent":{"id":"seti_123","object":"setup_intent"}}`)
				return
			default:
				_, _ = io.WriteString(w, `{"id":"cs_credits","object":"checkout.session","url":"https://stripe.test/checkout/credits","mode":"payment","customer":{"id":"cus_123","object":"customer"},"payment_intent":{"id":"pi_123","object":"payment_intent"},"amount_total":1234,"currency":"usd"}`)
				return
			}

		default:
			http.NotFound(w, r)
			return
		}
	}))
	t.Cleanup(srv.Close)

	oldBackend := stripe.GetBackend(stripe.APIBackend)
	oldKey := stripe.Key
	t.Cleanup(func() {
		stripe.SetBackend(stripe.APIBackend, oldBackend)
		stripe.Key = oldKey
	})

	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL: stripe.String(srv.URL),
	}))

	fn(srv.URL)
}

func TestHandlePortalCreateCreditsCheckout_SucceedsWithStripeMock(t *testing.T) {
	seedStripeSecretsCache(t)

	withStripeMockServer(t, func(_ string) {
		tdb := newBillingTestDB()
		s := &Server{
			store: store.New(tdb.db),
			cfg:   configForTestsWithPaymentsEnabled(),
		}

		tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(theoryErrors.ErrItemNotFound).Once()

		tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
			*dest = models.Instance{Slug: "demo", Owner: "alice"}
		}).Once()

		tdb.qProfile.On("First", mock.AnythingOfType("*models.BillingProfile")).Return(theoryErrors.ErrItemNotFound).Once()
		tdb.qProfile.On("CreateOrUpdate").Return(nil).Once()

		tdb.qPurchase.On("Create").Return(nil).Once()
		tdb.qPurchase.On("First", mock.AnythingOfType("*models.CreditPurchase")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.CreditPurchase](t, args, 0)
			*dest = models.CreditPurchase{ID: "p1"}
		}).Once()

		tdb.qAudit.On("Create").Return(nil).Once()

		body, _ := json.Marshal(portalCreditsCheckoutRequest{InstanceSlug: "demo", Credits: 1000, Month: "2026-02"})
		resp, err := s.handlePortalCreateCreditsCheckout(&apptheory.Context{
			AuthIdentity: "alice",
			RequestID:    "rid",
			Request:      apptheory.Request{Body: body},
		})
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}

		var out portalCreditsCheckoutResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if strings.TrimSpace(out.CheckoutURL) == "" {
			t.Fatalf("expected checkout_url, got %#v", out)
		}
	})
}

func TestHandlePortalCreatePaymentMethodCheckout_SucceedsWithStripeMock(t *testing.T) {
	seedStripeSecretsCache(t)

	withStripeMockServer(t, func(_ string) {
		tdb := newBillingTestDB()
		s := &Server{
			store: store.New(tdb.db),
			cfg:   configForTestsWithPaymentsEnabled(),
		}

		tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.User](t, args, 0)
			*dest = models.User{Email: "alice@example.com"}
		}).Once()

		tdb.qProfile.On("First", mock.AnythingOfType("*models.BillingProfile")).Return(theoryErrors.ErrItemNotFound).Once()
		tdb.qProfile.On("CreateOrUpdate").Return(nil).Once()

		tdb.qAudit.On("Create").Return(nil).Once()

		resp, err := s.handlePortalCreatePaymentMethodCheckout(&apptheory.Context{
			AuthIdentity: "alice",
			RequestID:    "rid",
		})
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}

		var out portalPaymentMethodCheckoutResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if strings.TrimSpace(out.CheckoutURL) == "" {
			t.Fatalf("expected checkout_url, got %#v", out)
		}
	})
}

func configForTestsWithPaymentsEnabled() config.Config {
	return config.Config{
		PaymentsProvider:            "stripe",
		PaymentsCentsPer1000Credits: 100,
		PaymentsCheckoutSuccessURL:  "https://example.com/success",
		PaymentsCheckoutCancelURL:   "https://example.com/cancel",
	}
}
