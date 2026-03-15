package controlplane

import (
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
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func requireBoundaryAppErrorCode(t *testing.T, err error, want string) {
	t.Helper()

	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected *apptheory.AppError, got %T: %v", err, err)
	}
	if appErr.Code != want {
		t.Fatalf("expected app error %q, got %q", want, appErr.Code)
	}
}

func newBoundaryCoverageServer(tdb soulLifecycleTestDB, packs soulPackStore) *Server {
	return &Server{
		store:     store.New(tdb.db),
		soulPacks: packs,
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulPackBucketName:          "bucket",
		},
	}
}

func seedBoundaryPortalAccess(t *testing.T, tdb soulLifecycleTestDB, agentIDHex string, wallet string, version int) {
	t.Helper()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Maybe()

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Maybe()

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:                agentIDHex,
			Domain:                 "example.com",
			LocalID:                "agent-alice",
			Wallet:                 strings.ToLower(strings.TrimSpace(wallet)),
			Status:                 models.SoulAgentStatusActive,
			LifecycleStatus:        models.SoulAgentStatusActive,
			SelfDescriptionVersion: version,
			UpdatedAt:              time.Now().UTC(),
		}
	}).Maybe()
}

func mustGenerateBoundaryWallet(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key, strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex())
}

func mustSignBoundaryStatement(t *testing.T, key *ecdsa.PrivateKey, statement string) string {
	t.Helper()

	digest := crypto.Keccak256([]byte(statement))
	sig, err := crypto.Sign(accounts.TextHash(digest), key)
	if err != nil {
		t.Fatalf("sign boundary statement: %v", err)
	}
	return "0x" + hex.EncodeToString(sig)
}

func mustSignBoundaryDigest(t *testing.T, key *ecdsa.PrivateKey, digest []byte) string {
	t.Helper()

	sig, err := crypto.Sign(accounts.TextHash(digest), key)
	if err != nil {
		t.Fatalf("sign boundary digest: %v", err)
	}
	return "0x" + hex.EncodeToString(sig)
}

func seedBoundaryRegistrationObject(t *testing.T, packs *fakeSoulPackStore, agentIDHex string, reg map[string]any) {
	t.Helper()

	body, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("marshal registration: %v", err)
	}
	if packs.objects == nil {
		packs.objects = map[string]fakePut{}
	}
	key := soulRegistrationS3Key(agentIDHex)
	packs.objects[key] = fakePut{key: key, body: body}
}

func mustBuildBoundarySelfAttestation(t *testing.T, s *Server, key *ecdsa.PrivateKey, base map[string]any, baseVersion string, identity *models.SoulAgentIdentity, input soulBoundaryAppendBuildInput) string {
	t.Helper()

	_, _, _, digest, _, _, appErr := s.buildSoulBoundaryAppendRegistration(t.Context(), base, baseVersion, identity.AgentID, identity, input)
	if appErr != nil {
		t.Fatalf("build boundary registration digest: %#v", appErr)
	}
	return mustSignBoundaryDigest(t, key, digest)
}

func validBoundaryBaseRegistrationV2(t *testing.T, key *ecdsa.PrivateKey, agentIDHex string, wallet string) map[string]any {
	t.Helper()

	declaration := boundaryTestPrincipalDeclaration
	declarationDigest := crypto.Keccak256([]byte(declaration))
	principalSig, err := crypto.Sign(accounts.TextHash(declarationDigest), key)
	if err != nil {
		t.Fatalf("sign principal declaration: %v", err)
	}

	return map[string]any{
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
			"declaration": declaration,
			"signature":   "0x" + hex.EncodeToString(principalSig),
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
				"id":             "boundary-existing",
				"category":       "refusal",
				"statement":      "I will not impersonate humans.",
				"addedAt":        "2026-03-01T00:00:00Z",
				"addedInVersion": "1",
				"signature":      "0x00",
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
		"attestations": map[string]any{
			"selfAttestation": "0xdeadbeef",
		},
		"created": "2026-03-01T00:00:00Z",
		"updated": "2026-03-01T00:00:00Z",
	}
}

func validBoundaryBaseRegistrationV3(t *testing.T, key *ecdsa.PrivateKey, agentIDHex string, wallet string) map[string]any {
	t.Helper()

	reg := validBoundaryBaseRegistrationV2(t, key, agentIDHex, wallet)
	reg["version"] = "3"
	reg["channels"] = map[string]any{
		"ens": map[string]any{
			"name":            "agent-alice.lessersoul.eth",
			"resolverAddress": "0x0000000000000000000000000000000000000002",
			"chain":           "mainnet",
		},
	}
	reg["contactPreferences"] = map[string]any{
		"preferred": "email",
		"availability": map[string]any{
			"schedule": "always",
		},
		"responseExpectation": map[string]any{
			"target":    "PT4H",
			"guarantee": "best-effort",
		},
		"languages": []any{"en"},
	}
	return reg
}

func TestSoulBoundaryValidationHelpers_MoreCoverage(t *testing.T) {
	t.Parallel()

	assertValidBoundaryCategories(t)
	t.Run("parse_and_validate_inputs", func(t *testing.T) { t.Parallel(); testParseAndValidateSoulBoundaryInputs(t) })
	t.Run("compute_self_attestation_digest", func(t *testing.T) { t.Parallel(); testComputeSoulBoundarySelfAttestationDigest(t) })
}

func assertValidBoundaryCategories(t *testing.T) {
	t.Helper()
	valid := []string{
		models.SoulBoundaryCategoryRefusal,
		models.SoulBoundaryCategoryScopeLimit,
		models.SoulBoundaryCategoryEthicalCommitment,
		models.SoulBoundaryCategoryCircuitBreaker,
	}
	for _, category := range valid {
		if !isValidBoundaryCategory(category) {
			t.Fatalf("expected category %q to be valid", category)
		}
	}
	if isValidBoundaryCategory("nope") {
		t.Fatalf("expected invalid category to fail")
	}
}

func testParseAndValidateSoulBoundaryInputs(t *testing.T) {
	t.Helper()
	type tc struct {
		name       string
		boundaryID string
		category   string
		statement  string
		rationale  string
		supersedes string
		signature  string
		wantCode   string
	}

	cases := []tc{
		{name: "boundary_required", wantCode: appErrCodeBadRequest, category: boundaryTestCategoryRefusal, statement: "x", signature: "0x1"},
		{name: "boundary_too_long", wantCode: appErrCodeBadRequest, boundaryID: strings.Repeat("a", 129), category: boundaryTestCategoryRefusal, statement: "x", signature: "0x1"},
		{name: "invalid_category", wantCode: appErrCodeBadRequest, boundaryID: "b1", category: "invalid", statement: "x", signature: "0x1"},
		{name: "statement_required", wantCode: appErrCodeBadRequest, boundaryID: "b1", category: boundaryTestCategoryRefusal, signature: "0x1"},
		{name: "statement_too_long", wantCode: appErrCodeBadRequest, boundaryID: "b1", category: boundaryTestCategoryRefusal, statement: strings.Repeat("x", 4097), signature: "0x1"},
		{name: "rationale_too_long", wantCode: appErrCodeBadRequest, boundaryID: "b1", category: boundaryTestCategoryRefusal, statement: "x", rationale: strings.Repeat("r", 8193), signature: "0x1"},
		{name: "supersedes_too_long", wantCode: appErrCodeBadRequest, boundaryID: "b1", category: boundaryTestCategoryRefusal, statement: "x", supersedes: strings.Repeat("s", 129), signature: "0x1"},
		{name: "signature_required", wantCode: appErrCodeBadRequest, boundaryID: "b1", category: boundaryTestCategoryRefusal, statement: "x"},
	}

	for _, tc := range cases {
		_, _, _, _, _, _, appErr := parseAndValidateSoulBoundaryAppendInput(tc.boundaryID, tc.category, tc.statement, tc.rationale, tc.supersedes, tc.signature)
		if appErr == nil || appErr.Code != tc.wantCode {
			t.Fatalf("%s: expected %s, got %#v", tc.name, tc.wantCode, appErr)
		}
	}

	boundaryID, category, statement, rationale, supersedes, signature, appErr := parseAndValidateSoulBoundaryAppendInput(
		" "+boundaryTestID1+" ",
		" Refusal ",
		" "+boundaryTestStatementNoDo+" ",
		" because ",
		" old-boundary ",
		" 0xabc ",
	)
	if appErr != nil {
		t.Fatalf("unexpected appErr: %#v", appErr)
	}
	if boundaryID != boundaryTestID1 || category != boundaryTestCategoryRefusal || statement != boundaryTestStatementNoDo || rationale != "because" || supersedes != "old-boundary" || signature != "0xabc" {
		t.Fatalf("unexpected normalized values: %q %q %q %q %q %q", boundaryID, category, statement, rationale, supersedes, signature)
	}
}

func testComputeSoulBoundarySelfAttestationDigest(t *testing.T) {
	t.Helper()
	if _, appErr := computeSoulRegistrationSelfAttestationDigest(nil); appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected bad_request for nil reg, got %#v", appErr)
	}
	if _, appErr := computeSoulRegistrationSelfAttestationDigest(map[string]any{}); appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected bad_request for missing attestations, got %#v", appErr)
	}
	if _, appErr := computeSoulRegistrationSelfAttestationDigest(map[string]any{"attestations": "nope"}); appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected bad_request for wrong attestations type, got %#v", appErr)
	}
	if _, appErr := computeSoulRegistrationSelfAttestationDigest(map[string]any{
		"attestations": map[string]any{"selfAttestation": "0xdeadbeef"},
		"bad":          func() {},
	}); appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected bad_request for invalid json, got %#v", appErr)
	}

	reg := map[string]any{
		"version": "2",
		"agentId": soulLifecycleTestAgentIDHex,
		"attestations": map[string]any{
			"selfAttestation": "0xdeadbeef",
			"hostAttestation": "https://example.com/host",
		},
	}
	digest, appErr := computeSoulRegistrationSelfAttestationDigest(reg)
	if appErr != nil {
		t.Fatalf("unexpected appErr: %#v", appErr)
	}
	if len(digest) != 32 {
		t.Fatalf("expected 32-byte digest, got %d", len(digest))
	}
	attestations := requireBoundaryMap(t, reg["attestations"])
	if got := attestations["selfAttestation"]; got != "0xdeadbeef" {
		t.Fatalf("expected original selfAttestation preserved, got %#v", got)
	}
}

func TestBuildSoulBoundaryAppendRegistration_MoreCoverage(t *testing.T) {
	t.Parallel()

	key, wallet := mustGenerateBoundaryWallet(t)
	identity := &models.SoulAgentIdentity{
		AgentID: soulLifecycleTestAgentIDHex,
		Domain:  "example.com",
		LocalID: "agent-alice",
		Wallet:  wallet,
		Status:  models.SoulAgentStatusActive,
	}

	input := soulBoundaryAppendBuildInput{
		BoundaryID:      "boundary-new",
		Category:        models.SoulBoundaryCategoryRefusal,
		Statement:       "I will not do that.",
		Rationale:       "because",
		Supersedes:      "",
		Signature:       "0x00",
		IssuedAt:        time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC),
		ExpectedPrev:    2,
		NextVersion:     3,
		SelfAttestation: "0xdeadbeef",
	}

	t.Run("guard_and_validation_errors", func(t *testing.T) {
		t.Parallel()
		testBoundaryAppendRegistrationGuardAndValidationErrors(t, key, wallet, identity, input)
	})
	t.Run("duplicate_boundary_in_base_registration", func(t *testing.T) {
		t.Parallel()
		testBoundaryAppendRegistrationDuplicateBoundaryInBaseRegistration(t, key, wallet, identity, input)
	})
	t.Run("list_error_and_duplicate_db_boundary", func(t *testing.T) {
		t.Parallel()
		testBoundaryAppendRegistrationListErrorAndDuplicateDBBoundary(t, key, wallet, identity, input)
	})
	t.Run("success_v3_merges_missing_boundaries", func(t *testing.T) {
		t.Parallel()
		testBoundaryAppendRegistrationSuccessV3MergesMissingBoundaries(t, key, wallet, identity, input)
	})
	t.Run("unknown_capability_allowed", func(t *testing.T) {
		t.Parallel()
		testBoundaryAppendRegistrationUnknownCapabilityAllowed(t, key, wallet, identity, input)
	})
}

func testBoundaryAppendRegistrationGuardAndValidationErrors(t *testing.T, key *ecdsa.PrivateKey, wallet string, identity *models.SoulAgentIdentity, input soulBoundaryAppendBuildInput) {
	t.Helper()
	base := validBoundaryBaseRegistrationV2(t, key, soulLifecycleTestAgentIDHex, wallet)
	if _, _, _, _, _, _, appErr := (*Server)(nil).buildSoulBoundaryAppendRegistration(t.Context(), base, "2", soulLifecycleTestAgentIDHex, identity, input); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error, got %#v", appErr)
	}
	s := &Server{}
	if _, _, _, _, _, _, appErr := s.buildSoulBoundaryAppendRegistration(t.Context(), base, "2", soulLifecycleTestAgentIDHex, nil, input); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error, got %#v", appErr)
	}
	if _, _, _, _, _, _, appErr := s.buildSoulBoundaryAppendRegistration(t.Context(), nil, "2", soulLifecycleTestAgentIDHex, identity, input); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error, got %#v", appErr)
	}
	if _, _, _, _, _, _, appErr := s.buildSoulBoundaryAppendRegistration(t.Context(), base, "9", soulLifecycleTestAgentIDHex, identity, input); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected conflict, got %#v", appErr)
	}
	badInput := input
	badInput.ExpectedPrev = -1
	if _, _, _, _, _, _, appErr := s.buildSoulBoundaryAppendRegistration(t.Context(), base, "2", soulLifecycleTestAgentIDHex, identity, badInput); appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected bad_request, got %#v", appErr)
	}
}

func testBoundaryAppendRegistrationDuplicateBoundaryInBaseRegistration(t *testing.T, key *ecdsa.PrivateKey, wallet string, identity *models.SoulAgentIdentity, input soulBoundaryAppendBuildInput) {
	t.Helper()
	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulPackBucketName: "bucket"}}
	base := validBoundaryBaseRegistrationV2(t, key, soulLifecycleTestAgentIDHex, wallet)
	base["boundaries"] = append(requireBoundarySlice(t, base["boundaries"]), map[string]any{
		"id":             input.BoundaryID,
		"category":       boundaryTestCategoryRefusal,
		"statement":      "already here",
		"addedAt":        "2026-03-02T00:00:00Z",
		"addedInVersion": "2",
		"signature":      "0xabc",
	})
	if _, _, _, _, _, _, appErr := s.buildSoulBoundaryAppendRegistration(t.Context(), base, "2", soulLifecycleTestAgentIDHex, identity, input); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected conflict, got %#v", appErr)
	}
}

func testBoundaryAppendRegistrationListErrorAndDuplicateDBBoundary(t *testing.T, key *ecdsa.PrivateKey, wallet string, identity *models.SoulAgentIdentity, input soulBoundaryAppendBuildInput) {
	t.Helper()
	base := validBoundaryBaseRegistrationV2(t, key, soulLifecycleTestAgentIDHex, wallet)

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulPackBucketName: "bucket"}}
	tdb.qBoundary.On("All", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(errors.New("boom")).Once()
	if _, _, _, _, _, _, appErr := s.buildSoulBoundaryAppendRegistration(t.Context(), base, "2", soulLifecycleTestAgentIDHex, identity, input); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error, got %#v", appErr)
	}

	tdb2 := newSoulLifecycleTestDB()
	s2 := &Server{store: store.New(tdb2.db), cfg: config.Config{SoulPackBucketName: "bucket"}}
	tdb2.qBoundary.On("All", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
		*dest = []*models.SoulAgentBoundary{{BoundaryID: input.BoundaryID}}
	}).Once()
	if _, _, _, _, _, _, appErr := s2.buildSoulBoundaryAppendRegistration(t.Context(), base, "2", soulLifecycleTestAgentIDHex, identity, input); appErr == nil || appErr.Code != appErrCodeConflict {
		t.Fatalf("expected conflict, got %#v", appErr)
	}
}

func testBoundaryAppendRegistrationSuccessV3MergesMissingBoundaries(t *testing.T, key *ecdsa.PrivateKey, wallet string, identity *models.SoulAgentIdentity, input soulBoundaryAppendBuildInput) {
	t.Helper()
	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulPackBucketName: "bucket"}}
	base := validBoundaryBaseRegistrationV3(t, key, soulLifecycleTestAgentIDHex, wallet)
	older := time.Date(2026, 3, 1, 6, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	tdb.qBoundary.On("All", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
		*dest = []*models.SoulAgentBoundary{
			{BoundaryID: "boundary-db-b", Category: boundaryTestCategoryRefusal, Statement: "later", AddedAt: newer, AddedInVersion: 2, Signature: "0x0bbb"},
			nil,
			{BoundaryID: "boundary-db-a", Category: "scope_limit", Statement: "earlier", AddedAt: older, AddedInVersion: 2, Signature: "0x0aaa"},
		}
	}).Once()

	reg, regV2, regV3, digest, capsNorm, claimLevels, appErr := s.buildSoulBoundaryAppendRegistration(t.Context(), base, "3", soulLifecycleTestAgentIDHex, identity, input)
	if appErr != nil {
		t.Fatalf("unexpected appErr: %#v", appErr)
	}
	if regV2 != nil || regV3 == nil || len(digest) != 32 {
		t.Fatalf("unexpected registration outputs: regV2=%#v regV3=%#v digest=%d", regV2, regV3, len(digest))
	}
	if len(capsNorm) != 1 || capsNorm[0] != "text-summarization" || claimLevels["text-summarization"] != soulClaimLevelSelfDeclared {
		t.Fatalf("unexpected capability metadata: caps=%#v claimLevels=%#v", capsNorm, claimLevels)
	}
	if got := extractStringField(reg, "changeSummary"); got != "Append boundary boundary-new" {
		t.Fatalf("unexpected changeSummary: %q", got)
	}
	if got := extractStringField(reg, "previousVersionUri"); !strings.Contains(got, "/versions/2/registration.json") {
		t.Fatalf("unexpected previousVersionUri: %q", got)
	}

	boundaries := requireBoundarySlice(t, reg["boundaries"])
	if len(boundaries) != 4 {
		t.Fatalf("expected 4 boundaries, got %#v", boundaries)
	}
	gotIDs := []string{
		extractStringField(requireBoundaryMap(t, boundaries[0]), "id"),
		extractStringField(requireBoundaryMap(t, boundaries[1]), "id"),
		extractStringField(requireBoundaryMap(t, boundaries[2]), "id"),
		extractStringField(requireBoundaryMap(t, boundaries[3]), "id"),
	}
	wantIDs := []string{"boundary-existing", "boundary-db-a", "boundary-db-b", "boundary-new"}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("unexpected boundary order: got=%v want=%v", gotIDs, wantIDs)
	}
}

func testBoundaryAppendRegistrationUnknownCapabilityAllowed(t *testing.T, key *ecdsa.PrivateKey, wallet string, identity *models.SoulAgentIdentity, input soulBoundaryAppendBuildInput) {
	t.Helper()
	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulPackBucketName:        "bucket",
			SoulSupportedCapabilities: []string{"commerce"},
		},
	}
	base := validBoundaryBaseRegistrationV2(t, key, soulLifecycleTestAgentIDHex, wallet)
	tdb.qBoundary.On("All", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
		*dest = []*models.SoulAgentBoundary{}
	}).Once()
	_, _, _, _, capsNorm, _, appErr := s.buildSoulBoundaryAppendRegistration(t.Context(), base, "2", soulLifecycleTestAgentIDHex, identity, input)
	if appErr != nil {
		t.Fatalf("unexpected err: %#v", appErr)
	}
	if len(capsNorm) != 1 || capsNorm[0] != "text-summarization" {
		t.Fatalf("expected normalized capabilities, got %#v", capsNorm)
	}
}

func TestBuildSoulBoundaryAppendRegistration_MoreBranches(t *testing.T) {
	t.Parallel()

	key, wallet := mustGenerateBoundaryWallet(t)
	identity := &models.SoulAgentIdentity{
		AgentID: soulLifecycleTestAgentIDHex,
		Domain:  "example.com",
		LocalID: "agent-alice",
		Wallet:  wallet,
		Status:  models.SoulAgentStatusActive,
	}

	t.Run("first_version_initializes_missing_fields", func(t *testing.T) {
		t.Parallel()
		testBoundaryAppendRegistrationFirstVersionInitializesMissingFields(t, key, wallet, identity)
	})
	t.Run("non_map_boundary_items_fail_schema", func(t *testing.T) {
		t.Parallel()
		testBoundaryAppendRegistrationNonMapBoundaryItemsFailSchema(t, key, wallet, identity)
	})
	t.Run("skips_existing_db_boundaries", func(t *testing.T) {
		t.Parallel()
		testBoundaryAppendRegistrationSkipsExistingDBBoundaries(t, key, wallet, identity)
	})
	t.Run("invalid_json_and_schema_errors", func(t *testing.T) {
		t.Parallel()
		testBoundaryAppendRegistrationInvalidJSONAndSchemaErrors(t, key, wallet, identity)
	})
}

func testBoundaryAppendRegistrationFirstVersionInitializesMissingFields(t *testing.T, key *ecdsa.PrivateKey, wallet string, identity *models.SoulAgentIdentity) {
	t.Helper()
	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulPackBucketName: "bucket"}}
	base := validBoundaryBaseRegistrationV2(t, key, soulLifecycleTestAgentIDHex, wallet)
	delete(base, "attestations")
	delete(base, "boundaries")
	tdb.qBoundary.On("All", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
		*dest = []*models.SoulAgentBoundary{}
	}).Once()

	reg, regV2, regV3, digest, _, _, appErr := s.buildSoulBoundaryAppendRegistration(t.Context(), base, "2", soulLifecycleTestAgentIDHex, identity, soulBoundaryAppendBuildInput{
		BoundaryID:      "boundary-first",
		Category:        models.SoulBoundaryCategoryRefusal,
		Statement:       boundaryTestStatementNoDo,
		Supersedes:      "boundary-old",
		Signature:       "0x00",
		IssuedAt:        time.Date(2026, 3, 5, 14, 0, 0, 0, time.UTC),
		ExpectedPrev:    0,
		NextVersion:     1,
		SelfAttestation: "0x00",
	})
	if appErr != nil {
		t.Fatalf("unexpected appErr: %#v", appErr)
	}
	if regV2 == nil || regV3 != nil || len(digest) != 32 {
		t.Fatalf("unexpected registration outputs: regV2=%#v regV3=%#v digest=%d", regV2, regV3, len(digest))
	}
	if _, ok := reg["previousVersionUri"]; ok {
		t.Fatalf("expected previousVersionUri to be omitted: %#v", reg["previousVersionUri"])
	}
	att := requireBoundaryMap(t, reg["attestations"])
	if extractStringField(att, "selfAttestation") != "0x00" {
		t.Fatalf("unexpected attestations: %#v", reg["attestations"])
	}
	boundaries := requireBoundarySlice(t, reg["boundaries"])
	if len(boundaries) != 1 {
		t.Fatalf("expected one boundary, got %#v", boundaries)
	}
	boundary := requireBoundaryMap(t, boundaries[0])
	if got := extractStringField(boundary, "supersedes"); got != "boundary-old" {
		t.Fatalf("unexpected supersedes: %q", got)
	}
	if got := extractStringField(boundary, "addedInVersion"); got != "1" {
		t.Fatalf("unexpected addedInVersion: %q", got)
	}
}

func testBoundaryAppendRegistrationNonMapBoundaryItemsFailSchema(t *testing.T, key *ecdsa.PrivateKey, wallet string, identity *models.SoulAgentIdentity) {
	t.Helper()
	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulPackBucketName: "bucket"}}
	base := validBoundaryBaseRegistrationV2(t, key, soulLifecycleTestAgentIDHex, wallet)
	base["boundaries"] = []any{
		"ignore-me",
		map[string]any{
			"id":             "boundary-existing",
			"category":       boundaryTestCategoryRefusal,
			"statement":      "base",
			"addedAt":        "2026-03-01T00:00:00Z",
			"addedInVersion": "1",
			"signature":      "0x00",
		},
	}
	tdb.qBoundary.On("All", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
		*dest = []*models.SoulAgentBoundary{
			{BoundaryID: "boundary-existing", Category: models.SoulBoundaryCategoryRefusal, Statement: "db-existing", AddedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)},
		}
	}).Once()
	_, _, _, _, _, _, appErr := s.buildSoulBoundaryAppendRegistration(t.Context(), base, "2", soulLifecycleTestAgentIDHex, identity, soulBoundaryAppendBuildInput{
		BoundaryID:      "boundary-new",
		Category:        models.SoulBoundaryCategoryCircuitBreaker,
		Statement:       "Stop if uncertain.",
		Signature:       "0x00",
		IssuedAt:        time.Date(2026, 3, 5, 15, 0, 0, 0, time.UTC),
		ExpectedPrev:    2,
		NextVersion:     3,
		SelfAttestation: "0x00",
	})
	if appErr == nil || appErr.Code != appErrCodeBadRequest {
		t.Fatalf("expected bad_request, got %#v", appErr)
	}
}

func testBoundaryAppendRegistrationSkipsExistingDBBoundaries(t *testing.T, key *ecdsa.PrivateKey, wallet string, identity *models.SoulAgentIdentity) {
	t.Helper()
	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulPackBucketName: "bucket"}}
	base := validBoundaryBaseRegistrationV2(t, key, soulLifecycleTestAgentIDHex, wallet)
	tdb.qBoundary.On("All", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
		*dest = []*models.SoulAgentBoundary{
			{BoundaryID: "boundary-existing", Category: models.SoulBoundaryCategoryRefusal, Statement: "db-existing", AddedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)},
			{BoundaryID: "boundary-db", Category: models.SoulBoundaryCategoryScopeLimit, Statement: "db-new", AddedAt: time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC), AddedInVersion: 2, Signature: "0xdb"},
		}
	}).Once()

	reg, _, _, _, _, _, appErr := s.buildSoulBoundaryAppendRegistration(t.Context(), base, "2", soulLifecycleTestAgentIDHex, identity, soulBoundaryAppendBuildInput{
		BoundaryID:      "boundary-new",
		Category:        models.SoulBoundaryCategoryCircuitBreaker,
		Statement:       "Stop if uncertain.",
		Signature:       "0x00",
		IssuedAt:        time.Date(2026, 3, 5, 15, 0, 0, 0, time.UTC),
		ExpectedPrev:    2,
		NextVersion:     3,
		SelfAttestation: "0x00",
	})
	if appErr != nil {
		t.Fatalf("unexpected appErr: %#v", appErr)
	}

	boundaries := requireBoundarySlice(t, reg["boundaries"])
	gotIDs := make([]string, 0, len(boundaries))
	for _, item := range boundaries {
		gotIDs = append(gotIDs, extractStringField(requireBoundaryMap(t, item), "id"))
	}
	if strings.Join(gotIDs, ",") != "boundary-existing,boundary-db,boundary-new" {
		t.Fatalf("unexpected boundary ids: %v", gotIDs)
	}
}

func testBoundaryAppendRegistrationInvalidJSONAndSchemaErrors(t *testing.T, key *ecdsa.PrivateKey, wallet string, identity *models.SoulAgentIdentity) {
	t.Helper()
	type tc struct {
		name        string
		baseVersion string
		base        map[string]any
	}

	cases := []tc{
		{
			name:        "invalid_json",
			baseVersion: "2",
			base: func() map[string]any {
				base := validBoundaryBaseRegistrationV2(t, key, soulLifecycleTestAgentIDHex, wallet)
				base["bad"] = func() {}
				return base
			}(),
		},
		{
			name:        "invalid_v2_schema",
			baseVersion: "2",
			base: func() map[string]any {
				base := validBoundaryBaseRegistrationV2(t, key, soulLifecycleTestAgentIDHex, wallet)
				base["principal"] = "bad"
				return base
			}(),
		},
		{
			name:        "invalid_v3_schema",
			baseVersion: "3",
			base: func() map[string]any {
				base := validBoundaryBaseRegistrationV3(t, key, soulLifecycleTestAgentIDHex, wallet)
				base["contactPreferences"] = "bad"
				return base
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tdb := newSoulLifecycleTestDB()
			s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulPackBucketName: "bucket"}}
			tdb.qBoundary.On("All", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(nil).Run(func(args mock.Arguments) {
				dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
				*dest = []*models.SoulAgentBoundary{}
			}).Once()

			_, _, _, _, _, _, appErr := s.buildSoulBoundaryAppendRegistration(t.Context(), tc.base, tc.baseVersion, soulLifecycleTestAgentIDHex, identity, soulBoundaryAppendBuildInput{
				BoundaryID:      "boundary-new",
				Category:        models.SoulBoundaryCategoryRefusal,
				Statement:       boundaryTestStatementNoDo,
				Signature:       "0x00",
				IssuedAt:        time.Date(2026, 3, 5, 16, 0, 0, 0, time.UTC),
				ExpectedPrev:    2,
				NextVersion:     3,
				SelfAttestation: "0x00",
			})
			if appErr == nil || appErr.Code != appErrCodeBadRequest {
				t.Fatalf("expected bad_request, got %#v", appErr)
			}
		})
	}
}

func TestListSoulAgentBoundariesNoTruncation_MoreCoverage(t *testing.T) {
	t.Parallel()

	if _, appErr := (*Server)(nil).listSoulAgentBoundariesNoTruncation(t.Context(), soulLifecycleTestAgentIDHex); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error, got %#v", appErr)
	}

	tdb := newSoulLifecycleTestDB()
	s := &Server{store: store.New(tdb.db)}
	if _, appErr := s.listSoulAgentBoundariesNoTruncation(t.Context(), " "); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error, got %#v", appErr)
	}

	tdb.qBoundary.On("All", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(errors.New("boom")).Once()
	if _, appErr := s.listSoulAgentBoundariesNoTruncation(t.Context(), soulLifecycleTestAgentIDHex); appErr == nil || appErr.Code != appErrCodeInternal {
		t.Fatalf("expected internal error, got %#v", appErr)
	}

	tdb2 := newSoulLifecycleTestDB()
	s2 := &Server{store: store.New(tdb2.db)}
	tdb2.qBoundary.On("All", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
		*dest = []*models.SoulAgentBoundary{{BoundaryID: "b-1"}, nil}
	}).Once()

	items, appErr := s2.listSoulAgentBoundariesNoTruncation(t.Context(), soulLifecycleTestAgentIDHex)
	if appErr != nil {
		t.Fatalf("unexpected appErr: %#v", appErr)
	}
	if len(items) != 2 || items[0] == nil || items[0].BoundaryID != "b-1" {
		t.Fatalf("unexpected items: %#v", items)
	}
}

func TestHandleSoulPublicGetBoundaries_ErrorBranches(t *testing.T) {
	t.Parallel()

	t.Run("store_not_configured", func(t *testing.T) {
		t.Parallel()

		s := &Server{cfg: config.Config{SoulEnabled: true}}
		_, err := s.handleSoulPublicGetBoundaries(&apptheory.Context{Params: map[string]string{"agentId": soulLifecycleTestAgentIDHex}})
		requireBoundaryAppErrorCode(t, err, "app.internal")
	})

	t.Run("invalid_agent_id", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
		_, err := s.handleSoulPublicGetBoundaries(&apptheory.Context{Params: map[string]string{"agentId": "not-hex"}})
		requireBoundaryAppErrorCode(t, err, "app.bad_request")
	})
}

func TestHandleSoulPublicGetBoundaries_MoreCoverage(t *testing.T) {
	t.Parallel()

	t.Run("disabled_and_list_error", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: false}}
		_, err := s.handleSoulPublicGetBoundaries(&apptheory.Context{Params: map[string]string{"agentId": soulLifecycleTestAgentIDHex}})
		requireBoundaryAppErrorCode(t, err, "app.not_found")

		tdb2 := newSoulLifecycleTestDB()
		s2 := &Server{store: store.New(tdb2.db), cfg: config.Config{SoulEnabled: true}}
		tdb2.qBoundary.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return((*core.PaginatedResult)(nil), errors.New("boom")).Once()
		_, err = s2.handleSoulPublicGetBoundaries(&apptheory.Context{Params: map[string]string{"agentId": soulLifecycleTestAgentIDHex}})
		requireBoundaryAppErrorCode(t, err, "app.internal")
	})

	t.Run("cursor_branch_and_limit_clamp", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		s := &Server{store: store.New(tdb.db), cfg: config.Config{SoulEnabled: true}}
		tdb.qBoundary.On("Cursor", mock.Anything).Return(tdb.qBoundary).Once()
		tdb.qBoundary.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return((*core.PaginatedResult)(nil), nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
			*dest = []*models.SoulAgentBoundary{}
		}).Once()

		ctx := &apptheory.Context{
			Params: map[string]string{"agentId": soulLifecycleTestAgentIDHex},
			Request: apptheory.Request{Query: map[string][]string{
				"cursor": {" c1 "},
				"limit":  {"999"},
			}},
		}
		resp, err := s.handleSoulPublicGetBoundaries(ctx)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.Status)
		}

		var out soulListBoundariesResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Count != 0 || out.HasMore || out.NextCursor != "" {
			t.Fatalf("unexpected response: %#v", out)
		}
	})
}

func TestHandleSoulBeginAppendBoundary_MoreBranches(t *testing.T) {
	t.Parallel()

	t.Run("requires_pack_store", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		s := &Server{
			store: store.New(tdb.db),
			cfg: config.Config{
				SoulEnabled:                 true,
				SoulChainID:                 1,
				SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			},
		}
		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		_, err := s.handleSoulBeginAppendBoundary(ctx)
		requireBoundaryAppErrorCode(t, err, "app.conflict")
	})

	t.Run("invalid_agent_id", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		s := newBoundaryCoverageServer(tdb, &fakeSoulPackStore{})
		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": "bad-agent"}
		_, err := s.handleSoulBeginAppendBoundary(ctx)
		requireBoundaryAppErrorCode(t, err, "app.bad_request")
	})

	t.Run("supersedes_not_found", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		s := newBoundaryCoverageServer(tdb, &fakeSoulPackStore{})
		key, wallet := mustGenerateBoundaryWallet(t)
		seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)
		tdb.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(theoryErrors.ErrItemNotFound).Once()

		body := mustMarshalJSON(t, soulAppendBoundaryRequest{
			BoundaryID: "boundary-1",
			Category:   "refusal",
			Statement:  "I will not do that.",
			Supersedes: "boundary-old",
			Signature:  mustSignBoundaryStatement(t, key, "I will not do that."),
		})
		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		ctx.Request.Body = body

		_, err := s.handleSoulBeginAppendBoundary(ctx)
		requireBoundaryAppErrorCode(t, err, "app.bad_request")
	})

	t.Run("boundary_check_internal_error", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		s := newBoundaryCoverageServer(tdb, &fakeSoulPackStore{})
		key, wallet := mustGenerateBoundaryWallet(t)
		seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)
		tdb.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(errors.New("boom")).Once()

		body := mustMarshalJSON(t, soulAppendBoundaryRequest{
			BoundaryID: "boundary-1",
			Category:   "refusal",
			Statement:  "I will not do that.",
			Signature:  mustSignBoundaryStatement(t, key, "I will not do that."),
		})
		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		ctx.Request.Body = body

		_, err := s.handleSoulBeginAppendBoundary(ctx)
		requireBoundaryAppErrorCode(t, err, "app.internal")
	})

	t.Run("load_registration_and_duplicate_registration_errors", func(t *testing.T) {
		t.Parallel()

		key, wallet := mustGenerateBoundaryWallet(t)
		signature := mustSignBoundaryStatement(t, key, "I will not do that.")

		t.Run("load_registration_error", func(t *testing.T) {
			tdb := newSoulLifecycleTestDB()
			s := newBoundaryCoverageServer(tdb, &fakeSoulPackStore{})
			seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)
			tdb.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(theoryErrors.ErrItemNotFound).Once()

			body := mustMarshalJSON(t, soulAppendBoundaryRequest{
				BoundaryID: "boundary-1",
				Category:   "refusal",
				Statement:  "I will not do that.",
				Signature:  signature,
			})
			ctx := adminCtx()
			ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
			ctx.Request.Body = body

			_, err := s.handleSoulBeginAppendBoundary(ctx)
			requireBoundaryAppErrorCode(t, err, "app.conflict")
		})

		t.Run("registration_already_contains_boundary", func(t *testing.T) {
			tdb := newSoulLifecycleTestDB()
			packs := &fakeSoulPackStore{}
			s := newBoundaryCoverageServer(tdb, packs)
			seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)
			tdb.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(theoryErrors.ErrItemNotFound).Once()

			base := validBoundaryBaseRegistrationV2(t, key, soulLifecycleTestAgentIDHex, wallet)
			base["boundaries"] = append(requireBoundarySlice(t, base["boundaries"]), map[string]any{
				"id":             "boundary-1",
				"category":       boundaryTestCategoryRefusal,
				"statement":      "already present",
				"addedAt":        "2026-03-02T00:00:00Z",
				"addedInVersion": "2",
				"signature":      "0xdup",
			})
			seedBoundaryRegistrationObject(t, packs, soulLifecycleTestAgentIDHex, base)

			body := mustMarshalJSON(t, soulAppendBoundaryRequest{
				BoundaryID: "boundary-1",
				Category:   "refusal",
				Statement:  "I will not do that.",
				Signature:  signature,
			})
			ctx := adminCtx()
			ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
			ctx.Request.Body = body

			_, err := s.handleSoulBeginAppendBoundary(ctx)
			requireBoundaryAppErrorCode(t, err, "app.conflict")
		})
	})
}

func TestHandleSoulBeginAppendBoundary_MoreCoverage(t *testing.T) {
	t.Parallel()

	t.Run("registration_not_published", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		s := newBoundaryCoverageServer(tdb, &fakeSoulPackStore{})
		_, wallet := mustGenerateBoundaryWallet(t)
		seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 0)

		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		_, err := s.handleSoulBeginAppendBoundary(ctx)
		requireBoundaryAppErrorCode(t, err, "app.conflict")
	})

	t.Run("invalid_boundary_signature", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		s := newBoundaryCoverageServer(tdb, &fakeSoulPackStore{})
		_, wallet := mustGenerateBoundaryWallet(t)
		seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)

		body := mustMarshalJSON(t, soulAppendBoundaryRequest{
			BoundaryID: "boundary-1",
			Category:   "refusal",
			Statement:  "I will not do that.",
			Signature:  "0xdeadbeef",
		})
		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		ctx.Request.Body = body

		_, err := s.handleSoulBeginAppendBoundary(ctx)
		requireBoundaryAppErrorCode(t, err, "app.bad_request")
	})

	t.Run("boundary_already_exists", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		s := newBoundaryCoverageServer(tdb, &fakeSoulPackStore{})
		key, wallet := mustGenerateBoundaryWallet(t)
		seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)
		tdb.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentBoundary](t, args, 0)
			*dest = models.SoulAgentBoundary{AgentID: soulLifecycleTestAgentIDHex, BoundaryID: "boundary-1"}
		}).Once()

		body := mustMarshalJSON(t, soulAppendBoundaryRequest{
			BoundaryID: "boundary-1",
			Category:   "refusal",
			Statement:  "I will not do that.",
			Signature:  mustSignBoundaryStatement(t, key, "I will not do that."),
		})
		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		ctx.Request.Body = body

		_, err := s.handleSoulBeginAppendBoundary(ctx)
		requireBoundaryAppErrorCode(t, err, "app.conflict")
	})

	t.Run("supersedes_missing_boundary", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		s := newBoundaryCoverageServer(tdb, &fakeSoulPackStore{})
		key, wallet := mustGenerateBoundaryWallet(t)
		seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)
		tdb.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(theoryErrors.ErrItemNotFound).Once()

		body := mustMarshalJSON(t, soulAppendBoundaryRequest{
			BoundaryID: "boundary-2",
			Category:   "refusal",
			Statement:  "I will not do that either.",
			Supersedes: "missing-boundary",
			Signature:  mustSignBoundaryStatement(t, key, "I will not do that either."),
		})
		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		ctx.Request.Body = body

		_, err := s.handleSoulBeginAppendBoundary(ctx)
		requireBoundaryAppErrorCode(t, err, "app.bad_request")
	})

	t.Run("boundary_exists_in_registration", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		key, wallet := mustGenerateBoundaryWallet(t)
		packs := &fakeSoulPackStore{}
		s := newBoundaryCoverageServer(tdb, packs)
		seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)
		tdb.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(theoryErrors.ErrItemNotFound).Once()

		base := validBoundaryBaseRegistrationV2(t, key, soulLifecycleTestAgentIDHex, wallet)
		bodyBytes := mustMarshalJSON(t, base)
		packs.objects = map[string]fakePut{
			soulRegistrationS3Key(soulLifecycleTestAgentIDHex): {key: soulRegistrationS3Key(soulLifecycleTestAgentIDHex), body: bodyBytes},
		}

		body := mustMarshalJSON(t, soulAppendBoundaryRequest{
			BoundaryID: "boundary-existing",
			Category:   "refusal",
			Statement:  "I will not do that either.",
			Signature:  mustSignBoundaryStatement(t, key, "I will not do that either."),
		})
		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		ctx.Request.Body = body

		_, err := s.handleSoulBeginAppendBoundary(ctx)
		requireBoundaryAppErrorCode(t, err, "app.conflict")
	})
}

func TestHandleSoulAppendBoundary_MoreBranches(t *testing.T) {
	t.Parallel()

	t.Run("requires_pack_store_and_valid_agent_id", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		s := &Server{
			store: store.New(tdb.db),
			cfg: config.Config{
				SoulEnabled:                 true,
				SoulChainID:                 1,
				SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			},
		}

		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		_, err := s.handleSoulAppendBoundary(ctx)
		requireBoundaryAppErrorCode(t, err, "app.conflict")

		s2 := newBoundaryCoverageServer(newSoulLifecycleTestDB(), &fakeSoulPackStore{})
		ctx2 := adminCtx()
		ctx2.Params = map[string]string{"agentId": "bad-agent"}
		_, err = s2.handleSoulAppendBoundary(ctx2)
		requireBoundaryAppErrorCode(t, err, "app.bad_request")
	})

	t.Run("signature_and_registration_errors", func(t *testing.T) {
		t.Parallel()

		key, wallet := mustGenerateBoundaryWallet(t)

		makeCtx := func(body soulConfirmAppendBoundaryRequest) *apptheory.Context {
			raw := mustMarshalJSON(t, body)
			ctx := adminCtx()
			ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
			ctx.Request.Body = raw
			return ctx
		}

		t.Run("invalid_boundary_signature", func(t *testing.T) {
			tdb := newSoulLifecycleTestDB()
			s := newBoundaryCoverageServer(tdb, &fakeSoulPackStore{})
			seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)

			_, err := s.handleSoulAppendBoundary(makeCtx(soulConfirmAppendBoundaryRequest{
				BoundaryID:      "boundary-1",
				Category:        "refusal",
				Statement:       "I will not do that.",
				Signature:       "0xdeadbeef",
				IssuedAt:        "2026-03-05T12:00:00Z",
				ExpectedVersion: ptrInt(2),
				SelfAttestation: "0x00",
			}))
			requireBoundaryAppErrorCode(t, err, "app.bad_request")
		})

		t.Run("supersedes_not_found", func(t *testing.T) {
			tdb := newSoulLifecycleTestDB()
			s := newBoundaryCoverageServer(tdb, &fakeSoulPackStore{})
			seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)
			tdb.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(theoryErrors.ErrItemNotFound).Once()

			_, err := s.handleSoulAppendBoundary(makeCtx(soulConfirmAppendBoundaryRequest{
				BoundaryID:      "boundary-1",
				Category:        "refusal",
				Statement:       "I will not do that.",
				Supersedes:      "boundary-old",
				Signature:       mustSignBoundaryStatement(t, key, "I will not do that."),
				IssuedAt:        "2026-03-05T12:00:00Z",
				ExpectedVersion: ptrInt(2),
				SelfAttestation: "0x00",
			}))
			requireBoundaryAppErrorCode(t, err, "app.bad_request")
		})

		t.Run("registration_already_contains_boundary", func(t *testing.T) {
			tdb := newSoulLifecycleTestDB()
			packs := &fakeSoulPackStore{}
			s := newBoundaryCoverageServer(tdb, packs)
			seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)
			base := validBoundaryBaseRegistrationV2(t, key, soulLifecycleTestAgentIDHex, wallet)
			base["boundaries"] = append(requireBoundarySlice(t, base["boundaries"]), map[string]any{
				"id":             "boundary-1",
				"category":       boundaryTestCategoryRefusal,
				"statement":      "already present",
				"addedAt":        "2026-03-02T00:00:00Z",
				"addedInVersion": "2",
				"signature":      "0xdup",
			})
			seedBoundaryRegistrationObject(t, packs, soulLifecycleTestAgentIDHex, base)

			_, err := s.handleSoulAppendBoundary(makeCtx(soulConfirmAppendBoundaryRequest{
				BoundaryID:      "boundary-1",
				Category:        "refusal",
				Statement:       "I will not do that.",
				Signature:       mustSignBoundaryStatement(t, key, "I will not do that."),
				IssuedAt:        "2026-03-05T12:00:00Z",
				ExpectedVersion: ptrInt(2),
				SelfAttestation: "0x00",
			}))
			requireBoundaryAppErrorCode(t, err, "app.conflict")
		})

		t.Run("invalid_registration_signature", func(t *testing.T) {
			tdb := newSoulLifecycleTestDB()
			packs := &fakeSoulPackStore{}
			s := newBoundaryCoverageServer(tdb, packs)
			seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)
			tdb.qBoundary.On("All", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(nil).Run(func(args mock.Arguments) {
				dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
				*dest = []*models.SoulAgentBoundary{}
			}).Once()
			seedBoundaryRegistrationObject(t, packs, soulLifecycleTestAgentIDHex, validBoundaryBaseRegistrationV2(t, key, soulLifecycleTestAgentIDHex, wallet))

			_, err := s.handleSoulAppendBoundary(makeCtx(soulConfirmAppendBoundaryRequest{
				BoundaryID:      "boundary-2",
				Category:        "refusal",
				Statement:       "I will not do that.",
				Signature:       mustSignBoundaryStatement(t, key, "I will not do that."),
				IssuedAt:        "2026-03-05T12:00:00Z",
				ExpectedVersion: ptrInt(2),
				SelfAttestation: "0xdeadbeef",
			}))
			requireBoundaryAppErrorCode(t, err, "app.bad_request")
		})
	})

	t.Run("publish_v3_success", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulLifecycleTestDB()
		packs := &fakeSoulPackStore{}
		s := newBoundaryCoverageServer(tdb, packs)
		key, wallet := mustGenerateBoundaryWallet(t)
		seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)
		tdb.qVersion.On("First", mock.AnythingOfType("*models.SoulAgentVersion")).Return(theoryErrors.ErrItemNotFound).Maybe()
		tdb.qCapIdx.On("First", mock.AnythingOfType("*models.SoulCapabilityAgentIndex")).Return(theoryErrors.ErrItemNotFound).Maybe()
		tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Maybe()

		base := validBoundaryBaseRegistrationV3(t, key, soulLifecycleTestAgentIDHex, wallet)
		seedBoundaryRegistrationObject(t, packs, soulLifecycleTestAgentIDHex, base)

		identity := &models.SoulAgentIdentity{
			AgentID:                soulLifecycleTestAgentIDHex,
			Domain:                 "example.com",
			LocalID:                "agent-alice",
			Wallet:                 wallet,
			Status:                 models.SoulAgentStatusActive,
			LifecycleStatus:        models.SoulAgentStatusActive,
			SelfDescriptionVersion: 2,
		}
		input := soulBoundaryAppendBuildInput{
			BoundaryID:      "boundary-v3",
			Category:        models.SoulBoundaryCategoryRefusal,
			Statement:       "I will not do that.",
			Signature:       mustSignBoundaryStatement(t, key, "I will not do that."),
			IssuedAt:        time.Date(2026, 3, 5, 18, 0, 0, 0, time.UTC),
			ExpectedPrev:    2,
			NextVersion:     3,
			SelfAttestation: "0x00",
		}
		tdb.qBoundary.On("All", mock.AnythingOfType("*[]*models.SoulAgentBoundary")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentBoundary](t, args, 0)
			*dest = []*models.SoulAgentBoundary{}
		}).Twice()
		selfSig := mustBuildBoundarySelfAttestation(t, s, key, base, "3", identity, input)

		body := mustMarshalJSON(t, soulConfirmAppendBoundaryRequest{
			BoundaryID:      input.BoundaryID,
			Category:        input.Category,
			Statement:       input.Statement,
			Signature:       input.Signature,
			IssuedAt:        input.IssuedAt.Format(time.RFC3339Nano),
			ExpectedVersion: ptrInt(input.ExpectedPrev),
			SelfAttestation: selfSig,
		})
		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		ctx.Request.Body = body

		resp, err := s.handleSoulAppendBoundary(ctx)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.Status)
		}
	})
}

func TestHandleSoulAppendBoundary_MoreCoverage(t *testing.T) {
	t.Parallel()

	newServer := func(version int) (*Server, soulLifecycleTestDB, string) {
		tdb := newSoulLifecycleTestDB()
		s := newBoundaryCoverageServer(tdb, &fakeSoulPackStore{})
		_, wallet := mustGenerateBoundaryWallet(t)
		seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, version)
		return s, tdb, wallet
	}

	t.Run("request_validation", func(t *testing.T) {
		t.Parallel()

		makeBody := func(fields map[string]any) []byte {
			fields["boundary_id"] = "boundary-1"
			fields["category"] = boundaryTestCategoryRefusal
			fields["statement"] = boundaryTestStatementNoDo
			fields["signature"] = "0xabc"
			return mustMarshalJSON(t, fields)
		}

		s, _, _ := newServer(3)
		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		ctx.Request.Body = makeBody(map[string]any{
			"expected_version": 3,
			"self_attestation": "0xabc",
		})
		_, err := s.handleSoulAppendBoundary(ctx)
		requireBoundaryAppErrorCode(t, err, "app.bad_request")

		s2, _, _ := newServer(3)
		ctx2 := adminCtx()
		ctx2.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		ctx2.Request.Body = makeBody(map[string]any{
			"issued_at":        "nope",
			"expected_version": 3,
			"self_attestation": "0xabc",
		})
		_, err = s2.handleSoulAppendBoundary(ctx2)
		requireBoundaryAppErrorCode(t, err, "app.bad_request")

		s3, _, _ := newServer(3)
		ctx3 := adminCtx()
		ctx3.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		ctx3.Request.Body = makeBody(map[string]any{
			"issued_at":        "2026-03-05T12:00:00Z",
			"self_attestation": "0xabc",
		})
		_, err = s3.handleSoulAppendBoundary(ctx3)
		requireBoundaryAppErrorCode(t, err, "app.bad_request")

		s4, _, _ := newServer(3)
		ctx4 := adminCtx()
		ctx4.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		ctx4.Request.Body = makeBody(map[string]any{
			"issued_at":        "2026-03-05T12:00:00Z",
			"expected_version": -1,
			"self_attestation": "0xabc",
		})
		_, err = s4.handleSoulAppendBoundary(ctx4)
		requireBoundaryAppErrorCode(t, err, "app.bad_request")

		s5, _, _ := newServer(3)
		ctx5 := adminCtx()
		ctx5.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		ctx5.Request.Body = makeBody(map[string]any{
			"issued_at":        "2026-03-05T12:00:00Z",
			"expected_version": 3,
		})
		_, err = s5.handleSoulAppendBoundary(ctx5)
		requireBoundaryAppErrorCode(t, err, "app.bad_request")
	})

	t.Run("stale_version_paths", func(t *testing.T) {
		t.Parallel()

		s, tdb, _ := newServer(3)
		tdb.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentBoundary](t, args, 0)
			*dest = models.SoulAgentBoundary{AgentID: soulLifecycleTestAgentIDHex, BoundaryID: "boundary-1"}
		}).Once()

		body := mustMarshalJSON(t, soulConfirmAppendBoundaryRequest{
			BoundaryID:      "boundary-1",
			Category:        "refusal",
			Statement:       "I will not do that.",
			Signature:       "0xabc",
			IssuedAt:        "2026-03-05T12:00:00Z",
			ExpectedVersion: ptrInt(2),
			SelfAttestation: "0xabc",
		})
		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		ctx.Request.Body = body

		resp, err := s.handleSoulAppendBoundary(ctx)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.Status)
		}

		var out soulAppendBoundaryResponse
		unmarshalErr := json.Unmarshal(resp.Body, &out)
		if unmarshalErr != nil {
			t.Fatalf("unmarshal: %v", unmarshalErr)
		}
		if out.Boundary.BoundaryID != "boundary-1" {
			t.Fatalf("unexpected boundary: %#v", out.Boundary)
		}

		s2, tdb2, _ := newServer(3)
		tdb2.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(theoryErrors.ErrItemNotFound).Once()

		ctx2 := adminCtx()
		ctx2.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
		ctx2.Request.Body = body
		_, err = s2.handleSoulAppendBoundary(ctx2)
		requireBoundaryAppErrorCode(t, err, "app.conflict")
	})

	t.Run("invalid_signature_supersedes_and_registration_conflicts", func(t *testing.T) {
		t.Parallel()

		key, wallet := mustGenerateBoundaryWallet(t)

		t.Run("invalid_boundary_signature", func(t *testing.T) {
			tdb := newSoulLifecycleTestDB()
			s := newBoundaryCoverageServer(tdb, &fakeSoulPackStore{})
			seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)

			body := mustMarshalJSON(t, soulConfirmAppendBoundaryRequest{
				BoundaryID:      "boundary-1",
				Category:        "refusal",
				Statement:       "I will not do that.",
				Signature:       "0xdeadbeef",
				IssuedAt:        "2026-03-05T12:00:00Z",
				ExpectedVersion: ptrInt(2),
				SelfAttestation: "0xabc",
			})
			ctx := adminCtx()
			ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
			ctx.Request.Body = body

			_, err := s.handleSoulAppendBoundary(ctx)
			requireBoundaryAppErrorCode(t, err, "app.bad_request")
		})

		t.Run("supersedes_missing_boundary", func(t *testing.T) {
			tdb := newSoulLifecycleTestDB()
			s := newBoundaryCoverageServer(tdb, &fakeSoulPackStore{})
			seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)
			tdb.qBoundary.On("First", mock.AnythingOfType("*models.SoulAgentBoundary")).Return(theoryErrors.ErrItemNotFound).Once()

			statement := boundaryTestStatementNoDo
			body, _ := json.Marshal(soulConfirmAppendBoundaryRequest{
				BoundaryID:      boundaryTestID1,
				Category:        boundaryTestCategoryRefusal,
				Statement:       statement,
				Supersedes:      "missing-boundary",
				Signature:       mustSignBoundaryStatement(t, key, statement),
				IssuedAt:        "2026-03-05T12:00:00Z",
				ExpectedVersion: ptrInt(2),
				SelfAttestation: "0xabc",
			})
			ctx := adminCtx()
			ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
			ctx.Request.Body = body

			_, err := s.handleSoulAppendBoundary(ctx)
			requireBoundaryAppErrorCode(t, err, "app.bad_request")
		})

		t.Run("boundary_exists_in_registration", func(t *testing.T) {
			tdb := newSoulLifecycleTestDB()
			packs := &fakeSoulPackStore{}
			s := newBoundaryCoverageServer(tdb, packs)
			seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)

			base := validBoundaryBaseRegistrationV2(t, key, soulLifecycleTestAgentIDHex, wallet)
			bodyBytes := mustMarshalJSON(t, base)
			packs.objects = map[string]fakePut{
				soulRegistrationS3Key(soulLifecycleTestAgentIDHex): {key: soulRegistrationS3Key(soulLifecycleTestAgentIDHex), body: bodyBytes},
			}

			statement := "I will not do that."
			body := mustMarshalJSON(t, soulConfirmAppendBoundaryRequest{
				BoundaryID:      "boundary-existing",
				Category:        "refusal",
				Statement:       statement,
				Signature:       mustSignBoundaryStatement(t, key, statement),
				IssuedAt:        "2026-03-05T12:00:00Z",
				ExpectedVersion: ptrInt(2),
				SelfAttestation: "0xabc",
			})
			ctx := adminCtx()
			ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
			ctx.Request.Body = body

			_, err := s.handleSoulAppendBoundary(ctx)
			requireBoundaryAppErrorCode(t, err, "app.conflict")
		})

		t.Run("missing_registration_conflicts", func(t *testing.T) {
			tdb := newSoulLifecycleTestDB()
			s := newBoundaryCoverageServer(tdb, &fakeSoulPackStore{})
			seedBoundaryPortalAccess(t, tdb, soulLifecycleTestAgentIDHex, wallet, 2)

			statement := "I will not do that."
			body := mustMarshalJSON(t, soulConfirmAppendBoundaryRequest{
				BoundaryID:      "boundary-missing",
				Category:        "refusal",
				Statement:       statement,
				Signature:       mustSignBoundaryStatement(t, key, statement),
				IssuedAt:        "2026-03-05T12:00:00Z",
				ExpectedVersion: ptrInt(2),
				SelfAttestation: "0xabc",
			})
			ctx := adminCtx()
			ctx.Params = map[string]string{"agentId": soulLifecycleTestAgentIDHex}
			ctx.Request.Body = body

			_, err := s.handleSoulAppendBoundary(ctx)
			requireBoundaryAppErrorCode(t, err, "app.conflict")
		})
	})
}

func TestSortSoulBoundariesByAddedAt_AllNil(t *testing.T) {
	t.Parallel()

	items := []*models.SoulAgentBoundary{nil, nil}
	sortSoulBoundariesByAddedAt(items)
	if len(items) != 2 || items[0] != nil || items[1] != nil {
		t.Fatalf("unexpected items: %#v", items)
	}
}

func ptrInt(v int) *int {
	return &v
}
