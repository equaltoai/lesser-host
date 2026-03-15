package controlplane

import (
	"crypto/ecdsa"
	"encoding/hex"
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

	fixture := newBoundaryAppendPublishFixture(t)
	beginOut := fixture.beginBoundaryAppend(t)
	confirmOut := fixture.confirmBoundaryAppend(t, beginOut)
	if confirmOut.Boundary.BoundaryID != boundaryTestID001 {
		t.Fatalf("expected boundary_id %s, got %q", boundaryTestID001, confirmOut.Boundary.BoundaryID)
	}
	if confirmOut.Boundary.AddedInVersion != 4 {
		t.Fatalf("expected added_in_version=4, got %d", confirmOut.Boundary.AddedInVersion)
	}
	fixture.assertBoundaryAppendPublished(t)
}

type boundaryAppendPublishFixture struct {
	server     *Server
	packs      *fakeSoulPackStore
	key        *ecdsa.PrivateKey
	wallet     string
	statement  string
	signature  string
	agentIDHex string
	s3Key      string
}

func newBoundaryAppendPublishFixture(t *testing.T) *boundaryAppendPublishFixture {
	t.Helper()
	tdb := newSoulLifecycleTestDB()
	packs := &fakeSoulPackStore{}
	server := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulPackBucketName:          "bucket",
			SoulSupportedCapabilities:   []string{"social"},
		},
		soulPacks: packs,
	}

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
	principalDigest := crypto.Keccak256([]byte(boundaryTestPrincipalDeclaration))
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
			AgentID:                soulLifecycleTestAgentIDHex,
			Domain:                 "example.com",
			LocalID:                "agent-alice",
			Wallet:                 wallet,
			Status:                 models.SoulAgentStatusActive,
			SelfDescriptionVersion: 3,
			UpdatedAt:              time.Now().Add(-time.Minute).UTC(),
		}
	}).Twice()
	tdb.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qBoundary.On("All", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
		*dest = []*models.SoulAgentBoundary{}
	}).Twice()
	tdb.qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(theoryErrors.ErrItemNotFound).Maybe()
	tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(theoryErrors.ErrItemNotFound).Maybe()
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	s3Key := soulRegistrationS3Key(soulLifecycleTestAgentIDHex)
	initialReg := map[string]any{
		"version": "2",
		"agentId": soulLifecycleTestAgentIDHex,
		"domain":  "example.com",
		"localId": "agent-alice",
		"wallet":  wallet,
		"principal": map[string]any{
			"type":        "individual",
			"identifier":  wallet,
			"displayName": "Alice",
			"contactUri":  "https://example.com/alice",
			"declaration": boundaryTestPrincipalDeclaration,
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
		"previousVersionUri": "s3://bucket/registry/v1/agents/" + soulLifecycleTestAgentIDHex + "/versions/2/registration.json",
		"changeSummary":      "seed",
		"attestations": map[string]any{
			"selfAttestation": "0xdeadbeef",
		},
		"created": "2026-03-01T00:00:00Z",
		"updated": "2026-03-01T00:00:00Z",
	}
	packs.objects = map[string]fakePut{
		s3Key: {key: s3Key, body: mustMarshalJSON(t, initialReg)},
	}

	statement := "I will not impersonate real people."
	statementDigest := crypto.Keccak256([]byte(statement))
	sig, _ := crypto.Sign(accounts.TextHash(statementDigest), key)

	return &boundaryAppendPublishFixture{
		server:     server,
		packs:      packs,
		key:        key,
		wallet:     wallet,
		statement:  statement,
		signature:  "0x" + hex.EncodeToString(sig),
		agentIDHex: soulLifecycleTestAgentIDHex,
		s3Key:      s3Key,
	}
}

func (f *boundaryAppendPublishFixture) beginBoundaryAppend(t *testing.T) soulAppendBoundaryBeginResponse {
	t.Helper()
	beginCtx := &apptheory.Context{
		RequestID:    "r-boundary-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": f.agentIDHex},
		Request: apptheory.Request{Body: mustMarshalJSON(t, map[string]any{
			"boundary_id": boundaryTestID001,
			"category":    boundaryTestCategoryRefusal,
			"statement":   f.statement,
			"signature":   f.signature,
		})},
	}
	beginCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	beginResp, err := f.server.handleSoulBeginAppendBoundary(beginCtx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if beginResp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", beginResp.Status, string(beginResp.Body))
	}
	return mustUnmarshalJSON[soulAppendBoundaryBeginResponse](t, beginResp.Body)
}

func (f *boundaryAppendPublishFixture) confirmBoundaryAppend(t *testing.T, beginOut soulAppendBoundaryBeginResponse) soulAppendBoundaryResponse {
	t.Helper()
	digestBytes, err := hex.DecodeString(strings.TrimPrefix(beginOut.DigestHex, "0x"))
	if err != nil || len(digestBytes) != 32 {
		t.Fatalf("invalid digest_hex: %q", beginOut.DigestHex)
	}
	selfSigBytes, _ := crypto.Sign(accounts.TextHash(digestBytes), f.key)
	confirmCtx := &apptheory.Context{
		RequestID:    "r-boundary-1c",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": f.agentIDHex},
		Request: apptheory.Request{Body: mustMarshalJSON(t, map[string]any{
			"boundary_id":      boundaryTestID001,
			"category":         boundaryTestCategoryRefusal,
			"statement":        f.statement,
			"signature":        f.signature,
			"issued_at":        beginOut.IssuedAt,
			"expected_version": beginOut.ExpectedVersion,
			"self_attestation": "0x" + hex.EncodeToString(selfSigBytes),
		})},
	}
	confirmCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	confirmResp, err := f.server.handleSoulAppendBoundary(confirmCtx)
	if err != nil {
		t.Fatalf("unexpected confirm err: %v", err)
	}
	if confirmResp.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%q)", confirmResp.Status, string(confirmResp.Body))
	}
	return mustUnmarshalJSON[soulAppendBoundaryResponse](t, confirmResp.Body)
}

func (f *boundaryAppendPublishFixture) assertBoundaryAppendPublished(t *testing.T) {
	t.Helper()
	if len(f.packs.puts) < 2 {
		t.Fatalf("expected at least 2 puts, got %d", len(f.packs.puts))
	}
	obj, ok := f.packs.objects[f.s3Key]
	if !ok || len(obj.body) == 0 {
		t.Fatalf("expected registration at %q", f.s3Key)
	}
	published := mustUnmarshalJSON[map[string]any](t, obj.body)
	if got := strings.TrimSpace(extractStringField(published, "previousVersionUri")); got == "" || !strings.Contains(got, "/versions/3/registration.json") {
		t.Fatalf("expected previousVersionUri to point to version 3, got %q", got)
	}
	boundariesAny := requireBoundarySlice(t, published["boundaries"])
	if len(boundariesAny) == 0 {
		t.Fatalf("expected non-empty boundaries, got %#v", published["boundaries"])
	}
	found := false
	for _, item := range boundariesAny {
		m := requireBoundaryMap(t, item)
		if strings.TrimSpace(extractStringField(m, "id")) != boundaryTestID001 {
			continue
		}
		if strings.TrimSpace(extractStringField(m, "addedInVersion")) != "4" {
			t.Fatalf("expected addedInVersion=4, got %q", extractStringField(m, "addedInVersion"))
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("expected boundary %s in published registration", boundaryTestID001)
	}

	digest, appErr := computeSoulRegistrationSelfAttestationDigest(published)
	if appErr != nil {
		t.Fatalf("digest: %v", appErr)
	}
	att := requireBoundaryMap(t, published["attestations"])
	gotSelfSig := strings.TrimSpace(requireBoundaryString(t, att["selfAttestation"]))
	if err := verifyEthereumSignatureBytes(f.wallet, digest, gotSelfSig); err != nil {
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
			AgentID:                agentIDHex,
			Domain:                 "example.com",
			LocalID:                "agent-alice",
			Wallet:                 wallet,
			Status:                 models.SoulAgentStatusActive,
			SelfDescriptionVersion: 1,
			UpdatedAt:              time.Now().Add(-time.Minute).UTC(),
		}
	}).Once()

	// Superseded boundary lookup returns not found.
	tdb.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(theoryErrors.ErrItemNotFound).Once()

	statement := "I will not do X."
	statementDigest := crypto.Keccak256([]byte(statement))
	sig, _ := crypto.Sign(accounts.TextHash(statementDigest), key)
	sigHex := "0x" + hex.EncodeToString(sig)

	reqBody := mustMarshalJSON(t, map[string]any{
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
