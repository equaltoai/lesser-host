package controlplane

import (
	"context"
	"fmt"
	"strings"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/manageddomain"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) loadDomainMetadata(ctx context.Context, normalizedDomain string) (*models.Domain, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, fmt.Errorf("store not configured")
	}
	normalizedDomain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(normalizedDomain)), ".")
	if normalizedDomain == "" {
		return nil, theoryErrors.ErrItemNotFound
	}

	var d models.Domain
	err := s.store.DB.WithContext(ctx).
		Model(&models.Domain{}).
		Where("PK", "=", fmt.Sprintf("DOMAIN#%s", normalizedDomain)).
		Where("SK", "=", models.SKMetadata).
		First(&d)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Server) loadInstanceMetadata(ctx context.Context, instanceSlug string) (*models.Instance, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, fmt.Errorf("store not configured")
	}
	instanceSlug = strings.TrimSpace(instanceSlug)
	if instanceSlug == "" {
		return nil, theoryErrors.ErrItemNotFound
	}

	var inst models.Instance
	err := s.store.DB.WithContext(ctx).
		Model(&models.Instance{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE#%s", instanceSlug)).
		Where("SK", "=", models.SKMetadata).
		First(&inst)
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

func (s *Server) loadManagedStageAliasPrimaryDomain(ctx context.Context, normalizedDomain string) (*models.Domain, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, fmt.Errorf("store not configured")
	}

	baseDomain, ok := manageddomain.BaseDomainFromStageDomain(s.cfg.Stage, normalizedDomain)
	if !ok {
		return nil, theoryErrors.ErrItemNotFound
	}

	d, err := s.loadDomainMetadata(ctx, baseDomain)
	if err != nil {
		return nil, err
	}
	if !domainIsVerifiedOrActive(d.Status) ||
		strings.TrimSpace(d.Type) != models.DomainTypePrimary ||
		!strings.EqualFold(strings.TrimSpace(d.VerificationMethod), "managed") {
		return nil, theoryErrors.ErrItemNotFound
	}
	return d, nil
}

func (s *Server) loadManagedStageAliasDomain(ctx context.Context, normalizedDomain string) (*models.Domain, error) {
	d, err := s.loadManagedStageAliasPrimaryDomain(ctx, normalizedDomain)
	if err != nil {
		return nil, err
	}
	inst, err := s.loadInstanceMetadata(ctx, d.InstanceSlug)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(inst.HostedBaseDomain), strings.TrimSpace(d.Domain)) {
		return nil, theoryErrors.ErrItemNotFound
	}
	return d, nil
}

func (s *Server) loadManagedStageAwareDomain(ctx context.Context, normalizedDomain string) (*models.Domain, error) {
	d, err := s.loadDomainMetadata(ctx, normalizedDomain)
	if err == nil {
		return d, nil
	}
	if !theoryErrors.IsNotFound(err) {
		return nil, err
	}
	return s.loadManagedStageAliasDomain(ctx, normalizedDomain)
}
