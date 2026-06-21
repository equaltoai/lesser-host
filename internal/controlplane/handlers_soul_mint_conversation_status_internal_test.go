package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestMintConversationStatusProjection_LegacyCompletedMapsOnlyWithValidDeclarations(t *testing.T) {
	valid := string(mustMarshalJSON(t, testMintConversationDecl()))
	conv := &models.SoulAgentMintConversation{
		AgentID:              "0x" + strings.Repeat("22", 32),
		ConversationID:       "conv-legacy",
		Messages:             encodeMintConversationBlob(`[{"role":"user","content":"secret prompt"},{"role":"assistant","content":"secret reply"}]`),
		ProducedDeclarations: encodeMintConversationBlob(valid),
		Status:               models.SoulMintConversationStatusCompleted,
		RequestID:            "req-legacy",
		CreatedAt:            time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC),
		CompletedAt:          time.Date(2026, 3, 7, 12, 5, 0, 0, time.UTC),
	}
	proj := mintConversationStatusProjectionFromModel(conv, true)
	if proj.ConversationID != "conv-legacy" || proj.Status != models.SoulMintConversationStatusDeclarationReady || proj.Reason != "" || !proj.ProducedDeclarationsPresent || !proj.ProducedDeclarationsValid || proj.RequestID != "req-legacy" || proj.MessageCount != 2 {
		t.Fatalf("unexpected valid legacy projection: %#v", proj)
	}
	resp := buildHostedGenesisConversationResponse(conv, hostedGenesisProjectionOptions{RegistrationID: "reg-1", RequestID: "req-legacy", CollapseCreated: true})
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "secret prompt") || strings.Contains(string(body), "secret reply") {
		t.Fatalf("status response leaked raw transcript: %s", string(body))
	}

	conv.ProducedDeclarations = encodeMintConversationBlob(`{"private":true}`)
	proj = mintConversationStatusProjectionFromModel(conv, true)
	if proj.Status != models.SoulMintConversationStatusFailed || proj.Reason != hostedGenesisFailureInvalidProducedDeclarations || !proj.ProducedDeclarationsPresent || proj.ProducedDeclarationsValid {
		t.Fatalf("unexpected invalid legacy projection: %#v", proj)
	}
}

func TestMintConversationStatusProjection_CollapsesCreatedForLesserPath(t *testing.T) {
	conv := &models.SoulAgentMintConversation{ConversationID: "conv-created", Status: models.SoulMintConversationStatusCreated, RequestID: "req-created"}
	if got := mintConversationStatusProjectionFromModel(conv, true); got.Status != models.SoulMintConversationStatusInProgress {
		t.Fatalf("expected created to collapse to in_progress for Lesser projection, got %#v", got)
	}
	if got := mintConversationStatusProjectionFromModel(conv, false); got.Status != models.SoulMintConversationStatusCreated {
		t.Fatalf("expected created to remain available without collapse, got %#v", got)
	}
}

func TestHostedGenesisRequestHashIncludesConversationID(t *testing.T) {
	withoutConversation := hostedGenesisRequestHash("reg-1", "", "anthropic:claude-sonnet-4-6", "same turn")
	withConversation := hostedGenesisRequestHash("reg-1", "conv-2", "anthropic:claude-sonnet-4-6", "same turn")
	if withoutConversation == withConversation {
		t.Fatalf("idempotency request hash must distinguish existing conversation turns")
	}
}

func TestHostedGenesisFailureProjectionHelpers(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		reason   string
		action   string
		retry    bool
		message  string
		maxTries int
	}{
		{reason: hostedGenesisFailureLLMUnavailable, action: hostedGenesisRecoveryRetrySameStep, retry: true, message: "Assistant turn failed before declaration extraction.", maxTries: 3},
		{reason: hostedGenesisFailureDeclarationExtractionFailed, action: hostedGenesisRecoveryRetrySameStep, retry: true, message: "Declaration extraction failed.", maxTries: 3},
		{reason: hostedGenesisFailureInvalidCompletionState, action: hostedGenesisRecoveryRefreshState, message: "Conversation cannot be completed from the current state."},
		{reason: hostedGenesisFailureMissingProducedDeclarations, action: hostedGenesisRecoveryRestartSoulBootstrap, message: "Produced declarations are missing."},
		{reason: hostedGenesisFailureInvalidProducedDeclarations, action: hostedGenesisRecoveryRestartSoulBootstrap, message: "Produced declarations are invalid."},
		{reason: hostedGenesisFailureTenantBoundaryViolation, action: hostedGenesisRecoveryOperatorAction, message: "Conversation failed instance boundary validation."},
		{reason: hostedGenesisFailureOperatorActionRequired, action: hostedGenesisRecoveryOperatorAction, message: "Operator action is required."},
		{reason: "unknown", action: hostedGenesisRecoveryRetrySameStep, retry: true, message: "Assistant turn failed before declaration extraction.", maxTries: 3},
	} {
		tc := tc
		t.Run(tc.reason, func(t *testing.T) {
			t.Parallel()

			failure := hostedGenesisFailureFromReason(tc.reason)
			if failure.Message != tc.message ||
				failure.Retryable != tc.retry ||
				failure.Recovery.Action != tc.action ||
				failure.Recovery.MaxAttempts != tc.maxTries {
				t.Fatalf("unexpected failure projection: %#v", failure)
			}
		})
	}
}

func TestHostedGenesisSafeTokenHelper(t *testing.T) {
	t.Parallel()

	if got, ok := hostedGenesisSafeToken(" token-1:ok ", 16); !ok || got != "token-1:ok" {
		t.Fatalf("expected safe token, got %q ok=%v", got, ok)
	}
	if got, ok := hostedGenesisSafeToken(strings.Repeat("x", 17), 16); ok || got != "" {
		t.Fatalf("expected oversize token rejection, got %q ok=%v", got, ok)
	}
	if got, ok := hostedGenesisSafeToken("bad/token", 16); ok || got != "" {
		t.Fatalf("expected invalid token rejection, got %q ok=%v", got, ok)
	}
	if appErr := hostedGenesisBadRequest("trace_id"); appErr.Code != appErrCodeBadRequest || !strings.Contains(appErr.Message, "trace_id") {
		t.Fatalf("unexpected bad request error: %#v", appErr)
	}
}

func TestHostedGenesisTimeAndTraceHelpers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC)
	if firstTime(time.Time{}, now) != now || !firstTime(time.Time{}).IsZero() {
		t.Fatalf("firstTime mismatch")
	}
	if timePtrIfSet(time.Time{}) != nil || !timePtrIfSet(now).Equal(now) {
		t.Fatalf("time pointer helper mismatch")
	}
	if nilIfEmptyTrace(hostedGenesisTraceIDs{}) != nil ||
		nilIfEmptyTrace(hostedGenesisTraceIDs{HostRequestID: "req-1"}) == nil {
		t.Fatalf("trace nil helper mismatch")
	}
}

func TestHostedGenesisMessageCountHelper(t *testing.T) {
	t.Parallel()

	conv := &models.SoulAgentMintConversation{Messages: encodeMintConversationBlob(`[{"role":"user","content":"one"},{"role":"assistant","content":"two"}]`)}
	if mintConversationMessageCount(conv) != 2 ||
		mintConversationMessageCount(&models.SoulAgentMintConversation{Messages: encodeMintConversationBlob(`not-json`)}) != 0 ||
		mintConversationMessageCount(nil) != 0 {
		t.Fatalf("message count helper mismatch")
	}
}

func TestMintConversationStatusProjection_ProgressAndFailedStates(t *testing.T) {
	t.Parallel()

	for _, status := range []string{
		models.SoulMintConversationStatusInProgress,
		models.SoulMintConversationStatusAssistantTurnReady,
		models.SoulMintConversationStatusDeclarationExtractionPending,
	} {
		status := status
		t.Run(status, func(t *testing.T) {
			t.Parallel()

			conv := &models.SoulAgentMintConversation{ConversationID: "conv-" + status, Status: status, RequestID: "req-progress"}
			resp := buildHostedGenesisConversationResponse(conv, hostedGenesisProjectionOptions{RegistrationID: "reg-1", RequestID: "req-progress", CollapseCreated: true})
			if resp.Conversation.Status != status || resp.Conversation.PollAfterSeconds == 0 || resp.Conversation.Failure != nil {
				t.Fatalf("unexpected progress projection: %#v", resp)
			}
		})
	}

	failed := &models.SoulAgentMintConversation{
		ConversationID: "conv-failed",
		Status:         models.SoulMintConversationStatusFailed,
		StatusReason:   hostedGenesisFailureTenantBoundaryViolation,
		RequestID:      "req-failed",
	}
	resp := buildHostedGenesisConversationResponse(failed, hostedGenesisProjectionOptions{RegistrationID: "reg-1", RequestID: "req-failed"})
	if resp.Conversation.Status != models.SoulMintConversationStatusFailed ||
		resp.Conversation.Failure == nil ||
		resp.Conversation.Failure.Recovery.Action != hostedGenesisRecoveryOperatorAction {
		t.Fatalf("unexpected failed projection: %#v", resp)
	}
}
