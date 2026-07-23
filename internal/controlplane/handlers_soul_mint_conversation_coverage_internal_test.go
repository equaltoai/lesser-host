package controlplane

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
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
	if strings.TrimSpace(conv.IdempotencyKey) == "" {
		stubHostedGenesisSessionFromConversation(t, tdb, conv)
	}
}

func stubHostedGenesisSessionFromConversation(t *testing.T, tdb *mintConversationTestDB, conv models.SoulAgentMintConversation) {
	t.Helper()
	tdb.qHosted.On("First", mock.AnythingOfType("*models.HostedGenesisSession")).Return(nil).Run(func(args mock.Arguments) {
		dest, ok := args.Get(0).(*models.HostedGenesisSession)
		if !ok || dest == nil {
			t.Fatalf("expected *models.HostedGenesisSession, got %#v", args.Get(0))
		}
		*dest = hostedGenesisSessionFromLegacyConversationForTest(tdb, conv)
	}).Maybe()
}

func hostedGenesisSessionFromLegacyConversationForTest(tdb *mintConversationTestDB, conv models.SoulAgentMintConversation) models.HostedGenesisSession {
	decodeMintConversationFields(&conv)
	registrationID := legacyHostedGenesisRegistrationIDForTest(tdb)
	now := firstTime(conv.UpdatedAt, conv.CompletedAt, conv.CreatedAt, time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC))
	if conv.CreatedAt.IsZero() {
		conv.CreatedAt = now
	}
	session := baseHostedGenesisSessionFromLegacyConversationForTest(conv, registrationID, now)
	applyHostedGenesisTurnLedgerFromLegacyForTest(&session, conv, now)
	applyHostedGenesisDeclarationFromLegacyForTest(&session, conv, registrationID)
	applyHostedGenesisFailureFromLegacyForTest(&session, now)
	_ = session.BeforeCreate()
	return session
}

func legacyHostedGenesisRegistrationIDForTest(tdb *mintConversationTestDB) string {
	if tdb != nil && tdb.lastReg != nil && strings.TrimSpace(tdb.lastReg.ID) != "" {
		return strings.TrimSpace(tdb.lastReg.ID)
	}
	return "reg-1"
}

func baseHostedGenesisSessionFromLegacyConversationForTest(conv models.SoulAgentMintConversation, registrationID string, now time.Time) models.HostedGenesisSession {
	status := hostedgenesis.NormalizeStatus(conv.Status)
	if status == hostedgenesis.Status("") || !hostedgenesis.IsAllowedStatus(status) {
		status = hostedgenesis.StatusFailed
	}
	messageCount := mintConversationMessageCount(&conv)
	session := models.HostedGenesisSession{
		InstanceSlug:   soulInstanceBootstrapTestInstanceSlug,
		RegistrationID: registrationID,
		AgentID:        conv.AgentID,
		ConversationID: conv.ConversationID,
		Status:         string(status),
		Model:          conv.Model,
		LatestTurnID:   conv.LatestTurnID,
		MessageCount:   messageCount,
		RequestID:      conv.RequestID,
		TraceIDs:       &hostedgenesis.TraceIDs{HostRequestID: conv.RequestID, CorrelationID: conv.CorrelationID, IdempotencyKey: conv.IdempotencyKey},
		CreatedAt:      firstTime(conv.CreatedAt, now),
		UpdatedAt:      now,
		CompletedAt:    conv.CompletedAt,
	}
	if status == hostedgenesis.StatusCreated || status == hostedgenesis.StatusInProgress || status == hostedgenesis.StatusAssistantTurnReady {
		binding := hostedgenesis.DeclarationCandidateBinding{
			InstanceSlug: session.InstanceSlug, RegistrationID: session.RegistrationID, AgentID: session.AgentID,
			ConversationID: session.ConversationID, SourceTurnID: firstNonEmpty(session.LatestTurnID, "turn-legacy-test"), Model: session.Model,
		}
		candidate, _ := hostedgenesis.NewDeclarationCandidate(binding, session.CreatedAt)
		if strings.Contains(models.DecodeSoulMintConversationBlob(conv.Messages), "Hosted Genesis owner review") {
			candidate = controlplaneCompleteReviewCandidateForTest(binding, session.CreatedAt)
		}
		session.DeclarationCandidate = candidate
	}
	return session
}

func controlplaneCompleteReviewCandidateForTest(binding hostedgenesis.DeclarationCandidateBinding, now time.Time) *hostedgenesis.DeclarationCandidate {
	candidate, _ := hostedgenesis.NewDeclarationCandidate(binding, now)
	calls := []struct{ name, body string }{
		{hostedgenesis.DeclarationToolIdentityPut, `{"section":{"summary":"I am the tenant-bound Hosted Genesis actor.","notes":[]}}`},
		{hostedgenesis.DeclarationToolPhilosophyPut, `{"section":{"summary":"I prefer auditable durable truth over implicit authority.","notes":[]}}`},
		{hostedgenesis.DeclarationToolDisciplinePut, `{"section":{"summary":"I ground, act, record, and re-ground at each checkpoint.","notes":[]}}`},
		{hostedgenesis.DeclarationToolBoundariesPut, `{"section":{"summary":"I remain within the managed instance and require owner authority.","notes":[]}}`},
		{hostedgenesis.DeclarationToolSoulPut, `{"section":{"summary":"Exact reviewed truth is load-bearing.","notes":[],"refusals":[{"bypass":"skip the candidate hash check","invariant":"exact reviewed bytes remain authoritative","closestSafePath":"submit a matching structural affirmation"},{"bypass":"reuse another tenant session","invariant":"tenant and session guards must match","closestSafePath":"restart in the correct managed instance"},{"bypass":"call a provider after affirmation","invariant":"finalization remains deterministic","closestSafePath":"publish the exact affirmed candidate bytes"}]},"selfDescription":{"purpose":"Construct a typed Hosted Genesis declaration.","constraints":"Remain tenant bound.","commitments":"Preserve exact durable truth.","limitations":"No provider after affirmation.","authoredBy":"agent","mintingModel":"anthropic:claude-sonnet-4-6"},"capabilities":[],"transparency":{"modelProviderUncertainty":"Provider content is self-declared.","operationalNotes":"Host validates every section.","selfDeclaredNotice":"Self-declared until publication."}}`},
	}
	for i, call := range calls {
		var payload map[string]any
		_ = json.Unmarshal([]byte(call.body), &payload)
		payload["candidateRevision"] = candidate.Revision
		payload["candidateHash"] = candidate.CandidateHash
		payloadBytes, _ := json.Marshal(payload)
		next, result, _ := hostedgenesis.ApplyDeclarationTool(candidate, hostedgenesis.DeclarationToolRequest{
			ToolName: call.name, ToolCallID: fmt.Sprintf("call-%d", i), ExpectedRevision: candidate.Revision,
			ExpectedHash: candidate.CandidateHash, SourceTurnID: binding.SourceTurnID, Payload: payloadBytes,
		}, now.Add(time.Duration(i)*time.Second))
		if result.Accepted {
			candidate = next
		}
	}
	return candidate
}

func applyHostedGenesisTurnLedgerFromLegacyForTest(session *models.HostedGenesisSession, conv models.SoulAgentMintConversation, now time.Time) {
	if session == nil {
		return
	}
	if session.MessageCount <= 0 && strings.TrimSpace(conv.LatestTurnID) != "" {
		session.MessageCount = 1
	}
	if strings.TrimSpace(conv.LatestTurnID) == "" {
		return
	}
	session.TurnLedger = []hostedgenesis.TurnLedgerEntry{{
		TurnID:         conv.LatestTurnID,
		IdempotencyKey: conv.IdempotencyKey,
		RequestHash:    strings.Repeat("a", 64),
		ChargedCredits: maxInt64(conv.ChargedCredits, soulMintConversationStreamBaseCredits),
		MessageCount:   maxInt(session.MessageCount, 1),
		AcceptedAt:     firstTime(conv.CreatedAt, now),
	}}
}

func applyHostedGenesisDeclarationFromLegacyForTest(session *models.HostedGenesisSession, conv models.SoulAgentMintConversation, registrationID string) {
	if session == nil || !legacyConversationNeedsDeclarationCheckpointForTest(conv) {
		return
	}
	messageCount := maxInt(session.MessageCount, 1)
	session.MessageCount = messageCount
	requestID := firstNonEmpty(conv.RequestID, "req-test")
	produced := buildHostedGenesisProducedDeclarations(&conv, registrationID, requestID, messageCount)
	if produced == nil {
		markHostedGenesisLegacyDeclarationFailureForTest(session, conv)
		return
	}
	if !setHostedGenesisLegacyDeclarationCheckpointForTest(session, conv, produced, registrationID, requestID, messageCount) {
		session.Status = string(hostedgenesis.StatusFailed)
		session.DeclarationCheckpoint = nil
	}
}

func legacyConversationNeedsDeclarationCheckpointForTest(conv models.SoulAgentMintConversation) bool {
	return hostedgenesis.NormalizeStatus(conv.Status) == hostedgenesis.StatusDeclarationReady || strings.TrimSpace(conv.Status) == models.SoulMintConversationStatusCompleted
}

func setHostedGenesisLegacyDeclarationCheckpointForTest(session *models.HostedGenesisSession, conv models.SoulAgentMintConversation, produced *hostedGenesisProducedDeclarations, registrationID string, requestID string, messageCount int) bool {
	producedAt, _ := time.Parse(time.RFC3339Nano, produced.ProducedAt)
	session.Status = string(hostedgenesis.StatusDeclarationReady)
	session.DeclarationCheckpoint = &hostedgenesis.DeclarationCheckpoint{
		DeclarationID:   produced.DeclarationID,
		DeclarationHash: produced.DeclarationHash,
		CheckpointRef:   "checkpoint://hosted-genesis/" + conv.ConversationID + "/declaration/" + strings.TrimPrefix(produced.DeclarationHash, "sha256:")[:16],
		ProducedAt:      producedAt,
		RegistrationID:  registrationID,
		ConversationID:  conv.ConversationID,
		AgentID:         conv.AgentID,
		MessageCount:    messageCount,
		Model:           conv.Model,
		SchemaVersion:   produced.Declarations.SchemaVersion,
		GuidanceVersion: produced.Declarations.GuidanceVersion,
		RequestID:       requestID,
	}
	return session.DeclarationCheckpoint.Validate() == nil
}

func markHostedGenesisLegacyDeclarationFailureForTest(session *models.HostedGenesisSession, conv models.SoulAgentMintConversation) {
	session.Status = string(hostedgenesis.StatusFailed)
	if strings.TrimSpace(conv.ProducedDeclarations) == "" {
		session.Failure = testHostedGenesisFailure(hostedgenesis.FailureCodeMissingProducedDeclarations)
		return
	}
	session.Failure = testHostedGenesisFailure(hostedgenesis.FailureCodeInvalidProducedDeclarations)
}

func applyHostedGenesisFailureFromLegacyForTest(session *models.HostedGenesisSession, now time.Time) {
	if session == nil || hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusFailed {
		return
	}
	if session.Failure == nil {
		session.Failure = testHostedGenesisFailure(hostedgenesis.FailureCodeInvalidCompletionState)
	}
	session.CompletedAt = firstTime(session.CompletedAt, now)
}

func testHostedGenesisFailure(code hostedgenesis.FailureCode) *hostedgenesis.Failure {
	action := hostedgenesis.RecoveryActionRefreshState
	if code == hostedgenesis.FailureCodeMissingProducedDeclarations || code == hostedgenesis.FailureCodeInvalidProducedDeclarations {
		action = hostedgenesis.RecoveryActionRestartSoulBootstrap
	}
	return &hostedgenesis.Failure{
		Code:      code,
		Message:   hostedGenesisFailureMessage(string(code)),
		Retryable: false,
		Recovery:  hostedgenesis.Recovery{Action: action, Reason: string(code)},
	}
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxInt64(a int64, b int64) int64 {
	if a > b {
		return a
	}
	return b
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
	assertMintConversationFinalizeSigningInput(t, out)
	if out.FinalizeRequestTemplate.ExpectedVersion != out.ExpectedVersion || out.FinalizeRequestTemplate.IssuedAt != out.IssuedAt {
		t.Fatalf("unexpected finalize request template: %#v", out.FinalizeRequestTemplate)
	}
}

func assertMintConversationFinalizeSigningInput(t *testing.T, out soulMintConversationFinalizeBeginResponse) {
	t.Helper()
	if out.SelfAttestationSigning == nil ||
		out.SelfAttestationSigning.CanonicalJSON == "" ||
		out.SelfAttestationSigning.MessageHex != out.DigestHex {
		t.Fatalf("unexpected self attestation signing input: %#v", out.SelfAttestationSigning)
	}
}

func testMintConversationFinalizePreflightAliasReturnsPreviewAndDigest(t *testing.T) {
	t.Helper()
	out := beginFinalizeCoverageResponse(t, true)
	if out.DigestHex == "" || out.SelfAttestationSigning == nil || out.SelfAttestationSigning.CanonicalJSON == "" {
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
	identity.Status = models.SoulAgentStatusActive
	identity.LifecycleStatus = models.SoulAgentStatusActive

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
	appErr, ok := err.(*apptheory.AppTheoryError)
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
	identity.Status = models.SoulAgentStatusActive
	identity.LifecycleStatus = models.SoulAgentStatusActive

	stubMintConversationRegistration(t, tdb, f.reg)
	stubMintConversationDomainAccess(t, tdb, f.reg.DomainNormalized)
	stubMintConversationConversation(t, tdb, f.makeConv(models.SoulMintConversationStatusCompleted))
	stubMintConversationIdentity(t, tdb, identity, nil)

	body := mustMarshalJSON(t, soulMintConversationFinalizeBeginRequest{BoundarySignatures: map[string]string{"other": "0x00"}})
	_, err := s.handleSoulBeginFinalizeMintConversation(f.makeCtx(body))
	appErr, ok := err.(*apptheory.AppTheoryError)
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
	identity.Status = models.SoulAgentStatusActive
	identity.LifecycleStatus = models.SoulAgentStatusActive

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
	appErr, ok := err.(*apptheory.AppTheoryError)
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
	identity.Status = models.SoulAgentStatusActive
	identity.LifecycleStatus = models.SoulAgentStatusActive

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
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok || appErr.Message != soulInstanceBootstrapTestInvalidRegSig {
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
	identity.Status = models.SoulAgentStatusActive
	identity.LifecycleStatus = models.SoulAgentStatusActive

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
	appErr, ok := err.(*apptheory.AppTheoryError)
	if !ok || appErr.Message != wantMessage {
		t.Fatalf("expected %q error, got %#v", wantMessage, err)
	}
}
