package controlplane

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandleSoulAppendBoundary_DoesNotPatchRepublishV2Registration(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	packs := &fakeSoulPackStore{}
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
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
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Once()
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:                agentIDHex,
			Domain:                 "example.com",
			LocalID:                "agent-alice",
			Wallet:                 wallet,
			Status:                 models.SoulAgentStatusActive,
			SelfDescriptionVersion: 3,
			UpdatedAt:              time.Now().Add(-time.Minute).UTC(),
		}
	}).Once()

	// Seed an existing v2 registration file in the fake pack store.
	s3Key := soulRegistrationS3Key(agentIDHex)
	initialReg := map[string]any{
		"version": "2",
		"agentId": agentIDHex,
		"domain":  "example.com",
		"localId": "agent-alice",
		"wallet":  wallet,
		"attestations": map[string]any{
			"selfAttestation": "0xdeadbeef",
		},
		"boundaries": []any{},
		"created":    "2026-03-01T00:00:00Z",
		"updated":    "2026-03-01T00:00:00Z",
	}
	initialBytes, _ := json.Marshal(initialReg)
	packs.objects = map[string]fakePut{
		s3Key: {key: s3Key, body: initialBytes},
	}

	// Build a valid boundary signature over keccak256(bytes(statement)).
	statement := "I will not impersonate real people."
	statementDigest := crypto.Keccak256([]byte(statement))
	sig, _ := crypto.Sign(accounts.TextHash(statementDigest), key)
	sigHex := "0x" + hex.EncodeToString(sig)

	reqBody, _ := json.Marshal(map[string]any{
		"boundary_id": "boundary-001",
		"category":    "refusal",
		"statement":   statement,
		"signature":   sigHex,
	})

	ctx := &apptheory.Context{
		RequestID:    "r-boundary-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex},
		Request:      apptheory.Request{Body: reqBody},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handleSoulAppendBoundary(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	// Ensure v2 registration file was not patch-and-republished (it would invalidate self-attestation).
	if len(packs.puts) != 0 {
		t.Fatalf("expected no republish puts, got %d", len(packs.puts))
	}

	obj, ok := packs.objects[s3Key]
	if !ok || len(obj.body) == 0 {
		t.Fatalf("expected registration at %q", s3Key)
	}
	var patched map[string]any
	if err := json.Unmarshal(obj.body, &patched); err != nil {
		t.Fatalf("unmarshal patched: %v", err)
	}
	boundariesAny, ok := patched["boundaries"].([]any)
	if !ok || len(boundariesAny) != 0 {
		t.Fatalf("expected boundaries unchanged, got %#v", patched["boundaries"])
	}
}

func TestHandleSoulAppendBoundary_SupersedesRequiresExistingBoundary(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
		soulPacks: &fakeSoulPackStore{},
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
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Once()
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

	// Superseded boundary lookup returns not found.
	tdb.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(theoryErrors.ErrItemNotFound).Once()

	statement := "I will not do X."
	statementDigest := crypto.Keccak256([]byte(statement))
	sig, _ := crypto.Sign(accounts.TextHash(statementDigest), key)
	sigHex := "0x" + hex.EncodeToString(sig)

	reqBody, _ := json.Marshal(map[string]any{
		"boundary_id": "boundary-002",
		"category":    "refusal",
		"statement":   statement,
		"supersedes":  "missing",
		"signature":   sigHex,
	})

	ctx := &apptheory.Context{
		RequestID:    "r-boundary-2",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex},
		Request:      apptheory.Request{Body: reqBody},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	_, err = s.handleSoulAppendBoundary(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
}
