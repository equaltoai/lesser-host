package controlplane

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/domains"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type listVanityDomainRequestsResponse struct {
	Requests []models.VanityDomainRequest `json:"requests"`
	Count    int                          `json:"count"`
}

type reviewNoteRequest struct {
	Note string `json:"note,omitempty"`
}

func listByGSI1PK[T any](ctx *apptheory.Context, s *Server, model any, pk string, limit int) ([]T, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if limit <= 0 {
		limit = 200
	}

	var items []*T
	err := s.store.DB.WithContext(ctx.Context()).
		Model(model).
		Index("gsi1").
		Where("gsi1PK", "=", strings.TrimSpace(pk)).
		Limit(limit).
		All(&items)
	if err != nil {
		return nil, err
	}

	out := make([]T, 0, len(items))
	for _, it := range items {
		if it != nil {
			out = append(out, *it)
		}
	}

	return out, nil
}

func (s *Server) handleListVanityDomainRequests(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	items, err := listByGSI1PK[models.VanityDomainRequest](
		ctx,
		s,
		&models.VanityDomainRequest{},
		fmt.Sprintf("VANITY_DOMAIN_REQUESTS#%s", models.VanityDomainRequestStatusPending),
		200,
	)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list requests"}
	}

	return apptheory.JSON(http.StatusOK, listVanityDomainRequestsResponse{Requests: items, Count: len(items)})
}

func (s *Server) handleApproveVanityDomainRequest(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	domain, err := domains.NormalizeDomain(ctx.Param("domain"))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	var req models.VanityDomainRequest
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.VanityDomainRequest{}).
		Where("PK", "=", fmt.Sprintf("VANITY_DOMAIN_REQUEST#%s", domain)).
		Where("SK", "=", models.SKMetadata).
		First(&req)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "request not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if strings.TrimSpace(req.Status) == models.VanityDomainRequestStatusApproved {
		return apptheory.JSON(http.StatusOK, req)
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
	if strings.TrimSpace(item.Type) != models.DomainTypeVanity {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "domain is not a vanity domain"}
	}
	if strings.TrimSpace(item.Status) == models.DomainStatusActive {
		return apptheory.JSON(http.StatusOK, req)
	}
	if strings.TrimSpace(item.Status) != models.DomainStatusVerified {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "domain must be verified before activation"}
	}

	var note reviewNoteRequest
	if len(ctx.Request.Body) > 0 {
		_ = parseJSON(ctx, &note)
	}

	now := time.Now().UTC()
	actor := strings.TrimSpace(ctx.AuthIdentity)

	// Update the domain and the request atomically.
	domainKey := &models.Domain{
		Domain:       domain,
		InstanceSlug: strings.TrimSpace(item.InstanceSlug),
		Type:         strings.TrimSpace(item.Type),
	}
	_ = domainKey.UpdateKeys()

	requestedAt := req.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = req.CreatedAt
	}
	reqKey := &models.VanityDomainRequest{Domain: domain}
	_ = reqKey.UpdateKeys()

	audit := &models.AuditLogEntry{
		Actor:     actor,
		Action:    "vanity_domain.approve",
		Target:    fmt.Sprintf("domain:%s", domain),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(domainKey, func(ub core.UpdateBuilder) error {
			ub.Set("Status", models.DomainStatusActive)
			ub.Set("UpdatedAt", now)
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.Condition("Status", "=", models.DomainStatusVerified),
		)

		tx.UpdateWithBuilder(reqKey, func(ub core.UpdateBuilder) error {
			ub.Set("Status", models.VanityDomainRequestStatusApproved)
			ub.Set("ReviewedBy", actor)
			ub.Set("ReviewedAt", now)
			ub.Set("UpdatedAt", now)
			ub.Set("Note", strings.TrimSpace(note.Note))
			ub.Set("GSI1PK", fmt.Sprintf("VANITY_DOMAIN_REQUESTS#%s", models.VanityDomainRequestStatusApproved))
			ub.Set("GSI1SK", fmt.Sprintf("%s#%s", requestedAt.UTC().Format(time.RFC3339Nano), domain))
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.Condition("Status", "=", models.VanityDomainRequestStatusPending),
		)

		tx.Put(audit)
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return apptheory.JSON(http.StatusOK, req)
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to approve request"}
	}

	req.Status = models.VanityDomainRequestStatusApproved
	req.ReviewedBy = actor
	req.ReviewedAt = now
	req.UpdatedAt = now
	req.Note = strings.TrimSpace(note.Note)

	return apptheory.JSON(http.StatusOK, req)
}

func (s *Server) handleRejectVanityDomainRequest(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	domain, err := domains.NormalizeDomain(ctx.Param("domain"))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	var req models.VanityDomainRequest
	err = s.store.DB.WithContext(ctx.Context()).
		Model(&models.VanityDomainRequest{}).
		Where("PK", "=", fmt.Sprintf("VANITY_DOMAIN_REQUEST#%s", domain)).
		Where("SK", "=", models.SKMetadata).
		First(&req)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "request not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	if strings.TrimSpace(req.Status) == models.VanityDomainRequestStatusRejected {
		return apptheory.JSON(http.StatusOK, req)
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
	if strings.TrimSpace(item.Type) != models.DomainTypeVanity {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "domain is not a vanity domain"}
	}

	var note reviewNoteRequest
	if len(ctx.Request.Body) > 0 {
		_ = parseJSON(ctx, &note)
	}

	now := time.Now().UTC()
	actor := strings.TrimSpace(ctx.AuthIdentity)

	domainKey := &models.Domain{
		Domain:       domain,
		InstanceSlug: strings.TrimSpace(item.InstanceSlug),
		Type:         strings.TrimSpace(item.Type),
	}
	_ = domainKey.UpdateKeys()

	requestedAt := req.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = req.CreatedAt
	}
	reqKey := &models.VanityDomainRequest{Domain: domain}
	_ = reqKey.UpdateKeys()

	audit := &models.AuditLogEntry{
		Actor:     actor,
		Action:    "vanity_domain.reject",
		Target:    fmt.Sprintf("domain:%s", domain),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(domainKey, func(ub core.UpdateBuilder) error {
			ub.Set("Status", models.DomainStatusRejected)
			ub.Set("UpdatedAt", now)
			return nil
		}, tabletheory.IfExists())

		tx.UpdateWithBuilder(reqKey, func(ub core.UpdateBuilder) error {
			ub.Set("Status", models.VanityDomainRequestStatusRejected)
			ub.Set("ReviewedBy", actor)
			ub.Set("ReviewedAt", now)
			ub.Set("UpdatedAt", now)
			ub.Set("Note", strings.TrimSpace(note.Note))
			ub.Set("GSI1PK", fmt.Sprintf("VANITY_DOMAIN_REQUESTS#%s", models.VanityDomainRequestStatusRejected))
			ub.Set("GSI1SK", fmt.Sprintf("%s#%s", requestedAt.UTC().Format(time.RFC3339Nano), domain))
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.Condition("Status", "=", models.VanityDomainRequestStatusPending),
		)

		tx.Put(audit)
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return apptheory.JSON(http.StatusOK, req)
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to reject request"}
	}

	req.Status = models.VanityDomainRequestStatusRejected
	req.ReviewedBy = actor
	req.ReviewedAt = now
	req.UpdatedAt = now
	req.Note = strings.TrimSpace(note.Note)

	return apptheory.JSON(http.StatusOK, req)
}

type externalInstanceRegistrationRequest struct {
	Slug string `json:"slug"`
	Note string `json:"note,omitempty"`
}

type externalInstanceRegistrationResponse struct {
	Registration models.ExternalInstanceRegistration `json:"registration"`
}

type listExternalInstanceRegistrationsResponse struct {
	Registrations []models.ExternalInstanceRegistration `json:"registrations"`
	Count         int                                   `json:"count"`
}

func (s *Server) handlePortalCreateExternalInstanceRegistration(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var req externalInstanceRegistrationRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	slug := strings.ToLower(strings.TrimSpace(req.Slug))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "slug is required"}
	}
	if !instanceSlugRE.MatchString(slug) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid slug"}
	}

	// Ensure no instance exists yet.
	if _, err := s.getInstance(ctx, slug); err == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "instance already exists"}
	} else if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	id, err := newToken(16)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create registration"}
	}

	now := time.Now().UTC()
	username := strings.TrimSpace(ctx.AuthIdentity)
	item := &models.ExternalInstanceRegistration{
		ID:        id,
		Username:  username,
		Slug:      slug,
		Status:    models.ExternalInstanceRegistrationStatusPending,
		Note:      strings.TrimSpace(req.Note),
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = item.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(item).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "registration already exists"}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create registration"}
	}

	audit := &models.AuditLogEntry{
		Actor:     username,
		Action:    "external_instance.registration.create",
		Target:    fmt.Sprintf("external_instance_registration:%s", id),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()

	return apptheory.JSON(http.StatusCreated, externalInstanceRegistrationResponse{Registration: *item})
}

func (s *Server) handlePortalListExternalInstanceRegistrations(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireAuthenticated(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	username := strings.TrimSpace(ctx.AuthIdentity)
	pk := fmt.Sprintf(models.KeyPatternUser, username)

	var items []*models.ExternalInstanceRegistration
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.ExternalInstanceRegistration{}).
		Where("PK", "=", pk).
		Where("SK", "BEGINS_WITH", "EXTERNAL_INSTANCE_REG#").
		Limit(200).
		All(&items)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list registrations"}
	}

	out := make([]models.ExternalInstanceRegistration, 0, len(items))
	for _, it := range items {
		if it != nil {
			out = append(out, *it)
		}
	}

	return apptheory.JSON(http.StatusOK, listExternalInstanceRegistrationsResponse{Registrations: out, Count: len(out)})
}

func (s *Server) handleListExternalInstanceRegistrations(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	items, err := listByGSI1PK[models.ExternalInstanceRegistration](
		ctx,
		s,
		&models.ExternalInstanceRegistration{},
		fmt.Sprintf("EXTERNAL_INSTANCE_REGS#%s", models.ExternalInstanceRegistrationStatusPending),
		200,
	)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list registrations"}
	}

	return apptheory.JSON(http.StatusOK, listExternalInstanceRegistrationsResponse{Registrations: items, Count: len(items)})
}

func (s *Server) handleApproveExternalInstanceRegistration(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	username := strings.TrimSpace(ctx.Param("username"))
	id := strings.TrimSpace(ctx.Param("id"))
	if username == "" || id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "username and id are required"}
	}

	var reg models.ExternalInstanceRegistration
	pk := fmt.Sprintf(models.KeyPatternUser, username)
	sk := fmt.Sprintf("EXTERNAL_INSTANCE_REG#%s", id)
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.ExternalInstanceRegistration{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		First(&reg)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "registration not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(reg.Status) == models.ExternalInstanceRegistrationStatusApproved {
		return apptheory.JSON(http.StatusOK, externalInstanceRegistrationResponse{Registration: reg})
	}
	if strings.TrimSpace(reg.Status) != models.ExternalInstanceRegistrationStatusPending {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "registration is not pending"}
	}

	slug := strings.ToLower(strings.TrimSpace(reg.Slug))
	if slug == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "registration is missing slug"}
	}

	// Create an external instance record (no managed primary domain).
	now := time.Now().UTC()
	inst := &models.Instance{
		Slug:      slug,
		Owner:     username,
		Status:    models.InstanceStatusActive,
		CreatedAt: now,
	}
	if err := inst.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	actor := strings.TrimSpace(ctx.AuthIdentity)
	requestedAt := reg.CreatedAt
	updateReg := &models.ExternalInstanceRegistration{
		ID:         id,
		Username:   username,
		Slug:       slug,
		Status:     models.ExternalInstanceRegistrationStatusApproved,
		ReviewedBy: actor,
		ReviewedAt: now,
		UpdatedAt:  now,
		CreatedAt:  requestedAt,
	}
	_ = updateReg.UpdateKeys()

	regKey := &models.ExternalInstanceRegistration{ID: id, Username: username, Slug: slug, CreatedAt: requestedAt}
	_ = regKey.UpdateKeys()

	audit := &models.AuditLogEntry{
		Actor:     actor,
		Action:    "external_instance.registration.approve",
		Target:    fmt.Sprintf("instance:%s", slug),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Create(inst)
		tx.UpdateWithBuilder(regKey, func(ub core.UpdateBuilder) error {
			ub.Set("Status", models.ExternalInstanceRegistrationStatusApproved)
			ub.Set("ReviewedBy", actor)
			ub.Set("ReviewedAt", now)
			ub.Set("UpdatedAt", now)
			ub.Set("GSI1PK", fmt.Sprintf("EXTERNAL_INSTANCE_REGS#%s", models.ExternalInstanceRegistrationStatusApproved))
			ub.Set("GSI1SK", fmt.Sprintf("%s#%s", requestedAt.UTC().Format(time.RFC3339Nano), id))
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.Condition("Status", "=", models.ExternalInstanceRegistrationStatusPending),
		)
		tx.Put(audit)
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return apptheory.JSON(http.StatusOK, externalInstanceRegistrationResponse{Registration: reg})
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to approve registration"}
	}

	reg.Status = models.ExternalInstanceRegistrationStatusApproved
	reg.ReviewedBy = actor
	reg.ReviewedAt = now
	reg.UpdatedAt = now

	return apptheory.JSON(http.StatusOK, externalInstanceRegistrationResponse{Registration: reg})
}

func (s *Server) handleRejectExternalInstanceRegistration(ctx *apptheory.Context) (*apptheory.Response, error) {
	if err := requireOperator(ctx); err != nil {
		return nil, err
	}
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	username := strings.TrimSpace(ctx.Param("username"))
	id := strings.TrimSpace(ctx.Param("id"))
	if username == "" || id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "username and id are required"}
	}

	var reg models.ExternalInstanceRegistration
	pk := fmt.Sprintf(models.KeyPatternUser, username)
	sk := fmt.Sprintf("EXTERNAL_INSTANCE_REG#%s", id)
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.ExternalInstanceRegistration{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		First(&reg)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "registration not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(reg.Status) == models.ExternalInstanceRegistrationStatusRejected {
		return apptheory.JSON(http.StatusOK, externalInstanceRegistrationResponse{Registration: reg})
	}

	now := time.Now().UTC()
	actor := strings.TrimSpace(ctx.AuthIdentity)
	requestedAt := reg.CreatedAt

	regKey := &models.ExternalInstanceRegistration{ID: id, Username: username, Slug: reg.Slug, CreatedAt: requestedAt}
	_ = regKey.UpdateKeys()

	audit := &models.AuditLogEntry{
		Actor:     actor,
		Action:    "external_instance.registration.reject",
		Target:    fmt.Sprintf("external_instance_registration:%s", id),
		RequestID: ctx.RequestID,
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()

	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(regKey, func(ub core.UpdateBuilder) error {
			ub.Set("Status", models.ExternalInstanceRegistrationStatusRejected)
			ub.Set("ReviewedBy", actor)
			ub.Set("ReviewedAt", now)
			ub.Set("UpdatedAt", now)
			ub.Set("GSI1PK", fmt.Sprintf("EXTERNAL_INSTANCE_REGS#%s", models.ExternalInstanceRegistrationStatusRejected))
			ub.Set("GSI1SK", fmt.Sprintf("%s#%s", requestedAt.UTC().Format(time.RFC3339Nano), id))
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.Condition("Status", "=", models.ExternalInstanceRegistrationStatusPending),
		)
		tx.Put(audit)
		return nil
	}); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			return apptheory.JSON(http.StatusOK, externalInstanceRegistrationResponse{Registration: reg})
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to reject registration"}
	}

	reg.Status = models.ExternalInstanceRegistrationStatusRejected
	reg.ReviewedBy = actor
	reg.ReviewedAt = now
	reg.UpdatedAt = now

	return apptheory.JSON(http.StatusOK, externalInstanceRegistrationResponse{Registration: reg})
}
