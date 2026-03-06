package controlplane

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
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

func newSoulRegistryVerifyHarness(t *testing.T, mutateCfg func(*config.Config)) (*Server, soulRegistryTestDB, *models.SoulAgentRegistration, *ecdsa.PrivateKey) {
	t.Helper()

	tdb := newSoulRegistryTestDB()
	cfg := config.Config{
		SoulEnabled:                 true,
		SoulChainID:                 1,
		SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		SoulTxMode:                  "safe",
		SoulAdminSafeAddress:        "0x0000000000000000000000000000000000000002",
		WebAuthnRPID:                "lesser.host",
		SoulMintSignerKey:           strings.Repeat("ab", 32),
	}
	if mutateCfg != nil {
		mutateCfg(&cfg)
	}

	s := &Server{store: store.New(tdb.db), cfg: cfg}

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: testDomainExampleCom, InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Once()

	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
	now := time.Now().UTC()
	reg := &models.SoulAgentRegistration{
		ID:               "reg-verify-1",
		Username:         "admin",
		DomainNormalized: testDomainExampleCom,
		LocalID:          "agent-alice",
		AgentID:          soulLifecycleTestAgentIDHex,
		Wallet:           wallet,
		WalletMessage:    "sign this registry message",
		ProofToken:       "proof-token",
		DNSVerified:      true,
		HTTPSVerified:    true,
		Status:           models.SoulAgentRegistrationStatusPending,
		CreatedAt:        now.Add(-5 * time.Minute),
		UpdatedAt:        now.Add(-5 * time.Minute),
		ExpiresAt:        now.Add(25 * time.Minute),
	}

	tdb.qReg.On("First", mock.AnythingOfType("*models.SoulAgentRegistration")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentRegistration](t, args, 0)
		*dest = *reg
	}).Once()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()

	return s, tdb, reg, key
}

func newSoulRegistryVerifyContext(t *testing.T, reg *models.SoulAgentRegistration, key *ecdsa.PrivateKey, mutate func(*soulAgentRegistrationVerifyRequest)) *apptheory.Context {
	t.Helper()

	walletSig, err := crypto.Sign(accounts.TextHash([]byte(reg.WalletMessage)), key)
	require.NoError(t, err)

	principalDeclaration := boundaryTestPrincipalDeclaration
	principalDigest := crypto.Keccak256([]byte(principalDeclaration))
	principalSig, err := crypto.Sign(accounts.TextHash(principalDigest), key)
	require.NoError(t, err)

	req := soulAgentRegistrationVerifyRequest{
		Signature:            "0x" + hex.EncodeToString(walletSig),
		PrincipalAddress:     reg.Wallet,
		PrincipalDeclaration: principalDeclaration,
		PrincipalSignature:   "0x" + hex.EncodeToString(principalSig),
		DeclaredAt:           reg.CreatedAt.Add(1 * time.Minute).Format(time.RFC3339),
	}
	if mutate != nil {
		mutate(&req)
	}
	body, err := json.Marshal(req)
	require.NoError(t, err)

	ctx := adminCtx()
	ctx.RequestID = "req-reg-verify"
	ctx.Params = map[string]string{"id": reg.ID}
	ctx.Request = apptheory.Request{Body: body}
	return ctx
}

func newSoulIdentityOnlyServer() (*Server, *ttmocks.MockQuery) {
	db, queries := newTestDBWithModelQueries("*models.SoulAgentIdentity")
	qIdentity := queries[0]
	qIdentity.ExpectedCalls = nil
	qIdentity.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qIdentity).Maybe()
	qIdentity.On("IfExists").Return(qIdentity).Maybe()
	qIdentity.On("IfNotExists").Return(qIdentity).Maybe()
	return &Server{store: store.New(db)}, qIdentity
}

func newSoulRegistrationOnlyServer() (*Server, *ttmocks.MockQuery) {
	db, queries := newTestDBWithModelQueries("*models.SoulAgentRegistration")
	qReg := queries[0]
	qReg.ExpectedCalls = nil
	qReg.On("IfNotExists").Return(qReg).Maybe()
	return &Server{store: store.New(db)}, qReg
}

func TestNormalizeSoulEVMAddress_Branches(t *testing.T) {
	t.Parallel()

	var nilServer *Server
	_, appErr := nilServer.normalizeSoulEVMAddress(context.Background(), "0x0000000000000000000000000000000000000001", "wallet_address")
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)

	s := &Server{}
	_, appErr = s.normalizeSoulEVMAddress(context.Background(), " ", "")
	require.NotNil(t, appErr)
	require.Equal(t, "address is required", appErr.Message)

	_, appErr = s.normalizeSoulEVMAddress(context.Background(), "not-a-wallet", "principal_address")
	require.NotNil(t, appErr)
	require.Equal(t, "invalid principal_address", appErr.Message)

	tdb := newSoulRegistryTestDB()
	s = &Server{store: store.New(tdb.db)}
	_, appErr = s.normalizeSoulEVMAddress(context.Background(), reservedWalletLesserHostAdmin, "wallet_address")
	require.NotNil(t, appErr)
	require.Equal(t, "wallet_address is reserved", appErr.Message)

	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.WalletIndex](t, args, 0)
		*dest = models.WalletIndex{Username: "ops"}
	}).Once()
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{Username: "ops", Role: models.RoleOperator}
	}).Once()
	_, appErr = s.normalizeSoulEVMAddress(context.Background(), "0x00000000000000000000000000000000000000aa", "principal_address")
	require.NotNil(t, appErr)
	require.Equal(t, "principal_address is not allowed", appErr.Message)

	tdb2 := newSoulRegistryTestDB()
	s = &Server{store: store.New(tdb2.db)}
	tdb2.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	addr, appErr := s.normalizeSoulEVMAddress(context.Background(), "0x00000000000000000000000000000000000000Bb", "principal_address")
	require.Nil(t, appErr)
	require.Equal(t, "0x00000000000000000000000000000000000000bb", addr)
}

func TestGetSoulAgentIdentity_RepairsLifecycleFields(t *testing.T) {
	t.Parallel()

	t.Run("fills missing lifecycle status from status", func(t *testing.T) {
		tdb := newSoulRegistryTestDB()
		s := &Server{store: store.New(tdb.db)}
		tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{AgentID: soulLifecycleTestAgentIDHex, Status: models.SoulAgentStatusActive}
		}).Once()

		item, err := s.getSoulAgentIdentity(context.Background(), soulLifecycleTestAgentIDHex)
		require.NoError(t, err)
		require.Equal(t, models.SoulAgentStatusActive, item.LifecycleStatus)
	})

	t.Run("fills missing status from lifecycle status", func(t *testing.T) {
		tdb := newSoulRegistryTestDB()
		s := &Server{store: store.New(tdb.db)}
		tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{AgentID: soulLifecycleTestAgentIDHex, LifecycleStatus: models.SoulAgentStatusPending}
		}).Once()

		item, err := s.getSoulAgentIdentity(context.Background(), soulLifecycleTestAgentIDHex)
		require.NoError(t, err)
		require.Equal(t, models.SoulAgentStatusPending, item.Status)
	})

	t.Run("strict integrity normalizes mismatch to status", func(t *testing.T) {
		tdb := newSoulRegistryTestDB()
		s := &Server{
			store: store.New(tdb.db),
			cfg:   config.Config{SoulV2StrictIntegrity: true},
		}
		tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID:         soulLifecycleTestAgentIDHex,
				Status:          models.SoulAgentStatusPending,
				LifecycleStatus: models.SoulAgentStatusActive,
			}
		}).Once()

		item, err := s.getSoulAgentIdentity(context.Background(), soulLifecycleTestAgentIDHex)
		require.NoError(t, err)
		require.Equal(t, models.SoulAgentStatusPending, item.LifecycleStatus)
	})
}

func TestCreateSoulAgentRegistration_Branches(t *testing.T) {
	t.Parallel()

	var nilServer *Server
	appErr := nilServer.createSoulAgentRegistration(context.Background(), &models.SoulAgentRegistration{})
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)

	s, qReg := newSoulRegistrationOnlyServer()
	appErr = s.createSoulAgentRegistration(context.Background(), nil)
	require.NotNil(t, appErr)
	require.Equal(t, "app.internal", appErr.Code)

	qReg.On("Create").Return(errors.New("boom")).Once()
	appErr = s.createSoulAgentRegistration(context.Background(), &models.SoulAgentRegistration{ID: "reg1"})
	require.NotNil(t, appErr)
	require.Equal(t, "failed to create registration", appErr.Message)
}

func TestEnsureSoulPendingAgentIdentity_Branches(t *testing.T) {
	t.Parallel()

	reg := &models.SoulAgentRegistration{
		AgentID:          soulLifecycleTestAgentIDHex,
		DomainNormalized: testDomainExampleCom,
		LocalID:          "agent-alice",
		Wallet:           "0x00000000000000000000000000000000000000aa",
		Capabilities:     []string{"social"},
	}
	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)

	t.Run("nil prereqs return internal", func(t *testing.T) {
		var nilServer *Server
		appErr := nilServer.ensureSoulPendingAgentIdentity(context.Background(), reg, "https://meta", reg.Wallet, "0xsig", "decl", now.Format(time.RFC3339), now)
		require.NotNil(t, appErr)
		require.Equal(t, "app.internal", appErr.Code)
	})

	t.Run("loads existing identity errors", func(t *testing.T) {
		s, qIdentity := newSoulIdentityOnlyServer()
		qIdentity.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
		qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(errors.New("boom")).Once()

		appErr := s.ensureSoulPendingAgentIdentity(context.Background(), reg, "https://meta", reg.Wallet, "0xsig", "decl", now.Format(time.RFC3339), now)
		require.NotNil(t, appErr)
		require.Equal(t, "failed to load agent identity", appErr.Message)
	})

	t.Run("active existing identity conflicts", func(t *testing.T) {
		s, qIdentity := newSoulIdentityOnlyServer()
		qIdentity.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
		qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{AgentID: reg.AgentID, Status: models.SoulAgentStatusActive}
		}).Once()

		appErr := s.ensureSoulPendingAgentIdentity(context.Background(), reg, "https://meta", reg.Wallet, "0xsig", "decl", now.Format(time.RFC3339), now)
		require.NotNil(t, appErr)
		require.Equal(t, "agent is already registered", appErr.Message)
	})

	t.Run("existing identity principal mismatch conflicts", func(t *testing.T) {
		s, qIdentity := newSoulIdentityOnlyServer()
		qIdentity.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
		qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{AgentID: reg.AgentID, Status: models.SoulAgentStatusPending, PrincipalAddress: "0x00000000000000000000000000000000000000bb"}
		}).Once()

		appErr := s.ensureSoulPendingAgentIdentity(context.Background(), reg, "https://meta", reg.Wallet, "0xsig", "decl", now.Format(time.RFC3339), now)
		require.NotNil(t, appErr)
		require.Equal(t, "principal_address mismatch for existing identity", appErr.Message)
	})

	t.Run("existing identity declaration mismatch conflicts", func(t *testing.T) {
		s, qIdentity := newSoulIdentityOnlyServer()
		qIdentity.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
		qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{AgentID: reg.AgentID, Status: models.SoulAgentStatusPending, PrincipalDeclaration: "other"}
		}).Once()

		appErr := s.ensureSoulPendingAgentIdentity(context.Background(), reg, "https://meta", reg.Wallet, "0xsig", "decl", now.Format(time.RFC3339), now)
		require.NotNil(t, appErr)
		require.Equal(t, "principal_declaration mismatch for existing identity", appErr.Message)
	})

	t.Run("existing identity declared_at mismatch conflicts", func(t *testing.T) {
		s, qIdentity := newSoulIdentityOnlyServer()
		qIdentity.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
		qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{AgentID: reg.AgentID, Status: models.SoulAgentStatusPending, PrincipalDeclaredAt: "2026-03-05T11:00:00Z"}
		}).Once()

		appErr := s.ensureSoulPendingAgentIdentity(context.Background(), reg, "https://meta", reg.Wallet, "0xsig", "decl", now.Format(time.RFC3339), now)
		require.NotNil(t, appErr)
		require.Equal(t, "declared_at mismatch for existing identity", appErr.Message)
	})

	t.Run("missing principal fields are backfilled", func(t *testing.T) {
		s, qIdentity := newSoulIdentityOnlyServer()
		qIdentity.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
		qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{AgentID: reg.AgentID, Status: models.SoulAgentStatusPending}
		}).Once()
		qIdentity.On("Update", mock.Anything).Return(nil).Once()

		appErr := s.ensureSoulPendingAgentIdentity(context.Background(), reg, "https://meta", reg.Wallet, "0xsig", "decl", now.Format(time.RFC3339), now)
		require.Nil(t, appErr)
	})

	t.Run("backfill update errors surface", func(t *testing.T) {
		s, qIdentity := newSoulIdentityOnlyServer()
		qIdentity.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
		qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{AgentID: reg.AgentID, Status: models.SoulAgentStatusPending}
		}).Once()
		qIdentity.On("Update", mock.Anything).Return(errors.New("boom")).Once()

		appErr := s.ensureSoulPendingAgentIdentity(context.Background(), reg, "https://meta", reg.Wallet, "0xsig", "decl", now.Format(time.RFC3339), now)
		require.NotNil(t, appErr)
		require.Equal(t, "failed to update agent identity", appErr.Message)
	})
}

func TestHandleSoulAgentRegistrationVerify_ValidationBranches(t *testing.T) {
	t.Parallel()

	t.Run("invalid principal address", func(t *testing.T) {
		s, _, reg, key := newSoulRegistryVerifyHarness(t, nil)
		ctx := newSoulRegistryVerifyContext(t, reg, key, func(req *soulAgentRegistrationVerifyRequest) {
			req.PrincipalAddress = "not-a-wallet"
		})

		_, err := s.handleSoulAgentRegistrationVerify(ctx)
		var appErr *apptheory.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "invalid principal_address", appErr.Message)
	})

	t.Run("principal declaration required", func(t *testing.T) {
		s, tdb, reg, key := newSoulRegistryVerifyHarness(t, nil)
		tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
		ctx := newSoulRegistryVerifyContext(t, reg, key, func(req *soulAgentRegistrationVerifyRequest) {
			req.PrincipalDeclaration = " "
		})

		_, err := s.handleSoulAgentRegistrationVerify(ctx)
		var appErr *apptheory.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "principal_declaration is required", appErr.Message)
	})

	t.Run("principal declaration too long", func(t *testing.T) {
		s, tdb, reg, key := newSoulRegistryVerifyHarness(t, nil)
		tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
		ctx := newSoulRegistryVerifyContext(t, reg, key, func(req *soulAgentRegistrationVerifyRequest) {
			req.PrincipalDeclaration = strings.Repeat("x", 8193)
		})

		_, err := s.handleSoulAgentRegistrationVerify(ctx)
		var appErr *apptheory.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "principal_declaration is too long", appErr.Message)
	})

	t.Run("declared_at required", func(t *testing.T) {
		s, tdb, reg, key := newSoulRegistryVerifyHarness(t, nil)
		tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
		ctx := newSoulRegistryVerifyContext(t, reg, key, func(req *soulAgentRegistrationVerifyRequest) {
			req.DeclaredAt = " "
		})

		_, err := s.handleSoulAgentRegistrationVerify(ctx)
		var appErr *apptheory.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "declared_at is required", appErr.Message)
	})

	t.Run("declared_at must be rfc3339", func(t *testing.T) {
		s, tdb, reg, key := newSoulRegistryVerifyHarness(t, nil)
		tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
		ctx := newSoulRegistryVerifyContext(t, reg, key, func(req *soulAgentRegistrationVerifyRequest) {
			req.DeclaredAt = "yesterday"
		})

		_, err := s.handleSoulAgentRegistrationVerify(ctx)
		var appErr *apptheory.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "declared_at must be an RFC3339 timestamp", appErr.Message)
	})

	t.Run("principal signature required", func(t *testing.T) {
		s, tdb, reg, key := newSoulRegistryVerifyHarness(t, nil)
		tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
		ctx := newSoulRegistryVerifyContext(t, reg, key, func(req *soulAgentRegistrationVerifyRequest) {
			req.PrincipalSignature = " "
		})

		_, err := s.handleSoulAgentRegistrationVerify(ctx)
		var appErr *apptheory.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "principal_signature is required", appErr.Message)
	})

	t.Run("invalid principal signature", func(t *testing.T) {
		s, tdb, reg, key := newSoulRegistryVerifyHarness(t, nil)
		tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
		ctx := newSoulRegistryVerifyContext(t, reg, key, func(req *soulAgentRegistrationVerifyRequest) {
			req.PrincipalSignature = "0x1234"
		})

		_, err := s.handleSoulAgentRegistrationVerify(ctx)
		var appErr *apptheory.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "invalid principal_signature", appErr.Message)
	})

	t.Run("mint signer key misconfiguration bubbles out", func(t *testing.T) {
		s, tdb, reg, key := newSoulRegistryVerifyHarness(t, func(cfg *config.Config) {
			cfg.SoulMintSignerKey = ""
		})
		tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
		ctx := newSoulRegistryVerifyContext(t, reg, key, nil)

		_, err := s.handleSoulAgentRegistrationVerify(ctx)
		var appErr *apptheory.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, "mint signer key is not configured", appErr.Message)
	})
}

func TestParseSoulRegistrationBeginCapabilities_Branches(t *testing.T) {
	t.Parallel()

	_, appErr := parseSoulRegistrationBeginCapabilities([]any{map[string]any{"scope": "general"}})
	require.NotNil(t, appErr)
	require.Equal(t, "capability objects must include capability", appErr.Message)

	_, appErr = parseSoulRegistrationBeginCapabilities([]any{123})
	require.NotNil(t, appErr)
	require.Equal(t, "capabilities must be an array of strings or objects", appErr.Message)
}

func TestVerifySoulRegistryHTTPS_LocalhostBlocked(t *testing.T) {
	t.Parallel()

	require.False(t, verifySoulRegistryHTTPS(context.Background(), "localhost", "proof-token"))
}
