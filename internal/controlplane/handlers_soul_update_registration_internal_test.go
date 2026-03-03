package controlplane

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
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

func TestHandleSoulAgentUpdateRegistration_V2_FirstVersion_AllowsNullPreviousVersionURI(t *testing.T) {
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
		},
		soulPacks: packs,
	}

	agentIDHex := soulLifecycleTestAgentIDHex
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()
	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:   agentIDHex,
			Domain:    "example.com",
			LocalID:   "agent-alice",
			Wallet:    wallet,
			Status:    models.SoulAgentStatusActive,
			UpdatedAt: time.Now().Add(-time.Minute).UTC(),
		}
	}).Once()
	tdb.qVersion.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentVersion](t, args, 0)
		*dest = nil
	}).Once()
	tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()

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
		"version": "2",
		"agentId": agentIDHex,
		"domain":  "example.com",
		"localId": "agent-alice",
		"wallet":  wallet,
		"principal": map[string]any{
			"type":        "individual",
			"identifier":  "0xHumanWalletAddress",
			"displayName": "Alice",
			"contactUri":  "https://example.com/alice",
			"declaration": "I accept responsibility for this agent's behavior.",
			"signature":   "0xabc",
			"declaredAt":  "2026-03-01T00:00:00Z",
		},
		"selfDescription": map[string]any{
			"purpose":    "I summarize documents for humans.",
			"authoredBy": "agent",
		},
		"capabilities": []any{
			map[string]any{
				"capability": "text-summarization",
				"scope":      "general",
				"constraints": map[string]any{
					"maxTokens": 4096,
				},
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
		"transparency": map[string]any{
			"modelFamily": "unknown",
		},
		"endpoints": map[string]any{
			"mcp": "https://example.com/soul/mcp",
		},
		"lifecycle": map[string]any{
			"status":           "active",
			"statusChangedAt":  "2026-03-01T00:00:00Z",
			"reason":           nil,
			"successorAgentId": nil,
		},
		"previousVersionUri": nil,
		"changeSummary":      nil,
		"attestations":       map[string]any{},
		"created":            "2026-03-01T00:00:00Z",
		"updated":            "2026-03-01T00:00:00Z",
	}

	unsignedBytes, _ := json.Marshal(unsigned)
	jcsBytes, err := jsoncanonicalizer.Transform(unsignedBytes)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	digest := crypto.Keccak256(jcsBytes)
	sig, _ := crypto.Sign(accounts.TextHash(digest), key)
	sigHex := "0x" + hex.EncodeToString(sig)

	reg := map[string]any{}
	if err := json.Unmarshal(unsignedBytes, &reg); err != nil {
		t.Fatalf("unmarshal unsigned: %v", err)
	}
	regAtt := reg["attestations"].(map[string]any)
	regAtt["selfAttestation"] = sigHex
	regBytes, _ := json.Marshal(reg)

	ctx := &apptheory.Context{
		RequestID:    "r-v2-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex},
		Request:      apptheory.Request{Body: regBytes},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handleSoulAgentUpdateRegistration(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulUpdateRegistrationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Version != 1 {
		t.Fatalf("expected version 1, got %d", out.Version)
	}
	// Two puts: current path + versioned path.
	if len(packs.puts) < 2 {
		t.Fatalf("expected at least 2 puts, got %d", len(packs.puts))
	}
}

func TestHandleSoulAgentUpdateRegistration_V2_RequiresPreviousVersionURI_ForSubsequentVersions(t *testing.T) {
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
		},
		soulPacks: packs,
	}

	agentIDHex := soulLifecycleTestAgentIDHex
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())

	// Pretend we already have version 1 in the DB.
	tdb.qVersion.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentVersion](t, args, 0)
		*dest = []*models.SoulAgentVersion{{AgentID: agentIDHex, VersionNumber: 1, RegistrationUri: "s3://bucket/x"}}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()
	tdb.qWalletIdx.On("First", mock.AnythingOfType("*models.WalletIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:                agentIDHex,
			Domain:                 "example.com",
			LocalID:                "agent-alice",
			Wallet:                 wallet,
			Status:                 models.SoulAgentStatusActive,
			SelfDescriptionVersion: 1,
			UpdatedAt:              time.Now().Add(-time.Minute).UTC(),
		}
	}).Once()
	tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()

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

	prevURI := "s3://bucket/" + soulRegistrationVersionedS3Key(agentIDHex, 1)

	unsigned := map[string]any{
		"version": "2",
		"agentId": agentIDHex,
		"domain":  "example.com",
		"localId": "agent-alice",
		"wallet":  wallet,
		"principal": map[string]any{
			"type":        "individual",
			"identifier":  "0xHumanWalletAddress",
			"declaration": "I accept responsibility for this agent's behavior.",
			"signature":   "0xabc",
			"declaredAt":  "2026-03-01T00:00:00Z",
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
				"addedInVersion": "2",
				"signature":      "0xabc",
			},
		},
		"transparency": map[string]any{"modelFamily": "unknown"},
		"endpoints":    map[string]any{"mcp": "https://example.com/soul/mcp"},
		"lifecycle": map[string]any{
			"status":          "active",
			"statusChangedAt": "2026-03-01T00:00:00Z",
		},
		"previousVersionUri": prevURI,
		"changeSummary":      "Added boundaries",
		"attestations":       map[string]any{},
		"created":            "2026-03-01T00:00:00Z",
		"updated":            "2026-03-02T00:00:00Z",
	}

	unsignedBytes, _ := json.Marshal(unsigned)
	jcsBytes, err := jsoncanonicalizer.Transform(unsignedBytes)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	digest := crypto.Keccak256(jcsBytes)
	sig, _ := crypto.Sign(accounts.TextHash(digest), key)
	sigHex := "0x" + hex.EncodeToString(sig)

	reg := map[string]any{}
	if err := json.Unmarshal(unsignedBytes, &reg); err != nil {
		t.Fatalf("unmarshal unsigned: %v", err)
	}
	regAtt := reg["attestations"].(map[string]any)
	regAtt["selfAttestation"] = sigHex
	regBytes, _ := json.Marshal(reg)

	ctx := &apptheory.Context{
		RequestID:    "r-v2-2",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex},
		Request:      apptheory.Request{Body: regBytes},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handleSoulAgentUpdateRegistration(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulUpdateRegistrationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Version != 2 {
		t.Fatalf("expected version 2, got %d", out.Version)
	}
}

func TestValidateSoulRegistrationPreviousVersionURI_RejectsMismatch(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{SoulPackBucketName: "bucket"}}

	reg := &soul.RegistrationFileV2{
		Version:            "2",
		PreviousVersionURI: ptr("s3://bucket/wrong"),
	}

	if err := s.validateSoulRegistrationPreviousVersionURI(reg, soulLifecycleTestAgentIDHex, 2); err == nil {
		t.Fatalf("expected error")
	}
}

func ptr[T any](v T) *T { return &v }

func TestGetNextSoulAgentVersion_IgnoresLexicographicSKOrder(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db)}

	// Versions include 9 and 10; max should be 10 even if SK ordering would be wrong.
	tdb.qVersion.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentVersion](t, args, 0)
		*dest = []*models.SoulAgentVersion{
			{AgentID: soulLifecycleTestAgentIDHex, VersionNumber: 9},
			{AgentID: soulLifecycleTestAgentIDHex, VersionNumber: 10},
			{AgentID: soulLifecycleTestAgentIDHex, VersionNumber: 2},
		}
	}).Once()

	n, appErr := s.getNextSoulAgentVersion(context.Background(), soulLifecycleTestAgentIDHex)
	if appErr != nil {
		t.Fatalf("unexpected appErr: %v", appErr)
	}
	if n != 11 {
		t.Fatalf("expected 11, got %d", n)
	}
}

func TestValidateCapabilityClaimLevelTransitions_RejectsDowngrade(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db)}

	identity := &models.SoulAgentIdentity{
		AgentID: "0xabc",
		Domain:  "example.com",
		LocalID: "agent",
	}

	tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCapabilityAgentIndex](t, args, 0)
		*dest = models.SoulCapabilityAgentIndex{
			Capability: "social",
			ClaimLevel: "challenge-passed",
			Domain:     identity.Domain,
			LocalID:    identity.LocalID,
			AgentID:    identity.AgentID,
		}
	}).Once()

	appErr := s.validateCapabilityClaimLevelTransitions(context.Background(), identity, []string{"social"}, map[string]string{
		"social": "self-declared",
	})
	if appErr == nil {
		t.Fatalf("expected error")
	}
	if appErr.Code != "app.bad_request" {
		t.Fatalf("expected bad_request, got %q", appErr.Code)
	}
}
