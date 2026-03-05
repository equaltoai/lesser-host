package controlplane

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

func TestHandleSoulProvisionEmail_BeginThenConfirm_PublishesV3WithChannelAndIsIdempotent(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	packs := &fakeSoulPackStore{}
	ssm := map[string]string{}

	var migaduCalls []struct {
		localPart string
		name      string
		password  string
	}

	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulPackBucketName:          "bucket",
			Stage:                       "lab",
		},
		soulPacks: packs,
		ssmGetParameter: func(ctx context.Context, name string) (string, error) {
			v, ok := ssm[name]
			if !ok {
				return "", fmt.Errorf("not found")
			}
			return v, nil
		},
		ssmPutSecureValue: func(ctx context.Context, name string, value string, overwrite bool) error {
			if !overwrite {
				if _, ok := ssm[name]; ok {
					return fmt.Errorf("ParameterAlreadyExists")
				}
			}
			ssm[name] = value
			return nil
		},
		migaduCreateEmail: func(ctx context.Context, localPart string, name string, password string) error {
			migaduCalls = append(migaduCalls, struct {
				localPart string
				name      string
				password  string
			}{localPart: localPart, name: name, password: password})
			return nil
		},
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

	// Domain + instance access (operator bypasses portal approval but not domain access).
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Times(3)
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Times(3)

	identityCalls := 0
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		identityCalls++
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		ver := 3
		if identityCalls >= 3 {
			ver = 4
		}
		*dest = models.SoulAgentIdentity{
			AgentID:                agentIDHex,
			Domain:                 "example.com",
			LocalID:                "agent-alice",
			Wallet:                 wallet,
			Status:                 models.SoulAgentStatusActive,
			LifecycleStatus:        models.SoulAgentStatusActive,
			SelfDescriptionVersion: ver,
			UpdatedAt:              time.Now().Add(-time.Minute).UTC(),
		}
	}).Times(3)

	// Version history reads: treat as empty (non-strict integrity mode).
	tdb.qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(theoryErrors.ErrItemNotFound).Maybe()
	// Cap claim-level history: default to self-declared.
	tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(theoryErrors.ErrItemNotFound).Maybe()

	// Publish uses one DynamoDB transaction for the v3 version record.
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	// Channel reads: sync pass returns not found for ENS/email/phone; idempotency check returns the email channel.
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Times(3)
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:     agentIDHex,
			ChannelType: models.SoulChannelTypeEmail,
			Identifier:  "agent-alice@lessersoul.ai",
		}
	}).Once()

	// Seed an existing v2 registration file in the fake pack store (with at least one boundary).
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
				"claimLevel": "self-declared",
			},
		},
		"boundaries": []any{
			map[string]any{
				"id":             "b-001",
				"category":       "refusal",
				"statement":      "I will not impersonate real people.",
				"addedAt":        "2026-03-01T00:00:00Z",
				"addedInVersion": "1",
				"signature":      "0xdeadbeef",
			},
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

	beginCtx := &apptheory.Context{
		RequestID:    "r-email-begin-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex},
		Request:      apptheory.Request{Body: []byte(`{}`)},
	}
	beginCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	beginResp, err := s.handleSoulBeginProvisionEmailChannel(beginCtx)
	if err != nil {
		t.Fatalf("begin unexpected err: %v", err)
	}
	if beginResp.Status != http.StatusOK {
		t.Fatalf("begin expected 200, got %d (body=%q)", beginResp.Status, string(beginResp.Body))
	}

	var beginOut soulProvisionEmailBeginResponse
	if err := json.Unmarshal(beginResp.Body, &beginOut); err != nil {
		t.Fatalf("begin unmarshal: %v", err)
	}
	if beginOut.ExpectedVersion != 3 || beginOut.NextVersion != 4 {
		t.Fatalf("begin expected version chain 3->4, got %d->%d", beginOut.ExpectedVersion, beginOut.NextVersion)
	}
	if beginOut.Address != "agent-alice@lessersoul.ai" {
		t.Fatalf("begin expected address agent-alice@lessersoul.ai, got %q", beginOut.Address)
	}
	if beginOut.ENSName != "agent-alice.lessersoul.eth" {
		t.Fatalf("begin expected ens agent-alice.lessersoul.eth, got %q", beginOut.ENSName)
	}
	digestBytes, err := hex.DecodeString(strings.TrimPrefix(beginOut.DigestHex, "0x"))
	if err != nil || len(digestBytes) != 32 {
		t.Fatalf("begin invalid digest_hex: %q", beginOut.DigestHex)
	}

	selfSigBytes, _ := crypto.Sign(accounts.TextHash(digestBytes), key)
	selfSigHex := "0x" + hex.EncodeToString(selfSigBytes)

	confirmBody, _ := json.Marshal(map[string]any{
		"issued_at":        beginOut.IssuedAt,
		"expected_version": beginOut.ExpectedVersion,
		"self_attestation": selfSigHex,
	})
	confirmCtx := &apptheory.Context{
		RequestID:    "r-email-confirm-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex},
		Request:      apptheory.Request{Body: confirmBody},
	}
	confirmCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	confirmResp, err := s.handleSoulProvisionEmailChannel(confirmCtx)
	if err != nil {
		t.Fatalf("confirm unexpected err: %v", err)
	}
	if confirmResp.Status != http.StatusCreated {
		t.Fatalf("confirm expected 201, got %d (body=%q)", confirmResp.Status, string(confirmResp.Body))
	}

	if len(migaduCalls) != 1 {
		t.Fatalf("expected 1 migadu call, got %d", len(migaduCalls))
	}
	if migaduCalls[0].localPart != "agent-alice" {
		t.Fatalf("expected migadu localPart agent-alice, got %q", migaduCalls[0].localPart)
	}

	// Verify current registration in S3 is v3 and includes channels.
	obj, ok := packs.objects[s3Key]
	if !ok || len(obj.body) == 0 {
		t.Fatalf("expected registration at %q", s3Key)
	}
	var published map[string]any
	if err := json.Unmarshal(obj.body, &published); err != nil {
		t.Fatalf("unmarshal published: %v", err)
	}
	if got := strings.TrimSpace(extractStringField(published, "version")); got != "3" {
		t.Fatalf("expected published version 3, got %q", got)
	}
	chAny, ok := published["channels"].(map[string]any)
	if !ok || chAny == nil {
		t.Fatalf("expected published channels object, got %#v", published["channels"])
	}
	emailAny, ok := chAny["email"].(map[string]any)
	if !ok || emailAny == nil {
		t.Fatalf("expected published channels.email object, got %#v", chAny["email"])
	}
	if strings.TrimSpace(extractStringField(emailAny, "address")) != "agent-alice@lessersoul.ai" {
		t.Fatalf("expected published channels.email.address agent-alice@lessersoul.ai, got %#v", emailAny["address"])
	}

	// Second confirm: expected_version is behind; should return idempotent 200 without provisioning again.
	confirmCtx2 := &apptheory.Context{
		RequestID:    "r-email-confirm-2",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": agentIDHex},
		Request:      apptheory.Request{Body: confirmBody},
	}
	confirmCtx2.Set(ctxKeyOperatorRole, models.RoleAdmin)

	confirmResp2, err := s.handleSoulProvisionEmailChannel(confirmCtx2)
	if err != nil {
		t.Fatalf("confirm2 unexpected err: %v", err)
	}
	if confirmResp2.Status != http.StatusOK {
		t.Fatalf("confirm2 expected 200, got %d (body=%q)", confirmResp2.Status, string(confirmResp2.Body))
	}
	if len(migaduCalls) != 1 {
		t.Fatalf("expected migadu not called again; got %d calls", len(migaduCalls))
	}
}
