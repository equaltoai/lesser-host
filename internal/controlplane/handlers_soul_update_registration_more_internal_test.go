package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/ethereum/go-ethereum"
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
		"local_id": "@Agent-Alice/",
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
			message: "local_id must not contain /, :, or @",
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
		cfg:   config.Config{WebAuthnRPID: "portal.example"},
	}
	now := time.Date(2026, time.March, 5, 13, 14, 15, 0, time.UTC)
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
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:     identity.AgentID,
			ChannelType: models.SoulChannelTypeEmail,
			Identifier:  "old-agent@example.com",
		}
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

	regV3 := &soul.RegistrationFileV3{
		Channels: &soul.ChannelsV3{
			ENS: &soul.ENSChannelV3{
				Name:            "agent-alice.lessersoul.eth",
				ResolverAddress: "0x0000000000000000000000000000000000000002",
				Chain:           "mainnet",
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
	}

	if appErr := s.syncSoulV3StateFromRegistration(context.Background(), identity.AgentID, identity, regV3, now); appErr != nil {
		t.Fatalf("unexpected appErr: %v", appErr)
	}

	summary := collectSyncV3StateModels(tdb.db.Calls, identity.AgentID)

	assertSyncV3PhoneModel(t, summary.phoneModel, now)
	assertSyncV3PrefsModel(t, summary.prefsModel, rep)
	assertSyncV3Indexes(t, summary)
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
	if phoneModel.Provider != "twilio" {
		t.Fatalf("expected host-managed provider to be preserved, got %q", phoneModel.Provider)
	}
	if phoneModel.SecretRef != "/ssm/phone" {
		t.Fatalf("expected host-managed secret ref to be preserved, got %q", phoneModel.SecretRef)
	}
	if !phoneModel.ProvisionedAt.Equal(now.Add(-24 * time.Hour)) {
		t.Fatalf("expected provisionedAt preservation, got %v", phoneModel.ProvisionedAt)
	}
	if !phoneModel.DeprovisionedAt.Equal(now.Add(-12 * time.Hour)) {
		t.Fatalf("expected deprovisionedAt preservation, got %v", phoneModel.DeprovisionedAt)
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

func assertSyncV3Indexes(t *testing.T, summary syncV3StateModels) {
	t.Helper()
	if !summary.ensNames["old-agent.lessersoul.eth"] || !summary.ensNames["agent-alice.lessersoul.eth"] {
		t.Fatalf("expected old ENS cleanup and new ENS upsert, got %v", summary.ensNames)
	}
	if !summary.emailIndexSeen {
		t.Fatalf("expected old email index cleanup")
	}
	if !summary.phoneIndexes["+15551234567"] || !summary.phoneIndexes["+15557654321"] {
		t.Fatalf("expected old and new phone index models, got %v", summary.phoneIndexes)
	}
	if !summary.channelIndexTypes[models.SoulChannelTypeEmail] || !summary.channelIndexTypes[models.SoulChannelTypePhone] {
		t.Fatalf("expected email delete and phone upsert channel index models, got %v", summary.channelIndexTypes)
	}
}
