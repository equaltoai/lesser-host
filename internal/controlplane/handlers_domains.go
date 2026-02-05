package controlplane

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/domains"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	domainVerificationRecordPrefix = "_lesser-host-verification."
	domainVerificationValuePrefix  = "lesser-host-verification="
)

type domainResponse struct {
	Domain       string `json:"domain"`
	InstanceSlug string `json:"instance_slug"`
	Type         string `json:"type"`
	Status       string `json:"status"`

	VerificationMethod string    `json:"verification_method,omitempty"`
	VerifiedAt         time.Time `json:"verified_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type listDomainsResponse struct {
	Domains []domainResponse `json:"domains"`
	Count   int              `json:"count"`
}

type addDomainRequest struct {
	Domain string `json:"domain"`
}

type addDomainVerification struct {
	Method   string `json:"method"`
	TXTName  string `json:"txt_name,omitempty"`
	TXTValue string `json:"txt_value,omitempty"`
}

type addDomainResponse struct {
	Domain       domainResponse        `json:"domain"`
	Verification addDomainVerification `json:"verification"`
}

func domainResponseFromModel(d *models.Domain) domainResponse {
	if d == nil {
		return domainResponse{}
	}
	return domainResponse{
		Domain:             strings.TrimSpace(d.Domain),
		InstanceSlug:       strings.TrimSpace(d.InstanceSlug),
		Type:               strings.TrimSpace(d.Type),
		Status:             strings.TrimSpace(d.Status),
		VerificationMethod: strings.TrimSpace(d.VerificationMethod),
		VerifiedAt:         d.VerifiedAt,
		CreatedAt:          d.CreatedAt,
		UpdatedAt:          d.UpdatedAt,
	}
}

func (s *Server) handleListInstanceDomains(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	slug := strings.ToLower(strings.TrimSpace(ctx.Param("slug")))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}

	// Ensure the instance exists.
	if _, err := s.getInstance(ctx, slug); theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "instance not found"}
	} else if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var items []*models.Domain
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.Domain{}).
		Index("gsi1").
		Where("gsi1PK", "=", fmt.Sprintf("INSTANCE_DOMAINS#%s", slug)).
		All(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list domains"}
	}

	out := make([]domainResponse, 0, len(items))
	for _, d := range items {
		out = append(out, domainResponseFromModel(d))
	}

	return apptheory.JSON(http.StatusOK, listDomainsResponse{
		Domains: out,
		Count:   len(out),
	})
}

func (s *Server) handleAddInstanceDomain(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	slug := strings.ToLower(strings.TrimSpace(ctx.Param("slug")))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}

	// Ensure the instance exists.
	if _, err := s.getInstance(ctx, slug); theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "instance not found"}
	} else if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var req addDomainRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	rawDomain := strings.TrimSpace(req.Domain)
	domain, err := domains.NormalizeDomain(rawDomain)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	// Prevent operators from manually re-adding the managed primary domain.
	parentDomain := strings.TrimSpace(s.cfg.ManagedParentDomain)
	if parentDomain == "" {
		parentDomain = defaultManagedParentDomain
	}
	primary := fmt.Sprintf("%s.%s", slug, strings.TrimPrefix(parentDomain, "."))
	if domain == primary {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "domain is already managed as the primary domain"}
	}

	token, err := newToken(16)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create verification token"}
	}

	now := time.Now().UTC()
	item := &models.Domain{
		Domain:             domain,
		DomainRaw:          rawDomain,
		InstanceSlug:       slug,
		Type:               models.DomainTypeVanity,
		Status:             models.DomainStatusPending,
		VerificationMethod: domainVerificationMethodDNSTXT,
		VerificationToken:  token,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	_ = item.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(item).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "domain already exists"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to add domain"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "domain.add",
		Target:    fmt.Sprintf("domain:%s", domain),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	txtName := domainVerificationRecordPrefix + domain
	txtValue := domainVerificationValuePrefix + token

	return apptheory.JSON(http.StatusCreated, addDomainResponse{
		Domain: domainResponseFromModel(item),
		Verification: addDomainVerification{
			Method:   domainVerificationMethodDNSTXT,
			TXTName:  txtName,
			TXTValue: txtValue,
		},
	})
}

type verifyDomainResponse struct {
	Domain domainResponse `json:"domain"`
}

func (s *Server) handleVerifyInstanceDomain(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	slug := strings.ToLower(strings.TrimSpace(ctx.Param("slug")))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}

	domain, err := domains.NormalizeDomain(ctx.Param("domain"))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	var item models.Domain
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.Domain{}).
		Where("PK", "=", fmt.Sprintf("DOMAIN#%s", domain)).
		Where("SK", "=", models.SKMetadata).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(item.InstanceSlug) != slug {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
	}

	if strings.TrimSpace(item.Status) == models.DomainStatusVerified || strings.TrimSpace(item.Status) == models.DomainStatusActive {
		return apptheory.JSON(http.StatusOK, verifyDomainResponse{Domain: domainResponseFromModel(&item)})
	}

	token := strings.TrimSpace(item.VerificationToken)
	if token == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "domain is not eligible for verification"}
	}

	want := domainVerificationValuePrefix + token
	txtName := domainVerificationRecordPrefix + domain

	lookupCtx := ctx.Context()
	if lookupCtx == nil {
		lookupCtx = context.Background()
	}
	rc, cancel := context.WithTimeout(lookupCtx, 4*time.Second)
	defer cancel()

	records, err := net.DefaultResolver.LookupTXT(rc, txtName)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "verification record not found"}
	}

	found := false
	for _, r := range records {
		r = strings.TrimSpace(r)
		if r == want {
			found = true
			break
		}
	}
	if !found {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "verification record not found"}
	}

	now := time.Now().UTC()
	update := &models.Domain{
		Domain:       domain,
		InstanceSlug: slug,
		Type:         strings.TrimSpace(item.Type),
		Status:       models.DomainStatusVerified,
		// Keep method stable; clear token after successful verification.
		VerificationMethod: domainVerificationMethodDNSTXT,
		VerificationToken:  "",
		VerifiedAt:         now,
		UpdatedAt:          now,
	}
	_ = update.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(update).IfExists().Update(
		"Status",
		"VerificationMethod",
		"VerificationToken",
		"VerifiedAt",
		"UpdatedAt",
	); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to verify domain"}
	}

	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "domain.verify",
		Target:    fmt.Sprintf("domain:%s", domain),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	item.Status = models.DomainStatusVerified
	item.VerificationMethod = domainVerificationMethodDNSTXT
	item.VerificationToken = ""
	item.VerifiedAt = now
	item.UpdatedAt = now

	// Create an operator review request for vanity domain activation.
	if strings.TrimSpace(item.Type) == models.DomainTypeVanity {
		req := &models.VanityDomainRequest{
			Domain:       domain,
			DomainRaw:    strings.TrimSpace(item.DomainRaw),
			InstanceSlug: slug,
			RequestedBy:  strings.TrimSpace(ctx.AuthIdentity),
			Status:       models.VanityDomainRequestStatusPending,
			VerifiedAt:   now,
			RequestedAt:  now,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		_ = req.UpdateKeys()
		_ = s.store.DB.WithContext(ctx.Context()).Model(req).CreateOrUpdate()
	}

	// Best-effort: ensure the vanity domain hostId is registered on-chain for tips (managed instances).
	if s.cfg.TipEnabled {
		_, _, _ = s.ensureTipRegistryHostOperation(ctx.Context(), domain, strings.TrimSpace(item.DomainRaw), strings.TrimSpace(ctx.AuthIdentity), ctx.RequestID)
	}

	return apptheory.JSON(http.StatusOK, verifyDomainResponse{
		Domain: domainResponseFromModel(&item),
	})
}

func (s *Server) handleDeleteInstanceDomain(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	slug := strings.ToLower(strings.TrimSpace(ctx.Param("slug")))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}
	domain, err := domains.NormalizeDomain(ctx.Param("domain"))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	var item models.Domain
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.Domain{}).
		Where("PK", "=", fmt.Sprintf("DOMAIN#%s", domain)).
		Where("SK", "=", models.SKMetadata).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(item.InstanceSlug) != slug {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "domain not found"}
	}

	if strings.TrimSpace(item.Type) == models.DomainTypePrimary {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "primary domain cannot be removed"}
	}

	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.Domain{}).
		Where("PK", "=", item.PK).
		Where("SK", "=", item.SK).
		Delete(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to delete domain"}
	}

	now := time.Now().UTC()
	audit := &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "domain.delete",
		Target:    fmt.Sprintf("domain:%s", domain),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusOK, map[string]any{
		"deleted": true,
		"domain":  domain,
	})
}
