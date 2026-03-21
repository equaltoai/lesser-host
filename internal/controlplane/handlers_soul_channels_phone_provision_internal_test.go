package controlplane

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
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

const provisionPhoneInternalError = "internal error"

func testProvisionPhoneIdentity() *models.SoulAgentIdentity {
	identity := testMintConversationIdentity()
	identity.Status = models.SoulAgentStatusActive
	identity.LifecycleStatus = models.SoulAgentStatusActive
	return identity
}

func testProvisionPhoneBaseReg(identity *models.SoulAgentIdentity) map[string]any {
	return map[string]any{
		"version": "2",
		"agentId": identity.AgentID,
		"domain":  "example.com",
		"localId": "agent-bot",
		"wallet":  identity.Wallet,
		"created": "2026-03-05T12:00:00Z",
		"updated": "2026-03-05T12:00:00Z",
		"principal": map[string]any{
			"type":        "individual",
			"identifier":  identity.PrincipalAddress,
			"declaration": identity.PrincipalDeclaration,
			"signature":   identity.PrincipalSignature,
			"declaredAt":  identity.PrincipalDeclaredAt,
		},
		"selfDescription": map[string]any{
			"purpose":    "Help users plan travel with explicit limitations.",
			"authoredBy": "agent",
		},
		"capabilities": []any{
			map[string]any{
				"capability": "travel_planning",
				"scope":      "Draft itineraries.",
				"claimLevel": "self-declared",
			},
		},
		"boundaries": []any{
			map[string]any{
				"id":             "b1",
				"category":       "refusal",
				"statement":      "I will not impersonate humans.",
				"addedAt":        "2026-03-05T12:00:00Z",
				"addedInVersion": "1",
				"signature":      "0x00",
			},
		},
		"transparency": map[string]any{},
		"endpoints":    map[string]any{},
		"lifecycle": map[string]any{
			"status":          "active",
			"statusChangedAt": "2026-03-05T12:00:00Z",
		},
		"attestations": map[string]any{
			"selfAttestation": "0x00",
		},
	}
}

func newProvisionPhoneServer(tdb soulLifecycleTestDB, packs soulPackStore) *Server {
	if packs == nil {
		packs = &fakeSoulPackStore{}
	}
	return &Server{
		store:     store.New(tdb.db),
		soulPacks: packs,
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulPackBucketName:          "bucket",
			SoulSupportedCapabilities:   []string{"social"},
			PublicBaseURL:               "https://lab.lesser.host",
			Stage:                       "lab",
		},
	}
}

func newProvisionPhoneSigningIdentity(t *testing.T, version int) (*ecdsa.PrivateKey, *models.SoulAgentIdentity) {
	t.Helper()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wallet := strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())

	principalDeclaration := boundaryTestPrincipalDeclaration
	principalDigest := crypto.Keccak256([]byte(principalDeclaration))
	principalSig, err := crypto.Sign(accounts.TextHash(principalDigest), key)
	if err != nil {
		t.Fatalf("sign principal declaration: %v", err)
	}

	return key, &models.SoulAgentIdentity{
		AgentID:                soulLifecycleTestAgentIDHex,
		Domain:                 "example.com",
		LocalID:                "agent-bot",
		Wallet:                 wallet,
		Status:                 models.SoulAgentStatusActive,
		LifecycleStatus:        models.SoulAgentStatusActive,
		SelfDescriptionVersion: version,
		PrincipalAddress:       wallet,
		PrincipalSignature:     "0x" + hex.EncodeToString(principalSig),
		PrincipalDeclaration:   principalDeclaration,
		PrincipalDeclaredAt:    "2026-03-05T12:00:00Z",
	}
}

func seedProvisionPhoneAccess(t *testing.T, tdb soulLifecycleTestDB, identities ...*models.SoulAgentIdentity) {
	t.Helper()
	if len(identities) == 0 {
		t.Fatal("identities are required")
	}

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{
			Domain:       "example.com",
			InstanceSlug: "inst1",
			Status:       models.DomainStatusVerified,
		}
	}).Times(len(identities))

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Times(len(identities))

	callIdx := 0
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = *identities[callIdx]
		callIdx++
	}).Times(len(identities))
}

func seedProvisionPhoneRegistration(t *testing.T, packs *fakeSoulPackStore, identity *models.SoulAgentIdentity) {
	t.Helper()
	body, err := json.Marshal(testProvisionPhoneBaseReg(identity))
	if err != nil {
		t.Fatalf("marshal base registration: %v", err)
	}
	if packs.objects == nil {
		packs.objects = map[string]fakePut{}
	}
	key := soulRegistrationS3Key(identity.AgentID)
	packs.objects[key] = fakePut{key: key, body: body}
}

func newProvisionPhoneCtx(body string) *apptheory.Context {
	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
	ctx.Request = apptheory.Request{Body: []byte(body)}
	return ctx
}

func signProvisionPhoneRequest(
	t *testing.T,
	s *Server,
	key *ecdsa.PrivateKey,
	identity *models.SoulAgentIdentity,
	number string,
	issuedAt time.Time,
) string {
	t.Helper()

	_, _, digest, appErr := s.buildSoulProvisionPhoneRegistration(
		context.Background(),
		testProvisionPhoneBaseReg(identity),
		"2",
		identity.AgentID,
		identity,
		soulProvisionPhoneBuildInput{
			PhoneNumber:        number,
			ENSName:            strings.TrimSpace(identity.LocalID) + ".lessersoul.eth",
			IssuedAt:           issuedAt,
			ExpectedPrev:       identity.SelfDescriptionVersion,
			NextVersion:        identity.SelfDescriptionVersion + 1,
			SelfAttestationHex: "",
		},
	)
	if appErr != nil {
		t.Fatalf("build registration digest: %v", appErr)
	}

	sig, err := crypto.Sign(accounts.TextHash(digest), key)
	if err != nil {
		t.Fatalf("sign registration digest: %v", err)
	}
	return "0x" + hex.EncodeToString(sig)
}

func requireProvisionPhoneAppErr(t *testing.T, err error) *apptheory.AppError {
	t.Helper()
	if err == nil {
		t.Fatal("expected app error")
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected *apptheory.AppError, got %T", err)
	}
	return appErr
}

func TestBuildSoulProvisionPhoneRegistration_ValidationErrors(t *testing.T) {
	s := &Server{cfg: config.Config{SoulPackBucketName: "bucket"}}
	identity := testProvisionPhoneIdentity()
	base := testProvisionPhoneBaseReg(identity)

	if _, _, _, appErr := (*Server)(nil).buildSoulProvisionPhoneRegistration(t.Context(), base, "2", identity.AgentID, identity, soulProvisionPhoneBuildInput{}); appErr == nil || appErr.Message != provisionPhoneInternalError {
		t.Fatalf("expected nil server error, got %#v", appErr)
	}
	if _, _, _, appErr := s.buildSoulProvisionPhoneRegistration(t.Context(), nil, "2", identity.AgentID, identity, soulProvisionPhoneBuildInput{}); appErr == nil || appErr.Message != provisionPhoneInternalError {
		t.Fatalf("expected nil base error, got %#v", appErr)
	}
	if _, _, _, appErr := s.buildSoulProvisionPhoneRegistration(t.Context(), base, "1", identity.AgentID, identity, soulProvisionPhoneBuildInput{}); appErr == nil || appErr.Message != "registration version is unsupported; update registration first" {
		t.Fatalf("expected unsupported version error, got %#v", appErr)
	}
	if _, _, _, appErr := s.buildSoulProvisionPhoneRegistration(t.Context(), base, "2", identity.AgentID, identity, soulProvisionPhoneBuildInput{ExpectedPrev: 1, NextVersion: 1}); appErr == nil || appErr.Message != "invalid expected_version" {
		t.Fatalf("expected invalid expected version error, got %#v", appErr)
	}
}

func TestBuildSoulProvisionPhoneRegistration_InvalidDocuments(t *testing.T) {
	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	s := &Server{cfg: config.Config{SoulPackBucketName: "bucket"}}
	identity := testProvisionPhoneIdentity()
	badJSON := testProvisionPhoneBaseReg(identity)
	badJSON["transparency"] = func() {}
	if _, _, _, appErr := s.buildSoulProvisionPhoneRegistration(t.Context(), badJSON, "2", identity.AgentID, identity, soulProvisionPhoneBuildInput{
		PhoneNumber:  "+15551234567",
		ENSName:      "agent-bot.eth",
		IssuedAt:     now,
		ExpectedPrev: 1,
		NextVersion:  2,
	}); appErr == nil || appErr.Message != "invalid registration JSON" {
		t.Fatalf("expected invalid json error, got %#v", appErr)
	}

	badSchema := testProvisionPhoneBaseReg(identity)
	if _, _, _, appErr := s.buildSoulProvisionPhoneRegistration(t.Context(), badSchema, "2", identity.AgentID, identity, soulProvisionPhoneBuildInput{
		PhoneNumber:  "bad-number",
		ENSName:      "agent-bot.eth",
		IssuedAt:     now,
		ExpectedPrev: 1,
		NextVersion:  2,
	}); appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected schema error, got %#v", appErr)
	}
}

func TestBuildSoulProvisionPhoneRegistration_SuccessCases(t *testing.T) {
	now := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	s := &Server{cfg: config.Config{SoulPackBucketName: "bucket"}}
	identity := testProvisionPhoneIdentity()
	base := testProvisionPhoneBaseReg(identity)

	reg, parsed, digest, appErr := s.buildSoulProvisionPhoneRegistration(t.Context(), base, "2", identity.AgentID, identity, soulProvisionPhoneBuildInput{
		PhoneNumber:  "+15551234567",
		ENSName:      "agent-bot.eth",
		IssuedAt:     now,
		ExpectedPrev: 1,
		NextVersion:  2,
	})
	if appErr != nil {
		t.Fatalf("unexpected error: %v", appErr)
	}
	if parsed.Version != "3" || len(digest) == 0 {
		t.Fatalf("unexpected parsed/digest output: %#v %x", parsed, digest)
	}
	assertProvisionPhoneRegistrationVersioned(t, reg, identity)

	fresh := testProvisionPhoneBaseReg(identity)
	fresh["previousVersionUri"] = "s3://bucket/old"
	reg, parsed, digest, appErr = s.buildSoulProvisionPhoneRegistration(t.Context(), fresh, "3", identity.AgentID, identity, soulProvisionPhoneBuildInput{
		PhoneNumber:        "+15551234567",
		IssuedAt:           now,
		ExpectedPrev:       0,
		NextVersion:        1,
		SelfAttestationHex: "0x1234",
	})
	if appErr != nil || parsed == nil || len(digest) == 0 {
		t.Fatalf("unexpected first-version result: %#v %#v %x", reg, appErr, digest)
	}
	assertProvisionPhoneRegistrationFirstVersion(t, reg)
}

func TestHandleSoulBeginProvisionPhoneChannel_ProviderMissingWhenNoNumberSupplied(t *testing.T) {
	tdb := newSoulLifecycleTestDB()
	_, identity := newProvisionPhoneSigningIdentity(t, 3)
	seedProvisionPhoneAccess(t, tdb, identity)

	s := newProvisionPhoneServer(tdb, &fakeSoulPackStore{})
	_, err := s.handleSoulBeginProvisionPhoneChannel(newProvisionPhoneCtx(`{}`))
	appErr := requireProvisionPhoneAppErr(t, err)
	if appErr.Code != appErrCodeConflict || appErr.Message != "phone provider is not configured" {
		t.Fatalf("unexpected app error: %#v", appErr)
	}
}

func TestHandleSoulBeginProvisionPhoneChannel_SearchFailureReturnsInternal(t *testing.T) {
	tdb := newSoulLifecycleTestDB()
	_, identity := newProvisionPhoneSigningIdentity(t, 3)
	seedProvisionPhoneAccess(t, tdb, identity)

	s := newProvisionPhoneServer(tdb, &fakeSoulPackStore{})
	s.telnyxSearchNums = func(ctx context.Context, countryCode string, limit int) ([]string, error) {
		return nil, errors.New("boom")
	}

	_, err := s.handleSoulBeginProvisionPhoneChannel(newProvisionPhoneCtx(`{"country_code":"US"}`))
	appErr := requireProvisionPhoneAppErr(t, err)
	if appErr.Code != appErrCodeInternal || appErr.Message != "failed to find available phone numbers" {
		t.Fatalf("unexpected app error: %#v", appErr)
	}
}

func TestHandleSoulBeginProvisionPhoneChannel_SearchSuccessBuildsProvisionalRegistration(t *testing.T) {
	tdb := newSoulLifecycleTestDB()
	_, identity := newProvisionPhoneSigningIdentity(t, 3)
	seedProvisionPhoneAccess(t, tdb, identity)

	packs := &fakeSoulPackStore{}
	seedProvisionPhoneRegistration(t, packs, identity)

	var gotCountry string
	var gotLimit int
	s := newProvisionPhoneServer(tdb, packs)
	s.telnyxSearchNums = func(ctx context.Context, countryCode string, limit int) ([]string, error) {
		gotCountry = countryCode
		gotLimit = limit
		return []string{"+15551234567", "+15551234568"}, nil
	}

	resp, err := s.handleSoulBeginProvisionPhoneChannel(newProvisionPhoneCtx(`{"country_code":" US "}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}
	if gotCountry != "US" || gotLimit != 5 {
		t.Fatalf("unexpected search args: country=%q limit=%d", gotCountry, gotLimit)
	}

	out := mustUnmarshalJSON[soulProvisionPhoneBeginResponse](t, resp.Body)
	if out.Number != "+15551234567" {
		t.Fatalf("expected first available number, got %q", out.Number)
	}
	if out.ExpectedVersion != 3 || out.NextVersion != 4 {
		t.Fatalf("unexpected version chain: %d -> %d", out.ExpectedVersion, out.NextVersion)
	}
	if !strings.HasPrefix(out.DigestHex, "0x") || len(out.DigestHex) <= 2 {
		t.Fatalf("expected digest hex, got %q", out.DigestHex)
	}
	channels, ok := out.Registration["channels"].(map[string]any)
	if !ok || channels["phone"] == nil {
		t.Fatalf("expected phone channel in registration: %#v", out.Registration)
	}
}

func TestHandleSoulProvisionPhoneChannel_ExpectedVersionIsRequired(t *testing.T) {
	tdb := newSoulLifecycleTestDB()
	_, identity := newProvisionPhoneSigningIdentity(t, 3)
	seedProvisionPhoneAccess(t, tdb, identity)

	s := newProvisionPhoneServer(tdb, &fakeSoulPackStore{})
	_, err := s.handleSoulProvisionPhoneChannel(newProvisionPhoneCtx(`{"number":"+15551234567","issued_at":"2026-03-05T12:00:00Z","self_attestation":"0x1"}`))
	appErr := requireProvisionPhoneAppErr(t, err)
	if appErr.Code != appErrCodeBadRequest || appErr.Message != "expected_version is required" {
		t.Fatalf("unexpected app error: %#v", appErr)
	}
}

func TestHandleSoulProvisionPhoneChannel_StaleRequestIsIdempotentWhenPhoneExists(t *testing.T) {
	tdb := newSoulLifecycleTestDB()
	_, identity := newProvisionPhoneSigningIdentity(t, 4)
	seedProvisionPhoneAccess(t, tdb, identity)

	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:     identity.AgentID,
			ChannelType: models.SoulChannelTypePhone,
			Identifier:  "+15551234567",
		}
	}).Once()

	s := newProvisionPhoneServer(tdb, &fakeSoulPackStore{})
	resp, err := s.handleSoulProvisionPhoneChannel(newProvisionPhoneCtx(`{"issued_at":"2026-03-05T12:00:00Z","expected_version":3,"self_attestation":"0x1"}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	out := mustUnmarshalJSON[soulProvisionPhoneConfirmResponse](t, resp.Body)
	if out.Number != "+15551234567" || out.RegistrationVersion != 4 {
		t.Fatalf("unexpected idempotent response: %#v", out)
	}
}

func TestHandleSoulProvisionPhoneChannel_ConflictWhenPhoneAlreadyProvisioned(t *testing.T) {
	tdb := newSoulLifecycleTestDB()
	_, identity := newProvisionPhoneSigningIdentity(t, 3)
	seedProvisionPhoneAccess(t, tdb, identity)

	tdb.qPhoneIdx.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulPhoneAgentIndex](t, args, 0)
		*dest = models.SoulPhoneAgentIndex{Phone: "+15551234567", AgentID: "0xother"}
	}).Once()

	s := newProvisionPhoneServer(tdb, &fakeSoulPackStore{})
	_, err := s.handleSoulProvisionPhoneChannel(newProvisionPhoneCtx(`{"number":"+15551234567","issued_at":"2026-03-05T12:00:00Z","expected_version":3,"self_attestation":"0x1"}`))
	appErr := requireProvisionPhoneAppErr(t, err)
	if appErr.Code != appErrCodeConflict || appErr.Message != "phone number is already provisioned" {
		t.Fatalf("unexpected app error: %#v", appErr)
	}
}

func TestHandleSoulProvisionPhoneChannel_InvalidSignatureIsRejected(t *testing.T) {
	tdb := newSoulLifecycleTestDB()
	_, identity := newProvisionPhoneSigningIdentity(t, 3)
	seedProvisionPhoneAccess(t, tdb, identity)

	packs := &fakeSoulPackStore{}
	seedProvisionPhoneRegistration(t, packs, identity)

	tdb.qPhoneIdx.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()

	s := newProvisionPhoneServer(tdb, packs)
	_, err := s.handleSoulProvisionPhoneChannel(newProvisionPhoneCtx(`{"number":"+15551234567","issued_at":"2026-03-05T12:00:00Z","expected_version":3,"self_attestation":"0x1234"}`))
	appErr := requireProvisionPhoneAppErr(t, err)
	if appErr.Code != appErrCodeBadRequest || appErr.Message != "invalid registration signature" {
		t.Fatalf("unexpected app error: %#v", appErr)
	}
}

func TestHandleSoulProvisionPhoneChannel_SuccessPublishesRegistrationAndRecordsPhoneChannel(t *testing.T) {
	tdb := newSoulLifecycleTestDB()
	key, identity := newProvisionPhoneSigningIdentity(t, 3)
	seedProvisionPhoneAccess(t, tdb, identity)

	packs := &fakeSoulPackStore{}
	seedProvisionPhoneRegistration(t, packs, identity)

	tdb.qPhoneIdx.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(theoryErrors.ErrItemNotFound).Maybe()
	tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(theoryErrors.ErrItemNotFound).Maybe()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Times(3)
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	var ordered []string
	var configured []string
	s := newProvisionPhoneServer(tdb, packs)
	s.telnyxOrderNumber = func(ctx context.Context, phoneNumber string) (string, error) {
		ordered = append(ordered, phoneNumber)
		return "ord-1", nil
	}
	s.telnyxUpdateProfile = func(ctx context.Context, webhookURL string) error {
		configured = append(configured, webhookURL)
		return nil
	}

	issuedAt := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	selfSig := signProvisionPhoneRequest(t, s, key, identity, "+15551234567", issuedAt)
	body := mustMarshalJSON(t, map[string]any{
		"number":           "+15551234567",
		"issued_at":        issuedAt.Format(time.RFC3339Nano),
		"expected_version": 3,
		"self_attestation": selfSig,
	})

	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": identity.AgentID}
	ctx.Request = apptheory.Request{Body: body}

	resp, err := s.handleSoulProvisionPhoneChannel(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body=%q)", resp.Status, string(resp.Body))
	}
	if len(ordered) != 1 || ordered[0] != "+15551234567" {
		t.Fatalf("unexpected telnyx orders: %#v", ordered)
	}
	if len(configured) != 1 || configured[0] != "https://lab.lesser.host/webhooks/comm/sms/inbound" {
		t.Fatalf("unexpected telnyx webhook config: %#v", configured)
	}

	out := mustUnmarshalJSON[soulProvisionPhoneConfirmResponse](t, resp.Body)
	if out.Number != "+15551234567" || out.RegistrationVersion != 4 {
		t.Fatalf("unexpected response: %#v", out)
	}

	obj, ok := packs.objects[soulRegistrationS3Key(identity.AgentID)]
	if !ok || len(obj.body) == 0 {
		t.Fatalf("expected published registration at %q", soulRegistrationS3Key(identity.AgentID))
	}
	published := mustUnmarshalJSON[map[string]any](t, obj.body)
	if got := strings.TrimSpace(extractStringField(published, "version")); got != "3" {
		t.Fatalf("expected published version 3, got %q", got)
	}
	channels, ok := published["channels"].(map[string]any)
	if !ok || channels["phone"] == nil {
		t.Fatalf("expected published phone channel: %#v", published)
	}
	phone, ok := channels["phone"].(map[string]any)
	if !ok || strings.TrimSpace(extractStringField(phone, "number")) != "+15551234567" {
		t.Fatalf("expected published phone number, got %#v", channels["phone"])
	}
}

func TestHandleSoulProvisionPhoneChannel_WebhookConfigFailureStopsPublish(t *testing.T) {
	tdb := newSoulLifecycleTestDB()
	key, identity := newProvisionPhoneSigningIdentity(t, 3)
	seedProvisionPhoneAccess(t, tdb, identity)

	packs := &fakeSoulPackStore{}
	seedProvisionPhoneRegistration(t, packs, identity)

	tdb.qPhoneIdx.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()

	var ordered []string
	var configured []string
	s := newProvisionPhoneServer(tdb, packs)
	s.telnyxOrderNumber = func(ctx context.Context, phoneNumber string) (string, error) {
		ordered = append(ordered, phoneNumber)
		return "ord-1", nil
	}
	s.telnyxUpdateProfile = func(ctx context.Context, webhookURL string) error {
		configured = append(configured, webhookURL)
		return errors.New("boom")
	}

	issuedAt := time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)
	selfSig := signProvisionPhoneRequest(t, s, key, identity, "+15551234567", issuedAt)
	body := mustMarshalJSON(t, map[string]any{
		"number":           "+15551234567",
		"issued_at":        issuedAt.Format(time.RFC3339Nano),
		"expected_version": 3,
		"self_attestation": selfSig,
	})

	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": identity.AgentID}
	ctx.Request = apptheory.Request{Body: body}

	_, err := s.handleSoulProvisionPhoneChannel(ctx)
	appErr := requireProvisionPhoneAppErr(t, err)
	if appErr.Code != appErrCodeInternal || appErr.Message != "failed to provision phone number" {
		t.Fatalf("unexpected app error: %#v", appErr)
	}
	if len(ordered) != 1 || ordered[0] != "+15551234567" {
		t.Fatalf("unexpected telnyx orders: %#v", ordered)
	}
	if len(configured) != 1 || configured[0] != "https://lab.lesser.host/webhooks/comm/sms/inbound" {
		t.Fatalf("unexpected telnyx webhook config: %#v", configured)
	}
	published := mustUnmarshalJSON[map[string]any](t, packs.objects[soulRegistrationS3Key(identity.AgentID)].body)
	if got := strings.TrimSpace(extractStringField(published, "version")); got != "2" {
		t.Fatalf("expected registration version to remain 2 after webhook config failure, got %q", got)
	}
}

func TestHandleSoulDeprovisionPhoneChannel_MissingPhoneChannelIsIdempotent(t *testing.T) {
	tdb := newSoulLifecycleTestDB()
	_, identity := newProvisionPhoneSigningIdentity(t, 3)
	seedProvisionPhoneAccess(t, tdb, identity)
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()

	s := newProvisionPhoneServer(tdb, nil)
	resp, err := s.handleSoulDeprovisionPhoneChannel(newProvisionPhoneCtx(``))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}
}

func TestHandleSoulDeprovisionPhoneChannel_ChannelLookupErrorReturnsInternal(t *testing.T) {
	tdb := newSoulLifecycleTestDB()
	_, identity := newProvisionPhoneSigningIdentity(t, 3)
	seedProvisionPhoneAccess(t, tdb, identity)
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(errors.New("boom")).Once()

	s := newProvisionPhoneServer(tdb, nil)
	_, err := s.handleSoulDeprovisionPhoneChannel(newProvisionPhoneCtx(``))
	appErr := requireProvisionPhoneAppErr(t, err)
	if appErr.Code != appErrCodeInternal || appErr.Message != "failed to load phone channel" {
		t.Fatalf("unexpected app error: %#v", appErr)
	}
}

func TestHandleSoulDeprovisionPhoneChannel_AlreadyDecommissionedIsIdempotent(t *testing.T) {
	tdb := newSoulLifecycleTestDB()
	_, identity := newProvisionPhoneSigningIdentity(t, 3)
	seedProvisionPhoneAccess(t, tdb, identity)
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:         identity.AgentID,
			ChannelType:     models.SoulChannelTypePhone,
			Identifier:      "+15551234567",
			Status:          models.SoulChannelStatusDecommissioned,
			DeprovisionedAt: time.Date(2026, 3, 5, 13, 0, 0, 0, time.UTC),
		}
	}).Once()

	s := newProvisionPhoneServer(tdb, nil)
	resp, err := s.handleSoulDeprovisionPhoneChannel(newProvisionPhoneCtx(``))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}
}

func TestHandleSoulDeprovisionPhoneChannel_UpdateFailureReturnsInternal(t *testing.T) {
	tdb := newSoulLifecycleTestDB()
	_, identity := newProvisionPhoneSigningIdentity(t, 3)
	seedProvisionPhoneAccess(t, tdb, identity)
	tdb.qChannel.ExpectedCalls = nil
	tdb.qChannel.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(tdb.qChannel).Maybe()
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:     identity.AgentID,
			ChannelType: models.SoulChannelTypePhone,
			Identifier:  "+15551234567",
			Status:      models.SoulChannelStatusActive,
		}
	}).Once()
	tdb.qChannel.On("CreateOrUpdate").Return(errors.New("boom")).Once()

	s := newProvisionPhoneServer(tdb, nil)
	_, err := s.handleSoulDeprovisionPhoneChannel(newProvisionPhoneCtx(``))
	appErr := requireProvisionPhoneAppErr(t, err)
	if appErr.Code != appErrCodeInternal || appErr.Message != "failed to update phone channel" {
		t.Fatalf("unexpected app error: %#v", appErr)
	}
}

func TestHandleSoulDeprovisionPhoneChannel_SuccessReleasesProviderNumberAndClearsENS(t *testing.T) {
	tdb := newSoulLifecycleTestDB()
	_, identity := newProvisionPhoneSigningIdentity(t, 3)
	seedProvisionPhoneAccess(t, tdb, identity)
	tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:     identity.AgentID,
			ChannelType: models.SoulChannelTypePhone,
			Identifier:  "+15551234567",
			Status:      models.SoulChannelStatusActive,
		}
	}).Once()
	tdb.qENS.On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentENSResolution](t, args, 0)
		*dest = models.SoulAgentENSResolution{
			ENSName: "agent-bot.lessersoul.eth",
			AgentID: identity.AgentID,
			Phone:   "+15551234567",
		}
	}).Once()

	var released []string
	s := newProvisionPhoneServer(tdb, nil)
	s.telnyxRelease = func(ctx context.Context, phoneNumber string) error {
		released = append(released, phoneNumber)
		return errors.New("ignored")
	}

	resp, err := s.handleSoulDeprovisionPhoneChannel(newProvisionPhoneCtx(``))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}
	if len(released) != 1 || released[0] != "+15551234567" {
		t.Fatalf("unexpected released numbers: %#v", released)
	}
}

func assertProvisionPhoneRegistrationVersioned(t *testing.T, reg map[string]any, identity *models.SoulAgentIdentity) {
	t.Helper()
	if reg["previousVersionUri"] != "s3://bucket/"+soulRegistrationVersionedS3Key(identity.AgentID, 1) {
		t.Fatalf("unexpected previousVersionUri: %#v", reg["previousVersionUri"])
	}
	channels, ok := reg["channels"].(map[string]any)
	if !ok || channels["phone"] == nil || channels["ens"] == nil {
		t.Fatalf("expected channels to include phone and ens: %#v", channels)
	}
}

func assertProvisionPhoneRegistrationFirstVersion(t *testing.T, reg map[string]any) {
	t.Helper()
	if _, hasPreviousVersionURI := reg["previousVersionUri"]; hasPreviousVersionURI {
		t.Fatalf("expected previousVersionUri to be removed on first version")
	}
	attestations, ok := reg["attestations"].(map[string]any)
	if !ok {
		t.Fatalf("expected attestations map, got %#v", reg["attestations"])
	}
	if attestations["selfAttestation"] != "0x1234" {
		t.Fatalf("expected self attestation override, got %#v", attestations)
	}
}
