package controlplane

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestExtractStringSliceField_TrimsAndSkipsNonStrings(t *testing.T) {
	t.Parallel()

	got := extractStringSliceField(map[string]any{
		"channels": []any{" email ", 123, "phone"},
	}, "channels")
	want := []string{"email", "phone"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}

	if got := extractStringSliceField(nil, "channels"); got != nil {
		t.Fatalf("expected nil for nil map, got %v", got)
	}
	if got := extractStringSliceField(map[string]any{"channels": "email"}, "channels"); got != nil {
		t.Fatalf("expected nil for non-slice field, got %v", got)
	}
}

func TestParseSoulUpdateRegistrationBody_HandlesWrapperAndInvalidJSON(t *testing.T) {
	t.Parallel()

	regBytes, reg, expectedVersion, appErr := parseSoulUpdateRegistrationBody([]byte(`{
		"registration": {"version":"3","agentId":"agent-1"},
		"expected_version": 7
	}`))
	if appErr != nil {
		t.Fatalf("unexpected appErr: %v", appErr)
	}
	if expectedVersion == nil || *expectedVersion != 7 {
		t.Fatalf("expected version 7, got %v", expectedVersion)
	}
	if extractStringField(reg, "version") != "3" {
		t.Fatalf("expected wrapped registration body to be parsed, got %q", string(regBytes))
	}

	_, reg, expectedVersion, appErr = parseSoulUpdateRegistrationBody([]byte(`{
		"registration": {"version":"2","agentId":"agent-2"},
		"expectedVersion": 9
	}`))
	if appErr != nil {
		t.Fatalf("unexpected appErr using expectedVersionAlt: %v", appErr)
	}
	if expectedVersion == nil || *expectedVersion != 9 {
		t.Fatalf("expected fallback version 9, got %v", expectedVersion)
	}
	if extractStringField(reg, "version") != "2" {
		t.Fatalf("expected version 2, got %q", extractStringField(reg, "version"))
	}

	_, _, _, appErr = parseSoulUpdateRegistrationBody([]byte(`{"registration":`))
	if appErr == nil || appErr.Code != appErrCodeBadRequest || appErr.Message != "invalid JSON" {
		t.Fatalf("expected invalid JSON app error, got %v", appErr)
	}
}

func TestValidateSoulUpdateRegistrationIdentityFields_UsesFallbackKeysAndRejectsMismatches(t *testing.T) {
	t.Parallel()

	identity := &models.SoulAgentIdentity{
		AgentID: soulLifecycleTestAgentIDHex,
		Domain:  "example.com",
		LocalID: "agent-alice",
	}

	if appErr := validateSoulUpdateRegistrationIdentityFields(map[string]any{
		"agent_id": soulLifecycleTestAgentIDHex,
		"domain":   "EXAMPLE.COM",
		"local_id": "agent-alice",
	}, soulLifecycleTestAgentIDHex, identity); appErr != nil {
		t.Fatalf("expected fallback field names to validate, got %v", appErr)
	}

	tests := []struct {
		name    string
		reg     map[string]any
		message string
	}{
		{
			name: "agentId mismatch",
			reg: map[string]any{
				"agentId": "0xdeadbeef",
				"domain":  "example.com",
				"localId": "agent-alice",
			},
			message: "agentId does not match path",
		},
		{
			name: "domain mismatch",
			reg: map[string]any{
				"agentId": soulLifecycleTestAgentIDHex,
				"domain":  "other.example",
				"localId": "agent-alice",
			},
			message: "domain does not match agent",
		},
		{
			name: "invalid local id",
			reg: map[string]any{
				"agentId": soulLifecycleTestAgentIDHex,
				"domain":  "example.com",
				"localId": "bad/local",
			},
			message: "managed handle must be 3-63 lowercase letters, digits, or hyphens and start/end with a letter or digit",
		},
		{
			name: "ambiguous local id",
			reg: map[string]any{
				"agentId": soulLifecycleTestAgentIDHex,
				"domain":  "example.com",
				"localId": "agent.alice",
			},
			message: "managed handle must be 3-63 lowercase letters, digits, or hyphens and start/end with a letter or digit",
		},
		{
			name: "localId mismatch",
			reg: map[string]any{
				"agentId": soulLifecycleTestAgentIDHex,
				"domain":  "example.com",
				"localId": "agent-bob",
			},
			message: "localId does not match agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appErr := validateSoulUpdateRegistrationIdentityFields(tt.reg, soulLifecycleTestAgentIDHex, identity)
			if appErr == nil || appErr.Code != appErrCodeBadRequest || appErr.Message != tt.message {
				t.Fatalf("expected %q bad_request, got %v", tt.message, appErr)
			}
		})
	}
}

func TestExtractSoulUpdateRegistrationSelfAttestation_ValidatesShape(t *testing.T) {
	t.Parallel()

	att, sig, appErr := extractSoulUpdateRegistrationSelfAttestation(map[string]any{
		"attestations": map[string]any{"selfAttestation": " 0xsig "},
	})
	if appErr != nil {
		t.Fatalf("unexpected appErr: %v", appErr)
	}
	if sig != "0xsig" || extractStringField(att, "selfAttestation") != "0xsig" {
		t.Fatalf("expected trimmed self attestation, got sig=%q att=%v", sig, att)
	}

	tests := []struct {
		name    string
		reg     map[string]any
		message string
	}{
		{name: "missing attestations", reg: map[string]any{}, message: "attestations are required"},
		{name: "attestations not object", reg: map[string]any{"attestations": "bad"}, message: "attestations must be an object"},
		{name: "missing self attestation", reg: map[string]any{"attestations": map[string]any{}}, message: "attestations.selfAttestation is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, appErr := extractSoulUpdateRegistrationSelfAttestation(tt.reg)
			if appErr == nil || appErr.Code != appErrCodeBadRequest || appErr.Message != tt.message {
				t.Fatalf("expected %q bad_request, got %v", tt.message, appErr)
			}
		})
	}
}

func TestComputeSoulUpdateRegistrationDigest_OmitsSelfAttestationAndRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	reg := map[string]any{
		"agentId": "agent-1",
		"attestations": map[string]any{
			"selfAttestation": "0xsig",
			"hostAttestation": "0xhost",
		},
	}
	att, ok := reg["attestations"].(map[string]any)
	if !ok {
		t.Fatalf("expected attestations map, got %#v", reg["attestations"])
	}

	got, appErr := computeSoulUpdateRegistrationDigest(reg, att)
	if appErr != nil {
		t.Fatalf("unexpected appErr: %v", appErr)
	}
	if _, ok := att["selfAttestation"]; ok {
		t.Fatalf("expected computeSoulUpdateRegistrationDigest to omit selfAttestation")
	}

	unsignedBytes, err := json.Marshal(map[string]any{
		"agentId": "agent-1",
		"attestations": map[string]any{
			"hostAttestation": "0xhost",
		},
	})
	if err != nil {
		t.Fatalf("marshal expected digest input: %v", err)
	}
	jcsBytes, err := jsoncanonicalizer.Transform(unsignedBytes)
	if err != nil {
		t.Fatalf("canonicalize expected digest input: %v", err)
	}
	want := crypto.Keccak256(jcsBytes)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected digest %x, got %x", want, got)
	}

	_, appErr = computeSoulUpdateRegistrationDigest(map[string]any{
		"attestations": map[string]any{},
		"bad":          func() {},
	}, map[string]any{})
	if appErr == nil || appErr.Code != appErrCodeBadRequest || appErr.Message != "invalid registration JSON" {
		t.Fatalf("expected invalid registration JSON error, got %v", appErr)
	}
}

func TestParseRFC3339Loose_ParsesRFC3339AndNano(t *testing.T) {
	t.Parallel()

	nano, ok := parseRFC3339Loose("2026-03-05T10:11:12.123456789Z")
	if !ok || nano.Format(time.RFC3339Nano) != "2026-03-05T10:11:12.123456789Z" {
		t.Fatalf("expected RFC3339Nano timestamp, got %v ok=%v", nano, ok)
	}

	plain, ok := parseRFC3339Loose("2026-03-05T10:11:12Z")
	if !ok || plain.Format(time.RFC3339) != "2026-03-05T10:11:12Z" {
		t.Fatalf("expected RFC3339 timestamp, got %v ok=%v", plain, ok)
	}

	if ts, ok := parseRFC3339Loose("  "); ok || !ts.IsZero() {
		t.Fatalf("expected empty input to return zero,false; got %v ok=%v", ts, ok)
	}
	if ts, ok := parseRFC3339Loose("not-a-time"); ok || !ts.IsZero() {
		t.Fatalf("expected invalid input to return zero,false; got %v ok=%v", ts, ok)
	}
}

func TestRequireActiveSoulAgentWithDomainAccess_ReturnsExpectedErrors(t *testing.T) {
	t.Parallel()

	t.Run("agent not found", func(t *testing.T) {
		tdb := newSoulLifecycleTestDB()
		s := &Server{store: store.New(tdb.db)}
		tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()

		_, appErr := s.requireActiveSoulAgentWithDomainAccess(&apptheory.Context{AuthIdentity: "alice"}, soulLifecycleTestAgentIDHex)
		if appErr == nil || appErr.Code != "app.not_found" || appErr.Message != "agent not found" {
			t.Fatalf("expected not_found agent error, got %v", appErr)
		}
	})

	t.Run("inactive agent", func(t *testing.T) {
		tdb := newSoulLifecycleTestDB()
		s := &Server{store: store.New(tdb.db)}
		tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID:         soulLifecycleTestAgentIDHex,
				Domain:          "example.com",
				LocalID:         "agent-alice",
				LifecycleStatus: "suspended",
			}
		}).Once()

		_, appErr := s.requireActiveSoulAgentWithDomainAccess(&apptheory.Context{AuthIdentity: "alice"}, soulLifecycleTestAgentIDHex)
		if appErr == nil || appErr.Code != appErrCodeConflict || appErr.Message != "agent is not active" {
			t.Fatalf("expected inactive agent conflict, got %v", appErr)
		}
	})

	t.Run("domain access failure", func(t *testing.T) {
		tdb := newSoulLifecycleTestDB()
		s := &Server{store: store.New(tdb.db)}
		tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID:         soulLifecycleTestAgentIDHex,
				Domain:          "example.com",
				LocalID:         "agent-alice",
				LifecycleStatus: models.SoulAgentStatusActive,
			}
		}).Once()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()

		_, appErr := s.requireActiveSoulAgentWithDomainAccess(&apptheory.Context{AuthIdentity: "alice"}, soulLifecycleTestAgentIDHex)
		if appErr == nil || appErr.Code != appErrCodeBadRequest || appErr.Message != "domain is not registered" {
			t.Fatalf("expected domain access error, got %v", appErr)
		}
	})
}

func TestRequireActiveSoulAgentForInstance_VerifiesOwnership(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		tdb := newSoulLifecycleTestDB()
		s := &Server{store: store.New(tdb.db)}
		tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID:         soulLifecycleTestAgentIDHex,
				Domain:          "example.com",
				LocalID:         "agent-alice",
				LifecycleStatus: models.SoulAgentStatusActive,
			}
		}).Once()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
			*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
		}).Once()

		identity, appErr := s.requireActiveSoulAgentForInstance(context.Background(), soulLifecycleTestAgentIDHex, "inst1")
		if appErr != nil {
			t.Fatalf("expected success, got %v", appErr)
		}
		if identity == nil || identity.AgentID != soulLifecycleTestAgentIDHex {
			t.Fatalf("unexpected identity: %+v", identity)
		}
	})

	t.Run("other instance forbidden", func(t *testing.T) {
		tdb := newSoulLifecycleTestDB()
		s := &Server{store: store.New(tdb.db)}
		tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID:         soulLifecycleTestAgentIDHex,
				Domain:          "example.com",
				LocalID:         "agent-alice",
				LifecycleStatus: models.SoulAgentStatusActive,
			}
		}).Once()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
			*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst2", Status: models.DomainStatusVerified}
		}).Once()

		_, appErr := s.requireActiveSoulAgentForInstance(context.Background(), soulLifecycleTestAgentIDHex, "inst1")
		if appErr == nil || appErr.Code != "app.unauthorized" || appErr.Message != "unauthorized" {
			t.Fatalf("expected unauthorized, got %v", appErr)
		}
	})
}

func TestUpdateSoulAgentRegistrationForInstance_V3_SyncsENSWithoutS3Key(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	packs := &fakeSoulPackStore{}
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRPCURL:                  "http://rpc.local",
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulPackBucketName:          "bucket",
			WebAuthnRPID:                "lesser.host",
		},
		soulPacks: packs,
	}

	agentIDHex := soulLifecycleTestAgentIDHex
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
	principalDeclaration := boundaryTestPrincipalDeclaration
	principalSigHex := signSoulUpdateRegistrationPrincipalForTest(t, key, principalDeclaration)
	verifiedPrincipalSigHex := signSoulUpdateRegistrationVerifiedPrincipalForTest(t, s, key, agentIDHex, wallet, principalDeclaration)

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		identity := models.SoulAgentIdentity{
			AgentID:   agentIDHex,
			Domain:    "example.com",
			LocalID:   "agent-alice",
			Wallet:    wallet,
			Status:    models.SoulAgentStatusActive,
			UpdatedAt: time.Now().Add(-time.Minute).UTC(),
		}
		applySoulUpdateRegistrationPrincipalForTest(&identity, wallet, principalDeclaration, verifiedPrincipalSigHex)
		*dest = identity
	}).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qVersion.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentVersion](t, args, 0)
		*dest = nil
	}).Once()
	tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Times(3)
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(theoryErrors.ErrItemNotFound).Once()

	parsedABI, err := abi.JSON(strings.NewReader(soul.SoulRegistryABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}
	walletRet, _ := parsedABI.Methods["getAgentWallet"].Outputs.Pack(common.HexToAddress(wallet))
	client := &fakeEVMClient{callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
		if bytes.HasPrefix(msg.Data, parsedABI.Methods["getAgentWallet"].ID) {
			return walletRet, nil
		}
		return nil, ethereum.NotFound
	}}
	s.dialEVM = func(ctx context.Context, rpcURL string) (ethRPCClient, error) { return client, nil }

	unsigned := map[string]any{
		"version": "3",
		"agentId": agentIDHex,
		"domain":  "example.com",
		"localId": "agent-alice",
		"wallet":  wallet,
		"principal": map[string]any{
			"type":        "individual",
			"identifier":  wallet,
			"displayName": "Alice",
			"contactUri":  "https://example.com/alice",
			"declaration": principalDeclaration,
			"signature":   principalSigHex,
			"declaredAt":  soulUpdateRegistrationPrincipalDeclaredAtForTest,
		},
		"selfDescription": map[string]any{
			"purpose":    "I summarize documents for humans.",
			"authoredBy": "agent",
		},
		"capabilities": []any{
			map[string]any{
				"capability": "text-summarization",
				"scope":      "general",
				"claimLevel": "self-declared",
			},
		},
		"boundaries": []any{
			map[string]any{
				"id":             "boundary-001",
				"category":       "refusal",
				"statement":      "I will not impersonate real people.",
				"addedAt":        "2026-03-01T00:00:00Z",
				"addedInVersion": "1",
				"signature":      "0xabc",
			},
		},
		"channels": map[string]any{
			"ens": map[string]any{
				"name":            "agent-alice.lessersoul.eth",
				"resolverAddress": "0x0000000000000000000000000000000000000002",
				"chain":           "mainnet",
			},
			"email": map[string]any{
				"address":      "agent-alice@lessersoul.ai",
				"capabilities": []any{"receive", "send"},
				"protocols":    []any{"smtp"},
				"verified":     true,
				"verifiedAt":   "2026-03-01T00:00:00Z",
			},
		},
		"contactPreferences": map[string]any{
			"preferred": "email",
			"availability": map[string]any{
				"schedule": "always",
				"timezone": "UTC",
			},
			"responseExpectation": map[string]any{
				"target":    "PT4H",
				"guarantee": "best-effort",
			},
			"languages": []any{"en"},
		},
		"transparency": map[string]any{
			"modelFamily": "unknown",
		},
		"endpoints": map[string]any{
			"mcp": "https://example.com/soul/mcp",
		},
		"lifecycle": map[string]any{
			"status":          "active",
			"statusChangedAt": "2026-03-01T00:00:00Z",
		},
		"attestations": map[string]any{},
		"created":      "2026-03-01T00:00:00Z",
		"updated":      "2026-03-01T00:00:00Z",
	}

	unsignedBytes, err := json.Marshal(unsigned)
	if err != nil {
		t.Fatalf("marshal unsigned: %v", err)
	}
	jcsBytes, err := jsoncanonicalizer.Transform(unsignedBytes)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	digest := crypto.Keccak256(jcsBytes)
	sig, err := crypto.Sign(accounts.TextHash(digest), key)
	if err != nil {
		t.Fatalf("sign registration: %v", err)
	}

	reg := mustUnmarshalJSON[map[string]any](t, unsignedBytes)
	regAttestations, ok := reg["attestations"].(map[string]any)
	if !ok {
		t.Fatalf("expected attestations map, got %#v", reg["attestations"])
	}
	regAttestations["selfAttestation"] = "0x" + hex.EncodeToString(sig)
	regBytes, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("marshal signed registration: %v", err)
	}

	resp, appErr := s.UpdateSoulAgentRegistrationForInstance(context.Background(), "inst1", "rid-v3-instance", agentIDHex, regBytes)
	if appErr != nil {
		t.Fatalf("unexpected appErr: %v", appErr)
	}
	if resp.Version != 1 {
		t.Fatalf("expected version 1, got %d", resp.Version)
	}
	if resp.S3Key != "" {
		t.Fatalf("expected instance-auth response to omit s3 key, got %q", resp.S3Key)
	}
	if len(packs.puts) < 2 {
		t.Fatalf("expected registration objects to be published, got %d puts", len(packs.puts))
	}

	summary := collectSyncV3StateModels(tdb.db.Calls, agentIDHex)
	if !summary.ensNames["agent-alice.lessersoul.eth"] {
		t.Fatalf("expected self-asserted ENS resolution material, got %v", summary.ensNames)
	}
}

func TestVerifySoulAgentWalletOnChain_ReturnsExpectedErrors(t *testing.T) {
	t.Parallel()

	agentInt := big.NewInt(123)
	wallet := "0x00000000000000000000000000000000000000aa"
	otherWallet := "0x00000000000000000000000000000000000000bb"
	tests := []struct {
		name        string
		server      func(t *testing.T) *Server
		identity    *models.SoulAgentIdentity
		wantCode    string
		wantMessage string
	}{
		{
			name:        "contract not configured",
			server:      func(t *testing.T) *Server { t.Helper(); return &Server{} },
			identity:    &models.SoulAgentIdentity{Wallet: wallet},
			wantCode:    "app.conflict",
			wantMessage: "soul registry is not configured",
		},
		{
			name: "rpc dial failure",
			server: func(t *testing.T) *Server {
				t.Helper()
				return newWalletVerificationTestServer(t, "", errors.New("boom"))
			},
			identity:    &models.SoulAgentIdentity{Wallet: wallet},
			wantCode:    appErrCodeInternal,
			wantMessage: "failed to connect to rpc",
		},
		{
			name: "agent not minted",
			server: func(t *testing.T) *Server {
				t.Helper()
				return newWalletVerificationTestServer(t, common.Address{}.Hex(), nil)
			},
			identity:    &models.SoulAgentIdentity{Wallet: wallet},
			wantCode:    "app.conflict",
			wantMessage: "agent is not minted",
		},
		{
			name: "wallet mismatch",
			server: func(t *testing.T) *Server {
				t.Helper()
				return newWalletVerificationTestServer(t, otherWallet, nil)
			},
			identity:    &models.SoulAgentIdentity{Wallet: wallet},
			wantCode:    appErrCodeBadRequest,
			wantMessage: "wallet does not match on-chain state",
		},
		{
			name: "identity out of sync",
			server: func(t *testing.T) *Server {
				t.Helper()
				return newWalletVerificationTestServer(t, wallet, nil)
			},
			identity:    &models.SoulAgentIdentity{Wallet: otherWallet},
			wantCode:    "app.conflict",
			wantMessage: "agent wallet is out of sync; record operation execution first",
		},
		{
			name: "success",
			server: func(t *testing.T) *Server {
				t.Helper()
				return newWalletVerificationTestServer(t, wallet, nil)
			},
			identity: &models.SoulAgentIdentity{Wallet: wallet},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			s := tt.server(t)
			appErr := s.verifySoulAgentWalletOnChain(context.Background(), agentInt, wallet, tt.identity)
			assertWalletVerificationResult(t, appErr, tt.wantCode, tt.wantMessage)
		})
	}
}

func TestValidateSoulRegistrationPreviousVersionURI_CoversFirstAndSubsequentRules(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{SoulPackBucketName: "bucket"}}

	first := &soul.RegistrationFileV2{
		Version:            "2",
		PreviousVersionURI: ptr("s3://bucket/unexpected"),
	}
	if appErr := s.validateSoulRegistrationPreviousVersionURI(first, soulLifecycleTestAgentIDHex, 1); appErr == nil || appErr.Message != "previousVersionUri must be null for the first version" {
		t.Fatalf("expected first-version previousVersionUri error, got %v", appErr)
	}

	subsequent := &soul.RegistrationFileV2{Version: "2"}
	if appErr := s.validateSoulRegistrationPreviousVersionURI(subsequent, soulLifecycleTestAgentIDHex, 2); appErr == nil || appErr.Message != "previousVersionUri is required for subsequent versions" {
		t.Fatalf("expected missing previousVersionUri error, got %v", appErr)
	}
}

func TestNormalizeCapabilityClaimLevelAndRank(t *testing.T) {
	t.Parallel()

	if got, ok := normalizeCapabilityClaimLevel(""); !ok || got != soulClaimLevelSelfDeclared {
		t.Fatalf("expected empty claim level to normalize to %s, got %q ok=%v", soulClaimLevelSelfDeclared, got, ok)
	}
	if got, ok := normalizeCapabilityClaimLevel(" Peer-Endorsed "); !ok || got != "peer-endorsed" {
		t.Fatalf("expected peer-endorsed normalization, got %q ok=%v", got, ok)
	}
	if got, ok := normalizeCapabilityClaimLevel("bogus"); ok || got != "" {
		t.Fatalf("expected invalid claim level rejection, got %q ok=%v", got, ok)
	}

	if got := claimLevelRank(soulClaimLevelSelfDeclared); got != 1 {
		t.Fatalf("expected %s rank 1, got %d", soulClaimLevelSelfDeclared, got)
	}
	if got := claimLevelRank("challenge-passed"); got != 2 {
		t.Fatalf("expected challenge-passed rank 2, got %d", got)
	}
	if got := claimLevelRank("peer-endorsed"); got != 3 {
		t.Fatalf("expected peer-endorsed rank 3, got %d", got)
	}
	if got := claimLevelRank("deprecated"); got != 0 {
		t.Fatalf("expected deprecated rank 0, got %d", got)
	}
}

func TestGetExistingCapabilityClaimLevel_DefaultsBlankAndNotFound(t *testing.T) {
	t.Parallel()

	identity := &models.SoulAgentIdentity{
		AgentID: "0xabc",
		Domain:  "example.com",
		LocalID: "agent-alice",
	}

	t.Run("not found defaults to self-declared", func(t *testing.T) {
		tdb := newSoulLifecycleTestDB()
		s := &Server{store: store.New(tdb.db)}
		tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()

		got, appErr := s.getExistingCapabilityClaimLevel(context.Background(), identity, "social")
		if appErr != nil || got != soulClaimLevelSelfDeclared {
			t.Fatalf("expected %s default, got %q appErr=%v", soulClaimLevelSelfDeclared, got, appErr)
		}
	})

	t.Run("blank stored level defaults to self-declared", func(t *testing.T) {
		tdb := newSoulLifecycleTestDB()
		s := &Server{store: store.New(tdb.db)}
		tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulCapabilityAgentIndex](t, args, 0)
			*dest = models.SoulCapabilityAgentIndex{ClaimLevel: ""}
		}).Once()

		got, appErr := s.getExistingCapabilityClaimLevel(context.Background(), identity, "social")
		if appErr != nil || got != soulClaimLevelSelfDeclared {
			t.Fatalf("expected blank stored level to default, got %q appErr=%v", got, appErr)
		}
	})

	t.Run("query error returns internal error", func(t *testing.T) {
		tdb := newSoulLifecycleTestDB()
		s := &Server{store: store.New(tdb.db)}
		tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(errors.New("boom")).Once()

		_, appErr := s.getExistingCapabilityClaimLevel(context.Background(), identity, "social")
		if appErr == nil || appErr.Code != appErrCodeInternal || appErr.Message != "failed to read capability index" {
			t.Fatalf("expected capability read error, got %v", appErr)
		}
	})
}

func TestValidateCapabilityClaimLevelTransitions_DeprecatedAndInvalidRules(t *testing.T) {
	t.Parallel()

	identity := &models.SoulAgentIdentity{
		AgentID: "0xabc",
		Domain:  "example.com",
		LocalID: "agent-alice",
	}

	t.Run("invalid claim level rejected before lookup", func(t *testing.T) {
		s := &Server{}
		appErr := s.validateCapabilityClaimLevelTransitions(context.Background(), identity, []string{"social"}, map[string]string{
			"social": "bogus",
		})
		if appErr == nil || appErr.Code != appErrCodeBadRequest || appErr.Message != "invalid claimLevel for capability: social" {
			t.Fatalf("expected invalid claimLevel error, got %v", appErr)
		}
	})

	t.Run("cannot un-deprecate capability", func(t *testing.T) {
		tdb := newSoulLifecycleTestDB()
		s := &Server{store: store.New(tdb.db)}
		tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulCapabilityAgentIndex](t, args, 0)
			*dest = models.SoulCapabilityAgentIndex{ClaimLevel: "deprecated"}
		}).Once()

		appErr := s.validateCapabilityClaimLevelTransitions(context.Background(), identity, []string{"social"}, map[string]string{
			"social": "peer-endorsed",
		})
		if appErr == nil || appErr.Code != appErrCodeBadRequest || appErr.Message != "cannot un-deprecate capability: social" {
			t.Fatalf("expected cannot un-deprecate error, got %v", appErr)
		}
	})

	t.Run("deprecation is allowed", func(t *testing.T) {
		tdb := newSoulLifecycleTestDB()
		s := &Server{store: store.New(tdb.db)}
		tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulCapabilityAgentIndex](t, args, 0)
			*dest = models.SoulCapabilityAgentIndex{ClaimLevel: "peer-endorsed"}
		}).Once()

		if appErr := s.validateCapabilityClaimLevelTransitions(context.Background(), identity, []string{"social"}, map[string]string{
			"social": "deprecated",
		}); appErr != nil {
			t.Fatalf("expected deprecation transition to succeed, got %v", appErr)
		}
	})
}

func TestPublishLegacySoulRegistration_RejectsClaimTransitionBeforeS3Write(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	packs := &fakeSoulPackStoreForPublish{}
	s := &Server{
		store:     store.New(tdb.db),
		soulPacks: packs,
		cfg: config.Config{
			SoulEnabled:        true,
			SoulPackBucketName: "soul-packs",
		},
	}
	identity := &models.SoulAgentIdentity{
		AgentID: "0xabc",
		Domain:  "example.com",
		LocalID: "agent-alice",
	}
	tdb.qVersion.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentVersion](t, args, 0)
		*dest = []*models.SoulAgentVersion{}
	}).Once()
	tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCapabilityAgentIndex](t, args, 0)
		*dest = models.SoulCapabilityAgentIndex{ClaimLevel: soulClaimLevelPeerEndorsed}
	}).Once()

	regBytes := []byte(`{"capabilities":[{"capability":"social","claimLevel":"self-declared"}]}`)
	_, _, appErr := s.publishLegacySoulRegistration(
		context.Background(),
		identity.AgentID,
		identity,
		regBytes,
		regSHA256Hex(regBytes),
		"self-sig",
		"bad downgrade",
		[]string{"social"},
		map[string]string{"social": soulClaimLevelSelfDeclared},
		time.Now().UTC(),
	)
	if appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected claim-level rejection, got %v", appErr)
	}
	if len(packs.puts) != 0 {
		t.Fatalf("rejected registration must not publish S3 metadata, got %d puts", len(packs.puts))
	}
}

func TestUpdateSoulAgentCapabilities_UpdatesIdentityAndIndexModels(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db)}
	now := time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC)

	identity := &models.SoulAgentIdentity{
		AgentID:      "0xabc",
		Domain:       "example.com",
		LocalID:      "agent-alice",
		Capabilities: []string{"search", "social"},
	}

	appErr := s.updateSoulAgentCapabilities(context.Background(), identity, []string{"search", "reasoning"}, map[string]string{
		"search":    "peer-endorsed",
		"reasoning": "challenge-passed",
	}, now, true)
	if appErr != nil {
		t.Fatalf("unexpected appErr: %v", appErr)
	}
	if !reflect.DeepEqual(identity.Capabilities, []string{"reasoning", "search"}) {
		t.Fatalf("expected normalized capabilities, got %v", identity.Capabilities)
	}
	if !identity.UpdatedAt.Equal(now) {
		t.Fatalf("expected UpdatedAt to be set to %v, got %v", now, identity.UpdatedAt)
	}

	capsSeen := collectCapabilityClaimLevels(tdb.db.Calls)
	if got := capsSeen["social"]; got != "" {
		t.Fatalf("expected removed capability social to be modeled for delete without claim level, got %q", got)
	}
	if got := capsSeen["search"]; got != "peer-endorsed" {
		t.Fatalf("expected search claim level peer-endorsed, got %q", got)
	}
	if got := capsSeen["reasoning"]; got != "challenge-passed" {
		t.Fatalf("expected reasoning claim level challenge-passed, got %q", got)
	}
}

func TestSyncSoulV3StateFromRegistration_PreservesManagedFieldsAndCleansUpIndexes(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{SoulPackBucketName: "bucket", WebAuthnRPID: "portal.example"},
	}
	now := time.Date(2026, time.March, 5, 13, 14, 15, 0, time.UTC)
	canonicalENSName, err := soul.ManagedENSName("agent-alice", "inst1")
	if err != nil {
		t.Fatalf("ManagedENSName: %v", err)
	}
	rep := 0.8
	identity := &models.SoulAgentIdentity{
		AgentID:         soulLifecycleTestAgentIDHex,
		Domain:          "example.com",
		LocalID:         "agent-alice",
		Wallet:          "0x00000000000000000000000000000000000000aa",
		LifecycleStatus: models.SoulAgentStatusActive,
	}

	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:     identity.AgentID,
			ChannelType: models.SoulChannelTypeENS,
			Identifier:  "old-agent.lessersoul.eth",
		}
	}).Once()
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: "old-agent.lessersoul.eth", AgentID: identity.AgentID}
	}).Once()
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:     identity.AgentID,
			ChannelType: models.SoulChannelTypeEmail,
			Identifier:  "old-agent@example.com",
		}
	}).Once()
	tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulEmailAgentIndex](t, args, 0)
		*dest = models.SoulEmailAgentIndex{Email: "old-agent@example.com", AgentID: identity.AgentID}
	}).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:            identity.AgentID,
			ChannelType:        models.SoulChannelTypePhone,
			Identifier:         "+15551234567",
			Provider:           "Twilio",
			SecretRef:          " /ssm/phone ",
			ProvisionedAt:      now.Add(-24 * time.Hour),
			DeprovisionedAt:    now.Add(-12 * time.Hour),
			Status:             models.SoulChannelStatusActive,
			ENSChain:           "",
			ENSResolverAddress: "",
		}
	}).Once()
	tdb.qPhoneIdx.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulPhoneAgentIndex](t, args, 0)
		*dest = models.SoulPhoneAgentIndex{Phone: "+15551234567", AgentID: identity.AgentID}
	}).Once()

	regV3 := &soul.RegistrationFileV3{
		Version: "3",
		AgentID: identity.AgentID,
		Domain:  identity.Domain,
		LocalID: identity.LocalID,
		Wallet:  identity.Wallet,
		SelfDescription: soul.SelfDescriptionV2{
			Purpose: "I summarize documents for humans.",
		},
		Channels: &soul.ChannelsV3{
			ENS: &soul.ENSChannelV3{
				Name:            canonicalENSName,
				ResolverAddress: "0x0000000000000000000000000000000000000002",
				Chain:           "sepolia",
			},
			Phone: &soul.PhoneChannelV3{
				Number:       "+15557654321",
				Capabilities: []string{"sms-send", "sms-receive"},
				Verified:     true,
				VerifiedAt:   "2026-03-05T10:11:12.123456789Z",
			},
		},
		ContactPreferences: &soul.ContactPreferencesV3{
			Preferred: "voice",
			Fallback:  "email",
			Availability: soul.ContactAvailabilityV3{
				Schedule: "custom",
				Timezone: "UTC",
				Windows: []soul.ContactAvailabilityWindowV3{
					{Days: []string{"Mon", "Tue"}, StartTime: "09:00", EndTime: "17:00"},
				},
			},
			ResponseExpectation: soul.ResponseExpectationV3{
				Target:    "PT2H",
				Guarantee: "best-effort",
			},
			RateLimits:   map[string]any{"daily": 10},
			Languages:    []string{"EN"},
			ContentTypes: []string{"Text/Plain"},
			FirstContact: &soul.ContactFirstContactV3{
				RequireSoul:          true,
				RequireReputation:    &rep,
				IntroductionExpected: true,
			},
		},
		Endpoints: soul.EndpointsV2{
			MCP:         "https://example.com/mcp",
			ActivityPub: "https://example.com/activitypub",
		},
		Lifecycle: soul.LifecycleV2{
			Status: models.SoulAgentStatusActive,
		},
		Created: "2026-03-01T00:00:00Z",
		Updated: "2026-03-05T13:14:15Z",
	}

	if appErr := s.syncSoulV3StateFromRegistration(context.Background(), identity.AgentID, identity, regV3, now); appErr != nil {
		t.Fatalf("unexpected appErr: %v", appErr)
	}

	summary := collectSyncV3StateModels(tdb.db.Calls, identity.AgentID)

	assertSyncV3PhoneModel(t, summary.phoneModel, now)
	assertSyncV3PrefsModel(t, summary.prefsModel, rep)
	assertSyncV3Indexes(t, summary, canonicalENSName)
	assertSyncV3ENSResolution(t, summary.ensResolution, canonicalENSName, identity, now)
}

func TestSyncSoulV3StateFromRegistration_RejectsManagedChannelIdentifierChange(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db)}
	now := time.Date(2026, time.March, 5, 13, 14, 15, 0, time.UTC)
	identity := &models.SoulAgentIdentity{
		AgentID:         soulLifecycleTestAgentIDHex,
		Domain:          "example.com",
		LocalID:         "agent-alice",
		LifecycleStatus: models.SoulAgentStatusActive,
	}

	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:       identity.AgentID,
			ChannelType:   models.SoulChannelTypePhone,
			Identifier:    "+15551234567",
			Provider:      "telnyx",
			Verified:      true,
			ProvisionedAt: now.Add(-time.Hour),
			Status:        models.SoulChannelStatusActive,
		}
	}).Once()

	appErr := s.syncSoulV3StateFromRegistration(context.Background(), identity.AgentID, identity, &soul.RegistrationFileV3{
		Channels: &soul.ChannelsV3{
			Phone: &soul.PhoneChannelV3{Number: "+15557654321", Verified: true},
		},
	}, now)
	if appErr == nil || appErr.Code != appErrCodeConflict || appErr.Message != "managed channel must be deprovisioned before changing identifier" {
		t.Fatalf("expected managed channel conflict, got %v", appErr)
	}
}

func TestSyncSoulV3StateFromRegistration_AllowsProject37LegacyEmailMigration(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{cfg: config.Config{Stage: "lab"}, store: store.New(tdb.db)}
	now := time.Date(2026, time.May, 21, 14, 0, 0, 0, time.UTC)
	identity := &models.SoulAgentIdentity{
		AgentID:         soulLifecycleTestAgentIDHex,
		Domain:          "simulacrum.greater.website",
		LocalID:         "pilot",
		LifecycleStatus: models.SoulAgentStatusActive,
	}
	oldAddress := "pilot@lessersoul.ai"
	newAddress := provisionTestPilotScopedEmail

	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:       identity.AgentID,
			ChannelType:   models.SoulChannelTypeEmail,
			Identifier:    oldAddress,
			Provider:      "migadu",
			Verified:      true,
			ProvisionedAt: now.Add(-24 * time.Hour),
			Status:        models.SoulChannelStatusActive,
			SecretRef:     "/lesser-host/soul/lab/agents/0xabc/channels/email/migadu_password",
			Capabilities:  []string{"receive", "send"},
			Protocols:     []string{"imap", "smtp"},
		}
	}).Once()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:       identity.Domain,
			InstanceSlug: "simulacrum",
			Status:       models.DomainStatusVerified,
		}
	}).Once()
	tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulEmailAgentIndex](t, args, 0)
		*dest = models.SoulEmailAgentIndex{Email: oldAddress, AgentID: identity.AgentID}
	}).Once()
	tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qEmailAlias.On("First", mock.AnythingOfType("*models.SoulEmailLegacyAliasIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()

	appErr := s.syncSoulV3StateFromRegistration(context.Background(), identity.AgentID, identity, &soul.RegistrationFileV3{
		Channels: &soul.ChannelsV3{
			Email: &soul.EmailChannelV3{
				Address:      newAddress,
				Capabilities: []string{"receive", "send"},
				Protocols:    []string{"smtp", "imap"},
				Verified:     true,
				VerifiedAt:   now.Format(time.RFC3339Nano),
			},
		},
	}, now)
	if appErr != nil {
		t.Fatalf("unexpected appErr: %v", appErr)
	}

	migratedChannel, aliasIndex := findProject37EmailMigrationModels(tdb.db.Calls, oldAddress, newAddress)
	if migratedChannel == nil || migratedChannel.Provider != commDeliveryProviderMigadu || migratedChannel.ProvisionedAt.IsZero() || migratedChannel.SecretRef == "" {
		t.Fatalf("expected migrated managed channel metadata to be preserved, got %#v", migratedChannel)
	}
	if aliasIndex == nil || aliasIndex.CanonicalEmail != newAddress || aliasIndex.AgentID != identity.AgentID || aliasIndex.Status != models.SoulEmailLegacyAliasStatusActive {
		t.Fatalf("expected legacy alias index for old bare address, got %#v", aliasIndex)
	}
}

func TestSyncSoulV3StateFromRegistration_RejectsNonAgentLegacyEmailAlias(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{cfg: config.Config{Stage: "lab"}, store: store.New(tdb.db)}
	now := time.Date(2026, time.May, 22, 8, 0, 0, 0, time.UTC)
	identity := &models.SoulAgentIdentity{
		AgentID:         soulLifecycleTestAgentIDHex,
		Domain:          "simulacrum.greater.website",
		LocalID:         "pilot",
		LifecycleStatus: models.SoulAgentStatusActive,
	}

	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:       identity.AgentID,
			ChannelType:   models.SoulChannelTypeEmail,
			Identifier:    "other@lessersoul.ai",
			Provider:      "migadu",
			Verified:      true,
			ProvisionedAt: now.Add(-24 * time.Hour),
			Status:        models.SoulChannelStatusActive,
			SecretRef:     "/lesser-host/soul/lab/agents/0xabc/channels/email/migadu_password",
		}
	}).Once()

	appErr := s.syncSoulV3StateFromRegistration(context.Background(), identity.AgentID, identity, &soul.RegistrationFileV3{
		Channels: &soul.ChannelsV3{
			Email: &soul.EmailChannelV3{
				Address:  provisionTestPilotScopedEmail,
				Verified: true,
			},
		},
	}, now)
	if appErr == nil || appErr.Code != appErrCodeConflict || appErr.Message != "managed channel must be deprovisioned before changing identifier" {
		t.Fatalf("expected managed channel conflict for non-agent legacy alias, got %v", appErr)
	}
}

func findProject37EmailMigrationModels(calls []mock.Call, oldAddress string, newAddress string) (*models.SoulAgentChannel, *models.SoulEmailLegacyAliasIndex) {
	var migratedChannel *models.SoulAgentChannel
	var aliasIndex *models.SoulEmailLegacyAliasIndex
	for _, call := range calls {
		if call.Method != modelCallMethod || len(call.Arguments) == 0 {
			continue
		}
		migratedChannel, aliasIndex = recordProject37EmailMigrationModel(call.Arguments.Get(0), oldAddress, newAddress, migratedChannel, aliasIndex)
	}
	return migratedChannel, aliasIndex
}

func recordProject37EmailMigrationModel(model any, oldAddress string, newAddress string, migratedChannel *models.SoulAgentChannel, aliasIndex *models.SoulEmailLegacyAliasIndex) (*models.SoulAgentChannel, *models.SoulEmailLegacyAliasIndex) {
	switch v := model.(type) {
	case *models.SoulAgentChannel:
		if v.ChannelType == models.SoulChannelTypeEmail && v.Identifier == newAddress {
			migratedChannel = v
		}
	case *models.SoulEmailLegacyAliasIndex:
		if v.AliasEmail == oldAddress {
			aliasIndex = v
		}
	}
	return migratedChannel, aliasIndex
}

func TestEnsureSoulEmailAgentIndex_RejectsForeignOwner(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db)}
	tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulEmailAgentIndex](t, args, 0)
		*dest = models.SoulEmailAgentIndex{Email: "agent@example.com", AgentID: "0xother"}
	}).Once()

	appErr := s.ensureSoulEmailAgentIndex(context.Background(), &models.SoulEmailAgentIndex{
		Email:   "agent@example.com",
		AgentID: soulLifecycleTestAgentIDHex,
	})
	if appErr == nil || appErr.Code != appErrCodeConflict || appErr.Message != soulEmailProvisionErrAddressTaken {
		t.Fatalf("expected foreign owner conflict, got %v", appErr)
	}
}

func TestEnsureSoulContactIndexes_CreatesMissingAndAcceptsSameOwner(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db)}

	tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulEmailAgentIndex](t, args, 0)
		*dest = models.SoulEmailAgentIndex{Email: "agent@example.com", AgentID: soulLifecycleTestAgentIDHex}
	}).Once()
	if appErr := s.ensureSoulEmailAgentIndex(context.Background(), &models.SoulEmailAgentIndex{
		Email:   "agent@example.com",
		AgentID: soulLifecycleTestAgentIDHex,
	}); appErr != nil {
		t.Fatalf("expected same-owner email index to pass, got %v", appErr)
	}

	tdb.qPhoneIdx.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	if appErr := s.ensureSoulPhoneAgentIndex(context.Background(), &models.SoulPhoneAgentIndex{
		Phone:   "+15550142",
		AgentID: soulLifecycleTestAgentIDHex,
	}); appErr != nil {
		t.Fatalf("expected missing phone index to be created, got %v", appErr)
	}
}

func TestEnsureSoulContactIndexes_FailClosedOnInvalidAndRaces(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db)}

	if appErr := s.ensureSoulEmailAgentIndex(context.Background(), &models.SoulEmailAgentIndex{
		AgentID: soulLifecycleTestAgentIDHex,
	}); appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected invalid email index to fail closed, got %v", appErr)
	}

	tdb.qEmailIdx.ExpectedCalls = nil
	tdb.qEmailIdx.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(tdb.qEmailIdx).Maybe()
	tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qEmailIdx.On("Create").Return(theoryErrors.ErrConditionFailed).Once()
	appErr := s.ensureSoulEmailAgentIndex(context.Background(), &models.SoulEmailAgentIndex{
		Email:   "agent@example.com",
		AgentID: soulLifecycleTestAgentIDHex,
	})
	if appErr == nil || appErr.Code != appErrCodeConflict || appErr.Message != soulEmailProvisionErrAddressTaken {
		t.Fatalf("expected create race conflict, got %v", appErr)
	}
}

func TestTrustedManagedSoulChannelForIndex(t *testing.T) {
	t.Parallel()

	activeAt := time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		ch   *models.SoulAgentChannel
		want bool
	}{
		{name: "nil"},
		{name: "unverified", ch: &models.SoulAgentChannel{ChannelType: models.SoulChannelTypeEmail, Identifier: "agent@example.com", Provider: "migadu", ProvisionedAt: activeAt, Status: models.SoulChannelStatusActive}},
		{name: "missing provisioned", ch: &models.SoulAgentChannel{ChannelType: models.SoulChannelTypeEmail, Identifier: "agent@example.com", Provider: "migadu", Verified: true, Status: models.SoulChannelStatusActive}},
		{name: "deprovisioned", ch: &models.SoulAgentChannel{ChannelType: models.SoulChannelTypePhone, Identifier: "+15550142", Provider: commDeliveryProviderTelnyx, Verified: true, ProvisionedAt: activeAt, DeprovisionedAt: activeAt.Add(time.Hour), Status: models.SoulChannelStatusActive}},
		{name: "email unmanaged provider", ch: &models.SoulAgentChannel{ChannelType: models.SoulChannelTypeEmail, Identifier: "agent@example.com", Provider: "other", Verified: true, ProvisionedAt: activeAt, Status: models.SoulChannelStatusActive}},
		{name: "email managed", ch: &models.SoulAgentChannel{ChannelType: models.SoulChannelTypeEmail, Identifier: "Agent@Example.com", Provider: "migadu", Verified: true, ProvisionedAt: activeAt, Status: models.SoulChannelStatusActive}, want: true},
		{name: "phone managed", ch: &models.SoulAgentChannel{ChannelType: models.SoulChannelTypePhone, Identifier: "+1 (555) 0142", Provider: commDeliveryProviderTelnyx, Verified: true, ProvisionedAt: activeAt, Status: models.SoulChannelStatusActive}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trustedManagedSoulChannelForIndex(tt.ch); got != tt.want {
				t.Fatalf("trustedManagedSoulChannelForIndex()=%v want %v", got, tt.want)
			}
		})
	}
}

func TestUpsertSoulV3ChannelIndexes_CreatesTrustedManagedIndexes(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db)}
	tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()

	identity := &models.SoulAgentIdentity{
		AgentID: soulLifecycleTestAgentIDHex,
		Domain:  "example.com",
		LocalID: "agent-alice",
	}
	desired := &models.SoulAgentChannel{
		AgentID:       identity.AgentID,
		ChannelType:   models.SoulChannelTypeEmail,
		Identifier:    "agent-alice@lessersoul.ai",
		Provider:      "migadu",
		Verified:      true,
		ProvisionedAt: time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC),
		Status:        models.SoulChannelStatusActive,
	}
	emailIdx := &models.SoulEmailAgentIndex{Email: desired.Identifier, AgentID: identity.AgentID}
	_ = emailIdx.UpdateKeys()

	if appErr := s.upsertSoulV3ChannelIndexes(context.Background(), identity, models.SoulChannelTypeEmail, desired, emailIdx, nil, nil); appErr != nil {
		t.Fatalf("expected trusted managed channel indexes to upsert, got %v", appErr)
	}
}

func TestUpsertSoulV3ChannelIndexes_IgnoresUntrustedContactIndex(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db)}
	desired := &models.SoulAgentChannel{
		AgentID:     soulLifecycleTestAgentIDHex,
		ChannelType: models.SoulChannelTypeEmail,
		Identifier:  "external@example.com",
		Provider:    "external",
		Verified:    true,
		Status:      models.SoulChannelStatusActive,
	}
	identity := &models.SoulAgentIdentity{AgentID: desired.AgentID, Domain: "example.com", LocalID: "agent-alice"}

	if appErr := s.upsertSoulV3ChannelIndexes(context.Background(), identity, models.SoulChannelTypeEmail, desired, &models.SoulEmailAgentIndex{Email: desired.Identifier, AgentID: desired.AgentID}, nil, nil); appErr != nil {
		t.Fatalf("expected untrusted contact index to be ignored, got %v", appErr)
	}
}

func TestUpsertSoulV3ChannelIndexes_HandlesENSAndMissingIdentityIndex(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db)}
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(theoryErrors.ErrItemNotFound).Once()

	identity := &models.SoulAgentIdentity{AgentID: soulLifecycleTestAgentIDHex}
	ensIdx := &models.SoulAgentENSResolution{ENSName: "agent-alice.lessersoul.eth", AgentID: soulLifecycleTestAgentIDHex}
	if appErr := s.upsertSoulV3ChannelIndexes(context.Background(), identity, models.SoulChannelTypeENS, nil, nil, nil, ensIdx); appErr != nil {
		t.Fatalf("expected ENS resolution to upsert without contact index, got %v", appErr)
	}

	activeAt := time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC)
	phoneIdx := &models.SoulPhoneAgentIndex{Phone: "+15550142", AgentID: soulLifecycleTestAgentIDHex}
	tdb.qPhoneIdx.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	if appErr := s.upsertSoulV3ChannelIndexes(context.Background(), identity, models.SoulChannelTypePhone, &models.SoulAgentChannel{
		AgentID:       soulLifecycleTestAgentIDHex,
		ChannelType:   models.SoulChannelTypePhone,
		Identifier:    "+15550142",
		Provider:      commDeliveryProviderTelnyx,
		Verified:      true,
		ProvisionedAt: activeAt,
		Status:        models.SoulChannelStatusActive,
	}, nil, phoneIdx, nil); appErr != nil {
		t.Fatalf("expected missing domain/local id to skip aggregate contact index, got %v", appErr)
	}
}

func TestPreserveManagedSoulChannelMetadata(t *testing.T) {
	t.Parallel()

	provisionedAt := time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC)
	verifiedAt := provisionedAt.Add(time.Minute)
	deprovisionedAt := provisionedAt.Add(time.Hour)
	existing := &models.SoulAgentChannel{
		ChannelType:     models.SoulChannelTypeEmail,
		Identifier:      "agent@example.com",
		Provider:        "migadu",
		SecretRef:       "ssm://secret",
		ProvisionedAt:   provisionedAt,
		DeprovisionedAt: deprovisionedAt,
		Verified:        true,
		VerifiedAt:      verifiedAt,
	}
	desired := &models.SoulAgentChannel{ChannelType: models.SoulChannelTypeEmail, Identifier: "AGENT@example.com"}
	preserveManagedSoulChannelMetadata(desired, existing)
	if desired.Provider != existing.Provider || desired.SecretRef != existing.SecretRef || !desired.ProvisionedAt.Equal(provisionedAt) || !desired.DeprovisionedAt.Equal(deprovisionedAt) {
		t.Fatalf("expected managed metadata to be preserved, got %#v", desired)
	}
	if !desired.Verified || !desired.VerifiedAt.Equal(verifiedAt) {
		t.Fatalf("expected verification metadata to be preserved, got %#v", desired)
	}

	mismatched := &models.SoulAgentChannel{ChannelType: models.SoulChannelTypeEmail, Identifier: "other@example.com"}
	preserveManagedSoulChannelMetadata(mismatched, existing)
	if mismatched.Provider != "" || mismatched.Verified {
		t.Fatalf("did not expect metadata copy for different identifier, got %#v", mismatched)
	}
}

func TestEnsureSoulENSResolution_CreatesAndPreservesOwnedIndex(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db)}
	ens := &models.SoulAgentENSResolution{ENSName: "agent-alice.lessersoul.eth", AgentID: soulLifecycleTestAgentIDHex}

	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(theoryErrors.ErrItemNotFound).Once()
	if appErr := s.ensureSoulENSResolution(context.Background(), ens); appErr != nil {
		t.Fatalf("expected missing ENS resolution to be created, got %v", appErr)
	}

	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: ens.ENSName, AgentID: soulLifecycleTestAgentIDHex}
	}).Once()
	if appErr := s.ensureSoulENSResolution(context.Background(), ens); appErr != nil {
		t.Fatalf("expected owned ENS resolution to update, got %v", appErr)
	}
}

func TestEnsureSoulENSResolution_RejectsInvalidAndForeignOwner(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db)}
	if appErr := s.ensureSoulENSResolution(context.Background(), &models.SoulAgentENSResolution{AgentID: soulLifecycleTestAgentIDHex}); appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected invalid ENS resolution to fail closed, got %v", appErr)
	}

	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{ENSName: "agent-alice.lessersoul.eth", AgentID: "0xother"}
	}).Once()
	appErr := s.ensureSoulENSResolution(context.Background(), &models.SoulAgentENSResolution{ENSName: "agent-alice.lessersoul.eth", AgentID: soulLifecycleTestAgentIDHex})
	if appErr == nil || appErr.Code != appErrCodeConflict || appErr.Message != "ens name is already provisioned" {
		t.Fatalf("expected ENS ownership conflict, got %v", appErr)
	}
}

func TestSyncSoulV3StateFromRegistration_DeletesContactPreferencesWhenOmitted(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db)}
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Times(3)

	identity := &models.SoulAgentIdentity{
		AgentID: soulLifecycleTestAgentIDHex,
		Domain:  "example.com",
		LocalID: "agent-alice",
	}

	if appErr := s.syncSoulV3StateFromRegistration(context.Background(), identity.AgentID, identity, &soul.RegistrationFileV3{}, time.Date(2026, time.March, 5, 14, 0, 0, 0, time.UTC)); appErr != nil {
		t.Fatalf("unexpected appErr: %v", appErr)
	}

	foundPrefsDeleteModel := false
	for _, call := range tdb.db.Calls {
		if call.Method != "Model" || len(call.Arguments) == 0 {
			continue
		}
		prefs, ok := call.Arguments.Get(0).(*models.SoulAgentContactPreferences)
		if ok && prefs.AgentID == identity.AgentID {
			foundPrefsDeleteModel = true
		}
	}
	if !foundPrefsDeleteModel {
		t.Fatalf("expected contact preference delete model call when preferences are omitted")
	}
}

func packSoulRegistryWalletResult(t testing.TB, wallet string) []byte {
	t.Helper()

	parsedABI, err := abi.JSON(strings.NewReader(soul.SoulRegistryABI))
	if err != nil {
		t.Fatalf("parse soul registry ABI: %v", err)
	}
	out, err := parsedABI.Methods["getAgentWallet"].Outputs.Pack(common.HexToAddress(wallet))
	if err != nil {
		t.Fatalf("pack getAgentWallet output: %v", err)
	}
	return out
}

func newWalletVerificationTestServer(t testing.TB, walletResult string, dialErr error) *Server {
	t.Helper()
	return &Server{
		cfg: config.Config{
			SoulRPCURL:                  "http://rpc.local",
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
		dialEVM: func(ctx context.Context, rpcURL string) (ethRPCClient, error) {
			if dialErr != nil {
				return nil, dialErr
			}
			return &fakeEVMClient{callContract: func(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
				return packSoulRegistryWalletResult(t, walletResult), nil
			}}, nil
		},
	}
}

func assertWalletVerificationResult(t *testing.T, appErr *apptheory.AppError, wantCode string, wantMessage string) {
	t.Helper()
	if wantCode == "" {
		if appErr != nil {
			t.Fatalf("expected wallet verification success, got %v", appErr)
		}
		return
	}
	if appErr == nil || appErr.Code != wantCode || appErr.Message != wantMessage {
		t.Fatalf("expected %s/%q error, got %v", wantCode, wantMessage, appErr)
	}
}

const modelCallMethod = "Model"

func collectCapabilityClaimLevels(calls []mock.Call) map[string]string {
	levels := map[string]string{}
	for _, call := range calls {
		if call.Method != modelCallMethod || len(call.Arguments) == 0 {
			continue
		}
		idx, ok := call.Arguments.Get(0).(*models.SoulCapabilityAgentIndex)
		if !ok || strings.TrimSpace(idx.Capability) == "" {
			continue
		}
		levels[idx.Capability] = idx.ClaimLevel
	}
	return levels
}

type syncV3StateModels struct {
	phoneModel        *models.SoulAgentChannel
	prefsModel        *models.SoulAgentContactPreferences
	ensResolution     *models.SoulAgentENSResolution
	ensNames          map[string]bool
	emailIndexSeen    bool
	phoneIndexes      map[string]bool
	channelIndexTypes map[string]bool
}

func collectSyncV3StateModels(calls []mock.Call, agentID string) syncV3StateModels {
	summary := syncV3StateModels{
		ensNames:          map[string]bool{},
		phoneIndexes:      map[string]bool{},
		channelIndexTypes: map[string]bool{},
	}
	for _, call := range calls {
		if call.Method != modelCallMethod || len(call.Arguments) == 0 {
			continue
		}
		recordSyncV3StateModel(&summary, call.Arguments.Get(0), agentID)
	}
	return summary
}

func recordSyncV3StateModel(summary *syncV3StateModels, model any, agentID string) {
	switch v := model.(type) {
	case *models.SoulAgentChannel:
		recordSyncV3ChannelModel(summary, v)
	case *models.SoulAgentContactPreferences:
		if v.AgentID == agentID {
			summary.prefsModel = v
		}
	case *models.SoulAgentENSResolution:
		if strings.TrimSpace(v.ENSName) != "" {
			summary.ensNames[v.ENSName] = true
		}
		if v.AgentID == agentID && strings.TrimSpace(v.SoulRegistrationURI) != "" {
			summary.ensResolution = v
		}
	case *models.SoulEmailAgentIndex:
		if v.Email == "old-agent@example.com" {
			summary.emailIndexSeen = true
		}
	case *models.SoulPhoneAgentIndex:
		if strings.TrimSpace(v.Phone) != "" {
			summary.phoneIndexes[v.Phone] = true
		}
	case *models.SoulChannelAgentIndex:
		if strings.TrimSpace(v.ChannelType) != "" {
			summary.channelIndexTypes[v.ChannelType] = true
		}
	}
}

func recordSyncV3ChannelModel(summary *syncV3StateModels, channel *models.SoulAgentChannel) {
	if channel.ChannelType == models.SoulChannelTypePhone && channel.Identifier == "+15557654321" {
		summary.phoneModel = channel
	}
}

func assertSyncV3PhoneModel(t *testing.T, phoneModel *models.SoulAgentChannel, now time.Time) {
	t.Helper()
	if phoneModel == nil {
		t.Fatalf("expected updated phone channel model call")
	}
	if phoneModel.Provider != "" {
		t.Fatalf("expected self-asserted phone update not to inherit provider, got %q", phoneModel.Provider)
	}
	if phoneModel.SecretRef != "" {
		t.Fatalf("expected self-asserted phone update not to inherit secret ref, got %q", phoneModel.SecretRef)
	}
	if !phoneModel.ProvisionedAt.IsZero() {
		t.Fatalf("expected self-asserted phone update not to inherit provisionedAt, got %v", phoneModel.ProvisionedAt)
	}
	if !phoneModel.DeprovisionedAt.IsZero() {
		t.Fatalf("expected self-asserted phone update not to inherit deprovisionedAt, got %v", phoneModel.DeprovisionedAt)
	}
}

func assertSyncV3PrefsModel(t *testing.T, prefsModel *models.SoulAgentContactPreferences, rep float64) {
	t.Helper()
	if prefsModel == nil {
		t.Fatalf("expected contact preferences upsert model call")
	}
	if prefsModel.Preferred != "voice" || prefsModel.Fallback != "email" {
		t.Fatalf("expected normalized preferences, got preferred=%q fallback=%q", prefsModel.Preferred, prefsModel.Fallback)
	}
	if len(prefsModel.AvailabilityWindows) != 1 || prefsModel.AvailabilityWindows[0].StartTime != "09:00" {
		t.Fatalf("expected availability window to be persisted, got %+v", prefsModel.AvailabilityWindows)
	}
	if prefsModel.FirstContactRequireReputation == nil || *prefsModel.FirstContactRequireReputation != rep {
		t.Fatalf("expected first-contact reputation to be preserved, got %+v", prefsModel.FirstContactRequireReputation)
	}
}

func assertSyncV3Indexes(t *testing.T, summary syncV3StateModels, canonicalENSName string) {
	t.Helper()
	if !summary.ensNames["old-agent.lessersoul.eth"] || !summary.ensNames[canonicalENSName] {
		t.Fatalf("expected old ENS cleanup and canonical ENS upsert, got %v", summary.ensNames)
	}
	if !summary.emailIndexSeen {
		t.Fatalf("expected old email index cleanup")
	}
	if !summary.phoneIndexes["+15551234567"] || summary.phoneIndexes["+15557654321"] {
		t.Fatalf("expected old phone index cleanup without self-asserted phone upsert, got %v", summary.phoneIndexes)
	}
	if !summary.channelIndexTypes[models.SoulChannelTypeEmail] || summary.channelIndexTypes[models.SoulChannelTypePhone] {
		t.Fatalf("expected only removed email channel index cleanup, got %v", summary.channelIndexTypes)
	}
}

func assertSyncV3ENSResolution(t *testing.T, resolution *models.SoulAgentENSResolution, canonicalENSName string, identity *models.SoulAgentIdentity, now time.Time) {
	t.Helper()
	if resolution == nil {
		t.Fatalf("expected canonical ENS resolution material")
	}
	if resolution.ENSName != canonicalENSName || resolution.AgentID != identity.AgentID {
		t.Fatalf("unexpected ENS resolution identity: %#v", resolution)
	}
	assertSyncV3ENSResolutionIdentity(t, resolution, identity)
	assertSyncV3ENSResolutionMaterial(t, resolution, now)
}

func assertSyncV3ENSResolutionIdentity(t *testing.T, resolution *models.SoulAgentENSResolution, identity *models.SoulAgentIdentity) {
	t.Helper()
	if resolution.Wallet != identity.Wallet || resolution.LocalID != identity.LocalID || resolution.Domain != identity.Domain {
		t.Fatalf("expected identity fields in ENS resolution, got %#v", resolution)
	}
	if resolution.SoulRegistrationURI != "s3://bucket/"+soulRegistrationS3Key(identity.AgentID) {
		t.Fatalf("unexpected registration URI: %q", resolution.SoulRegistrationURI)
	}
}

func assertSyncV3ENSResolutionMaterial(t *testing.T, resolution *models.SoulAgentENSResolution, now time.Time) {
	t.Helper()
	if resolution.MCPEndpoint != "https://example.com/mcp" || resolution.ActivityPubURI != "https://example.com/activitypub" {
		t.Fatalf("unexpected endpoint material: %#v", resolution)
	}
	if resolution.Phone != "+15557654321" || resolution.Email != "" {
		t.Fatalf("unexpected contact material: %#v", resolution)
	}
	if resolution.Description != "I summarize documents for humans." || resolution.Status != models.SoulAgentStatusActive {
		t.Fatalf("unexpected text/status material: %#v", resolution)
	}
	if !resolution.CreatedAt.Equal(time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)) || !resolution.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected ENS resolution timestamps: created=%v updated=%v", resolution.CreatedAt, resolution.UpdatedAt)
	}
}
