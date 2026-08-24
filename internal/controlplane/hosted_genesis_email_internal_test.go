package controlplane

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const hostedGenesisEmailTestExistingPassword = "existing-password"

func TestEnsureHostedGenesisRequiredEmail_EnforcesInstanceTrustOnly(t *testing.T) {
	t.Parallel()

	identity := &models.SoulAgentIdentity{AgentID: soulLifecycleTestAgentIDHex, AuthorityModel: models.SoulAuthorityModelInstanceTrust}
	reg := &models.SoulAgentRegistration{AuthorityModel: models.SoulAuthorityModelInstanceTrust}
	inst := &models.Instance{Slug: provisionTestInstanceSlug}
	wantErr := newAppTheoryError("app.internal", "email failed")
	calls := 0
	s := &Server{hostedGenesisEmailProvisioner: func(_ *apptheory.Context, gotIdentity *models.SoulAgentIdentity, gotInst *models.Instance) *apptheory.AppTheoryError {
		calls++
		if gotIdentity != identity || gotInst != inst {
			t.Fatalf("unexpected provision inputs: identity=%p instance=%p", gotIdentity, gotInst)
		}
		return wantErr
	}}
	ctx := &apptheory.Context{}

	if got := s.ensureHostedGenesisRequiredEmail(ctx, mintConversationFinalizeContext{reg: reg, identity: identity, inst: inst}); got != wantErr {
		t.Fatalf("expected provisioning failure to block publication, got %v", got)
	}
	if calls != 1 {
		t.Fatalf("expected one automatic provision call, got %d", calls)
	}

	reg.AuthorityModel = models.SoulAuthorityModelWalletPrincipal
	identity.AuthorityModel = models.SoulAuthorityModelWalletPrincipal
	if got := s.ensureHostedGenesisRequiredEmail(ctx, mintConversationFinalizeContext{reg: reg, identity: identity, inst: inst}); got != nil {
		t.Fatalf("wallet-principal path must retain explicit signed provisioning, got %v", got)
	}
	if calls != 1 {
		t.Fatalf("wallet-principal path unexpectedly provisioned email; calls=%d", calls)
	}
}

func TestEnsureHostedGenesisEmailChannel_ProvisionsRequiredChannel(t *testing.T) {
	t.Parallel()

	fixture := newHostedGenesisEmailFixture(t)
	fixture.tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()
	fixture.tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Twice()

	if appErr := fixture.server.ensureHostedGenesisEmailChannel(fixture.ctx, fixture.identity, fixture.instance); appErr != nil {
		t.Fatalf("provision required email: %v", appErr)
	}
	if fixture.createCalls != 1 || fixture.updateCalls != 0 || fixture.forwardCalls != 1 {
		t.Fatalf("unexpected provider calls create=%d update=%d forwarding=%d", fixture.createCalls, fixture.updateCalls, fixture.forwardCalls)
	}
	if fixture.createdLocalPart != provisionTestEmailLocalPart || fixture.forwardedAddress != provisionTestEmailLocalPart+"@inbound.lessersoul.ai" {
		t.Fatalf("unexpected provider addressing local=%q forwarding=%q", fixture.createdLocalPart, fixture.forwardedAddress)
	}
	param := fixture.server.soulAgentEmailPasswordSSMParam(fixture.identity.AgentID)
	if fixture.ssm[param] == "" {
		t.Fatalf("expected per-agent password in SSM fixture")
	}
	fixture.tdb.qChannel.AssertNumberOfCalls(t, "Create", 1)
	fixture.tdb.qChannel.AssertNumberOfCalls(t, "CreateOrUpdate", 1)
	fixture.tdb.qChannelIdx.AssertNumberOfCalls(t, "CreateOrUpdate", 1)
}

func TestEnsureHostedGenesisEmailChannel_RecoversPausedClaim(t *testing.T) {
	t.Parallel()

	fixture := newHostedGenesisEmailFixture(t)
	param := fixture.server.soulAgentEmailPasswordSSMParam(fixture.identity.AgentID)
	fixture.ssm[param] = hostedGenesisEmailTestExistingPassword
	fixture.updateErr = &migaduAPIError{operation: "migadu update mailbox", statusCode: http.StatusNotFound}
	fixture.tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = *provisionalHostedGenesisEmailChannel(fixture.identity.AgentID, provisionTestEmailAddress, param, time.Now().Add(-time.Minute))
	}).Once()
	fixture.tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Twice()

	if appErr := fixture.server.ensureHostedGenesisEmailChannel(fixture.ctx, fixture.identity, fixture.instance); appErr != nil {
		t.Fatalf("recover required email: %v", appErr)
	}
	if fixture.updateCalls != 1 || fixture.createCalls != 1 || fixture.forwardCalls != 1 {
		t.Fatalf("expected update-not-found recovery, got create=%d update=%d forwarding=%d", fixture.createCalls, fixture.updateCalls, fixture.forwardCalls)
	}
	fixture.tdb.qChannel.AssertNumberOfCalls(t, "Create", 0)
	fixture.tdb.qChannel.AssertNumberOfCalls(t, "CreateOrUpdate", 1)
}

func TestEnsureHostedGenesisEmailChannel_HealthyIsIdempotent(t *testing.T) {
	t.Parallel()

	fixture := newHostedGenesisEmailFixture(t)
	param := fixture.server.soulAgentEmailPasswordSSMParam(fixture.identity.AgentID)
	fixture.ssm[param] = hostedGenesisEmailTestExistingPassword
	healthy := provisionalHostedGenesisEmailChannel(fixture.identity.AgentID, provisionTestEmailAddress, param, time.Now().Add(-time.Minute))
	activateHostedGenesisEmailChannel(healthy, time.Now().Add(-time.Minute))
	fixture.tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = *healthy
	}).Once()
	fixture.tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Twice()

	if appErr := fixture.server.ensureHostedGenesisEmailChannel(fixture.ctx, fixture.identity, fixture.instance); appErr != nil {
		t.Fatalf("reconcile healthy required email: %v", appErr)
	}
	if fixture.createCalls != 0 || fixture.updateCalls != 0 || fixture.forwardCalls != 1 {
		t.Fatalf("healthy reconciliation must not recreate credentials: create=%d update=%d forwarding=%d", fixture.createCalls, fixture.updateCalls, fixture.forwardCalls)
	}
	fixture.tdb.qChannel.AssertNumberOfCalls(t, "Create", 0)
	fixture.tdb.qChannel.AssertNumberOfCalls(t, "CreateOrUpdate", 0)
	fixture.tdb.qChannelIdx.AssertNumberOfCalls(t, "CreateOrUpdate", 1)
}

func TestEnsureHostedGenesisEmailChannel_ProviderCollisionFailsClosed(t *testing.T) {
	t.Parallel()

	fixture := newHostedGenesisEmailFixture(t)
	fixture.createErr = &migaduAPIError{operation: "migadu create mailbox", statusCode: http.StatusConflict}
	fixture.tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()
	fixture.tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()

	appErr := fixture.server.ensureHostedGenesisEmailChannel(fixture.ctx, fixture.identity, fixture.instance)
	if appErr == nil || appErr.Code != appErrCodeConflict || appErr.Message != soulEmailProvisionErrAddressTaken {
		t.Fatalf("expected provider collision to fail closed, got %v", appErr)
	}
	if fixture.createCalls != 1 || fixture.updateCalls != 0 || fixture.forwardCalls != 0 {
		t.Fatalf("collision must not take over or forward provider mailbox: create=%d update=%d forwarding=%d", fixture.createCalls, fixture.updateCalls, fixture.forwardCalls)
	}
	fixture.tdb.qChannel.AssertNumberOfCalls(t, "Create", 1)
	fixture.tdb.qChannel.AssertNumberOfCalls(t, "CreateOrUpdate", 1)
}

func TestEnsureHostedGenesisEmailChannel_RecoveryNeverDeletesExistingMailbox(t *testing.T) {
	t.Parallel()

	fixture := newHostedGenesisEmailFixture(t)
	param := fixture.server.soulAgentEmailPasswordSSMParam(fixture.identity.AgentID)
	fixture.ssm[param] = hostedGenesisEmailTestExistingPassword
	fixture.forwardErr = errors.New("forwarding unavailable")
	fixture.tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = *provisionalHostedGenesisEmailChannel(fixture.identity.AgentID, provisionTestEmailAddress, param, time.Now().Add(-time.Minute))
	}).Once()
	fixture.tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()

	appErr := fixture.server.ensureHostedGenesisEmailChannel(fixture.ctx, fixture.identity, fixture.instance)
	if appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected forwarding failure, got %v", appErr)
	}
	if fixture.updateCalls != 1 || fixture.forwardCalls != 1 || fixture.deleteCalls != 0 {
		t.Fatalf("existing mailbox recovery must never roll back mailbox data: update=%d forwarding=%d delete=%d", fixture.updateCalls, fixture.forwardCalls, fixture.deleteCalls)
	}
}

type hostedGenesisEmailFixture struct {
	tdb              soulLifecycleTestDB
	server           *Server
	ctx              *apptheory.Context
	identity         *models.SoulAgentIdentity
	instance         *models.Instance
	ssm              map[string]string
	createCalls      int
	updateCalls      int
	forwardCalls     int
	createdLocalPart string
	forwardedAddress string
	createErr        error
	updateErr        error
	forwardErr       error
	deleteCalls      int
}

func newHostedGenesisEmailFixture(t *testing.T) *hostedGenesisEmailFixture {
	t.Helper()
	fixture := &hostedGenesisEmailFixture{
		tdb: newSoulLifecycleTestDB(),
		ctx: &apptheory.Context{RequestID: "req-hosted-email", AuthIdentity: "instance:inst1"},
		identity: &models.SoulAgentIdentity{
			AgentID:        soulLifecycleTestAgentIDHex,
			LocalID:        provisionTestAgentLocalID,
			Domain:         provisionTestAgentDomain,
			AuthorityModel: models.SoulAuthorityModelInstanceTrust,
		},
		instance: &models.Instance{Slug: provisionTestInstanceSlug},
		ssm:      map[string]string{},
	}
	fixture.server = &Server{
		store: store.New(fixture.tdb.db),
		cfg: config.Config{
			Stage:                  "lab",
			SoulEmailInboundDomain: "inbound.lessersoul.ai",
		},
		ssmGetParameter: func(_ context.Context, name string) (string, error) {
			value, ok := fixture.ssm[name]
			if !ok {
				return "", errors.New("not found")
			}
			return value, nil
		},
		ssmPutSecureValue: func(_ context.Context, name string, value string, overwrite bool) error {
			if !overwrite {
				if _, ok := fixture.ssm[name]; ok {
					return errors.New("ParameterAlreadyExists")
				}
			}
			fixture.ssm[name] = value
			return nil
		},
		migaduCreateEmail: func(_ context.Context, localPart string, _ string, _ string) error {
			fixture.createCalls++
			fixture.createdLocalPart = localPart
			return fixture.createErr
		},
		migaduUpdateEmail: func(_ context.Context, _ string, _ string) error {
			fixture.updateCalls++
			return fixture.updateErr
		},
		migaduForwarding: func(_ context.Context, _ string, address string) error {
			fixture.forwardCalls++
			fixture.forwardedAddress = address
			return fixture.forwardErr
		},
		migaduDeleteEmail: func(_ context.Context, _ string) error {
			fixture.deleteCalls++
			return nil
		},
	}
	return fixture
}
