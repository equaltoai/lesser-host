package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

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
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

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
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var req portalCreditsCheckoutRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	req.InstanceSlug = strings.ToLower(strings.TrimSpace(req.InstanceSlug))
	if req.InstanceSlug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "instance_slug is required"}
	}
	if req.Credits <= 0 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "credits must be > 0"}
	}

	inst, err := s.requireInstanceAccess(ctx, req.InstanceSlug)
	if err != nil {
		return nil, err
	}

	month := strings.TrimSpace(req.Month)
	if month == "" {
		month = time.Now().UTC().Format("2006-01")
	}
	if _, err := time.Parse("2006-01", month); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "month must be YYYY-MM"}
	}

	amountCents, err := creditsAmountCents(req.Credits, s.cfg.PaymentsCentsPer1000Credits)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: err.Error()}
	}

	provider := payments.NewProvider(s.cfg.PaymentsProvider)
	if provider.Name() != "stripe" {
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
		cid, err := provider.EnsureCustomer(ctx.Context(), payments.EnsureCustomerInput{
			Username: username,
			Email:    email,
		})
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create customer"}
		}
		profile.Provider = models.BillingProviderStripe
		profile.StripeCustomerID = cid
		if err := s.putBillingProfile(ctx, profile); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to store billing profile"}
		}
	}

	purchaseID, err := newToken(16)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create purchase"}
	}

	now := time.Now().UTC()
	purchase := &models.CreditPurchase{
		ID:           purchaseID,
		Username:     username,
		InstanceSlug: strings.TrimSpace(inst.Slug),
		Month:        month,
		Credits:      req.Credits,
		AmountCents:  amountCents,
		Currency:     "usd",
		Provider:     provider.Name(),
		Status:       models.CreditPurchaseStatusPending,
		RequestID:    strings.TrimSpace(ctx.RequestID),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	_ = purchase.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(purchase).IfNotExists().Create(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create purchase"}
	}

	session, err := provider.CreateCreditsCheckout(ctx.Context(), payments.CreditsCheckoutInput{
		CustomerID:   strings.TrimSpace(profile.StripeCustomerID),
		PurchaseID:   purchaseID,
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
		fail := &models.CreditPurchase{
			ID:        purchaseID,
			Status:    models.CreditPurchaseStatusFailed,
			UpdatedAt: time.Now().UTC(),
		}
		_ = fail.UpdateKeys()
		_ = s.store.DB.WithContext(ctx.Context()).Model(fail).IfExists().Update("Status", "UpdatedAt")
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create checkout"}
	}

	purchaseUpdate := &models.CreditPurchase{
		ID:                        purchaseID,
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

	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "billing.credits.checkout.create",
		Target:    fmt.Sprintf("credit_purchase:%s", purchaseID),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	// Reload purchase for response.
	var latest models.CreditPurchase
	_ = s.store.DB.WithContext(ctx.Context()).
		Model(&models.CreditPurchase{}).
		Where("PK", "=", fmt.Sprintf("CREDIT_PURCHASE#%s", purchaseID)).
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

	provider := payments.NewProvider(s.cfg.PaymentsProvider)
	if provider.Name() != "stripe" {
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
		cid, err := provider.EnsureCustomer(ctx.Context(), payments.EnsureCustomerInput{
			Username: username,
			Email:    email,
		})
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create customer"}
		}
		profile.Provider = models.BillingProviderStripe
		profile.StripeCustomerID = cid
		if err := s.putBillingProfile(ctx, profile); err != nil {
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
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

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

	provider := payments.NewProvider(s.cfg.PaymentsProvider)
	if provider.Name() != "stripe" {
		// Ignore webhooks when payments are disabled.
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "ignored": true})
	}

	ev, err := provider.ParseWebhookEvent(ctx.Context(), ctx.Request.Headers, ctx.Request.Body)
	if err != nil {
		// Stripe retries on non-2xx; only fail on signature/parse issues.
		return apptheory.JSON(http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
	}
	if ev == nil {
		return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
	}

	now := time.Now().UTC()
	switch ev.Type {
	case "checkout.session.completed":
		switch strings.ToLower(strings.TrimSpace(ev.Session.Mode)) {
		case "payment":
			purchaseID := strings.TrimSpace(ev.Session.Metadata["purchase_id"])
			if purchaseID == "" {
				return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "skipped": "missing purchase_id"})
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

			updatePurchase := &models.CreditPurchase{ID: purchaseID}
			_ = updatePurchase.UpdateKeys()

			updateBudget := &models.InstanceBudgetMonth{
				InstanceSlug: strings.TrimSpace(purchase.InstanceSlug),
				Month:        strings.TrimSpace(purchase.Month),
				UpdatedAt:    now,
			}
			_ = updateBudget.UpdateKeys()

			audit := &models.AuditLogEntry{
				Actor:     "stripe",
				Action:    "billing.credits.purchase.paid",
				Target:    fmt.Sprintf("credit_purchase:%s", purchaseID),
				RequestID: strings.TrimSpace(ctx.RequestID),
				CreatedAt: now,
			}
			_ = audit.UpdateKeys()

			err = s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
				tx.UpdateWithBuilder(updatePurchase, func(ub core.UpdateBuilder) error {
					ub.Set("Status", models.CreditPurchaseStatusPaid)
					ub.Set("PaidAt", now)
					ub.Set("UpdatedAt", now)
					if strings.TrimSpace(ev.Session.ID) != "" {
						ub.Set("ProviderCheckoutSessionID", strings.TrimSpace(ev.Session.ID))
					}
					if strings.TrimSpace(ev.Session.PaymentIntentID) != "" {
						ub.Set("ProviderPaymentIntentID", strings.TrimSpace(ev.Session.PaymentIntentID))
					}
					if strings.TrimSpace(ev.Session.CustomerID) != "" {
						ub.Set("ProviderCustomerID", strings.TrimSpace(ev.Session.CustomerID))
					}
					if strings.TrimSpace(ev.Session.Currency) != "" {
						ub.Set("Currency", strings.TrimSpace(ev.Session.Currency))
					}
					if ev.Session.AmountTotal > 0 {
						ub.Set("AmountCents", ev.Session.AmountTotal)
					}
					return nil
				},
					tabletheory.IfExists(),
					tabletheory.ConditionExpression("status = :pending", map[string]any{":pending": models.CreditPurchaseStatusPending}),
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
			if theoryErrors.IsConditionFailed(err) {
				return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
			}
			if err != nil {
				return apptheory.JSON(http.StatusOK, map[string]any{"ok": true, "error": "failed to apply credits"})
			}

			return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})

		case "setup":
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
				Actor:     "stripe",
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

	case "checkout.session.expired":
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
		if err == nil && strings.TrimSpace(purchase.Status) == models.CreditPurchaseStatusPending {
			update := &models.CreditPurchase{
				ID:        purchaseID,
				Status:    models.CreditPurchaseStatusExpired,
				UpdatedAt: now,
			}
			_ = update.UpdateKeys()
			_ = s.store.DB.WithContext(ctx.Context()).Model(update).IfExists().Update("Status", "UpdatedAt")
		}
	}

	return apptheory.JSON(http.StatusOK, map[string]any{"ok": true})
}
