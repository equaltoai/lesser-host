package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/payments"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type portalCreditsCheckoutRequest struct {
	InstanceSlug string `json:"instance_slug"`
	Credits      int64  `json:"credits"`
	Month        string `json:"month,omitempty"` // YYYY-MM (defaults to current month)
}

type portalCreditsCheckoutResponse struct {
	Purchase    models.CreditPurchase `json:"purchase"`
	CheckoutURL string                `json:"checkout_url"`
}

type portalListCreditPurchasesResponse struct {
	Purchases []models.CreditPurchase `json:"purchases"`
	Count     int                     `json:"count"`
}

type portalPaymentMethodCheckoutResponse struct {
	CheckoutURL string `json:"checkout_url"`
}

type portalListPaymentMethodsResponse struct {
	DefaultPaymentMethodID string                        `json:"default_payment_method_id,omitempty"`
	Methods                []models.BillingPaymentMethod `json:"methods"`
	Count                  int                           `json:"count"`
}

func creditsAmountCents(credits int64, centsPer1000 int64) (int64, error) {
	if credits <= 0 {
		return 0, fmt.Errorf("credits must be > 0")
	}
	if centsPer1000 <= 0 {
		return 0, fmt.Errorf("pricing is not configured")
	}
	if credits > 1_000_000_000 {
		return 0, fmt.Errorf("credits too large")
	}
	// Ceil to cents to avoid systematic undercharging.
	return (credits*centsPer1000 + 999) / 1000, nil
}

func (s *Server) loadUser(ctx *apptheory.Context, username string) (*models.User, error) {
	var user models.User
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.User{}).
		Where("PK", "=", fmt.Sprintf(models.KeyPatternUser, username)).
		Where("SK", "=", models.SKProfile).
		First(&user)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Server) loadBillingProfile(ctx *apptheory.Context, username string) (*models.BillingProfile, bool, error) {
	var profile models.BillingProfile
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.BillingProfile{}).
		Where("PK", "=", fmt.Sprintf(models.KeyPatternUser, username)).
		Where("SK", "=", "BILLING").
		First(&profile)
	if theoryErrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &profile, true, nil
}

func (s *Server) putBillingProfile(ctx *apptheory.Context, profile *models.BillingProfile) error {
	if profile == nil {
		return fmt.Errorf("profile is required")
	}
	_ = profile.UpdateKeys()
	return s.store.DB.WithContext(ctx.Context()).Model(profile).CreateOrUpdate()
}

func parsePortalCreditsCheckoutRequest(ctx *apptheory.Context) (portalCreditsCheckoutRequest, *apptheory.AppError) {
	var req portalCreditsCheckoutRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		if appErr, ok := err.(*apptheory.AppError); ok {
			return portalCreditsCheckoutRequest{}, appErr
		}
		return portalCreditsCheckoutRequest{}, &apptheory.AppError{Code: "app.bad_request", Message: "invalid request"}
	}

	req.InstanceSlug = strings.ToLower(strings.TrimSpace(req.InstanceSlug))
	if req.InstanceSlug == "" {
		return portalCreditsCheckoutRequest{}, &apptheory.AppError{Code: "app.bad_request", Message: "instance_slug is required"}
	}
	if req.Credits <= 0 {
		return portalCreditsCheckoutRequest{}, &apptheory.AppError{Code: "app.bad_request", Message: "credits must be > 0"}
	}

	req.Month = strings.TrimSpace(req.Month)
	return req, nil
}

func normalizeCreditsCheckoutMonth(raw string, now time.Time) (string, *apptheory.AppError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = now.UTC().Format("2006-01")
	}
	if _, parseErr := time.Parse("2006-01", raw); parseErr != nil {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "month must be YYYY-MM"}
	}
	return raw, nil
}

func (s *Server) portalUserEmailBestEffort(ctx *apptheory.Context, username string) string {
	if s == nil || ctx == nil {
		return ""
	}

	user, _ := s.loadUser(ctx, username)
	if user == nil {
		return ""
	}
	return strings.TrimSpace(user.Email)
}

func (s *Server) ensureStripeCustomerProfile(ctx *apptheory.Context, provider payments.Provider, username string, email string) (*models.BillingProfile, *apptheory.AppError) {
	profile, ok, err := s.loadBillingProfile(ctx, username)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !ok || profile == nil {
		profile = &models.BillingProfile{
			Username:  username,
			Provider:  models.BillingProviderStripe,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
	}
	if strings.TrimSpace(profile.StripeCustomerID) != "" {
		return profile, nil
	}

	cid, ensureErr := provider.EnsureCustomer(ctx.Context(), payments.EnsureCustomerInput{
		Username: username,
		Email:    email,
	})
	if ensureErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create customer"}
	}

	profile.Provider = models.BillingProviderStripe
	profile.StripeCustomerID = cid
	if putErr := s.putBillingProfile(ctx, profile); putErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to store billing profile"}
	}

	return profile, nil
}

func (s *Server) createPendingCreditPurchase(
	ctx *apptheory.Context,
	username string,
	instanceSlug string,
	month string,
	credits int64,
	amountCents int64,
	providerName string,
) (*models.CreditPurchase, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	purchaseID, err := newToken(16)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create purchase"}
	}

	now := time.Now().UTC()
	purchase := &models.CreditPurchase{
		ID:           purchaseID,
		Username:     strings.TrimSpace(username),
		InstanceSlug: strings.TrimSpace(instanceSlug),
		Month:        strings.TrimSpace(month),
		Credits:      credits,
		AmountCents:  amountCents,
		Currency:     "usd",
		Provider:     strings.TrimSpace(providerName),
		Status:       models.CreditPurchaseStatusPending,
		RequestID:    strings.TrimSpace(ctx.RequestID),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	_ = purchase.UpdateKeys()

	if createErr := s.store.DB.WithContext(ctx.Context()).Model(purchase).IfNotExists().Create(); createErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create purchase"}
	}

	return purchase, nil
}

func (s *Server) markCreditPurchaseFailedBestEffort(ctx *apptheory.Context, purchaseID string) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return
	}

	fail := &models.CreditPurchase{
		ID:        strings.TrimSpace(purchaseID),
		Status:    models.CreditPurchaseStatusFailed,
		UpdatedAt: time.Now().UTC(),
	}
	_ = fail.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(fail).IfExists().Update("Status", "UpdatedAt")
}

func (s *Server) updateCreditPurchaseWithCheckoutSessionBestEffort(ctx *apptheory.Context, purchaseID string, session *payments.CheckoutSession) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil || session == nil {
		return
	}

	purchaseUpdate := &models.CreditPurchase{
		ID:                        strings.TrimSpace(purchaseID),
		ProviderCheckoutSessionID: strings.TrimSpace(session.ID),
		ProviderPaymentIntentID:   strings.TrimSpace(session.PaymentIntentID),
		ProviderCustomerID:        strings.TrimSpace(session.CustomerID),
		UpdatedAt:                 time.Now().UTC(),
	}
	_ = purchaseUpdate.UpdateKeys()

	fields := []string{"ProviderCheckoutSessionID", "UpdatedAt"}
	if strings.TrimSpace(purchaseUpdate.ProviderPaymentIntentID) != "" {
		fields = append(fields, "ProviderPaymentIntentID")
	}
	if strings.TrimSpace(purchaseUpdate.ProviderCustomerID) != "" {
		fields = append(fields, "ProviderCustomerID")
	}
	_ = s.store.DB.WithContext(ctx.Context()).Model(purchaseUpdate).IfExists().Update(fields...)
}

func (s *Server) handlePortalCreateInstanceKey(ctx *apptheory.Context) (*apptheory.Response, error) {
	inst, err := s.requireInstanceAccess(ctx, ctx.Param("slug"))
	if err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(inst.Slug))

	secret, err := newToken(32)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create key"}
	}
	plaintext := "lhk_" + secret

	sum := sha256.Sum256([]byte(plaintext))
	keyID := hex.EncodeToString(sum[:])

	now := time.Now().UTC()
	key := &models.InstanceKey{
		ID:           keyID,
		InstanceSlug: slug,
		CreatedAt:    now,
	}
	if err := key.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(key).IfNotExists().Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create key"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "portal.instance_key.create",
		Target:    fmt.Sprintf("instance:%s", slug),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	s.tryWriteAuditLog(ctx, audit)

	return apptheory.JSON(http.StatusCreated, createInstanceKeyResponse{
		InstanceSlug: slug,
		Key:          plaintext,
		KeyID:        keyID,
	})
}

func (s *Server) handlePortalCreateCreditsCheckout(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	req, appErr := parsePortalCreditsCheckoutRequest(ctx)
	if appErr != nil {
		return nil, appErr
	}

	inst, err := s.requireInstanceAccess(ctx, req.InstanceSlug)
	if err != nil {
		return nil, err
	}

	month, appErr := normalizeCreditsCheckoutMonth(req.Month, time.Now().UTC())
	if appErr != nil {
		return nil, appErr
	}

	amountCents, err := creditsAmountCents(req.Credits, s.cfg.PaymentsCentsPer1000Credits)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: err.Error()}
	}

	provider := payments.NewProvider(s.cfg.PaymentsProvider, nil)
	if provider.Name() != paymentsProviderStripeName {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "payments provider not configured"}
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	email := s.portalUserEmailBestEffort(ctx, username)

	profile, appErr := s.ensureStripeCustomerProfile(ctx, provider, username, email)
	if appErr != nil {
		return nil, appErr
	}

	purchase, appErr := s.createPendingCreditPurchase(ctx, username, inst.Slug, month, req.Credits, amountCents, provider.Name())
	if appErr != nil {
		return nil, appErr
	}

	session, err := provider.CreateCreditsCheckout(ctx.Context(), payments.CreditsCheckoutInput{
		CustomerID:   strings.TrimSpace(profile.StripeCustomerID),
		PurchaseID:   strings.TrimSpace(purchase.ID),
		Username:     username,
		InstanceSlug: strings.TrimSpace(inst.Slug),
		Month:        month,
		Credits:      req.Credits,
		AmountCents:  amountCents,
		Currency:     "usd",
		SuccessURL:   strings.TrimSpace(s.cfg.PaymentsCheckoutSuccessURL),
		CancelURL:    strings.TrimSpace(s.cfg.PaymentsCheckoutCancelURL),
	})
	if err != nil || session == nil || strings.TrimSpace(session.URL) == "" {
		s.markCreditPurchaseFailedBestEffort(ctx, purchase.ID)
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create checkout"}
	}

	s.updateCreditPurchaseWithCheckoutSessionBestEffort(ctx, purchase.ID, session)

	now := time.Now().UTC()
	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "billing.credits.checkout.create",
		Target:    fmt.Sprintf("credit_purchase:%s", strings.TrimSpace(purchase.ID)),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	s.tryWriteAuditLog(ctx, audit)

	// Reload purchase for response.
	var latest models.CreditPurchase
	_ = s.store.DB.WithContext(ctx.Context()).
		Model(&models.CreditPurchase{}).
		Where("PK", "=", fmt.Sprintf("CREDIT_PURCHASE#%s", strings.TrimSpace(purchase.ID))).
		Where("SK", "=", models.SKMetadata).
		First(&latest)

	return apptheory.JSON(http.StatusOK, portalCreditsCheckoutResponse{
		Purchase:    latest,
		CheckoutURL: session.URL,
	})
}

func (s *Server) handlePortalListCreditPurchases(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	pk := fmt.Sprintf("USER_PURCHASES#%s", username)

	var items []*models.CreditPurchase
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.CreditPurchase{}).
		Index("gsi1").
		Where("gsi1PK", "=", pk).
		Limit(200).
		All(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list purchases"}
	}

	out := make([]models.CreditPurchase, 0, len(items))
	for _, p := range items {
		if p != nil {
			out = append(out, *p)
		}
	}

	return apptheory.JSON(http.StatusOK, portalListCreditPurchasesResponse{
		Purchases: out,
		Count:     len(out),
	})
}

func (s *Server) handlePortalCreatePaymentMethodCheckout(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	provider := payments.NewProvider(s.cfg.PaymentsProvider, nil)
	if provider.Name() != paymentsProviderStripeName {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "payments provider not configured"}
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	user, _ := s.loadUser(ctx, username)
	email := ""
	if user != nil {
		email = strings.TrimSpace(user.Email)
	}

	profile, ok, err := s.loadBillingProfile(ctx, username)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if !ok {
		profile = &models.BillingProfile{
			Username:  username,
			Provider:  models.BillingProviderStripe,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
	}
	if strings.TrimSpace(profile.StripeCustomerID) == "" {
		cid, ensureErr := provider.EnsureCustomer(ctx.Context(), payments.EnsureCustomerInput{
			Username: username,
			Email:    email,
		})
		if ensureErr != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create customer"}
		}
		profile.Provider = models.BillingProviderStripe
		profile.StripeCustomerID = cid
		if putErr := s.putBillingProfile(ctx, profile); putErr != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to store billing profile"}
		}
	}

	session, err := provider.CreateSetupCheckout(ctx.Context(), payments.SetupCheckoutInput{
		CustomerID: strings.TrimSpace(profile.StripeCustomerID),
		Username:   username,
		SuccessURL: strings.TrimSpace(s.cfg.PaymentsCheckoutSuccessURL),
		CancelURL:  strings.TrimSpace(s.cfg.PaymentsCheckoutCancelURL),
	})
	if err != nil || session == nil || strings.TrimSpace(session.URL) == "" {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create checkout"}
	}

	now := time.Now().UTC()
	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "billing.payment_method.checkout.create",
		Target:    fmt.Sprintf("billing:%s", username),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	s.tryWriteAuditLog(ctx, audit)

	return apptheory.JSON(http.StatusOK, portalPaymentMethodCheckoutResponse{
		CheckoutURL: session.URL,
	})
}

func (s *Server) handlePortalListPaymentMethods(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	pk := fmt.Sprintf(models.KeyPatternUser, username)

	var methods []*models.BillingPaymentMethod
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.BillingPaymentMethod{}).
		Where("PK", "=", pk).
		Where("SK", "BEGINS_WITH", "PAYMENT_METHOD#").
		Limit(50).
		All(&methods)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list payment methods"}
	}

	out := make([]models.BillingPaymentMethod, 0, len(methods))
	for _, m := range methods {
		if m != nil {
			out = append(out, *m)
		}
	}

	defaultID := ""
	if profile, ok, _ := s.loadBillingProfile(ctx, username); ok && profile != nil {
		defaultID = strings.TrimSpace(profile.DefaultPaymentMethodID)
	}

	return apptheory.JSON(http.StatusOK, portalListPaymentMethodsResponse{
		DefaultPaymentMethodID: defaultID,
		Methods:                out,
		Count:                  len(out),
	})
}

func (s *Server) handleStripeWebhook(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	provider := payments.NewProvider(s.cfg.PaymentsProvider, nil)
	if provider.Name() != paymentsProviderStripeName {
		// Ignore webhooks when payments are disabled.
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "ignored": true})
	}

	ev, err := provider.ParseWebhookEvent(ctx.Context(), ctx.Request.Headers, ctx.Request.Body)
	if err != nil {
		// Stripe retries on non-2xx; only fail on signature/parse issues.
		// Return a generic message to avoid leaking internal error details.
		return apptheory.JSON(http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid webhook payload"})
	}
	if ev == nil {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
	}

	switch strings.TrimSpace(ev.Type) {
	case stripeWebhookEventCheckoutCompleted:
		return s.handleStripeCheckoutSessionCompleted(ctx, provider, ev)
	case stripeWebhookEventCheckoutAsyncPaymentSucceeded:
		return s.handleStripeCheckoutSessionCompleted(ctx, provider, ev)
	case stripeWebhookEventCheckoutExpired:
		return s.handleStripeCheckoutSessionExpired(ctx, ev)
	case stripeWebhookEventCheckoutAsyncPaymentFailed:
		return s.handleStripeCheckoutSessionExpired(ctx, ev)
	default:
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
	}
}

const (
	stripeWebhookEventCheckoutCompleted             = "checkout.session.completed"
	stripeWebhookEventCheckoutExpired               = "checkout.session.expired"
	stripeWebhookEventCheckoutAsyncPaymentSucceeded = "checkout.session.async_payment_succeeded"
	stripeWebhookEventCheckoutAsyncPaymentFailed    = "checkout.session.async_payment_failed"
	stripeCheckoutModePayment                       = "payment"
	stripeCheckoutModeSetup                         = "setup"
	stripeCheckoutStatusComplete                    = "complete"
	stripeCheckoutPaymentStatusPaid                 = "paid"
)

func (s *Server) handleStripeCheckoutSessionCompleted(ctx *apptheory.Context, provider payments.Provider, ev *payments.WebhookEvent) (*apptheory.Response, error) {
	mode := strings.ToLower(strings.TrimSpace(ev.Session.Mode))
	now := time.Now().UTC()

	switch mode {
	case stripeCheckoutModePayment:
		return s.handleStripePaymentCheckoutCompleted(ctx, ev, now)
	case stripeCheckoutModeSetup:
		return s.handleStripeSetupCheckoutCompleted(ctx, provider, ev, now)
	default:
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
	}
}

func stripeCheckoutSessionPaymentSettled(session payments.CheckoutSession) bool {
	if strings.ToLower(strings.TrimSpace(session.Mode)) != stripeCheckoutModePayment {
		return false
	}

	// For one-time payments, only credit on fully settled sessions.
	if strings.ToLower(strings.TrimSpace(session.PaymentStatus)) != stripeCheckoutPaymentStatusPaid {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(session.Status))
	if status != "" && status != stripeCheckoutStatusComplete {
		return false
	}
	return true
}

func validateStripePaymentCheckoutAgainstPurchase(session payments.CheckoutSession, purchase *models.CreditPurchase) string {
	if purchase == nil {
		return "purchase missing"
	}

	expectedSession := strings.TrimSpace(purchase.ProviderCheckoutSessionID)
	if expectedSession != "" && strings.TrimSpace(session.ID) != expectedSession {
		return "checkout session mismatch"
	}

	expectedCustomer := strings.TrimSpace(purchase.ProviderCustomerID)
	if expectedCustomer != "" && strings.TrimSpace(session.CustomerID) != expectedCustomer {
		return "customer mismatch"
	}

	expectedCurrency := strings.ToLower(strings.TrimSpace(purchase.Currency))
	gotCurrency := strings.ToLower(strings.TrimSpace(session.Currency))
	if expectedCurrency != "" && gotCurrency != "" && gotCurrency != expectedCurrency {
		return "currency mismatch"
	}

	// Guard against underpayment: only accept settled sessions at or above the expected charge.
	if purchase.AmountCents > 0 && session.AmountTotal < purchase.AmountCents {
		return "amount mismatch"
	}

	return ""
}

func (s *Server) handleStripePaymentCheckoutCompleted(ctx *apptheory.Context, ev *payments.WebhookEvent, now time.Time) (*apptheory.Response, error) {
	purchaseID := strings.TrimSpace(ev.Session.Metadata["purchase_id"])
	if purchaseID == "" {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "skipped": "missing purchase_id"})
	}
	if !stripeCheckoutSessionPaymentSettled(ev.Session) {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "skipped": "payment not settled"})
	}

	var purchase models.CreditPurchase
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.CreditPurchase{}).
		Where("PK", "=", fmt.Sprintf("CREDIT_PURCHASE#%s", purchaseID)).
		Where("SK", "=", models.SKMetadata).
		First(&purchase)
	if err != nil {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "skipped": "purchase not found"})
	}
	if strings.TrimSpace(purchase.Status) == models.CreditPurchaseStatusPaid {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
	}
	if reason := validateStripePaymentCheckoutAgainstPurchase(ev.Session, &purchase); reason != "" {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "skipped": reason})
	}

	err = s.applyCreditPurchasePaid(ctx.Context(), purchaseID, &purchase, ev.Session, strings.TrimSpace(ctx.RequestID), now)
	if theoryErrors.IsConditionFailed(err) {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
	}
	if err != nil {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "error": "failed to apply credits"})
	}

	return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) applyCreditPurchasePaid(
	ctx context.Context,
	purchaseID string,
	purchase *models.CreditPurchase,
	session payments.CheckoutSession,
	requestID string,
	now time.Time,
) error {
	if s == nil || s.store == nil || s.store.DB == nil || purchase == nil {
		return errors.New("store not configured")
	}

	updatePurchase := &models.CreditPurchase{ID: purchaseID}
	_ = updatePurchase.UpdateKeys()

	updateBudget := &models.InstanceBudgetMonth{
		InstanceSlug: strings.TrimSpace(purchase.InstanceSlug),
		Month:        strings.TrimSpace(purchase.Month),
		UpdatedAt:    now,
	}
	_ = updateBudget.UpdateKeys()

	audit := &models.AuditLogEntry{
		Actor:     paymentsActorStripe,
		Action:    "billing.credits.purchase.paid",
		Target:    fmt.Sprintf("credit_purchase:%s", purchaseID),
		RequestID: requestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	return s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(
			updatePurchase,
			func(ub core.UpdateBuilder) error {
				ub.Set("Status", models.CreditPurchaseStatusPaid)
				ub.Set("PaidAt", now)
				ub.Set("UpdatedAt", now)
				if strings.TrimSpace(session.ID) != "" {
					ub.Set("ProviderCheckoutSessionID", strings.TrimSpace(session.ID))
				}
				if strings.TrimSpace(session.PaymentIntentID) != "" {
					ub.Set("ProviderPaymentIntentID", strings.TrimSpace(session.PaymentIntentID))
				}
				if strings.TrimSpace(session.CustomerID) != "" {
					ub.Set("ProviderCustomerID", strings.TrimSpace(session.CustomerID))
				}
				if strings.TrimSpace(session.Currency) != "" {
					ub.Set("Currency", strings.TrimSpace(session.Currency))
				}
				if session.AmountTotal > 0 {
					ub.Set("AmountCents", session.AmountTotal)
				}
				return nil
			},
			tabletheory.IfExists(),
			tabletheory.Condition("Status", "=", models.CreditPurchaseStatusPending),
		)

		tx.UpdateWithBuilder(updateBudget, func(ub core.UpdateBuilder) error {
			ub.Add("IncludedCredits", purchase.Credits)
			ub.Set("UpdatedAt", now)
			ub.Set("InstanceSlug", strings.TrimSpace(purchase.InstanceSlug))
			ub.Set("Month", strings.TrimSpace(purchase.Month))
			return nil
		})

		tx.Put(audit)
		return nil
	})
}

func (s *Server) handleStripeSetupCheckoutCompleted(ctx *apptheory.Context, provider payments.Provider, ev *payments.WebhookEvent, now time.Time) (*apptheory.Response, error) {
	username := strings.TrimSpace(ev.Session.Metadata["username"])
	if username == "" {
		username = strings.TrimSpace(ev.Session.Metadata["user"])
	}
	if username == "" {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "skipped": "missing username"})
	}

	setupIntentID := strings.TrimSpace(ev.Session.SetupIntentID)
	if setupIntentID == "" {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "skipped": "missing setup_intent"})
	}

	pm, err := provider.ResolveSetupPaymentMethod(ctx.Context(), setupIntentID)
	if err != nil || pm == nil || strings.TrimSpace(pm.ID) == "" {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "error": "failed to resolve payment method"})
	}

	profile := &models.BillingProfile{
		Username:               username,
		Provider:               models.BillingProviderStripe,
		StripeCustomerID:       strings.TrimSpace(ev.Session.CustomerID),
		DefaultPaymentMethodID: strings.TrimSpace(pm.ID),
		UpdatedAt:              now,
	}
	_ = profile.UpdateKeys()

	method := &models.BillingPaymentMethod{
		Username:  username,
		Provider:  models.BillingProviderStripe,
		ID:        strings.TrimSpace(pm.ID),
		Type:      strings.TrimSpace(pm.Type),
		Brand:     strings.TrimSpace(pm.Brand),
		Last4:     strings.TrimSpace(pm.Last4),
		ExpMonth:  pm.ExpMonth,
		ExpYear:   pm.ExpYear,
		Status:    models.BillingPaymentMethodStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = method.UpdateKeys()

	audit := &models.AuditLogEntry{
		Actor:     paymentsActorStripe,
		Action:    "billing.payment_method.attached",
		Target:    fmt.Sprintf("billing:%s", username),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	err = s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Put(profile)
		tx.Put(method)
		tx.Put(audit)
		return nil
	})
	if err != nil {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "error": "failed to store payment method"})
	}

	return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleStripeCheckoutSessionExpired(ctx *apptheory.Context, ev *payments.WebhookEvent) (*apptheory.Response, error) {
	purchaseID := strings.TrimSpace(ev.Session.Metadata["purchase_id"])
	if purchaseID == "" {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
	}

	var purchase models.CreditPurchase
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.CreditPurchase{}).
		Where("PK", "=", fmt.Sprintf("CREDIT_PURCHASE#%s", purchaseID)).
		Where("SK", "=", models.SKMetadata).
		First(&purchase)
	if err != nil || strings.TrimSpace(purchase.Status) != models.CreditPurchaseStatusPending {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
	}

	now := time.Now().UTC()
	update := &models.CreditPurchase{
		ID:        purchaseID,
		Status:    models.CreditPurchaseStatusExpired,
		UpdatedAt: now,
	}
	_ = update.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(update).IfExists().Update("Status", "UpdatedAt")
	return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
}
