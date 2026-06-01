package controlplane

import (
	"context"
	"crypto/ecdsa"
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

const (
	provisionTestAgentLocalID     = "agent-alice"
	provisionTestInstanceSlug     = "inst1"
	provisionTestEmailLocalPart   = provisionTestAgentLocalID + "." + provisionTestInstanceSlug
	provisionTestEmailAddress     = provisionTestEmailLocalPart + "@lessersoul.ai"
	provisionTestEmailENSName     = provisionTestAgentLocalID + "." + provisionTestInstanceSlug + ".lessersoul.eth"
	provisionTestAgentDomain      = "example.com"
	provisionTestStageAlias       = "dev.simulacrum.greater.website"
	provisionTestStageBaseDomain  = "simulacrum.greater.website"
	provisionTestPilotScopedEmail = "pilot.simulacrum@lessersoul.ai"
)

func mustMarshalJSON(t testing.TB, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return body
}

func mustUnmarshalJSON[T any](t testing.TB, body []byte) T {
	t.Helper()
	var out T
	unmarshalErr := json.Unmarshal(body, &out)
	if unmarshalErr != nil {
		t.Fatalf("unmarshal json: %v", unmarshalErr)
	}
	return out
}

func testProvisionManagedChannelBaseRegistration(agentIDHex string, wallet string, principalDeclaration string, principalSigHex string) map[string]any {
	return map[string]any{
		"version": "2",
		"agentId": agentIDHex,
		"domain":  provisionTestAgentDomain,
		"localId": provisionTestAgentLocalID,
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
}

type provisionEmailMigaduCall struct {
	localPart string
	name      string
	password  string
}

type provisionEmailForwardingCall struct {
	localPart string
	address   string
}

type provisionEmailE2EFixture struct {
	tdb                  soulLifecycleTestDB
	packs                *fakeSoulPackStore
	ssm                  map[string]string
	migaduCalls          []provisionEmailMigaduCall
	forwardingCalls      []provisionEmailForwardingCall
	deleteMailboxCalls   []string
	server               *Server
	agentIDHex           string
	signingKey           *ecdsa.PrivateKey
	wallet               string
	principalDeclaration string
	principalSigHex      string
	s3Key                string
}

func TestHandleSoulProvisionEmail_BeginThenConfirm_PublishesV3WithChannelAndIsIdempotent(t *testing.T) {
	t.Parallel()

	fixture := newProvisionEmailE2EFixture(t)
	beginOut := runProvisionEmailBegin(t, fixture)
	confirmBody := buildProvisionEmailConfirmBody(t, fixture, beginOut)
	assertProvisionEmailConfirmCreated(t, fixture, confirmBody)
	assertProvisionEmailPublished(t, fixture)
	assertProvisionEmailIdempotent(t, fixture, confirmBody)
}

func TestBuildSoulProvisionManagedEmailAddress_DerivesInstanceScopedAddress(t *testing.T) {
	t.Parallel()

	_, appErr := buildSoulProvisionManagedEmailAddress(provisionTestAgentLocalID, provisionTestInstanceSlug, "victim")
	if appErr == nil || appErr.Code != appErrCodeConflict || appErr.Message != soulEmailProvisionErrDerivedLocalPartReq {
		t.Fatalf("expected stale local_part conflict, got %v", appErr)
	}

	got, appErr := buildSoulProvisionManagedEmailAddress(provisionTestAgentLocalID, provisionTestInstanceSlug, "")
	if appErr != nil {
		t.Fatalf("expected derived address success, got %v", appErr)
	}
	if got.AgentLocalID != provisionTestAgentLocalID ||
		got.InstanceSlug != provisionTestInstanceSlug ||
		got.ProviderLocalPart != provisionTestEmailLocalPart ||
		got.Address != provisionTestEmailAddress ||
		got.ENSName != provisionTestEmailENSName {
		t.Fatalf("unexpected resolved address: %#v", got)
	}

	got, appErr = buildSoulProvisionManagedEmailAddress(provisionTestAgentLocalID, provisionTestInstanceSlug, provisionTestEmailLocalPart)
	if appErr != nil {
		t.Fatalf("expected exact local_part success, got %v", appErr)
	}
	if got.ProviderLocalPart != provisionTestEmailLocalPart {
		t.Fatalf("unexpected exact local_part result: %#v", got)
	}
}

func TestBuildSoulProvisionManagedEmailAddress_RejectsAmbiguousHandleCharacters(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"Agent-With-Uppercase",
		"agent_with_underscore",
		"agent.with.dot",
		"agent with space",
		"agent%2falice",
		"agent/alice",
		"@agent",
		"-agent",
		"agent-",
	}
	for _, raw := range invalid {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			if _, appErr := buildSoulProvisionManagedEmailAddress(raw, "simulacrum", ""); appErr == nil || appErr.Code != appErrCodeConflict {
				t.Fatalf("expected invalid local id conflict for %q, got %v", raw, appErr)
			}
		})
	}
}

func TestBuildSoulProvisionManagedEmailAddress_DerivesDistinctAddressesAcrossInstances(t *testing.T) {
	t.Parallel()

	a, appErr := buildSoulProvisionManagedEmailAddress("pilot", "simulacrum", "")
	if appErr != nil {
		t.Fatalf("expected first instance success, got %v", appErr)
	}
	b, appErr := buildSoulProvisionManagedEmailAddress("pilot", "sim-lab", "")
	if appErr != nil {
		t.Fatalf("expected second instance success, got %v", appErr)
	}
	if a.Address == b.Address || a.Address != provisionTestPilotScopedEmail || b.Address != "pilot.sim-lab@lessersoul.ai" {
		t.Fatalf("expected same local id to derive distinct instance addresses: a=%#v b=%#v", a, b)
	}
}

func TestBuildSoulProvisionEmailRegistration_UpdatesOnlyManagedENSChannel(t *testing.T) {
	t.Parallel()

	fixture := newProvisionEmailE2EFixture(t)
	s := fixture.server
	now := time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC)

	legacyBase := testProvisionManagedChannelBaseRegistration(fixture.agentIDHex, fixture.wallet, fixture.principalDeclaration, fixture.principalSigHex)
	legacyBase["channels"] = map[string]any{
		"ens": map[string]any{
			"name":  provisionTestAgentLocalID + ".lessersoul.eth",
			"chain": "mainnet",
		},
	}
	legacyReg, _, _, appErr := s.buildSoulProvisionEmailRegistration(context.Background(), legacyBase, "2", fixture.agentIDHex, &models.SoulAgentIdentity{LocalID: provisionTestAgentLocalID}, soulProvisionEmailBuildInput{
		EmailAddress: provisionTestEmailAddress,
		ENSName:      provisionTestEmailENSName,
		IssuedAt:     now,
		ExpectedPrev: 3,
		NextVersion:  4,
	})
	if appErr != nil {
		t.Fatalf("legacy build unexpected appErr: %v", appErr)
	}
	assertProvisionENSChannelMetadata(t, legacyReg, provisionTestEmailENSName, "sepolia", "0x0000000000000000000000000000000000000002")

	externalBase := testProvisionManagedChannelBaseRegistration(fixture.agentIDHex, fixture.wallet, fixture.principalDeclaration, fixture.principalSigHex)
	externalBase["channels"] = map[string]any{
		"ens": map[string]any{
			"name":            "external.eth",
			"chain":           "mainnet",
			"resolverAddress": "0x00000000000000000000000000000000000000ee",
		},
	}
	externalReg, _, _, appErr := s.buildSoulProvisionEmailRegistration(context.Background(), externalBase, "2", fixture.agentIDHex, &models.SoulAgentIdentity{LocalID: provisionTestAgentLocalID}, soulProvisionEmailBuildInput{
		EmailAddress: provisionTestEmailAddress,
		ENSName:      provisionTestEmailENSName,
		IssuedAt:     now,
		ExpectedPrev: 3,
		NextVersion:  4,
	})
	if appErr != nil {
		t.Fatalf("external build unexpected appErr: %v", appErr)
	}
	assertProvisionENSChannelMetadata(t, externalReg, "external.eth", "mainnet", "0x00000000000000000000000000000000000000ee")
}

func TestBuildSoulProvisionManagedEmailAddress_RejectsOverflowAndInvalidSlug(t *testing.T) {
	t.Parallel()

	_, appErr := buildSoulProvisionManagedEmailAddress(strings.Repeat("a", 56), "simulacrum", "")
	if appErr == nil || appErr.Code != appErrCodeConflict || appErr.Message != "email local_part exceeds 64-octet SMTP limit" {
		t.Fatalf("expected SMTP local_part overflow conflict, got %v", appErr)
	}

	_, appErr = buildSoulProvisionManagedEmailAddress("pilot", "bad_slug", "")
	if appErr == nil || appErr.Code != appErrCodeConflict || appErr.Message != "agent domain instance slug is invalid" {
		t.Fatalf("expected invalid slug conflict, got %v", appErr)
	}
}

func TestValidateSoulProvisionEmailAddressAvailability_PreventsDuplicateFullAddress(t *testing.T) {
	t.Parallel()

	db, queries := newTestDBWithModelQueries("*models.SoulEmailAgentIndex")
	qEmail := queries[0]
	qEmail.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulEmailAgentIndex](t, args, 0)
		*dest = models.SoulEmailAgentIndex{
			Email:   "pilot.simulacrum@lessersoul.ai",
			AgentID: "0xother",
		}
	}).Once()

	s := &Server{store: store.New(db)}
	appErr := s.validateSoulProvisionEmailAddressAvailability(context.Background(), "0xagent", "pilot.simulacrum@lessersoul.ai")
	if appErr == nil || appErr.Code != appErrCodeConflict || appErr.Message != soulEmailProvisionErrAddressTaken {
		t.Fatalf("expected duplicate full-address conflict, got %v", appErr)
	}
}

func TestResolveSoulProvisionEmailAddress_ExactVanityDomain(t *testing.T) {
	t.Parallel()

	db, queries := newTestDBWithModelQueries("*models.Domain")
	qDomain := queries[0]
	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:       "agents.example.com",
			InstanceSlug: "vanity-inst",
			Type:         models.DomainTypeVanity,
			Status:       models.DomainStatusVerified,
		}
	}).Once()

	s := &Server{store: store.New(db)}
	got, appErr := s.resolveSoulProvisionEmailAddress(context.Background(), &models.SoulAgentIdentity{
		Domain:  "agents.example.com",
		LocalID: "pilot",
	}, "")
	if appErr != nil {
		t.Fatalf("expected exact vanity resolution, got %v", appErr)
	}
	if got.ProviderLocalPart != "pilot.vanity-inst" || got.Address != "pilot.vanity-inst@lessersoul.ai" {
		t.Fatalf("unexpected exact vanity address: %#v", got)
	}
}

func TestResolveSoulProvisionEmailAddress_ManagedStagePrimaryAlias(t *testing.T) {
	t.Parallel()

	db, queries := newTestDBWithModelQueries("*models.Domain", "*models.Instance")
	qDomain := queries[0]
	qInstance := queries[1]
	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
	qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:             provisionTestStageBaseDomain,
			InstanceSlug:       "simulacrum",
			Type:               models.DomainTypePrimary,
			Status:             models.DomainStatusActive,
			VerificationMethod: "managed",
		}
	}).Once()
	qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "simulacrum", HostedBaseDomain: provisionTestStageBaseDomain}
	}).Once()

	s := &Server{store: store.New(db), cfg: config.Config{Stage: "lab"}}
	got, appErr := s.resolveSoulProvisionEmailAddress(context.Background(), &models.SoulAgentIdentity{
		Domain:  provisionTestStageAlias,
		LocalID: "pilot",
	}, "")
	if appErr != nil {
		t.Fatalf("expected managed stage alias resolution, got %v", appErr)
	}
	if got.ProviderLocalPart != "pilot.simulacrum" || got.Address != "pilot.simulacrum@lessersoul.ai" {
		t.Fatalf("unexpected stage alias address: %#v", got)
	}
}

func TestResolveSoulProvisionEmailAddress_FailsClosedForDomainState(t *testing.T) {
	t.Parallel()

	t.Run("missing domain", func(t *testing.T) {
		t.Parallel()

		db, queries := newTestDBWithModelQueries("*models.Domain")
		queries[0].On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
		s := &Server{store: store.New(db), cfg: config.Config{Stage: "lab"}}
		_, appErr := s.resolveSoulProvisionEmailAddress(context.Background(), &models.SoulAgentIdentity{Domain: "example.com", LocalID: "pilot"}, "")
		if appErr == nil || appErr.Code != appErrCodeConflict || appErr.Message != "agent domain is not registered for managed email" {
			t.Fatalf("expected missing-domain conflict, got %v", appErr)
		}
	})

	t.Run("unverified exact domain", func(t *testing.T) {
		t.Parallel()

		db, queries := newTestDBWithModelQueries("*models.Domain")
		queries[0].On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
			*dest = models.Domain{Domain: "example.com", InstanceSlug: "simulacrum", Status: models.DomainStatusPending}
		}).Once()
		s := &Server{store: store.New(db)}
		_, appErr := s.resolveSoulProvisionEmailAddress(context.Background(), &models.SoulAgentIdentity{Domain: "example.com", LocalID: "pilot"}, "")
		if appErr == nil || appErr.Code != appErrCodeConflict || appErr.Message != "agent domain is not verified for managed email" {
			t.Fatalf("expected unverified-domain conflict, got %v", appErr)
		}
	})

	t.Run("stage vanity alias", func(t *testing.T) {
		t.Parallel()

		db, queries := newTestDBWithModelQueries("*models.Domain", "*models.Instance")
		qDomain := queries[0]
		qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(theoryErrors.ErrItemNotFound).Once()
		qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
			*dest = models.Domain{
				Domain:             "vanity.example.com",
				InstanceSlug:       "simulacrum",
				Type:               models.DomainTypeVanity,
				Status:             models.DomainStatusVerified,
				VerificationMethod: "dns_txt",
			}
		}).Once()
		s := &Server{store: store.New(db), cfg: config.Config{Stage: "lab"}}
		_, appErr := s.resolveSoulProvisionEmailAddress(context.Background(), &models.SoulAgentIdentity{Domain: "dev.vanity.example.com", LocalID: "pilot"}, "")
		if appErr == nil || appErr.Code != appErrCodeConflict || appErr.Message != "agent domain is not registered for managed email" {
			t.Fatalf("expected stage vanity alias conflict, got %v", appErr)
		}
	})
}

func newProvisionEmailE2EFixture(t *testing.T) *provisionEmailE2EFixture {
	t.Helper()

	fixture := &provisionEmailE2EFixture{
		tdb:        newSoulLifecycleTestDB(),
		packs:      &fakeSoulPackStore{},
		ssm:        map[string]string{},
		agentIDHex: soulLifecycleTestAgentIDHex,
	}

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	fixture.signingKey = key
	fixture.wallet = strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
	fixture.principalDeclaration = boundaryTestPrincipalDeclaration
	principalDigest := crypto.Keccak256([]byte(fixture.principalDeclaration))
	principalSig, _ := crypto.Sign(accounts.TextHash(principalDigest), key)
	fixture.principalSigHex = "0x" + hex.EncodeToString(principalSig)
	fixture.s3Key = soulRegistrationS3Key(fixture.agentIDHex)

	fixture.server = &Server{
		store: store.New(fixture.tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulPackBucketName:          "bucket",
			SoulSupportedCapabilities:   []string{"social"},
			PublicBaseURL:               "https://lab.lesser.host",
			SoulEmailInboundDomain:      "inbound.lessersoul.ai",
			ENSGatewayChainName:         "sepolia",
			ENSGatewayResolverAddress:   "0x0000000000000000000000000000000000000002",
			Stage:                       "lab",
		},
		soulPacks: fixture.packs,
		ssmGetParameter: func(ctx context.Context, name string) (string, error) {
			v, ok := fixture.ssm[name]
			if !ok {
				return "", fmt.Errorf("not found")
			}
			return v, nil
		},
		ssmPutSecureValue: func(ctx context.Context, name string, value string, overwrite bool) error {
			if !overwrite {
				if _, ok := fixture.ssm[name]; ok {
					return fmt.Errorf("ParameterAlreadyExists")
				}
			}
			fixture.ssm[name] = value
			return nil
		},
		migaduCreateEmail: func(ctx context.Context, localPart string, name string, password string) error {
			fixture.migaduCalls = append(fixture.migaduCalls, provisionEmailMigaduCall{localPart: localPart, name: name, password: password})
			return nil
		},
		migaduForwarding: func(ctx context.Context, localPart string, address string) error {
			fixture.forwardingCalls = append(fixture.forwardingCalls, provisionEmailForwardingCall{localPart: localPart, address: address})
			return nil
		},
		migaduDeleteEmail: func(ctx context.Context, localPart string) error {
			fixture.deleteMailboxCalls = append(fixture.deleteMailboxCalls, localPart)
			return nil
		},
	}

	seedProvisionEmailE2EAccess(t, fixture)
	initialBytes := mustMarshalJSON(t, testProvisionManagedChannelBaseRegistration(fixture.agentIDHex, fixture.wallet, fixture.principalDeclaration, fixture.principalSigHex))
	fixture.packs.objects = map[string]fakePut{
		fixture.s3Key: {key: fixture.s3Key, body: initialBytes},
	}
	return fixture
}

func seedProvisionEmailE2EAccess(t *testing.T, fixture *provisionEmailE2EFixture) {
	t.Helper()

	fixture.tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: provisionTestAgentDomain, InstanceSlug: provisionTestInstanceSlug, Status: models.DomainStatusVerified}
	}).Times(5)
	fixture.tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: provisionTestInstanceSlug, Owner: "admin"}
	}).Times(3)

	identityCalls := 0
	fixture.tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		identityCalls++
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		ver := 3
		if identityCalls >= 3 {
			ver = 4
		}
		*dest = models.SoulAgentIdentity{
			AgentID:                fixture.agentIDHex,
			Domain:                 provisionTestAgentDomain,
			LocalID:                provisionTestAgentLocalID,
			Wallet:                 fixture.wallet,
			Status:                 models.SoulAgentStatusActive,
			LifecycleStatus:        models.SoulAgentStatusActive,
			SelfDescriptionVersion: ver,
			UpdatedAt:              time.Now().Add(-time.Minute).UTC(),
		}
	}).Times(3)

	fixture.tdb.qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(theoryErrors.ErrItemNotFound).Maybe()
	fixture.tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(theoryErrors.ErrItemNotFound).Maybe()
	fixture.tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	fixture.tdb.qEmailIdx.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(theoryErrors.ErrItemNotFound).Times(3)
	fixture.tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(theoryErrors.ErrItemNotFound).Once()
	fixture.tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Times(3)
	fixture.tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:     fixture.agentIDHex,
			ChannelType: models.SoulChannelTypeEmail,
			Identifier:  provisionTestEmailAddress,
		}
	}).Once()
}

func runProvisionEmailBegin(t *testing.T, fixture *provisionEmailE2EFixture) soulProvisionEmailBeginResponse {
	t.Helper()

	beginCtx := &apptheory.Context{
		RequestID:    "r-email-begin-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": fixture.agentIDHex},
		Request:      apptheory.Request{Body: []byte(`{}`)},
	}
	beginCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	beginResp, err := fixture.server.handleSoulBeginProvisionEmailChannel(beginCtx)
	if err != nil {
		t.Fatalf("begin unexpected err: %v", err)
	}
	if beginResp.Status != http.StatusOK {
		t.Fatalf("begin expected 200, got %d (body=%q)", beginResp.Status, string(beginResp.Body))
	}

	beginOut := mustUnmarshalJSON[soulProvisionEmailBeginResponse](t, beginResp.Body)
	if beginOut.ExpectedVersion != 3 || beginOut.NextVersion != 4 {
		t.Fatalf("begin expected version chain 3->4, got %d->%d", beginOut.ExpectedVersion, beginOut.NextVersion)
	}
	if beginOut.Address != provisionTestEmailAddress {
		t.Fatalf("begin expected address %s, got %q", provisionTestEmailAddress, beginOut.Address)
	}
	if beginOut.ENSName != provisionTestEmailENSName {
		t.Fatalf("begin expected ens %s, got %q", provisionTestEmailENSName, beginOut.ENSName)
	}
	assertProvisionENSChannelMetadata(t, beginOut.Registration, provisionTestEmailENSName, "sepolia", "0x0000000000000000000000000000000000000002")
	return beginOut
}

func buildProvisionEmailConfirmBody(t *testing.T, fixture *provisionEmailE2EFixture, beginOut soulProvisionEmailBeginResponse) []byte {
	t.Helper()

	digestBytes, err := hex.DecodeString(strings.TrimPrefix(beginOut.DigestHex, "0x"))
	if err != nil || len(digestBytes) != 32 {
		t.Fatalf("begin invalid digest_hex: %q", beginOut.DigestHex)
	}
	selfSigBytes, _ := crypto.Sign(accounts.TextHash(digestBytes), fixture.signingKey)
	selfSigHex := "0x" + hex.EncodeToString(selfSigBytes)
	return mustMarshalJSON(t, map[string]any{
		"issued_at":        beginOut.IssuedAt,
		"expected_version": beginOut.ExpectedVersion,
		"self_attestation": selfSigHex,
	})
}

func assertProvisionEmailConfirmCreated(t *testing.T, fixture *provisionEmailE2EFixture, confirmBody []byte) {
	t.Helper()

	confirmCtx := &apptheory.Context{
		RequestID:    "r-email-confirm-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": fixture.agentIDHex},
		Request:      apptheory.Request{Body: confirmBody},
	}
	confirmCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	confirmResp, err := fixture.server.handleSoulProvisionEmailChannel(confirmCtx)
	if err != nil {
		t.Fatalf("confirm unexpected err: %v", err)
	}
	if confirmResp.Status != http.StatusCreated {
		t.Fatalf("confirm expected 201, got %d (body=%q)", confirmResp.Status, string(confirmResp.Body))
	}
	confirmOut := mustUnmarshalJSON[soulProvisionEmailConfirmResponse](t, confirmResp.Body)
	if confirmOut.Address != provisionTestEmailAddress || confirmOut.RegistrationVersion != 4 {
		t.Fatalf("unexpected confirm response: %#v", confirmOut)
	}
	if len(fixture.migaduCalls) != 1 {
		t.Fatalf("expected 1 migadu call, got %d", len(fixture.migaduCalls))
	}
	if fixture.migaduCalls[0].localPart != provisionTestEmailLocalPart {
		t.Fatalf("expected migadu localPart %s, got %q", provisionTestEmailLocalPart, fixture.migaduCalls[0].localPart)
	}
	if len(fixture.forwardingCalls) != 1 {
		t.Fatalf("expected 1 forwarding call, got %d", len(fixture.forwardingCalls))
	}
	if fixture.forwardingCalls[0].localPart != provisionTestEmailLocalPart {
		t.Fatalf("expected forwarding localPart %s, got %q", provisionTestEmailLocalPart, fixture.forwardingCalls[0].localPart)
	}
	if fixture.forwardingCalls[0].address != "agent-alice.inst1@inbound.lessersoul.ai" {
		t.Fatalf("unexpected forwarding address: %q", fixture.forwardingCalls[0].address)
	}
}

func assertProvisionEmailPublished(t *testing.T, fixture *provisionEmailE2EFixture) {
	t.Helper()

	obj, ok := fixture.packs.objects[fixture.s3Key]
	if !ok || len(obj.body) == 0 {
		t.Fatalf("expected registration at %q", fixture.s3Key)
	}
	published := mustUnmarshalJSON[map[string]any](t, obj.body)
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
	if strings.TrimSpace(extractStringField(emailAny, "address")) != provisionTestEmailAddress {
		t.Fatalf("expected published channels.email.address %s, got %#v", provisionTestEmailAddress, emailAny["address"])
	}
	assertProvisionENSChannelMetadata(t, published, provisionTestEmailENSName, "sepolia", "0x0000000000000000000000000000000000000002")
}

func assertProvisionENSChannelMetadata(t *testing.T, reg map[string]any, wantName string, wantChain string, wantResolver string) {
	t.Helper()
	chAny, ok := reg["channels"].(map[string]any)
	if !ok || chAny == nil {
		t.Fatalf("expected registration channels object, got %#v", reg["channels"])
	}
	ensAny, ok := chAny["ens"].(map[string]any)
	if !ok || ensAny == nil {
		t.Fatalf("expected channels.ens object, got %#v", chAny["ens"])
	}
	if got := strings.TrimSpace(extractStringField(ensAny, "name")); got != wantName {
		t.Fatalf("expected channels.ens.name %q, got %q", wantName, got)
	}
	if got := strings.TrimSpace(extractStringField(ensAny, "chain")); got != wantChain {
		t.Fatalf("expected channels.ens.chain %q, got %q", wantChain, got)
	}
	if got := strings.TrimSpace(extractStringField(ensAny, "resolverAddress")); got != wantResolver {
		t.Fatalf("expected channels.ens.resolverAddress %q, got %q", wantResolver, got)
	}
}

func assertProvisionEmailIdempotent(t *testing.T, fixture *provisionEmailE2EFixture, confirmBody []byte) {
	t.Helper()

	confirmCtx := &apptheory.Context{
		RequestID:    "r-email-confirm-2",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": fixture.agentIDHex},
		Request:      apptheory.Request{Body: confirmBody},
	}
	confirmCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	confirmResp, err := fixture.server.handleSoulProvisionEmailChannel(confirmCtx)
	if err != nil {
		t.Fatalf("confirm2 unexpected err: %v", err)
	}
	if confirmResp.Status != http.StatusOK {
		t.Fatalf("confirm2 expected 200, got %d (body=%q)", confirmResp.Status, string(confirmResp.Body))
	}
	confirmOut := mustUnmarshalJSON[soulProvisionEmailConfirmResponse](t, confirmResp.Body)
	if confirmOut.Address != provisionTestEmailAddress || confirmOut.RegistrationVersion != 4 {
		t.Fatalf("unexpected idempotent confirm response: %#v", confirmOut)
	}
	if len(fixture.migaduCalls) != 1 {
		t.Fatalf("expected migadu not called again; got %d calls", len(fixture.migaduCalls))
	}
	if len(fixture.forwardingCalls) != 1 {
		t.Fatalf("expected forwarding not called again; got %d calls", len(fixture.forwardingCalls))
	}
}

func TestHandleSoulProvisionEmail_ForwardingFailureRollsBackMailboxAndStopsPublish(t *testing.T) {
	t.Parallel()

	fixture := newProvisionEmailE2EFixture(t)
	fixture.server.migaduForwarding = func(ctx context.Context, localPart string, address string) error {
		fixture.forwardingCalls = append(fixture.forwardingCalls, provisionEmailForwardingCall{localPart: localPart, address: address})
		return fmt.Errorf("boom")
	}

	beginOut := runProvisionEmailBegin(t, fixture)
	confirmBody := buildProvisionEmailConfirmBody(t, fixture, beginOut)

	confirmCtx := &apptheory.Context{
		RequestID:    "r-email-confirm-fail-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": fixture.agentIDHex},
		Request:      apptheory.Request{Body: confirmBody},
	}
	confirmCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	_, err := fixture.server.handleSoulProvisionEmailChannel(confirmCtx)
	appErr := requireProvisionEmailAppErr(t, err)
	if appErr.Code != appErrCodeInternal || appErr.Message != "failed to provision email" {
		t.Fatalf("unexpected app error: %#v", appErr)
	}
	if len(fixture.migaduCalls) != 1 {
		t.Fatalf("expected mailbox create before rollback, got %d create calls", len(fixture.migaduCalls))
	}
	if len(fixture.forwardingCalls) != 1 {
		t.Fatalf("expected forwarding attempt, got %d calls", len(fixture.forwardingCalls))
	}
	if len(fixture.deleteMailboxCalls) != 1 || fixture.deleteMailboxCalls[0] != provisionTestEmailLocalPart {
		t.Fatalf("expected mailbox rollback for %s, got %#v", provisionTestEmailLocalPart, fixture.deleteMailboxCalls)
	}
	if len(fixture.packs.objects[fixture.s3Key].body) == 0 {
		t.Fatalf("expected seed registration to remain present")
	}
	published := mustUnmarshalJSON[map[string]any](t, fixture.packs.objects[fixture.s3Key].body)
	if got := strings.TrimSpace(extractStringField(published, "version")); got != "2" {
		t.Fatalf("expected registration version to remain 2 after rollback, got %q", got)
	}
}

func TestHandleSoulProvisionEmail_RejectsStaleBareLocalPartOnConfirm(t *testing.T) {
	t.Parallel()

	fixture := newProvisionEmailE2EFixture(t)
	beginOut := runProvisionEmailBegin(t, fixture)
	var confirmBody map[string]any
	if err := json.Unmarshal(buildProvisionEmailConfirmBody(t, fixture, beginOut), &confirmBody); err != nil {
		t.Fatalf("unmarshal confirm body: %v", err)
	}
	confirmBody["local_part"] = provisionTestAgentLocalID

	confirmCtx := &apptheory.Context{
		RequestID:    "r-email-confirm-stale-local-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": fixture.agentIDHex},
		Request:      apptheory.Request{Body: mustMarshalJSON(t, confirmBody)},
	}
	confirmCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	_, err := fixture.server.handleSoulProvisionEmailChannel(confirmCtx)
	appErr := requireProvisionEmailAppErr(t, err)
	if appErr.Code != appErrCodeConflict || appErr.Message != soulEmailProvisionErrDerivedLocalPartReq {
		t.Fatalf("unexpected app error: %#v", appErr)
	}
	if len(fixture.migaduCalls) != 0 || len(fixture.forwardingCalls) != 0 || len(fixture.deleteMailboxCalls) != 0 {
		t.Fatalf("expected stale local_part to stop before provider calls: creates=%#v forwardings=%#v deletes=%#v", fixture.migaduCalls, fixture.forwardingCalls, fixture.deleteMailboxCalls)
	}
}

func TestHandleSoulBeginProvisionEmail_RejectsStaleBareLocalPart(t *testing.T) {
	t.Parallel()

	fixture := newProvisionEmailE2EFixture(t)
	beginCtx := &apptheory.Context{
		RequestID:    "r-email-begin-stale-local-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": fixture.agentIDHex},
		Request:      apptheory.Request{Body: []byte(`{"local_part":"agent-alice"}`)},
	}
	beginCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	_, err := fixture.server.handleSoulBeginProvisionEmailChannel(beginCtx)
	appErr := requireProvisionEmailAppErr(t, err)
	if appErr.Code != appErrCodeConflict || appErr.Message != soulEmailProvisionErrDerivedLocalPartReq {
		t.Fatalf("unexpected app error: %#v", appErr)
	}
	if len(fixture.migaduCalls) != 0 || len(fixture.forwardingCalls) != 0 || len(fixture.deleteMailboxCalls) != 0 {
		t.Fatalf("expected stale local_part to stop before provider calls: creates=%#v forwardings=%#v deletes=%#v", fixture.migaduCalls, fixture.forwardingCalls, fixture.deleteMailboxCalls)
	}
}

func TestHandleSoulProvisionEmail_RequiresInboundBridgeDomain(t *testing.T) {
	t.Parallel()

	fixture := newProvisionEmailE2EFixture(t)
	fixture.server.cfg.SoulEmailInboundDomain = ""

	beginOut := runProvisionEmailBegin(t, fixture)
	confirmBody := buildProvisionEmailConfirmBody(t, fixture, beginOut)

	confirmCtx := &apptheory.Context{
		RequestID:    "r-email-confirm-nobridge-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": fixture.agentIDHex},
		Request:      apptheory.Request{Body: confirmBody},
	}
	confirmCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	_, err := fixture.server.handleSoulProvisionEmailChannel(confirmCtx)
	appErr := requireProvisionEmailAppErr(t, err)
	if appErr.Code != appErrCodeConflict || appErr.Message != "email inbound bridge is not configured" {
		t.Fatalf("unexpected app error: %#v", appErr)
	}
	if len(fixture.migaduCalls) != 0 {
		t.Fatalf("expected mailbox provisioning to be skipped, got %d calls", len(fixture.migaduCalls))
	}
	if len(fixture.forwardingCalls) != 0 {
		t.Fatalf("expected forwarding provisioning to be skipped, got %d calls", len(fixture.forwardingCalls))
	}
}

func requireProvisionEmailAppErr(t *testing.T, err error) *apptheory.AppError {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	return appErr
}
