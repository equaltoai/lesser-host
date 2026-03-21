package controlplane

import (
	"context"
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

type provisionPhoneE2EFixture struct {
	tdb                  soulLifecycleTestDB
	packs                *fakeSoulPackStore
	searched             []string
	ordered              []string
	configured           []string
	server               *Server
	agentIDHex           string
	signingKey           *ecdsa.PrivateKey
	wallet               string
	principalDeclaration string
	principalSigHex      string
	s3Key                string
}

func TestHandleSoulProvisionPhone_BeginThenConfirm_PublishesV3WithChannelAndIsIdempotent(t *testing.T) {
	t.Parallel()

	fixture := newProvisionPhoneE2EFixture(t)
	beginOut := runProvisionPhoneBegin(t, fixture)
	confirmBody := buildProvisionPhoneConfirmBody(t, fixture, beginOut)
	assertProvisionPhoneConfirmCreated(t, fixture, confirmBody)
	assertProvisionPhonePublished(t, fixture)
	assertProvisionPhoneIdempotent(t, fixture, confirmBody)
}

func newProvisionPhoneE2EFixture(t *testing.T) *provisionPhoneE2EFixture {
	t.Helper()

	fixture := &provisionPhoneE2EFixture{
		tdb:        newSoulLifecycleTestDB(),
		packs:      &fakeSoulPackStore{},
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
			PublicBaseURL:               "https://lab.lesser.host",
			Stage:                       "lab",
		},
		soulPacks: fixture.packs,
		telnyxSearchNums: func(ctx context.Context, countryCode string, limit int) ([]string, error) {
			fixture.searched = append(fixture.searched, strings.ToUpper(strings.TrimSpace(countryCode)))
			return []string{"+15551234567"}, nil
		},
		telnyxOrderNumber: func(ctx context.Context, phoneNumber string) (string, error) {
			fixture.ordered = append(fixture.ordered, strings.TrimSpace(phoneNumber))
			return "order-1", nil
		},
		telnyxUpdateProfile: func(ctx context.Context, webhookURL string) error {
			fixture.configured = append(fixture.configured, strings.TrimSpace(webhookURL))
			return nil
		},
	}

	seedProvisionPhoneE2EAccess(t, fixture)
	initialBytes := mustMarshalJSON(t, testProvisionManagedChannelBaseRegistration(fixture.agentIDHex, fixture.wallet, fixture.principalDeclaration, fixture.principalSigHex))
	fixture.packs.objects = map[string]fakePut{
		fixture.s3Key: {key: fixture.s3Key, body: initialBytes},
	}
	return fixture
}

func seedProvisionPhoneE2EAccess(t *testing.T, fixture *provisionPhoneE2EFixture) {
	t.Helper()

	fixture.tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Times(3)
	fixture.tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
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
			Domain:                 "example.com",
			LocalID:                "agent-alice",
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

	fixture.tdb.qPhoneIdx.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	fixture.tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Times(3)
	fixture.tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:     fixture.agentIDHex,
			ChannelType: models.SoulChannelTypePhone,
			Identifier:  "+15551234567",
		}
	}).Once()
}

func runProvisionPhoneBegin(t *testing.T, fixture *provisionPhoneE2EFixture) soulProvisionPhoneBeginResponse {
	t.Helper()

	beginCtx := &apptheory.Context{
		RequestID:    "r-phone-begin-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": fixture.agentIDHex},
		Request:      apptheory.Request{Body: []byte(`{"country_code":"us"}`)},
	}
	beginCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	beginResp, err := fixture.server.handleSoulBeginProvisionPhoneChannel(beginCtx)
	if err != nil {
		t.Fatalf("begin unexpected err: %v", err)
	}
	if beginResp.Status != http.StatusOK {
		t.Fatalf("begin expected 200, got %d (body=%q)", beginResp.Status, string(beginResp.Body))
	}

	beginOut := mustUnmarshalJSON[soulProvisionPhoneBeginResponse](t, beginResp.Body)
	if beginOut.ExpectedVersion != 3 || beginOut.NextVersion != 4 {
		t.Fatalf("begin expected version chain 3->4, got %d->%d", beginOut.ExpectedVersion, beginOut.NextVersion)
	}
	if beginOut.Number != "+15551234567" {
		t.Fatalf("begin expected selected number, got %q", beginOut.Number)
	}
	if len(fixture.searched) != 1 || fixture.searched[0] != "US" {
		t.Fatalf("expected telnyx search to be called with US, got %#v", fixture.searched)
	}
	return beginOut
}

func buildProvisionPhoneConfirmBody(t *testing.T, fixture *provisionPhoneE2EFixture, beginOut soulProvisionPhoneBeginResponse) []byte {
	t.Helper()

	digestBytes, err := hex.DecodeString(strings.TrimPrefix(beginOut.DigestHex, "0x"))
	if err != nil || len(digestBytes) != 32 {
		t.Fatalf("begin invalid digest_hex: %q", beginOut.DigestHex)
	}
	selfSigBytes, _ := crypto.Sign(accounts.TextHash(digestBytes), fixture.signingKey)
	selfSigHex := "0x" + hex.EncodeToString(selfSigBytes)
	return mustMarshalJSON(t, map[string]any{
		"number":           beginOut.Number,
		"issued_at":        beginOut.IssuedAt,
		"expected_version": beginOut.ExpectedVersion,
		"self_attestation": selfSigHex,
	})
}

func assertProvisionPhoneConfirmCreated(t *testing.T, fixture *provisionPhoneE2EFixture, confirmBody []byte) {
	t.Helper()

	confirmCtx := &apptheory.Context{
		RequestID:    "r-phone-confirm-1",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": fixture.agentIDHex},
		Request:      apptheory.Request{Body: confirmBody},
	}
	confirmCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	confirmResp, err := fixture.server.handleSoulProvisionPhoneChannel(confirmCtx)
	if err != nil {
		t.Fatalf("confirm unexpected err: %v", err)
	}
	if confirmResp.Status != http.StatusCreated {
		t.Fatalf("confirm expected 201, got %d (body=%q)", confirmResp.Status, string(confirmResp.Body))
	}
	if len(fixture.ordered) != 1 || fixture.ordered[0] != "+15551234567" {
		t.Fatalf("expected telnyx order call, got %#v", fixture.ordered)
	}
	if len(fixture.configured) != 1 || fixture.configured[0] != "https://lab.lesser.host/webhooks/comm/sms/inbound" {
		t.Fatalf("expected telnyx webhook config call, got %#v", fixture.configured)
	}
}

func assertProvisionPhonePublished(t *testing.T, fixture *provisionPhoneE2EFixture) {
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
		t.Fatalf("expected channels object, got %#v", published["channels"])
	}
	phoneAny, ok := chAny["phone"].(map[string]any)
	if !ok || phoneAny == nil || strings.TrimSpace(extractStringField(phoneAny, "number")) != "+15551234567" {
		t.Fatalf("expected channels.phone.number to be published, got %#v", chAny["phone"])
	}
}

func assertProvisionPhoneIdempotent(t *testing.T, fixture *provisionPhoneE2EFixture, confirmBody []byte) {
	t.Helper()

	confirmCtx := &apptheory.Context{
		RequestID:    "r-phone-confirm-2",
		AuthIdentity: "admin",
		Params:       map[string]string{"agentId": fixture.agentIDHex},
		Request:      apptheory.Request{Body: confirmBody},
	}
	confirmCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	confirmResp, err := fixture.server.handleSoulProvisionPhoneChannel(confirmCtx)
	if err != nil {
		t.Fatalf("confirm2 unexpected err: %v", err)
	}
	if confirmResp.Status != http.StatusOK {
		t.Fatalf("confirm2 expected 200, got %d (body=%q)", confirmResp.Status, string(confirmResp.Body))
	}
	if len(fixture.ordered) != 1 {
		t.Fatalf("expected telnyx not called again; got %#v", fixture.ordered)
	}
	if len(fixture.configured) != 1 {
		t.Fatalf("expected webhook config not called again; got %#v", fixture.configured)
	}
}

func TestHandleSoulDeprovisionPhoneChannel_SuccessAndIdempotent(t *testing.T) {
	t.Parallel()

	agentIDHex := soulLifecycleTestAgentIDHex

	t.Run("active phone channel is released and cleared", func(t *testing.T) {
		tdb := newSoulLifecycleTestDB()
		released := []string{}
		s := &Server{
			store: store.New(tdb.db),
			cfg: config.Config{
				SoulEnabled:                 true,
				SoulChainID:                 1,
				SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			},
			telnyxRelease: func(ctx context.Context, phoneNumber string) error {
				released = append(released, phoneNumber)
				return nil
			},
		}

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
				AgentID:         agentIDHex,
				Domain:          "example.com",
				LocalID:         "agent-alice",
				Status:          models.SoulAgentStatusActive,
				LifecycleStatus: models.SoulAgentStatusActive,
			}
		}).Once()
		tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
			*dest = models.SoulAgentChannel{
				AgentID:     agentIDHex,
				ChannelType: models.SoulChannelTypePhone,
				Identifier:  "+15551234567",
				Status:      models.SoulChannelStatusActive,
			}
		}).Once()
		tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
			*dest = models.SoulAgentENSResolution{ENSName: "agent-alice.lessersoul.eth", Phone: "+15551234567"}
		}).Once()

		ctx := &apptheory.Context{
			RequestID:    "r-phone-deprovision-1",
			AuthIdentity: "admin",
			Params:       map[string]string{"agentId": agentIDHex},
		}
		ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

		resp, err := s.handleSoulDeprovisionPhoneChannel(ctx)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("unexpected deprovision response: resp=%#v err=%v", resp, err)
		}
		if len(released) != 1 || released[0] != "+15551234567" {
			t.Fatalf("expected release call, got %#v", released)
		}
	})

	t.Run("already deprovisioned channel is idempotent", func(t *testing.T) {
		tdb := newSoulLifecycleTestDB()
		s := &Server{
			store: store.New(tdb.db),
			cfg: config.Config{
				SoulEnabled:                 true,
				SoulChainID:                 1,
				SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			},
		}

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
				AgentID:         agentIDHex,
				Domain:          "example.com",
				LocalID:         "agent-alice",
				Status:          models.SoulAgentStatusActive,
				LifecycleStatus: models.SoulAgentStatusActive,
			}
		}).Once()
		tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
			*dest = models.SoulAgentChannel{
				AgentID:         agentIDHex,
				ChannelType:     models.SoulChannelTypePhone,
				Identifier:      "+15551234567",
				Status:          models.SoulChannelStatusDecommissioned,
				DeprovisionedAt: time.Now().UTC(),
			}
		}).Once()

		ctx := &apptheory.Context{
			RequestID:    "r-phone-deprovision-2",
			AuthIdentity: "admin",
			Params:       map[string]string{"agentId": agentIDHex},
		}
		ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

		resp, err := s.handleSoulDeprovisionPhoneChannel(ctx)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("unexpected idempotent deprovision response: resp=%#v err=%v", resp, err)
		}
	})
}
