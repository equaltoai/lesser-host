package controlplane

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestUpdateSoulAgentRegistrationForInstance_RejectsDifferentInstance(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRPCURL:                  "http://rpc.local",
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulPackBucketName:          "bucket",
		},
		soulPacks: &fakeSoulPackStore{},
	}

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:                soulLifecycleTestAgentIDHex,
			Domain:                 "example.com",
			LocalID:                "agent-alice",
			Wallet:                 "0x000000000000000000000000000000000000beef",
			Status:                 models.SoulAgentStatusActive,
			LifecycleStatus:        models.SoulAgentStatusActive,
			SelfDescriptionVersion: 2,
		}
	}).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:       "example.com",
			InstanceSlug: "other-instance",
			Status:       models.DomainStatusVerified,
		}
	}).Once()

	_, appErr := s.UpdateSoulAgentRegistrationForInstance(context.Background(), "inst1", "rid-1", soulLifecycleTestAgentIDHex, nil)
	require.Error(t, appErr)
	require.Equal(t, "app.unauthorized", appErr.Code)
}

func TestRequireSoulAgentInstanceAccess_AllowsManagedStageAlias(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{Stage: "lab"},
	}

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:             "example.com",
			InstanceSlug:       "inst1",
			Type:               models.DomainTypePrimary,
			Status:             models.DomainStatusVerified,
			VerificationMethod: "managed",
		}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", HostedBaseDomain: "example.com"}
	}).Once()

	if appErr := s.requireSoulAgentInstanceAccess(context.Background(), "inst1", &models.SoulAgentIdentity{Domain: "dev.example.com"}); appErr != nil {
		t.Fatalf("expected success, got %v", appErr)
	}
}

func TestRequireSoulAgentInstanceAccess_RejectsManagedStageAliasForOtherInstance(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{Stage: "lab"},
	}

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:             "example.com",
			InstanceSlug:       "inst2",
			Type:               models.DomainTypePrimary,
			Status:             models.DomainStatusVerified,
			VerificationMethod: "managed",
		}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst2", HostedBaseDomain: "example.com"}
	}).Once()

	appErr := s.requireSoulAgentInstanceAccess(context.Background(), "inst1", &models.SoulAgentIdentity{Domain: "dev.example.com"})
	require.Error(t, appErr)
	require.Equal(t, "app.unauthorized", appErr.Code)
}
