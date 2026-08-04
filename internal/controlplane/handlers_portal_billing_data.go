package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"

	"github.com/equaltoai/lesser-host/internal/payments"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// invoiceSummary is a portal-safe invoice DTO. No raw Stripe objects, no
// account_ids, no internal storage keys, no permanent fabricated URLs.
type invoiceSummary struct {
	ID               string `json:"id"`
	PeriodStart      string `json:"period_start"`
	PeriodEnd        string `json:"period_end"`
	AmountDue        int64  `json:"amount_due"`
	Currency         string `json:"currency"`
	Status           string `json:"status"`
	HostedInvoiceURL string `json:"hosted_invoice_url,omitempty"`
	InvoicePDFURL    string `json:"invoice_pdf_url,omitempty"`
}

type listInvoicesResponse struct {
	Invoices []invoiceSummary `json:"invoices"`
	Count    int              `json:"count"`
}

// paymentMethodSafe is a portal-safe payment-method DTO. Masked card details
// only — no PAN, no CVV, no full tokens, no account_id, no PK/SK.
type paymentMethodSafe struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Brand    string `json:"brand,omitempty"`
	Last4    string `json:"last4,omitempty"`
	ExpMonth int64  `json:"exp_month,omitempty"`
	ExpYear  int64  `json:"exp_year,omitempty"`
	Status   string `json:"status"`
}

// getPaymentMethodResponse is a typed response for the payment method endpoint.
// It wraps a nullable paymentMethodSafe so callers can distinguish "no method"
// (null) from "method present."
type getPaymentMethodResponse struct {
	PaymentMethod *paymentMethodSafe `json:"payment_method"`
}

func (s *Server) paymentsProvider() payments.Provider {
	if s != nil && s.paymentsProviderFactory != nil {
		return s.paymentsProviderFactory(s.cfg.PaymentsProvider)
	}
	return payments.NewProvider(s.cfg.PaymentsProvider, nil)
}

func (s *Server) handlePortalListInvoices(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	provider := s.paymentsProvider()
	if provider.Name() != paymentsProviderStripeName {
		return apptheory.JSON(http.StatusOK, listInvoicesResponse{
			Invoices: []invoiceSummary{},
			Count:    0,
		})
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	profile, ok, err := s.loadBillingProfile(ctx, username)
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if !ok || profile == nil || strings.TrimSpace(profile.StripeCustomerID) == "" {
		return apptheory.JSON(http.StatusOK, listInvoicesResponse{
			Invoices: []invoiceSummary{},
			Count:    0,
		})
	}

	raw, listErr := provider.ListRecentInvoices(ctx.Context(), strings.TrimSpace(profile.StripeCustomerID), 25)
	if listErr != nil {
		return nil, newAppTheoryError("app.internal", "failed to list invoices")
	}

	out := make([]invoiceSummary, 0, len(raw))
	for _, inv := range raw {
		if strings.TrimSpace(inv.ID) == "" {
			continue
		}
		out = append(out, invoiceSummary{
			ID:               strings.TrimSpace(inv.ID),
			PeriodStart:      strings.TrimSpace(inv.PeriodStart),
			PeriodEnd:        strings.TrimSpace(inv.PeriodEnd),
			AmountDue:        inv.AmountDue,
			Currency:         strings.TrimSpace(inv.Currency),
			Status:           strings.TrimSpace(inv.Status),
			HostedInvoiceURL: strings.TrimSpace(inv.HostedURL),
			InvoicePDFURL:    strings.TrimSpace(inv.PDFURL),
		})
	}

	now := time.Now().UTC()
	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "billing.invoices.list",
		Target:    fmt.Sprintf("billing:%s", username),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	s.tryWriteAuditLog(ctx, audit)

	return apptheory.JSON(http.StatusOK, listInvoicesResponse{
		Invoices: out,
		Count:    len(out),
	})
}

func (s *Server) handlePortalGetPaymentMethod(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	username := strings.TrimSpace(ctx.AuthIdentity)

	profile, ok, err := s.loadBillingProfile(ctx, username)
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if !ok || profile == nil || strings.TrimSpace(profile.DefaultPaymentMethodID) == "" {
		return apptheory.JSON(http.StatusOK, getPaymentMethodResponse{})
	}

	defaultID := strings.TrimSpace(profile.DefaultPaymentMethodID)
	sk := fmt.Sprintf("PAYMENT_METHOD#%s#%s", models.BillingProviderStripe, defaultID)

	var method models.BillingPaymentMethod
	lookupErr := s.store.DB.WithContext(ctx.Context()).
		Model(&models.BillingPaymentMethod{}).
		Where("PK", "=", fmt.Sprintf(models.KeyPatternUser, username)).
		Where("SK", "=", sk).
		First(&method)
	if lookupErr != nil {
		if store.IsNotFound(lookupErr) {
			// Not found — return null payment_method without error.
			return apptheory.JSON(http.StatusOK, getPaymentMethodResponse{})
		}
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	// Map to safe DTO — never expose PK/SK/internal fields.
	safe := paymentMethodSafe{
		ID:       strings.TrimSpace(method.ID),
		Type:     strings.TrimSpace(method.Type),
		Brand:    strings.TrimSpace(method.Brand),
		Last4:    strings.TrimSpace(method.Last4),
		ExpMonth: method.ExpMonth,
		ExpYear:  method.ExpYear,
		Status:   strings.TrimSpace(method.Status),
	}

	now := time.Now().UTC()
	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "billing.payment_method.get",
		Target:    fmt.Sprintf("billing:%s", username),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	s.tryWriteAuditLog(ctx, audit)

	return apptheory.JSON(http.StatusOK, getPaymentMethodResponse{
		PaymentMethod: &safe,
	})
}
