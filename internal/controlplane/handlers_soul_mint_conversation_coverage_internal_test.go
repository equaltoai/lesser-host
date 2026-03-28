package controlplane

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func stubMintConversationConversation(t *testing.T, tdb *mintConversationTestDB, conv models.SoulAgentMintConversation) {
	t.Helper()

	tdb.qConv.On("First", mock.AnythingOfType("*models.SoulAgentMintConversation")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentMintConversation)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentMintConversation, got %#v", args.Get(0))
		}
		*dest = conv
	}).Once()
}

func stubMintConversationIdentity(t *testing.T, tdb *mintConversationTestDB, identity *models.SoulAgentIdentity, err error) {
	t.Helper()

	call := tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(err).Once()
	if err != nil {
		return
	}
	call.Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.SoulAgentIdentity)
		if !ok || dest == nil {
			t.Fatalf("expected *models.SoulAgentIdentity, got %#v", args.Get(0))
		}
		*dest = *identity
	})
}

func TestMintConversationBeginFinalizeCoverage(t *testing.T) {
	t.Parallel()
	testMintConversationBeginFinalizeReturnsPreviewAndDigest(t)
	testMintConversationFinalizePreflightAliasReturnsPreviewAndDigest(t)
	testMintConversationBeginFinalizeRejectsPublishedRegistrations(t)
	testMintConversationBeginFinalizeRequiresBoundarySignatures(t)
	testMintConversationFinalizeRequiresExpectedVersion(t)
	testMintConversationFinalizeRejectsAdvancedVersion(t)
	testMintConversationFinalizeRequiresReloadOnVersionConflict(t)
	testMintConversationFinalizeRejectsInvalidRegistrationSignature(t)
}

type mintConversationFinalizeCoverageFixture struct {
	reg       models.SoulAgentRegistration
	decl      soulMintConversationProducedDeclarations
	declBytes []byte
}

func newMintConversationFinalizeCoverageFixture(t testing.TB) mintConversationFinalizeCoverageFixture {
	t.Helper()
	decl := testMintConversationDecl()
	declBytes, err := json.Marshal(decl)
	if err != nil {
		t.Fatalf("Marshal declarations: %v", err)
	}
	return mintConversationFinalizeCoverageFixture{
		reg: models.SoulAgentRegistration{
			ID:               "reg-1",
			Username:         "alice",
			DomainNormalized: "example.com",
			AgentID:          "0x" + strings.Repeat("33", 32),
		},
		decl:      decl,
		declBytes: declBytes,
	}
}

func (f mintConversationFinalizeCoverageFixture) makeCtx(body []byte) *apptheory.Context {
	ctx := adminCtx()
	ctx.Params = map[string]string{"id": f.reg.ID, "conversationId": mintConversationTestConversationID}
	ctx.Request.Body = body
	return ctx
}

func (f mintConversationFinalizeCoverageFixture) makeConv(status string) models.SoulAgentMintConversation {
	return models.SoulAgentMintConversation{
		AgentID:              f.reg.AgentID,
		ConversationID:       mintConversationTestConversationID,
		Status:               status,
		ProducedDeclarations: string(f.declBytes),
	}
}

func testMintConversationBeginFinalizeReturnsPreviewAndDigest(t *testing.T) {
	t.Helper()
	out := beginFinalizeCoverageResponse(t, false)
	if out.ExpectedVersion != 0 || out.NextVersion != 1 || !strings.HasPrefix(out.DigestHex, "0x") {
		t.Fatalf("unexpected begin finalize response: %#v", out)
	}
	if out.RegistrationPreview == nil || out.RegistrationPreview["version"] != "2" {
		t.Fatalf("expected v2 registration preview, got %#v", out.RegistrationPreview)
	}
	if out.DeclarationsPreview.SelfDescription.Purpose == "" || len(out.BoundaryRequirements) != 1 {
		t.Fatalf("expected declaration and boundary preview, got %#v", out)
	}
	if out.BoundaryRequirements[0].BoundaryID != "b1" || out.BoundaryRequirements[0].SignatureHex == "" || !strings.HasPrefix(out.BoundaryRequirements[0].DigestHex, "0x") {
		t.Fatalf("unexpected boundary requirement: %#v", out.BoundaryRequirements)
	}
	if out.SelfAttestationSigning.CanonicalJSON == "" || out.SelfAttestationSigning.MessageHex != out.DigestHex {
		t.Fatalf("unexpected self attestation signing input: %#v", out.SelfAttestationSigning)
	}
	if out.FinalizeRequestTemplate.ExpectedVersion != out.ExpectedVersion || out.FinalizeRequestTemplate.IssuedAt != out.IssuedAt {
		t.Fatalf("unexpected finalize request template: %#v", out.FinalizeRequestTemplate)
	}
}

func testMintConversationFinalizePreflightAliasReturnsPreviewAndDigest(t *testing.T) {
	t.Helper()
	out := beginFinalizeCoverageResponse(t, true)
	if out.DigestHex == "" || out.SelfAttestationSigning.CanonicalJSON == "" {
		t.Fatalf("expected alias preflight response, got %#v", out)
	}
}

func beginFinalizeCoverageResponse(t *testing.T, usePreflightAlias bool) soulMintConversationFinalizeBeginResponse {
	t.Helper()
	f := newMintConversationFinalizeCoverageFixture(t)
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity, key := testMintConversationIdentityAndKey()
	identity.AgentID = f.reg.AgentID

	stubMintConversationRegistration(t, tdb, f.reg)
	stubMintConversationDomainAccess(t, tdb, f.reg.DomainNormalized)
	stubMintConversationConversation(t, tdb, f.makeConv(models.SoulMintConversationStatusCompleted))
	stubMintConversationIdentity(t, tdb, identity, nil)

	boundarySig, err := crypto.Sign(accounts.TextHash(crypto.Keccak256([]byte(f.decl.Boundaries[0].Statement))), key)
	if err != nil {
		t.Fatalf("Sign boundary: %v", err)
	}
	body := mustMarshalJSON(t, soulMintConversationFinalizeBeginRequest{BoundarySignatures: map[string]string{"b1": "0x" + hex.EncodeToString(boundarySig)}})

	var (
		resp    *apptheory.Response
		callErr error
	)
	if usePreflightAlias {
		resp, callErr = s.handleSoulFinalizeMintConversationPreflight(f.makeCtx(body))
	} else {
		resp, callErr = s.handleSoulBeginFinalizeMintConversation(f.makeCtx(body))
	}
	if callErr != nil {
		t.Fatalf("unexpected error: %v", callErr)
	}

	var out soulMintConversationFinalizeBeginResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("Unmarshal response: %v", err)
	}
	return out
}

func testMintConversationBeginFinalizeRejectsPublishedRegistrations(t *testing.T) {
	t.Helper()
	f := newMintConversationFinalizeCoverageFixture(t)
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()
	identity.AgentID = f.reg.AgentID
	identity.SelfDescriptionVersion = 1

	stubMintConversationRegistration(t, tdb, f.reg)
	stubMintConversationDomainAccess(t, tdb, f.reg.DomainNormalized)
	stubMintConversationConversation(t, tdb, f.makeConv(models.SoulMintConversationStatusCompleted))
	stubMintConversationIdentity(t, tdb, identity, nil)

	body := mustMarshalJSON(t, soulMintConversationFinalizeBeginRequest{BoundarySignatures: map[string]string{"b1": "0x00"}})
	_, err := s.handleSoulBeginFinalizeMintConversation(f.makeCtx(body))
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != soulMintConversationAlreadyPublishedMessage {
		t.Fatalf("expected already published error, got %#v", err)
	}
}

func testMintConversationBeginFinalizeRequiresBoundarySignatures(t *testing.T) {
	t.Helper()
	f := newMintConversationFinalizeCoverageFixture(t)
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()
	identity.AgentID = f.reg.AgentID

	stubMintConversationRegistration(t, tdb, f.reg)
	stubMintConversationDomainAccess(t, tdb, f.reg.DomainNormalized)
	stubMintConversationConversation(t, tdb, f.makeConv(models.SoulMintConversationStatusCompleted))
	stubMintConversationIdentity(t, tdb, identity, nil)

	body := mustMarshalJSON(t, soulMintConversationFinalizeBeginRequest{BoundarySignatures: map[string]string{"other": "0x00"}})
	_, err := s.handleSoulBeginFinalizeMintConversation(f.makeCtx(body))
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != "missing boundary signature for b1" {
		t.Fatalf("expected missing boundary signature error, got %#v", err)
	}
}

func testMintConversationFinalizeRequiresExpectedVersion(t *testing.T) {
	t.Helper()
	f := newMintConversationFinalizeCoverageFixture(t)
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()
	identity.AgentID = f.reg.AgentID

	stubMintConversationRegistration(t, tdb, f.reg)
	stubMintConversationDomainAccess(t, tdb, f.reg.DomainNormalized)
	stubMintConversationConversation(t, tdb, f.makeConv(models.SoulMintConversationStatusCompleted))
	stubMintConversationIdentity(t, tdb, identity, nil)

	body := mustMarshalJSON(t, soulMintConversationFinalizeRequest{
		BoundarySignatures: map[string]string{"b1": "0x00"},
		IssuedAt:           "2026-03-05T12:00:00Z",
		SelfAttestation:    "0x00",
	})
	_, err := s.handleSoulFinalizeMintConversation(f.makeCtx(body))
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != "expected_version is required" {
		t.Fatalf("expected missing expected_version error, got %#v", err)
	}
}

func testMintConversationFinalizeRejectsAdvancedVersion(t *testing.T) {
	t.Helper()
	assertMintConversationFinalizeIdentityVersionError(t, 2, 0, "agent has advanced beyond this version")
}

func testMintConversationFinalizeRequiresReloadOnVersionConflict(t *testing.T) {
	t.Helper()
	assertMintConversationFinalizeIdentityVersionError(t, 0, 1, "version conflict; reload and try again")
}

func testMintConversationFinalizeRejectsInvalidRegistrationSignature(t *testing.T) {
	t.Helper()
	f := newMintConversationFinalizeCoverageFixture(t)
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity, key := testMintConversationIdentityAndKey()
	identity.AgentID = f.reg.AgentID

	stubMintConversationRegistration(t, tdb, f.reg)
	stubMintConversationDomainAccess(t, tdb, f.reg.DomainNormalized)
	stubMintConversationConversation(t, tdb, f.makeConv(models.SoulMintConversationStatusCompleted))
	stubMintConversationIdentity(t, tdb, identity, nil)

	boundarySig, err := crypto.Sign(accounts.TextHash(crypto.Keccak256([]byte(f.decl.Boundaries[0].Statement))), key)
	if err != nil {
		t.Fatalf("Sign boundary: %v", err)
	}
	expectedVersion := 0
	body := mustMarshalJSON(t, soulMintConversationFinalizeRequest{
		BoundarySignatures: map[string]string{"b1": "0x" + hex.EncodeToString(boundarySig)},
		IssuedAt:           "2026-03-05T12:00:00Z",
		ExpectedVersion:    &expectedVersion,
		SelfAttestation:    "0x00",
	})
	_, err = s.handleSoulFinalizeMintConversation(f.makeCtx(body))
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != "invalid registration signature" {
		t.Fatalf("expected invalid registration signature error, got %#v", err)
	}
}

func assertMintConversationFinalizeIdentityVersionError(t *testing.T, identityVersion int, expectedVersion int, wantMessage string) {
	t.Helper()
	f := newMintConversationFinalizeCoverageFixture(t)
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	identity := testMintConversationIdentity()
	identity.AgentID = f.reg.AgentID
	identity.SelfDescriptionVersion = identityVersion

	stubMintConversationRegistration(t, tdb, f.reg)
	stubMintConversationDomainAccess(t, tdb, f.reg.DomainNormalized)
	stubMintConversationConversation(t, tdb, f.makeConv(models.SoulMintConversationStatusCompleted))
	stubMintConversationIdentity(t, tdb, identity, nil)

	body := mustMarshalJSON(t, soulMintConversationFinalizeRequest{
		BoundarySignatures: map[string]string{"b1": "0x00"},
		IssuedAt:           "2026-03-05T12:00:00Z",
		ExpectedVersion:    &expectedVersion,
		SelfAttestation:    "0x00",
	})
	_, err := s.handleSoulFinalizeMintConversation(f.makeCtx(body))
	appErr, ok := err.(*apptheory.AppError)
	if !ok || appErr.Message != wantMessage {
		t.Fatalf("expected %q error, got %#v", wantMessage, err)
	}
}
