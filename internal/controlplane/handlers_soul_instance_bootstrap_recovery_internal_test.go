package controlplane

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestSoulInstanceGetRegistrationMintConversation_ReturnsRecoveryWithoutQueueOrLeaks(t *testing.T) {
	tdb := newMintConversationTestDB()
	s := newMintConversationServer(tdb)
	s.enqueueHostedGenesisMessage = func(_ context.Context, msg hostedgenesis.QueueMessage) error {
		t.Fatalf("status/recovery reads must not depend on hosted genesis SQS: %#v", msg)
		return nil
	}
	reg := mintConversationHandleReg()
	expectMintConversationInstanceKey(t, tdb, mintConversationInstanceReadTestRawKey, soulInstanceBootstrapTestInstanceSlug)
	stubMintConversationRegistration(t, tdb, reg)
	stubSoulInstanceBootstrapDomainAndInstance(t, tdb, reg.DomainNormalized, soulInstanceBootstrapTestInstanceSlug)
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)
	stubFailedRecoveryLegacyMintConversation(t, tdb, reg, now)
	stubFailedRecoveryHostedGenesisSession(t, tdb, reg, now)

	resp, err := s.handleSoulInstanceGetRegistrationMintConversation(newSoulInstanceBootstrapContext(
		map[string]string{"authorization": "Bearer " + mintConversationInstanceReadTestRawKey},
		nil,
		map[string]string{"id": reg.ID, "conversationId": mintConversationTestConversationID},
	))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	var out hostedGenesisConversationResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	assertHostedGenesisFailedRecoveryProjection(t, out.Conversation)
	assertHostedGenesisResponseNoForbiddenValues(t, resp.Body, hostedGenesisStatusForbiddenValues())
	assertHostedGenesisStatusAuditNoLeaks(t, tdb, append(hostedGenesisStatusForbiddenValues(), soulInstanceBootstrapTestInstanceSlug, reg.AgentID, mintConversationTestConversationID))
}

func stubFailedRecoveryLegacyMintConversation(t *testing.T, tdb *mintConversationTestDB, reg models.SoulAgentRegistration, now time.Time) {
	t.Helper()
	stubMintConversationConversation(t, tdb, models.SoulAgentMintConversation{
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Model:          "anthropic:claude-sonnet-4-6",
		Messages:       encodeMintConversationBlob(`[{"role":"user","content":"raw transcript secret"},{"role":"assistant","content":"private answer"}]`),
		Status:         models.SoulMintConversationStatusFailed,
		LatestTurnID:   "turn-secret",
		RequestID:      "req-failed",
		CorrelationID:  "corr-safe",
		IdempotencyKey: "legacy-row-has-explicit-session",
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      now,
	})
}

func stubFailedRecoveryHostedGenesisSession(t *testing.T, tdb *mintConversationTestDB, reg models.SoulAgentRegistration, now time.Time) {
	t.Helper()
	dbSession := failedRecoveryHostedGenesisSessionFixture(t, reg, now)
	tdb.qHosted.On("First", mock.AnythingOfType("*models.HostedGenesisSession")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.HostedGenesisSession](t, args, 0)
		*dest = dbSession
	}).Once()
}

func failedRecoveryHostedGenesisSessionFixture(t *testing.T, reg models.SoulAgentRegistration, now time.Time) models.HostedGenesisSession {
	t.Helper()
	session := models.HostedGenesisSession{
		InstanceSlug:   soulInstanceBootstrapTestInstanceSlug,
		RegistrationID: reg.ID,
		AgentID:        reg.AgentID,
		ConversationID: mintConversationTestConversationID,
		Status:         string(hostedgenesis.StatusFailed),
		Model:          "anthropic:claude-sonnet-4-6",
		LatestTurnID:   "turn-secret",
		MessageCount:   2,
		TurnLedger:     failedRecoveryTurnLedger(now),
		RequestID:      "req-failed",
		TraceIDs:       failedRecoveryTraceIDs(),
		Failure:        failedRecoveryDeclarationFailure(),
		CreatedAt:      time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      now,
		CompletedAt:    now,
	}
	if err := session.BeforeCreate(); err != nil {
		t.Fatalf("session fixture: %v", err)
	}
	return session
}

func failedRecoveryTurnLedger(now time.Time) []hostedgenesis.TurnLedgerEntry {
	return []hostedgenesis.TurnLedgerEntry{{
		TurnID:         "turn-secret",
		IdempotencyKey: "idem-failed",
		RequestHash:    strings.Repeat("a", 64),
		ChargedCredits: soulMintConversationStreamBaseCredits,
		MessageCount:   2,
		AcceptedAt:     now.Add(-5 * time.Minute),
	}}
}

func failedRecoveryTraceIDs() *hostedgenesis.TraceIDs {
	return &hostedgenesis.TraceIDs{HostRequestID: "req-failed", CorrelationID: "corr-safe", IdempotencyKey: "idem-failed"}
}

func failedRecoveryDeclarationFailure() *hostedgenesis.Failure {
	return &hostedgenesis.Failure{
		Code:      hostedgenesis.FailureCodeDeclarationExtractionFailed,
		Message:   hostedGenesisFailureMessage(hostedGenesisFailureDeclarationExtractionFailed),
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			Reason:            hostedGenesisFailureDeclarationExtractionFailed,
			MaxAttempts:       3,
			RetryAfterSeconds: 30,
		},
	}
}

func assertHostedGenesisFailedRecoveryProjection(t *testing.T, conversation hostedGenesisConversationProjection) {
	t.Helper()
	if conversation.Status != models.SoulMintConversationStatusFailed || conversation.Failure == nil {
		t.Fatalf("expected durable failed recovery guidance, got %#v", conversation)
	}
	if conversation.Failure.Code != hostedGenesisFailureDeclarationExtractionFailed || conversation.Failure.Recovery.Action != hostedGenesisRecoveryRetrySameStep {
		t.Fatalf("expected declaration-extraction retry guidance, got %#v", conversation.Failure)
	}
}

func hostedGenesisStatusForbiddenValues() []string {
	return []string{
		"raw transcript secret",
		"private answer",
		mintConversationInstanceReadTestRawKey,
		"Bearer ",
		"provider_secret",
		"microvm_endpoint_token",
		"shell-auth-token",
	}
}

func assertHostedGenesisResponseNoForbiddenValues(t *testing.T, body []byte, forbiddenValues []string) {
	t.Helper()
	text := string(body)
	for _, forbidden := range forbiddenValues {
		if strings.Contains(text, forbidden) {
			t.Fatalf("status/recovery response leaked forbidden value %q: %s", forbidden, text)
		}
	}
}

func assertHostedGenesisStatusAuditNoLeaks(t *testing.T, tdb *mintConversationTestDB, forbiddenValues []string) {
	t.Helper()
	if len(tdb.auditModels) == 0 {
		t.Fatalf("expected instance status read audit event")
	}
	auditText := ""
	for _, entry := range tdb.auditModels {
		auditText += entry.Actor + "\n" + entry.Action + "\n" + entry.Target + "\n"
	}
	assertHostedGenesisAuditNoForbiddenValues(t, auditText, forbiddenValues)
	if !strings.Contains(auditText, "soul.mint_conversation.instance_read") || !strings.Contains(auditText, "sha256:") || !strings.Contains(auditText, "status=200") {
		t.Fatalf("expected safe hashed audit categories/status only, got %s", auditText)
	}
}

func assertHostedGenesisAuditNoForbiddenValues(t *testing.T, auditText string, forbiddenValues []string) {
	t.Helper()
	for _, forbidden := range append(forbiddenValues, "raw transcript secret", "private answer") {
		if strings.Contains(auditText, forbidden) {
			t.Fatalf("audit event leaked forbidden value %q: %s", forbidden, auditText)
		}
	}
}
