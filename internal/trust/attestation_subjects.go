package trust

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/domains"
	"github.com/equaltoai/lesser-host/internal/manageddomain"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type instanceAttestationSubject struct {
	InstanceSlug string
	ActorURI     string
	ObjectURI    string
	ContentHash  string
}

func (s *Server) normalizeInstanceAttestationSubject(ctx context.Context, instanceSlug, actorURI, objectURI, contentHash string) (instanceAttestationSubject, *apptheory.AppTheoryError) {
	subject := instanceAttestationSubject{
		InstanceSlug: strings.ToLower(strings.TrimSpace(instanceSlug)),
		ActorURI:     strings.TrimSpace(actorURI),
		ObjectURI:    strings.TrimSpace(objectURI),
		ContentHash:  strings.TrimSpace(contentHash),
	}
	if subject.ActorURI == "" && subject.ObjectURI == "" && subject.ContentHash == "" {
		return subject, nil
	}
	if subject.ActorURI == "" || subject.ObjectURI == "" || subject.ContentHash == "" {
		return instanceAttestationSubject{}, newAppTheoryError("app.bad_request", "actor_uri, object_uri, and content_hash are required together")
	}
	if subject.InstanceSlug == "" {
		return instanceAttestationSubject{}, newAppTheoryError("app.unauthorized", "unauthorized")
	}

	actorHost, err := attestationSubjectURIHost(subject.ActorURI)
	if err != nil {
		return instanceAttestationSubject{}, newAppTheoryError("app.bad_request", "actor_uri must be an http(s) URI with an instance-owned host")
	}
	objectHost, err := attestationSubjectURIHost(subject.ObjectURI)
	if err != nil {
		return instanceAttestationSubject{}, newAppTheoryError("app.bad_request", "object_uri must be an http(s) URI with an instance-owned host")
	}

	ok, appErr := s.instanceOwnsAttestationHosts(ctx, subject.InstanceSlug, actorHost, objectHost)
	if appErr != nil {
		return instanceAttestationSubject{}, appErr
	}
	if !ok {
		return instanceAttestationSubject{}, newAppTheoryError("app.bad_request", "attestation subject is not owned by authenticated instance")
	}
	return subject, nil
}

func attestationSubjectURIHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("uri is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != schemeHTTP && scheme != schemeHTTPS {
		return "", fmt.Errorf("unsupported scheme")
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("host is required")
	}
	return domains.NormalizeDomain(host)
}

func (s *Server) instanceOwnsAttestationHosts(ctx context.Context, instanceSlug, actorHost, objectHost string) (bool, *apptheory.AppTheoryError) {
	instanceSlug = strings.ToLower(strings.TrimSpace(instanceSlug))
	actorHost = strings.ToLower(strings.TrimSpace(actorHost))
	objectHost = strings.ToLower(strings.TrimSpace(objectHost))
	if instanceSlug == "" || actorHost == "" || objectHost == "" {
		return false, nil
	}
	inst, found, appErr := s.loadAttestationSubjectInstance(ctx, instanceSlug)
	if appErr != nil || !found {
		return false, appErr
	}

	owned := map[string]struct{}{}
	addOwnedAttestationDomain(owned, inst.HostedBaseDomain)
	addOwnedAttestationDomain(owned, manageddomain.StageDomain(s.cfg.Stage, inst.HostedBaseDomain))
	if attestationHostsInSet(owned, actorHost, objectHost) {
		return true, nil
	}

	domainItems, appErr := s.loadAttestationSubjectDomains(ctx, instanceSlug)
	if appErr != nil {
		return false, appErr
	}
	for _, d := range domainItems {
		if d == nil || !attestationDomainActive(d.Status) {
			continue
		}
		addOwnedAttestationDomain(owned, d.Domain)
		if strings.EqualFold(strings.TrimSpace(d.Type), models.DomainTypePrimary) || strings.EqualFold(strings.TrimSpace(d.Domain), strings.TrimSpace(inst.HostedBaseDomain)) {
			addOwnedAttestationDomain(owned, manageddomain.StageDomain(s.cfg.Stage, d.Domain))
		}
	}
	return attestationHostsInSet(owned, actorHost, objectHost), nil
}

func (s *Server) loadAttestationSubjectInstance(ctx context.Context, instanceSlug string) (models.Instance, bool, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return models.Instance{}, false, newAppTheoryError("app.internal", "internal error")
	}
	var inst models.Instance
	err := s.store.DB.WithContext(ctx).
		Model(&models.Instance{}).
		Where("PK", "=", "INSTANCE#"+instanceSlug).
		Where("SK", "=", models.SKMetadata).
		ConsistentRead().
		First(&inst)
	if theoryErrors.IsNotFound(err) {
		return models.Instance{}, false, nil
	}
	if err != nil {
		return models.Instance{}, false, newAppTheoryError("app.internal", "internal error")
	}
	return inst, true, nil
}

func (s *Server) loadAttestationSubjectDomains(ctx context.Context, instanceSlug string) ([]*models.Domain, *apptheory.AppTheoryError) {
	var domainItems []*models.Domain
	err := s.store.DB.WithContext(ctx).
		Model(&models.Domain{}).
		Index("gsi1").
		Where("gsi1PK", "=", "INSTANCE_DOMAINS#"+instanceSlug).
		All(&domainItems)
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	return domainItems, nil
}

func addOwnedAttestationDomain(owned map[string]struct{}, raw string) {
	if owned == nil {
		return
	}
	d, err := domains.NormalizeDomain(raw)
	if err != nil || d == "" {
		return
	}
	owned[d] = struct{}{}
}

func attestationHostsInSet(owned map[string]struct{}, actorHost, objectHost string) bool {
	if len(owned) == 0 {
		return false
	}
	_, actorOK := owned[strings.ToLower(strings.TrimSpace(actorHost))]
	_, objectOK := owned[strings.ToLower(strings.TrimSpace(objectHost))]
	return actorOK && objectOK
}

func attestationDomainActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case models.DomainStatusVerified, models.DomainStatusActive:
		return true
	default:
		return false
	}
}
