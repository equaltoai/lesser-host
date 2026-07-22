package controlplane

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/stretchr/testify/mock"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
)

const hostedGenesisBenignCredentialSafetyProse = "Never expose a private key or bearer token."

func TestHostedGenesisSessionProjectionFallbackAndTraceNil(t *testing.T) {
	t.Parallel()

	session := testHostedGenesisSessionProjectionBase()
	session.Status = string(hostedgenesis.StatusFailed)
	session.Failure = nil

	resp := buildHostedGenesisConversationResponseFromSession(session, nil, hostedGenesisProjectionOptions{})
	if resp.Conversation.Status != string(hostedgenesis.StatusFailed) || resp.Conversation.Failure == nil {
		t.Fatalf("expected failed fallback projection, got %#v", resp)
	}
	if resp.Conversation.Failure.Code != string(hostedgenesis.FailureCodeInvalidCompletionState) {
		t.Fatalf("unexpected fallback failure: %#v", resp.Conversation.Failure)
	}
	if traceCorrelationID(nil) != "" || traceIdempotencyKey(nil) != "" || traceLesserRequestID(nil) != "" || traceHostRequestID(nil) != "" {
		t.Fatalf("nil trace accessors should return empty strings")
	}

	if got := hostedGenesisFailureFromSession(nil); got == nil || got.Code != hostedGenesisFailureInvalidCompletionState {
		t.Fatalf("nil failure fallback = %#v", got)
	}
}

func TestHostedGenesisFailureProjectionMatchesCompatibilityAndSanitizesDetail(t *testing.T) {
	t.Parallel()

	session := testHostedGenesisSessionProjectionBase()
	session.Status = string(hostedgenesis.StatusFailed)
	session.Failure = &hostedgenesis.Failure{
		Code:    hostedgenesis.FailureCodeInvalidProducedDeclarations,
		Message: "provider output contained private transcript text",
		Recovery: hostedgenesis.Recovery{
			Action: hostedgenesis.RecoveryActionRestartSoulBootstrap,
			Reason: string(hostedgenesis.DeclarationCodeCapabilities),
		},
	}
	fromSession := hostedGenesisFailureFromSession(session.Failure)
	fromCompatibility := hostedGenesisFailureFromReason(string(hostedgenesis.DeclarationCodeCapabilities))
	if fromSession == nil || fromCompatibility == nil {
		t.Fatalf("expected both failure projections, session=%#v compatibility=%#v", fromSession, fromCompatibility)
	}
	if fromSession.Code != fromCompatibility.Code || fromSession.Message != fromCompatibility.Message ||
		fromSession.Recovery.Action != fromCompatibility.Recovery.Action || fromSession.Recovery.Reason != fromCompatibility.Recovery.Reason {
		t.Fatalf("terminal failure projections disagree: session=%#v compatibility=%#v", fromSession, fromCompatibility)
	}
	if strings.Contains(fromSession.Message, "private transcript") || strings.Contains(fromSession.Recovery.Reason, "private") {
		t.Fatalf("failure projection leaked private detail: session=%#v", fromSession)
	}

	response := buildHostedGenesisConversationResponseFromSession(session, nil, hostedGenesisProjectionOptions{})
	if response.Conversation.Status != string(hostedgenesis.StatusFailed) || response.Conversation.Failure == nil ||
		response.Conversation.Failure.Code != fromSession.Code || response.Conversation.Failure.Recovery.Reason != fromSession.Recovery.Reason {
		t.Fatalf("response failure projection disagrees with session projection: %#v", response.Conversation.Failure)
	}
}

func TestHostedGenesisProducedDeclarationsFromSessionTrustsCheckpointHash(t *testing.T) {
	t.Parallel()

	raw := string(mustMarshalJSON(t, testMintConversationDecl()))
	requestID := "req-session"
	session := testHostedGenesisDeclarationReadySession(raw, requestID)
	conv := &models.SoulAgentMintConversation{
		AgentID:              session.AgentID,
		ConversationID:       session.ConversationID,
		ProducedDeclarations: models.EncodeSoulMintConversationBlob(raw),
	}

	produced := buildHostedGenesisProducedDeclarationsFromSession(session, conv, requestID)
	if produced == nil || produced.Evidence.Source != "host_conversation" || produced.DeclarationHash != session.DeclarationCheckpoint.DeclarationHash {
		t.Fatalf("expected session-bound declarations, got %#v", produced)
	}

	session.DeclarationCheckpoint.DeclarationHash = "sha256:" + strings.Repeat("b", 64)
	if got := buildHostedGenesisProducedDeclarationsFromSession(session, conv, requestID); got != nil {
		t.Fatalf("hash mismatch should hide compatibility declarations, got %#v", got)
	}

	invalidRaw := "{"
	session = testHostedGenesisDeclarationReadySession(invalidRaw, requestID)
	conv.ProducedDeclarations = models.EncodeSoulMintConversationBlob(invalidRaw)
	if got := buildHostedGenesisProducedDeclarationsFromSession(session, conv, requestID); got != nil {
		t.Fatalf("invalid compatibility declarations should stay hidden, got %#v", got)
	}

	if got := buildHostedGenesisProducedDeclarationsFromSession(session, nil, requestID); got != nil {
		t.Fatalf("missing compatibility row should not publish declarations, got %#v", got)
	}
}

func TestHostedGenesisSessionProjectionIncludesInProgressMessages(t *testing.T) {
	t.Parallel()

	acceptedAt := time.Date(2026, 3, 7, 12, 1, 0, 0, time.UTC)
	session := testHostedGenesisSessionProjectionBase()
	session.Status = string(hostedgenesis.StatusInProgress)
	session.MessageCount = 1
	session.TurnLedger = []hostedgenesis.TurnLedgerEntry{{
		TurnID:         "turn-session",
		MessageCount:   1,
		ChargedCredits: soulMintConversationStreamBaseCredits,
		AcceptedAt:     acceptedAt,
	}}
	conv := &models.SoulAgentMintConversation{
		AgentID:        session.AgentID,
		ConversationID: session.ConversationID,
		Messages:       models.EncodeSoulMintConversationBlob(`[{"role":"user","content":"hello while waiting"}]`),
	}

	resp := buildHostedGenesisConversationResponseFromSession(session, conv, hostedGenesisProjectionOptions{RequestID: "req-visible"})
	if len(resp.Conversation.Messages) != 1 || resp.Conversation.MessagesTruncated {
		t.Fatalf("expected one untruncated in-progress transcript message, got %#v", resp.Conversation)
	}
	if got := resp.Conversation.Messages[0]; got.ID != "msg_000001" || got.Order != 1 || got.Role != hostedGenesisTranscriptRoleUser || got.Content != "hello while waiting" || got.CreatedAt == nil || !got.CreatedAt.Equal(acceptedAt) {
		t.Fatalf("unexpected in-progress message projection: %#v", got)
	}
}

func TestHostedGenesisSessionProjectionIncludesAssistantReadyMessages(t *testing.T) {
	t.Parallel()

	acceptedAt := time.Date(2026, 3, 7, 12, 1, 0, 0, time.UTC)
	session := testHostedGenesisSessionProjectionBase()
	session.Status = string(hostedgenesis.StatusAssistantTurnReady)
	session.MessageCount = 2
	session.TurnLedger = []hostedgenesis.TurnLedgerEntry{{
		TurnID:         "turn-session",
		MessageCount:   1,
		ChargedCredits: soulMintConversationStreamBaseCredits,
		AcceptedAt:     acceptedAt,
	}}
	conv := &models.SoulAgentMintConversation{
		AgentID:        session.AgentID,
		ConversationID: session.ConversationID,
		Messages:       models.EncodeSoulMintConversationBlob(`[{"role":"user","content":"hello host"},{"role":"assistant","content":"hello lesser"}]`),
	}

	resp := buildHostedGenesisConversationResponseFromSession(session, conv, hostedGenesisProjectionOptions{RequestID: "req-visible"})
	if len(resp.Conversation.Messages) != 2 || resp.Conversation.MessagesTruncated {
		t.Fatalf("expected two untruncated transcript messages, got %#v", resp.Conversation)
	}
	if got := resp.Conversation.Messages[0]; got.ID != "msg_000001" || got.Order != 1 || got.Role != hostedGenesisTranscriptRoleUser || got.Content != "hello host" || got.CreatedAt == nil || !got.CreatedAt.Equal(acceptedAt) {
		t.Fatalf("unexpected user message projection: %#v", got)
	}
	if got := resp.Conversation.Messages[1]; got.ID != "msg_000002" || got.Order != 2 || got.Role != hostedGenesisTranscriptRoleAssistant || got.Content != "hello lesser" || got.CreatedAt != nil {
		t.Fatalf("unexpected assistant message projection: %#v", got)
	}
}

func TestHostedGenesisSessionProjectionBoundsMessagesAndContent(t *testing.T) {
	t.Parallel()

	session := testHostedGenesisSessionProjectionBase()
	messages := make([]soulMintConversationMessage, 0, hostedGenesisTranscriptMaxMessages+2)
	for i := 0; i < hostedGenesisTranscriptMaxMessages+1; i++ {
		messages = append(messages, soulMintConversationMessage{Role: hostedGenesisTranscriptRoleUser, Content: "message"})
	}
	messages = append(messages, soulMintConversationMessage{Role: hostedGenesisTranscriptRoleAssistant, Content: strings.Repeat("x", hostedGenesisTranscriptMaxContentRunes+5)})
	conv := &models.SoulAgentMintConversation{
		AgentID:        session.AgentID,
		ConversationID: session.ConversationID,
		Messages:       models.EncodeSoulMintConversationBlob(string(mustMarshalJSON(t, messages))),
	}

	projected, bounded, redacted := buildHostedGenesisConversationMessages(session, conv)
	if len(projected) != hostedGenesisTranscriptMaxMessages || !bounded || redacted {
		t.Fatalf("expected bounded unredacted transcript projection, len=%d bounded=%v redacted=%v", len(projected), bounded, redacted)
	}
	if projected[0].Order != 3 || projected[len(projected)-1].Order != hostedGenesisTranscriptMaxMessages+2 || !projected[len(projected)-1].Truncated {
		t.Fatalf("unexpected bounded transcript entries: first=%#v last=%#v", projected[0], projected[len(projected)-1])
	}
	if len([]rune(projected[len(projected)-1].Content)) != hostedGenesisTranscriptMaxContentRunes {
		t.Fatalf("expected assistant content cap, got %d", len([]rune(projected[len(projected)-1].Content)))
	}
}

func TestHostedGenesisSessionProjectionRedactsOnlySecretShapedMessages(t *testing.T) {
	t.Parallel()

	session := testHostedGenesisSessionProjectionBase()
	conv := &models.SoulAgentMintConversation{
		AgentID:        session.AgentID,
		ConversationID: session.ConversationID,
	}
	benign := *conv
	benign.Messages = models.EncodeSoulMintConversationBlob(`[{"role":"user","content":"` + hostedGenesisBenignCredentialSafetyProse + `"},{"role":"assistant","content":"Correct: defensive prose must remain visible."}]`)
	projected, bounded, redacted := buildHostedGenesisConversationMessages(session, &benign)
	if len(projected) != 2 || bounded || redacted || projected[0].Content != hostedGenesisBenignCredentialSafetyProse {
		t.Fatalf("benign credential-safety prose must remain visible, got %#v bounded=%v redacted=%v", projected, bounded, redacted)
	}

	unsafe := *conv
	unsafe.Messages = models.EncodeSoulMintConversationBlob(`[{"role":"user","content":"Keep the prior safe message."},{"role":"assistant","content":"AWS_SECRET_ACCESS_KEY=do-not-project"},{"role":"assistant","content":"Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345"},{"role":"assistant","content":"sk-ant-abcdefghijklmnopqrstuvwxyz012345"}]`)
	projected, bounded, redacted = buildHostedGenesisConversationMessages(session, &unsafe)
	if len(projected) != 4 || bounded || !redacted || projected[0].Content != "Keep the prior safe message." {
		t.Fatalf("secret-shaped entries must not erase safe transcript entries, got %#v bounded=%v redacted=%v", projected, bounded, redacted)
	}
	for _, index := range []int{1, 2, 3} {
		if projected[index].Content != hostedGenesisTranscriptRedactedContent || !projected[index].Redacted {
			t.Fatalf("secret-shaped transcript entry must be explicitly redacted: %#v", projected[index])
		}
	}
}

func TestHostedGenesisSessionProjectionRejectsMismatchedCompatibilityIdentity(t *testing.T) {
	t.Parallel()

	session := testHostedGenesisSessionProjectionBase()
	mismatched := models.SoulAgentMintConversation{ConversationID: session.ConversationID}
	mismatched.AgentID = "0x" + strings.Repeat("99", 32)
	if projected, _, _ := buildHostedGenesisConversationMessages(session, &mismatched); len(projected) != 0 {
		t.Fatalf("mismatched compatibility row must not project transcript, got %#v", projected)
	}
}

func TestHostedGenesisSessionProjectionIncludesSafeFailedTranscriptWithSignals(t *testing.T) {
	t.Parallel()

	session := testHostedGenesisSessionProjectionBase()
	session.Status = string(hostedgenesis.StatusFailed)
	session.Failure = testHostedGenesisFailure(hostedgenesis.FailureCodeAssistantTurnFailed)
	conv := &models.SoulAgentMintConversation{
		AgentID:        session.AgentID,
		ConversationID: session.ConversationID,
		Messages:       models.EncodeSoulMintConversationBlob(`[{"role":"user","content":"Never share a private key or bearer token."},{"role":"assistant","content":"private_key=abcdefghijklmnopqrstuvwxyz012345"}]`),
	}

	resp := buildHostedGenesisConversationResponseFromSession(session, conv, hostedGenesisProjectionOptions{RequestID: "req-terminal"})
	if len(resp.Conversation.Messages) != 2 || resp.Conversation.MessagesTruncated || !resp.Conversation.MessagesRedacted {
		t.Fatalf("failed status should retain safe transcript and signal redaction, got %#v", resp.Conversation)
	}
	if resp.Conversation.Messages[0].Content != "Never share a private key or bearer token." || resp.Conversation.Messages[0].Redacted {
		t.Fatalf("benign defensive prose was not preserved: %#v", resp.Conversation.Messages[0])
	}
	if resp.Conversation.Messages[1].Content != hostedGenesisTranscriptRedactedContent || !resp.Conversation.Messages[1].Redacted {
		t.Fatalf("secret-shaped content was not redacted: %#v", resp.Conversation.Messages[1])
	}
	raw := string(mustMarshalJSON(t, resp))
	if strings.Contains(raw, "abcdefghijklmnopqrstuvwxyz012345") || !strings.Contains(raw, `"messages_redacted":true`) {
		t.Fatalf("failed transcript projection leaked a secret or omitted the redaction signal: %s", raw)
	}
}

func TestHostedGenesisFinalizeSessionGates(t *testing.T) {
	t.Parallel()

	raw := string(mustMarshalJSON(t, testMintConversationDecl()))
	session := testHostedGenesisDeclarationReadySession(raw, "req-gate")
	conv := &models.SoulAgentMintConversation{
		AgentID:              session.AgentID,
		ConversationID:       session.ConversationID,
		ProducedDeclarations: models.EncodeSoulMintConversationBlob(raw),
	}
	if appErr := requireHostedGenesisFinalizeDeclarationsMatchSession(session, conv); appErr != nil {
		t.Fatalf("expected valid session/compatibility match: %v", appErr)
	}

	session.DeclarationCheckpoint.DeclarationHash = "sha256:" + strings.Repeat("c", 64)
	if appErr := requireHostedGenesisFinalizeDeclarationsMatchSession(session, conv); appErr == nil || appErr.Message != "conversation has invalid produced declarations" {
		t.Fatalf("expected hash mismatch conflict, got %v", appErr)
	}

	failed := testHostedGenesisSessionProjectionBase()
	failed.Status = string(hostedgenesis.StatusFailed)
	failed.Failure = testHostedGenesisFailure(hostedgenesis.FailureCodeMissingProducedDeclarations)
	if appErr := requireHostedGenesisSessionReadyForFinalize(failed, "not ready", "missing declarations"); appErr == nil || appErr.Message != "missing declarations" {
		t.Fatalf("expected missing-declaration gate, got %v", appErr)
	}
}

func testHostedGenesisSessionProjectionBase() *models.HostedGenesisSession {
	now := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	return &models.HostedGenesisSession{
		InstanceSlug:   soulInstanceBootstrapTestInstanceSlug,
		RegistrationID: "reg-session",
		AgentID:        "0x" + strings.Repeat("42", 32),
		ConversationID: "conv-session",
		Status:         string(hostedgenesis.StatusInProgress),
		LatestTurnID:   "turn-session",
		MessageCount:   2,
		RequestID:      "req-session",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func testHostedGenesisDeclarationReadySession(raw string, requestID string) *models.HostedGenesisSession {
	session := testHostedGenesisSessionProjectionBase()
	sum := sha256.Sum256([]byte(raw))
	digest := hex.EncodeToString(sum[:])
	now := time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC)
	session.Status = string(hostedgenesis.StatusDeclarationReady)
	session.CompletedAt = now
	session.DeclarationCheckpoint = &hostedgenesis.DeclarationCheckpoint{
		DeclarationID:   "decl_" + digest[:16],
		DeclarationHash: "sha256:" + digest,
		CheckpointRef:   "checkpoint://hosted-genesis/" + session.ConversationID + "/declaration/" + digest[:16],
		ProducedAt:      now,
		RegistrationID:  session.RegistrationID,
		ConversationID:  session.ConversationID,
		AgentID:         session.AgentID,
		MessageCount:    session.MessageCount,
		Model:           "anthropic:claude-sonnet-4-6",
		SchemaVersion:   hostedgenesis.DeclarationSchemaVersionV2,
		GuidanceVersion: hostedgenesis.GuidanceVersionV2,
		RequestID:       requestID,
	}
	return session
}

func TestHostedGenesisSessionHelperBranches(t *testing.T) {
	t.Parallel()

	if err := validateHostedGenesisRequestIDs(nil); err != nil {
		t.Fatalf("nil request ids should pass: %v", err)
	}
	for name, req := range map[string]soulMintConversationRequest{
		"conversation": {ConversationID: "../bad"},
		"idempotency":  {IdempotencyKey: strings.Repeat("x", 129)},
		"correlation":  {CorrelationID: "bad space"},
		"lesser":       {LesserRequestID: "bad/slash"},
	} {
		req := req
		t.Run(name, func(t *testing.T) {
			if err := validateHostedGenesisRequestIDs(&req); err == nil {
				t.Fatalf("expected invalid %s request id", name)
			}
		})
	}

	for _, status := range []hostedgenesis.Status{hostedgenesis.StatusCreated, hostedgenesis.StatusAssistantTurnReady} {
		if err := requireHostedGenesisSessionAcceptsTurn(&models.HostedGenesisSession{Status: string(status)}); err != nil {
			t.Fatalf("status %s should accept turn: %v", status, err)
		}
	}
	if err := requireHostedGenesisSessionAcceptsTurn(&models.HostedGenesisSession{Status: string(hostedgenesis.StatusFailed)}); err == nil {
		t.Fatalf("failed session should not accept a new turn")
	}

	session := hostedGenesisTurnSession{modelSet: "anthropic:claude-sonnet-4-6"}
	if err := applyHostedGenesisSessionModel(&session, "openai:gpt-5"); err == nil {
		t.Fatalf("expected model conflict")
	}
	session = hostedGenesisTurnSession{}
	if err := applyHostedGenesisSessionModel(&session, "openai:gpt-5"); err != nil || session.modelSet != "openai:gpt-5" {
		t.Fatalf("expected stored model adoption, model=%q err=%v", session.modelSet, err)
	}
	if err := assignHostedGenesisTurnID(nil); err == nil {
		t.Fatalf("nil session should fail turn id assignment")
	}
	if err := assignHostedGenesisTurnID(&session); err != nil || !strings.HasPrefix(session.turnID, "turn_") {
		t.Fatalf("expected generated turn id, got %q err=%v", session.turnID, err)
	}
}

func TestHostedGenesisProcessingStatusesAreWaitOnly(t *testing.T) {
	t.Parallel()

	for _, status := range []hostedgenesis.Status{hostedgenesis.StatusInProgress} {
		if hostedGenesisStatusAcceptsTurn(status) {
			t.Fatalf("processing status %s must not accept a new owner turn", status)
		}
		if !hostedGenesisStatusRequiresWait(status) {
			t.Fatalf("processing status %s should return wait-only projection", status)
		}
	}
}

func TestHostedGenesisSessionCloneBranches(t *testing.T) {
	t.Parallel()

	if cloneHostedGenesisSession(nil) != nil {
		t.Fatalf("nil clone should stay nil")
	}
	raw := string(mustMarshalJSON(t, testMintConversationDecl()))
	session := testHostedGenesisDeclarationReadySession(raw, "req-clone")
	session.Failure = testHostedGenesisFailure(hostedgenesis.FailureCodeOperatorActionRequired)
	session.TraceIDs = &hostedgenesis.TraceIDs{HostRequestID: "req-clone", CorrelationID: "corr", IdempotencyKey: "idem", LesserRequestID: "lesser"}
	session.TurnLedger = []hostedgenesis.TurnLedgerEntry{{TurnID: "turn-1", IdempotencyKey: "idem", RequestHash: strings.Repeat("a", 64)}}
	cloned := cloneHostedGenesisSession(session)
	if cloned == session || cloned.DeclarationCheckpoint == session.DeclarationCheckpoint || cloned.Failure == session.Failure || cloned.TraceIDs == session.TraceIDs || &cloned.TurnLedger[0] == &session.TurnLedger[0] {
		t.Fatalf("expected deep clone of pointer/slice fields")
	}
	if err := addHostedGenesisSessionWrite(nil, session, false, 1, hostedgenesis.StatusInProgress); err == nil {
		t.Fatalf("nil transaction should fail")
	}
}

func TestHostedGenesisSessionReplayGateBranches(t *testing.T) {
	t.Parallel()

	session := testHostedGenesisDeclarationReadySession(string(mustMarshalJSON(t, testMintConversationDecl())), "req-clone")
	if ok, reason := hostedGenesisSessionCompletionReplayReady(nil); ok || reason != soulMintConversationCompleteReasonInvalidState {
		t.Fatalf("nil replay gate = ok %v reason %q", ok, reason)
	}
	if ok, reason := hostedGenesisSessionCompletionReplayReady(session); !ok || reason != "" {
		t.Fatalf("declaration-ready replay gate = ok %v reason %q", ok, reason)
	}
	badCheckpoint := *session
	badCheckpoint.DeclarationCheckpoint = nil
	if ok, reason := hostedGenesisSessionCompletionReplayReady(&badCheckpoint); ok || reason != soulMintConversationCompleteReasonInvalidDeclarations {
		t.Fatalf("invalid checkpoint replay gate = ok %v reason %q", ok, reason)
	}
	failed := testHostedGenesisSessionProjectionBase()
	failed.Status = string(hostedgenesis.StatusFailed)
	failed.Failure = testHostedGenesisFailure(hostedgenesis.FailureCodeInvalidProducedDeclarations)
	if ok, reason := hostedGenesisSessionCompletionReplayReady(failed); ok || reason != soulMintConversationCompleteReasonInvalidDeclarations {
		t.Fatalf("failed invalid replay gate = ok %v reason %q", ok, reason)
	}
	created := testHostedGenesisSessionProjectionBase()
	created.Status = string(hostedgenesis.StatusCreated)
	if ok, reason := hostedGenesisSessionCompletionReplayReady(created); ok || reason != "" {
		t.Fatalf("progress replay gate = ok %v reason %q", ok, reason)
	}
	unknown := testHostedGenesisSessionProjectionBase()
	unknown.Status = "bogus"
	if ok, reason := hostedGenesisSessionCompletionReplayReady(unknown); ok || reason != soulMintConversationCompleteReasonInvalidState {
		t.Fatalf("unknown replay gate = ok %v reason %q", ok, reason)
	}
}

func TestMintConversationFinalizeRequestParsingBranches(t *testing.T) {
	t.Parallel()

	ctx := adminCtx()
	ctx.Request.Body = mustMarshalJSON(t, map[string]any{
		"boundary_signatures": map[string]string{"b1": "0xsig"},
		"issued_at":           "2026-03-07T12:00:00Z",
		"expected_version":    2,
		"self_attestation":    "0xself",
	})
	req, issuedAt, expected, selfSig, err := parseMintConversationFinalizeRequestBody(ctx)
	if err != nil || issuedAt.IsZero() || expected == nil || *expected != 2 || selfSig != "0xself" || req.BoundarySignatures["b1"] == "" {
		t.Fatalf("expected valid finalize request, req=%#v issued=%v expected=%v self=%q err=%v", req, issuedAt, expected, selfSig, err)
	}

	for name, body := range map[string]map[string]any{
		"missing_signatures": {"issued_at": "2026-03-07T12:00:00Z", "expected_version": 2, "self_attestation": "0xself"},
		"missing_issued_at":  {"boundary_signatures": map[string]string{"b1": "0xsig"}, "expected_version": 2, "self_attestation": "0xself"},
		"bad_issued_at":      {"boundary_signatures": map[string]string{"b1": "0xsig"}, "issued_at": "bad", "expected_version": 2, "self_attestation": "0xself"},
		"missing_expected":   {"boundary_signatures": map[string]string{"b1": "0xsig"}, "issued_at": "2026-03-07T12:00:00Z", "self_attestation": "0xself"},
		"negative_expected":  {"boundary_signatures": map[string]string{"b1": "0xsig"}, "issued_at": "2026-03-07T12:00:00Z", "expected_version": -1, "self_attestation": "0xself"},
		"missing_self":       {"boundary_signatures": map[string]string{"b1": "0xsig"}, "issued_at": "2026-03-07T12:00:00Z", "expected_version": 2},
	} {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			ctx := adminCtx()
			ctx.Request.Body = mustMarshalJSON(t, body)
			if _, _, _, _, err := parseMintConversationFinalizeRequestBody(ctx); err == nil {
				t.Fatalf("expected parse error for %s", name)
			}
		})
	}
}

func TestMintConversationInstanceTrustRequestParsingBranches(t *testing.T) {
	t.Parallel()

	empty := adminCtx()
	req, issuedAt, expected, err := parseMintConversationFinalizeInstanceTrustRequestBody(empty, 7)
	if err != nil || issuedAt.IsZero() || expected == nil || *expected != 7 || req.BoundarySignatures == nil {
		t.Fatalf("expected empty body defaults, req=%#v issued=%v expected=%v err=%v", req, issuedAt, expected, err)
	}

	ctx := adminCtx()
	ctx.Request.Body = mustMarshalJSON(t, map[string]any{
		"boundary_signatures": map[string]string{"b1": "0xsig"},
		"issued_at":           "2026-03-07T12:00:00.123Z",
		"expected_version":    8,
	})
	req, issuedAt, expected, err = parseMintConversationFinalizeInstanceTrustRequestBody(ctx, 7)
	if err != nil || issuedAt.IsZero() || expected == nil || *expected != 8 || req.BoundarySignatures["b1"] == "" {
		t.Fatalf("expected valid instance-trust request, req=%#v issued=%v expected=%v err=%v", req, issuedAt, expected, err)
	}

	for name, body := range map[string]map[string]any{
		"self_attestation":  {"self_attestation": "0xself"},
		"bad_issued_at":     {"issued_at": "bad"},
		"negative_expected": {"expected_version": -1},
	} {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			ctx := adminCtx()
			ctx.Request.Body = mustMarshalJSON(t, body)
			if _, _, _, err := parseMintConversationFinalizeInstanceTrustRequestBody(ctx, 7); err == nil {
				t.Fatalf("expected instance-trust parse error for %s", name)
			}
		})
	}
}

func TestMintConversationProviderKeyEnvBranches(t *testing.T) {
	s := &Server{}
	t.Setenv("OPENAI_API_KEY", "openai-test")
	if key, appErr := s.apiKeyForMintConversationModel(t.Context(), "openai:gpt-test"); appErr != nil || key != "openai-test" {
		t.Fatalf("openai env key = %q err=%v", key, appErr)
	}
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-test")
	if key, appErr := s.apiKeyForMintConversationModel(t.Context(), "anthropic:claude-test"); appErr != nil || key != "anthropic-test" {
		t.Fatalf("anthropic env key = %q err=%v", key, appErr)
	}
	if _, appErr := s.apiKeyForMintConversationModel(t.Context(), "other:model"); appErr == nil || appErr.Message != mintConversationUnsupportedModelSetMessage {
		t.Fatalf("unsupported model should fail, got %v", appErr)
	}
}

func TestParseAndValidateMintConversationDeclarationsRejectsInvalidShapes(t *testing.T) {
	t.Parallel()

	valid := testMintConversationDecl()
	validBytes := mustMarshalJSON(t, valid)
	var base map[string]any
	if err := json.Unmarshal(validBytes, &base); err != nil {
		t.Fatalf("unmarshal valid declarations: %v", err)
	}

	cases := map[string]map[string]any{
		"missing_capabilities": cloneDeclarationMapWithout(base, "capabilities"),
		"missing_boundaries":   cloneDeclarationMapWithout(base, "boundaries"),
		"missing_transparency": cloneDeclarationMapWithout(base, "transparency"),
		"capabilities_object":  cloneDeclarationMapWith(base, "capabilities", map[string]any{}),
		"boundaries_object":    cloneDeclarationMapWith(base, "boundaries", map[string]any{}),
		"nil_transparency":     cloneDeclarationMapWith(base, "transparency", nil),
	}
	for name, body := range cases {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			if _, appErr := parseAndValidateMintConversationDeclarations(string(mustMarshalJSON(t, body))); appErr == nil {
				t.Fatalf("expected invalid declarations for %s", name)
			}
		})
	}
	if _, appErr := parseAndValidateMintConversationDeclarations(""); appErr == nil {
		t.Fatalf("empty declarations should fail")
	}
	if _, appErr := parseAndValidateMintConversationDeclarations("{"); appErr == nil {
		t.Fatalf("malformed declarations should fail")
	}
}

func cloneDeclarationMapWithout(in map[string]any, key string) map[string]any {
	out := cloneDeclarationMapWith(in, "", nil)
	delete(out, key)
	return out
}

func cloneDeclarationMapWith(in map[string]any, key string, value any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	if key != "" {
		out[key] = value
	}
	return out
}

func TestMintConversationManagedENSMaterialBranches(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	if _, appErr := (*Server)(nil).prepareMintConversationManagedENSMaterial(t.Context(), nil, nil, nil, now); appErr == nil {
		t.Fatalf("nil prepare inputs should fail")
	}
	s := &Server{}
	regV2 := &soul.RegistrationFileV2{}
	if _, appErr := s.prepareMintConversationManagedENSMaterial(t.Context(), &models.SoulAgentIdentity{LocalID: "bad space"}, &models.Instance{Slug: "inst1"}, regV2, now); appErr == nil {
		t.Fatalf("invalid local id should fail")
	}
	if _, appErr := s.prepareMintConversationManagedENSMaterial(t.Context(), &models.SoulAgentIdentity{LocalID: "alice"}, &models.Instance{Slug: "bad slug"}, regV2, now); appErr == nil {
		t.Fatalf("invalid instance slug should fail")
	}

	db, queries := newTestDBWithModelQueries("*models.SoulAgentENSResolution", "*models.SoulAgentChannel")
	s = &Server{store: store.New(db)}
	s.cfg.ENSGatewayResolverAddress = "0x0000000000000000000000000000000000000001"
	queries[0].On("First", mock.AnythingOfType("*models.SoulAgentENSResolution")).Return(theoryErrors.ErrItemNotFound).Once()
	queries[1].On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()
	identity := &models.SoulAgentIdentity{AgentID: "0x" + strings.Repeat("42", 32), LocalID: "alice", Domain: "alice.example", Wallet: "0x" + strings.Repeat("11", 20), LifecycleStatus: "active"}
	regV2 = &soul.RegistrationFileV2{Created: now.Format(time.RFC3339), Lifecycle: soul.LifecycleV2{Status: "active"}}
	regV2.Endpoints.MCP = "https://lesser.example/mcp"
	regV2.Endpoints.ActivityPub = "https://lesser.example/ap"
	regV2.SelfDescription.Purpose = "test purpose"
	material, appErr := s.prepareMintConversationManagedENSMaterial(t.Context(), identity, &models.Instance{Slug: "inst1"}, regV2, now)
	if appErr != nil || material == nil || material.ensName == "" || material.channel == nil || material.resolution == nil {
		t.Fatalf("expected managed ENS material, material=%#v err=%v", material, appErr)
	}

	if appErr := (*Server)(nil).persistMintConversationManagedENSMaterial(t.Context(), nil); appErr == nil {
		t.Fatalf("nil persist inputs should fail")
	}
}

func TestMintConversationLegacyFinalizeHelperBranches(t *testing.T) {
	t.Parallel()

	declRaw := string(mustMarshalJSON(t, testMintConversationDecl()))
	ready := &models.SoulAgentMintConversation{Status: models.SoulMintConversationStatusCompleted, ProducedDeclarations: models.EncodeSoulMintConversationBlob(declRaw)}
	if ok, reason := mintConversationCompletionReplayReady(ready); !ok || reason != "" {
		t.Fatalf("ready replay = ok %v reason %q", ok, reason)
	}
	missing := &models.SoulAgentMintConversation{Status: models.SoulMintConversationStatusCompleted}
	if ok, reason := mintConversationCompletionReplayReady(missing); ok || reason != soulMintConversationCompleteReasonMissingDeclarations {
		t.Fatalf("missing replay = ok %v reason %q", ok, reason)
	}
	failed := &models.SoulAgentMintConversation{Status: models.SoulMintConversationStatusFailed}
	if ok, reason := mintConversationCompletionReplayReady(failed); ok || reason != soulMintConversationCompleteReasonInvalidState {
		t.Fatalf("failed replay = ok %v reason %q", ok, reason)
	}
	if appErr := requireMintConversationReadyForFinalize(ready, "not complete", "missing"); appErr != nil {
		t.Fatalf("ready finalize should pass: %v", appErr)
	}
	inProgress := &models.SoulAgentMintConversation{Status: models.SoulMintConversationStatusInProgress}
	if appErr := requireMintConversationReadyForFinalize(inProgress, "not complete", "missing"); appErr == nil || appErr.Message != "not complete" {
		t.Fatalf("expected not-complete conflict, got %v", appErr)
	}
}

func TestMintConversationFinalizeBeginParsingBranches(t *testing.T) {
	t.Parallel()

	ctx := adminCtx()
	ctx.Request.Body = mustMarshalJSON(t, map[string]any{"boundary_signatures": map[string]string{"b": "0xsig"}})
	if req, err := parseMintConversationFinalizeBeginRequestBody(ctx); err != nil || req.BoundarySignatures["b"] == "" {
		t.Fatalf("expected valid begin request, req=%#v err=%v", req, err)
	}
	ctx = adminCtx()
	ctx.Request.Body = mustMarshalJSON(t, map[string]any{})
	if _, err := parseMintConversationFinalizeBeginRequestBody(ctx); err == nil {
		t.Fatalf("missing boundary signatures should fail")
	}
}

func TestHostedGenesisFinalizeGateErrorBranches(t *testing.T) {
	t.Parallel()

	if appErr := requireHostedGenesisSessionReadyForFinalize(nil, "not ready", "missing"); appErr == nil {
		t.Fatalf("nil session should fail finalize gate")
	}
	session := testHostedGenesisDeclarationReadySession(string(mustMarshalJSON(t, testMintConversationDecl())), "req-gate-extra")
	if appErr := requireHostedGenesisFinalizeDeclarationsMatchSession(session, nil); appErr == nil || appErr.Message != "conversation has no produced declarations" {
		t.Fatalf("expected missing compatibility declarations, got %v", appErr)
	}
}
