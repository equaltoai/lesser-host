package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestRequireSoulPortalPrereqs_UnauthorizedAndForbidden(t *testing.T) {
	t.Parallel()

	s := &Server{}
	require.Equal(t, "app.unauthorized", s.requireSoulPortalPrereqs(&apptheory.Context{}).Code)

	operatorCtx := adminCtx()
	require.Nil(t, s.requireSoulPortalPrereqs(operatorCtx))

	tdb := newSoulRegistryTestDB()
	s2 := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{SoulEnabled: true},
	}
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{Username: "alice", Role: models.RoleCustomer, ApprovalStatus: models.UserApprovalStatusRejected}
	}).Once()

	appErr := s2.requireSoulPortalPrereqs(&apptheory.Context{AuthIdentity: "alice"})
	require.NotNil(t, appErr)
	require.Equal(t, "app.forbidden", appErr.Code)
}

func TestRequireSoulDomainAccess_Errors(t *testing.T) {
	t.Parallel()

	tdb := newSoulRegistryTestDB()
	s := &Server{store: store.New(tdb.db)}

	_, _, appErr := s.requireSoulDomainAccess(nil, "example.com")
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)

	ctx := &apptheory.Context{AuthIdentity: "alice"}
	_, _, appErr = s.requireSoulDomainAccess(ctx, " ")
	require.NotNil(t, appErr)
	require.Equal(t, "app.bad_request", appErr.Code)

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
	_, _, appErr = s.requireSoulDomainAccess(ctx, "example.com")
	require.NotNil(t, appErr)
	require.Equal(t, "app.bad_request", appErr.Code)

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusPending}
	}).Once()
	_, _, appErr = s.requireSoulDomainAccess(ctx, "example.com")
	require.NotNil(t, appErr)
	require.Equal(t, "app.bad_request", appErr.Code)

	// Forbidden instance owner.
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "bob"}
	}).Once()
	_, _, appErr = s.requireSoulDomainAccess(ctx, "example.com")
	require.NotNil(t, appErr)
	require.Equal(t, "app.forbidden", appErr.Code)
}

func TestLoadSoulAgentRegistrationForVerify(t *testing.T) {
	t.Parallel()

	tdb := newSoulRegistryTestDB()
	s := &Server{store: store.New(tdb.db)}
	ctx := &apptheory.Context{AuthIdentity: "alice"}

	tdb.qReg.On("First", mock.AnythingOfType("*models.SoulAgentRegistration")).Return(theoryErrors.ErrItemNotFound).Once()
	_, appErr := s.loadSoulAgentRegistrationForVerify(ctx, "missing")
	require.NotNil(t, appErr)
	require.Equal(t, "app.not_found", appErr.Code)

	tdb.qReg.On("First", mock.AnythingOfType("*models.SoulAgentRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentRegistration](t, args, 0)
		*dest = models.SoulAgentRegistration{ID: "r1", ExpiresAt: time.Now().UTC().Add(-1 * time.Hour)}
	}).Once()
	_, appErr = s.loadSoulAgentRegistrationForVerify(ctx, "r1")
	require.NotNil(t, appErr)
	require.Equal(t, "app.bad_request", appErr.Code)

	tdb.qReg.On("First", mock.AnythingOfType("*models.SoulAgentRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentRegistration](t, args, 0)
		*dest = models.SoulAgentRegistration{ID: "r2", Status: models.SoulAgentRegistrationStatusCompleted}
	}).Once()
	_, appErr = s.loadSoulAgentRegistrationForVerify(ctx, "r2")
	require.NotNil(t, appErr)
	require.Equal(t, "app.conflict", appErr.Code)

	tdb.qReg.On("First", mock.AnythingOfType("*models.SoulAgentRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentRegistration](t, args, 0)
		*dest = models.SoulAgentRegistration{ID: "r3", Status: models.SoulAgentRegistrationStatusPending}
	}).Once()
	reg, appErr := s.loadSoulAgentRegistrationForVerify(ctx, "r3")
	require.Nil(t, appErr)
	require.NotNil(t, reg)
	require.Equal(t, "r3", reg.ID)
}

func TestSoulRegistryProofVerificationBranches(t *testing.T) {
	t.Parallel()

	require.False(t, verifySoulRegistryHTTPS(context.Background(), "", "x"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, verifySoulRegistryDNS(ctx, "example.com", "proof"))

	_, _, appErr := verifySoulAgentRegistrationProofs(context.Background(), nil)
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)

	reg := &models.SoulAgentRegistration{DNSVerified: true, HTTPSVerified: true}
	vDNS, vHTTPS, appErr := verifySoulAgentRegistrationProofs(context.Background(), reg)
	require.Nil(t, appErr)
	require.True(t, vDNS)
	require.True(t, vHTTPS)

	reg2 := &models.SoulAgentRegistration{DomainNormalized: "", ProofToken: "t", DNSVerified: false, HTTPSVerified: true}
	_, _, appErr = verifySoulAgentRegistrationProofs(context.Background(), reg2)
	require.NotNil(t, appErr)
	require.Equal(t, "app.bad_request", appErr.Code)

	reg3 := &models.SoulAgentRegistration{DomainNormalized: "", ProofToken: "t", DNSVerified: true, HTTPSVerified: false}
	_, _, appErr = verifySoulAgentRegistrationProofs(context.Background(), reg3)
	require.NotNil(t, appErr)
	require.Equal(t, "app.bad_request", appErr.Code)
}

func TestEnsureSoulAgentNotActive_Branches(t *testing.T) {
	t.Parallel()

	tdb := newSoulRegistryTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{Status: models.SoulAgentStatusActive}
	}).Once()
	appErr := s.ensureSoulAgentNotActive(context.Background(), "0x"+strings.Repeat("11", 32))
	require.NotNil(t, appErr)
	require.Equal(t, "app.conflict", appErr.Code)

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(errors.New("boom")).Once()
	appErr = s.ensureSoulAgentNotActive(context.Background(), "0x"+strings.Repeat("22", 32))
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)
}

func TestVerifySoulAgentRegistrationWallet_InvalidSignature(t *testing.T) {
	t.Parallel()

	appErr := verifySoulAgentRegistrationWallet(&models.SoulAgentRegistration{
		Wallet:        "0x0000000000000000000000000000000000000002",
		WalletMessage: "hello",
	}, "not-a-signature")
	require.NotNil(t, appErr)
	require.Equal(t, "app.forbidden", appErr.Code)
}

func TestParseSoulAgentRegistrationVerifyInput_BadRequests(t *testing.T) {
	t.Parallel()

	_, _, err := parseSoulAgentRegistrationVerifyInput(nil)
	require.NotNil(t, err)

	_, _, err = parseSoulAgentRegistrationVerifyInput(&apptheory.Context{})
	require.NotNil(t, err)

	ctxMissingSig := &apptheory.Context{
		Params:  map[string]string{"id": "r1"},
		Request: apptheory.Request{Body: []byte(`{"signature":""}`)},
	}
	_, _, err = parseSoulAgentRegistrationVerifyInput(ctxMissingSig)
	require.NotNil(t, err)
	if appErr, ok := err.(*apptheory.AppError); ok {
		require.Equal(t, "app.bad_request", appErr.Code)
	}
}

func TestBuildSoulMintPayload_ErrorsAndSuccess(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{}}
	_, _, _, appErr := s.buildSoulMintPayload(nil)
	require.NotNil(t, appErr)

	s.cfg.SoulRegistryContractAddress = "0x0000000000000000000000000000000000000001"
	// No mint signer key → conflict.
	_, _, _, appErr = s.buildSoulMintPayload(&models.SoulAgentRegistration{AgentID: "0x" + strings.Repeat("11", 32), Wallet: "0x0000000000000000000000000000000000000002"})
	require.NotNil(t, appErr)
	require.Equal(t, "app.conflict", appErr.Code)

	// Use a known test private key (not a real secret).
	s.cfg.SoulMintSignerKey = strings.Repeat("ab", 32)
	s.cfg.SoulChainID = 84532

	_, _, _, appErr = s.buildSoulMintPayload(&models.SoulAgentRegistration{AgentID: "", Wallet: "0x0000000000000000000000000000000000000002"})
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)

	_, _, _, appErr = s.buildSoulMintPayload(&models.SoulAgentRegistration{AgentID: "0x" + strings.Repeat("11", 32), Wallet: "not-a-wallet"})
	require.NotNil(t, appErr)
	require.Equal(t, "app.bad_request", appErr.Code)

	_, _, _, appErr = s.buildSoulMintPayload(&models.SoulAgentRegistration{AgentID: "0xzz", Wallet: "0x0000000000000000000000000000000000000002"})
	require.NotNil(t, appErr)
	require.Equal(t, "app.bad_request", appErr.Code)

	payload, metaURI, _, appErr := s.buildSoulMintPayload(&models.SoulAgentRegistration{AgentID: "0x" + strings.Repeat("11", 32), Wallet: "0x0000000000000000000000000000000000000002"})
	require.Nil(t, appErr)
	require.NotNil(t, payload)
	require.True(t, strings.HasPrefix(payload.Data, "0x"))
	require.NotEmpty(t, metaURI)
	require.True(t, payload.ChainID > 0)
	require.True(t, payload.Deadline > 0)
}

func TestCompleteSoulAgentRegistration_UpdateError(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qReg := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentRegistration")).Return(qReg).Maybe()

	qReg.On("IfExists").Return(qReg).Maybe()
	qReg.On("Update", mock.Anything).Return(errors.New("boom")).Once()

	s := &Server{store: store.New(db)}
	ctx := &apptheory.Context{}

	now := time.Unix(123, 0).UTC()
	reg := &models.SoulAgentRegistration{
		ID:               "r1",
		Username:         "alice",
		DomainRaw:        "example.com",
		DomainNormalized: "example.com",
		LocalIDRaw:       "agent-alice",
		LocalID:          "agent-alice",
		AgentID:          "0x" + strings.Repeat("11", 32),
		Wallet:           "0x0000000000000000000000000000000000000002",
		WalletNonce:      "n",
		WalletMessage:    "m",
		ProofToken:       "t",
		Status:           models.SoulAgentRegistrationStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(10 * time.Minute),
	}
	_ = reg.UpdateKeys()

	update, appErr := s.completeSoulAgentRegistration(ctx, reg, true, true, now)
	require.NotNil(t, appErr)
	require.Nil(t, update)
	require.Equal(t, "app.internal", appErr.Code)
}

func TestCompleteSoulAgentRegistration_NilPrereqs(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	var nilServer *Server
	_, appErr := nilServer.completeSoulAgentRegistration(&apptheory.Context{}, &models.SoulAgentRegistration{}, true, true, now)
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)

	db := ttmocks.NewMockExtendedDB()
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	s := &Server{store: store.New(db)}

	_, appErr = s.completeSoulAgentRegistration(nil, &models.SoulAgentRegistration{}, true, true, now)
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)

	_, appErr = s.completeSoulAgentRegistration(&apptheory.Context{}, nil, true, true, now)
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)
}

func TestUpsertSoulAgentIndexes_Capabilities(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qWallet := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qCap := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulWalletAgentIndex")).Return(qWallet).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulDomainAgentIndex")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(qCap).Maybe()

	for _, q := range []*ttmocks.MockQuery{qWallet, qDomain, qCap} {
		q.On("CreateOrUpdate").Return(nil).Maybe()
	}

	s := &Server{store: store.New(db)}
	s.upsertSoulAgentIndexes(context.Background(), &models.SoulAgentRegistration{
		AgentID:          "0x" + strings.Repeat("11", 32),
		Wallet:           "0x0000000000000000000000000000000000000002",
		DomainNormalized: "example.com",
		LocalID:          "agent-alice",
		Capabilities:     []string{"social", "commerce"},
	})
}

func TestCreateOrLoadSoulOperation(t *testing.T) {
	t.Parallel()

	// Build a DB without default Create() stubs so we can control return values.
	db := ttmocks.NewMockExtendedDB()
	qOp := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulOperation")).Return(qOp).Maybe()
	qOp.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qOp).Maybe()
	qOp.On("IfNotExists").Return(qOp).Maybe()

	s := &Server{store: store.New(db)}

	_, appErr := s.createOrLoadSoulOperation(context.Background(), nil)
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)

	qOp.On("Create").Return(nil).Once()
	op := &models.SoulOperation{OperationID: "op1"}
	_ = op.UpdateKeys()
	got, appErr := s.createOrLoadSoulOperation(context.Background(), op)
	require.Nil(t, appErr)
	require.NotNil(t, got)
	require.Equal(t, "op1", got.OperationID)

	// Condition failed returns existing record when load succeeds.
	db2 := ttmocks.NewMockExtendedDB()
	qOp2 := new(ttmocks.MockQuery)
	db2.On("WithContext", mock.Anything).Return(db2).Maybe()
	db2.On("Model", mock.AnythingOfType("*models.SoulOperation")).Return(qOp2).Maybe()
	qOp2.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qOp2).Maybe()
	qOp2.On("IfNotExists").Return(qOp2).Maybe()
	qOp2.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	qOp2.On("First", mock.AnythingOfType("*models.SoulOperation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulOperation](t, args, 0)
		*dest = models.SoulOperation{OperationID: "existing"}
		_ = dest.UpdateKeys()
	}).Once()

	s2 := &Server{store: store.New(db2)}
	op2 := &models.SoulOperation{OperationID: "op2"}
	_ = op2.UpdateKeys()
	got, appErr = s2.createOrLoadSoulOperation(context.Background(), op2)
	require.Nil(t, appErr)
	require.NotNil(t, got)
	require.Equal(t, "existing", got.OperationID)
}
