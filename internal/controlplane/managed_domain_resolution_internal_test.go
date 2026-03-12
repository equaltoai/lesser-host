package controlplane

import (
	"context"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestLoadManagedStageAwareDomain_ManagedAlias(t *testing.T) {
	t.Parallel()

	db, queries := newTestDBWithModelQueries("*models.Domain", "*models.Instance")
	qDomain := queries[0]
	qInstance := queries[1]

	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:             "simulacrum.greater.website",
			InstanceSlug:       "simulacrum",
			Status:             models.DomainStatusVerified,
			Type:               models.DomainTypePrimary,
			VerificationMethod: "managed",
		}
	}).Once()
	qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "simulacrum", HostedBaseDomain: "simulacrum.greater.website"}
	}).Once()

	s := &Server{store: store.New(db), cfg: config.Config{Stage: "lab"}}
	d, err := s.loadManagedStageAwareDomain(context.Background(), "dev.simulacrum.greater.website")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if d == nil || d.Domain != "simulacrum.greater.website" || d.InstanceSlug != "simulacrum" {
		t.Fatalf("unexpected managed alias resolution: %#v", d)
	}
}

func TestLoadManagedStageAwareDomain_ExactDomain(t *testing.T) {
	t.Parallel()

	db, queries := newTestDBWithModelQueries("*models.Domain", "*models.Instance")
	qDomain := queries[0]
	_ = queries[1]

	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:       "example.com",
			InstanceSlug: "inst1",
			Status:       models.DomainStatusVerified,
		}
	}).Once()

	s := &Server{store: store.New(db), cfg: config.Config{Stage: "lab"}}
	d, err := s.loadManagedStageAwareDomain(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if d == nil || d.Domain != "example.com" || d.InstanceSlug != "inst1" {
		t.Fatalf("unexpected exact domain resolution: %#v", d)
	}
}

func TestLoadManagedStageAwareDomain_ManagedAliasRejectsMismatchedHostedBaseDomain(t *testing.T) {
	t.Parallel()

	db, queries := newTestDBWithModelQueries("*models.Domain", "*models.Instance")
	qDomain := queries[0]
	qInstance := queries[1]

	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:             "simulacrum.greater.website",
			InstanceSlug:       "simulacrum",
			Status:             models.DomainStatusVerified,
			Type:               models.DomainTypePrimary,
			VerificationMethod: "managed",
		}
	}).Once()
	qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "simulacrum", HostedBaseDomain: "other.greater.website"}
	}).Once()

	s := &Server{store: store.New(db), cfg: config.Config{Stage: "lab"}}
	if _, err := s.loadManagedStageAwareDomain(context.Background(), "dev.simulacrum.greater.website"); !theoryErrors.IsNotFound(err) {
		t.Fatalf("expected not found for mismatched hosted base domain, got %v", err)
	}
}

func TestLoadManagedStageAwareDomain_ManagedAliasRejectsUnverifiedDomain(t *testing.T) {
	t.Parallel()

	db, queries := newTestDBWithModelQueries("*models.Domain", "*models.Instance")
	qDomain := queries[0]
	_ = queries[1]

	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:             "simulacrum.greater.website",
			InstanceSlug:       "simulacrum",
			Status:             models.DomainStatusPending,
			Type:               models.DomainTypePrimary,
			VerificationMethod: "managed",
		}
	}).Once()

	s := &Server{store: store.New(db), cfg: config.Config{Stage: "lab"}}
	if _, err := s.loadManagedStageAwareDomain(context.Background(), "dev.simulacrum.greater.website"); !theoryErrors.IsNotFound(err) {
		t.Fatalf("expected not found for unverified alias domain, got %v", err)
	}
}

func TestRequireSoulAgentInstanceAccess_ManagedStageAlias(t *testing.T) {
	t.Parallel()

	db, queries := newTestDBWithModelQueries("*models.Domain", "*models.Instance")
	qDomain := queries[0]
	qInstance := queries[1]

	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:             "simulacrum.greater.website",
			InstanceSlug:       "simulacrum",
			Status:             models.DomainStatusVerified,
			Type:               models.DomainTypePrimary,
			VerificationMethod: "managed",
		}
	}).Once()
	qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "simulacrum", HostedBaseDomain: "simulacrum.greater.website"}
	}).Once()

	s := &Server{store: store.New(db), cfg: config.Config{Stage: "lab"}}
	if appErr := s.requireSoulAgentInstanceAccess(context.Background(), "simulacrum", &models.SoulAgentIdentity{Domain: "dev.simulacrum.greater.website"}); appErr != nil {
		t.Fatalf("expected managed alias access, got %#v", appErr)
	}
}

func TestLoadTelnyxVoiceInstanceSlug_ManagedStageAlias(t *testing.T) {
	t.Parallel()

	db, queries := newTestDBWithModelQueries("*models.Domain", "*models.Instance")
	qDomain := queries[0]
	qInstance := queries[1]

	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:             "simulacrum.greater.website",
			InstanceSlug:       "simulacrum",
			Status:             models.DomainStatusVerified,
			Type:               models.DomainTypePrimary,
			VerificationMethod: "managed",
		}
	}).Once()
	qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "simulacrum", HostedBaseDomain: "simulacrum.greater.website"}
	}).Once()

	s := &Server{store: store.New(db), cfg: config.Config{Stage: "lab"}}
	slug, err := s.loadTelnyxVoiceInstanceSlug(&apptheory.Context{RequestID: "req"}, &models.SoulAgentIdentity{Domain: "dev.simulacrum.greater.website"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if slug != "simulacrum" {
		t.Fatalf("expected simulacrum slug, got %q", slug)
	}
}

func TestLoadMetadata_BlankAndNotFound(t *testing.T) {
	t.Parallel()

	db, queries := newTestDBWithModelQueries("*models.Domain", "*models.Instance")
	qDomain := queries[0]
	qInstance := queries[1]

	s := &Server{store: store.New(db), cfg: config.Config{Stage: "lab"}}
	if d, err := s.loadDomainMetadata(context.Background(), " "); !theoryErrors.IsNotFound(err) || d != nil {
		t.Fatalf("expected blank domain to miss, got domain=%#v err=%v", d, err)
	}
	if inst, err := s.loadInstanceMetadata(context.Background(), " "); !theoryErrors.IsNotFound(err) || inst != nil {
		t.Fatalf("expected blank instance slug to miss, got inst=%#v err=%v", inst, err)
	}

	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
	if d, err := s.loadDomainMetadata(context.Background(), "missing.example"); !theoryErrors.IsNotFound(err) || d != nil {
		t.Fatalf("expected missing domain to miss, got domain=%#v err=%v", d, err)
	}

	qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()
	if inst, err := s.loadInstanceMetadata(context.Background(), "missing"); !theoryErrors.IsNotFound(err) || inst != nil {
		t.Fatalf("expected missing instance slug to miss, got inst=%#v err=%v", inst, err)
	}
}
