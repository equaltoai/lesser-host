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

func TestHandleSoulAppendBoundary_BeginConfirm_PublishesSignedV2RegistrationVersion(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	packs := &fakeSoulPackStore{}
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
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

	principalDeclaration := "I accept responsibility for this agent's behavior."
	principalDigest := crypto.Keccak256([]byte(principalDeclaration))
	principalSig, _ := crypto.Sign(accounts.TextHash(principalDigest), key)
	principalSigHex := "0x" + hex.EncodeToString(principalSig)

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Twice()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Twice()
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
	}).Twice()

	// Boundary existence check (begin) returns not found.
	tdb.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(theoryErrors.ErrItemNotFound).Once()
	// Boundary list used to prevent truncation on republish.
	tdb.qBoundary.On("All", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
		*dest = []*models.SoulAgentBoundary{}
	}).Twice()

	// Version history reads: treat as empty (non-strict integrity mode).
	tdb.qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(theoryErrors.ErrItemNotFound).Maybe()
	// Cap claim-level history: default to self-declared.
	tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(theoryErrors.ErrItemNotFound).Maybe()

	// Version record + boundary record are created in one DynamoDB transaction.
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	// Seed an existing v2 registration file in the fake pack store.
	s3Key := soulRegistrationS3Key(agentIDHex)
	initialReg := map[string]any{
		"version": "2",
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
		"boundaries": []any{},
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
		"previousVersionUri": "s3://bucket/registry/v1/agents/" + agentIDHex + "/versions/2/registration.json",
		"changeSummary":      "seed",
		"attestations": map[string]any{
			"selfAttestation": "0xdeadbeef",
		},
		"created": "2026-03-01T00:00:00Z",
		"updated": "2026-03-01T00:00:00Z",
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

	beginBody, _ := json.Marshal(map[string]any{
		"boundary_id": "boundary-001",
		"category":    "refusal",
		"statement":   statement,
		"signature":   sigHex,
	})

	beginCtx := &apptheory.Context{
		RequestID:    "r-boundary-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex},
		Request:      apptheory.Request{Body: beginBody},
	}
	beginCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	beginResp, err := s.handleSoulBeginAppendBoundary(beginCtx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if beginResp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", beginResp.Status, string(beginResp.Body))
	}

	var beginOut soulAppendBoundaryBeginResponse
	if err := json.Unmarshal(beginResp.Body, &beginOut); err != nil {
		t.Fatalf("unmarshal begin: %v", err)
	}
	if beginOut.ExpectedVersion != 3 {
		t.Fatalf("expected expected_version=3, got %d", beginOut.ExpectedVersion)
	}
	if beginOut.NextVersion != 4 {
		t.Fatalf("expected next_version=4, got %d", beginOut.NextVersion)
	}
	digestBytes, err := hex.DecodeString(strings.TrimPrefix(beginOut.DigestHex, "0x"))
	if err != nil || len(digestBytes) != 32 {
		t.Fatalf("invalid digest_hex: %q", beginOut.DigestHex)
	}

	selfSigBytes, _ := crypto.Sign(accounts.TextHash(digestBytes), key)
	selfSigHex := "0x" + hex.EncodeToString(selfSigBytes)

	confirmBody, _ := json.Marshal(map[string]any{
		"boundary_id":      "boundary-001",
		"category":         "refusal",
		"statement":        statement,
		"signature":        sigHex,
		"issued_at":        beginOut.IssuedAt,
		"expected_version": beginOut.ExpectedVersion,
		"self_attestation": selfSigHex,
	})
	confirmCtx := &apptheory.Context{
		RequestID:    "r-boundary-1c",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex},
		Request:      apptheory.Request{Body: confirmBody},
	}
	confirmCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	confirmResp, err := s.handleSoulAppendBoundary(confirmCtx)
	if err != nil {
		t.Fatalf("unexpected confirm err: %v", err)
	}
	if confirmResp.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%q)", confirmResp.Status, string(confirmResp.Body))
	}

	var confirmOut soulAppendBoundaryResponse
	if err := json.Unmarshal(confirmResp.Body, &confirmOut); err != nil {
		t.Fatalf("unmarshal confirm: %v", err)
	}
	if confirmOut.Boundary.BoundaryID != "boundary-001" {
		t.Fatalf("expected boundary_id boundary-001, got %q", confirmOut.Boundary.BoundaryID)
	}
	if confirmOut.Boundary.AddedInVersion != 4 {
		t.Fatalf("expected added_in_version=4, got %d", confirmOut.Boundary.AddedInVersion)
	}

	// Two puts: versioned path + current path.
	if len(packs.puts) < 2 {
		t.Fatalf("expected at least 2 puts, got %d", len(packs.puts))
	}

	// Verify the published current registration includes the new boundary and is signature-valid.
	obj, ok := packs.objects[s3Key]
	if !ok || len(obj.body) == 0 {
		t.Fatalf("expected registration at %q", s3Key)
	}
	var published map[string]any
	if err := json.Unmarshal(obj.body, &published); err != nil {
		t.Fatalf("unmarshal published: %v", err)
	}
	if got := strings.TrimSpace(extractStringField(published, "previousVersionUri")); got == "" || !strings.Contains(got, "/versions/3/registration.json") {
		t.Fatalf("expected previousVersionUri to point to version 3, got %q", got)
	}
	boundariesAny, ok := published["boundaries"].([]any)
	if !ok || len(boundariesAny) == 0 {
		t.Fatalf("expected non-empty boundaries, got %#v", published["boundaries"])
	}
	found := false
	for _, item := range boundariesAny {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(extractStringField(m, "id")) != "boundary-001" {
			continue
		}
		if strings.TrimSpace(extractStringField(m, "addedInVersion")) != "4" {
			t.Fatalf("expected addedInVersion=4, got %q", extractStringField(m, "addedInVersion"))
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("expected boundary boundary-001 in published registration")
	}

	digest, appErr := computeSoulRegistrationSelfAttestationDigest(published)
	if appErr != nil {
		t.Fatalf("digest: %v", appErr)
	}
	att := published["attestations"].(map[string]any)
	gotSelfSig := strings.TrimSpace(att["selfAttestation"].(string))
	if err := verifyEthereumSignatureBytes(wallet, digest, gotSelfSig); err != nil {
		t.Fatalf("published selfAttestation did not verify: %v", err)
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
			SelfDescriptionVersion: 1,
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

	_, err = s.handleSoulBeginAppendBoundary(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
}
