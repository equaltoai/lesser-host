package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/v2"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// hostedGenesisRecoveryReconcileTimeout bounds the M16 controller get
// dispatch on the production recover path. Reconstruction queries real VM
// state through the constrained controller; it must fail closed within a
// bounded window rather than hang the recover request.
const hostedGenesisRecoveryReconcileTimeout = 10 * time.Second

func (s *Server) handleSoulInstanceRecoverMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	started := time.Now().UTC()
	convCtx, appErr := s.requireSoulInstanceBootstrapConversationContext(ctx)
	if appErr != nil {
		return nil, appErr
	}
	decodeMintConversationFields(convCtx.conv)
	if appErr := rejectOversizeSoulMintInstanceConversation(convCtx.conv); appErr != nil {
		return nil, appErr
	}

	if resp, handled, err := s.handleSoulInstanceRetryableMintConversationRecovery(ctx, convCtx, started); handled || err != nil {
		return resp, err
	}

	if !hostedGenesisSessionNeedsAssistantRecovery(convCtx.session) {
		resp, err := hostedGenesisConversationJSONFromSession(http.StatusOK, convCtx.session, convCtx.conv, hostedGenesisProjectionOptions{
			RegistrationID:  convCtx.reg.ID,
			RequestID:       strings.TrimSpace(ctx.RequestID),
			CollapseCreated: true,
		})
		if err != nil {
			return nil, err
		}
		s.recordSoulMintInstanceReadAudit(ctx, convCtx.key, convCtx.agentIDHex, convCtx.conversationID, soulMintInstanceReadRouteRecover, "noop", resp.Status, len(resp.Body), started)
		return resp, nil
	}

	// H1.3: when the session already carries a populated MicroVM lifecycle ref
	// (set by the H1.2 accept-path dispatch or the H1.3 extraction dispatch),
	// the recover path reaches production reconstruction by querying real VM
	// state through the M16 controller get command via the MicroVMDispatcher
	// seam. A dead/expired VM maps to a loud typed failure, never the silent
	// no-op reconstruction had before. A live VM that has not yet advanced the
	// durable Host status is returned as still-pending; the H1.1 workload's
	// assistant_turn_ready / declaration_ready / failed transitions are the
	// authoritative status advances Host reconciles against.
	if hostedGenesisSessionNeedsMicroVMReconciliation(convCtx.session) {
		reconciled, reconcileErr := s.reconcileHostedGenesisMicroVMRecovery(ctx, convCtx)
		if reconcileErr != nil {
			return nil, soulInstanceBootstrapConversationErrorFromAppError(reconcileErr)
		}
		resp, err := hostedGenesisConversationJSONFromSession(reconciled.httpStatus, reconciled.session, reconciled.conv, hostedGenesisProjectionOptions{
			RegistrationID:  convCtx.reg.ID,
			RequestID:       strings.TrimSpace(ctx.RequestID),
			CollapseCreated: true,
		})
		if err != nil {
			return nil, err
		}
		auditOutcome := "reconciled"
		if reconciled.terminalFailure {
			auditOutcome = "microvm_terminal"
		}
		s.recordSoulMintInstanceReadAudit(ctx, convCtx.key, convCtx.agentIDHex, convCtx.conversationID, soulMintInstanceReadRouteRecover, auditOutcome, resp.Status, len(resp.Body), started)
		return resp, nil
	}

	// No MicroVM lifecycle ref is populated: the session predates the H1.2
	// dispatch wiring (or the dispatch never landed). Re-dispatch the accepted
	// turn through the MicroVM path rather than silently stranding the turn.
	// H1.4: recovery is dispatch-only — it never re-runs a turn synchronously.
	// The retained sync assistant runner (hostedGenesisSyncAssistantFallbackEnabled)
	// is reachable only from the accept path's non-production guard, never from
	// recovery; an unwired dispatcher is a loud retryable failure, never a sync
	// LLM call and never a silent 200.
	turnSession, acceptedMessages, buildErr := hostedGenesisRecoveryTurnSession(convCtx)
	if buildErr != nil {
		return nil, soulInstanceBootstrapConversationErrorFromAppError(buildErr)
	}
	progressedSession, progressedConv, status, progressErr := s.dispatchHostedGenesisRecoveryTurn(
		ctx,
		mintConversationRegistrationContext{reg: convCtx.reg, inst: convCtx.inst, agentIDHex: convCtx.agentIDHex},
		turnSession,
		convCtx.conv,
		acceptedMessages,
	)
	if progressErr != nil {
		return nil, soulInstanceBootstrapConversationErrorFromAppError(progressErr)
	}
	resp, err := hostedGenesisConversationJSONFromSession(status, progressedSession, progressedConv, hostedGenesisProjectionOptions{
		RegistrationID:  convCtx.reg.ID,
		RequestID:       strings.TrimSpace(ctx.RequestID),
		CollapseCreated: true,
	})
	if err != nil {
		return nil, err
	}
	s.recordSoulMintInstanceReadAudit(ctx, convCtx.key, convCtx.agentIDHex, convCtx.conversationID, soulMintInstanceReadRouteRecover, "redispatched", resp.Status, len(resp.Body), started)
	return resp, nil
}

func (s *Server) handleSoulInstanceRetryableMintConversationRecovery(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext, started time.Time) (*apptheory.Response, bool, error) {
	if hostedGenesisSessionRequiresRestart(convCtx.session) {
		return nil, true, soulInstanceBootstrapError(
			soulInstanceBootstrapCodeConflict,
			"hosted genesis requires a fresh soul bootstrap",
			http.StatusConflict,
			map[string]any{
				"recovery_action": "restart_soul_bootstrap",
				"restart_path":    "/api/v1/soul/instance/agents/register/begin",
			},
		)
	}
	if hostedGenesisSessionCanRetryDeclarationExtraction(convCtx.session) {
		resp, err := s.handleSoulInstanceDeclarationExtractionRetry(ctx, convCtx, started)
		return resp, true, err
	}
	if hostedGenesisSessionCanRetryAssistantTurn(convCtx.session) ||
		hostedGenesisSessionHasPendingAssistantRetryFailure(convCtx.session) {
		resp, err := s.handleSoulInstanceAssistantTurnRetry(ctx, convCtx, started)
		return resp, true, err
	}
	if hostedGenesisSessionHasMicroVMUnavailableRetryFailure(convCtx.session) {
		if !convCtx.session.Failure.Retryable || convCtx.session.Failure.Recovery.MaxAttempts <= 0 {
			return nil, false, nil
		}
		if err := validateHostedGenesisMicroVMRecoveryCheckpoint(convCtx.session); err != nil {
			return nil, true, soulInstanceBootstrapConversationErrorFromAppError(newAppTheoryError(appErrCodeConflict, "conversation recovery checkpoint is unavailable"))
		}
		resp, err := s.handleSoulInstanceMicroVMUnavailableRetry(ctx, convCtx, started)
		return resp, true, err
	}
	return nil, false, nil
}

func (s *Server) handleSoulInstanceDeclarationExtractionRetry(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext, started time.Time) (*apptheory.Response, error) {
	progressedSession, progressedConv, progressErr := s.retryHostedGenesisFailedDeclarationExtraction(ctx, convCtx)
	if progressErr != nil {
		return nil, soulInstanceBootstrapConversationErrorFromAppError(progressErr)
	}
	resp, err := hostedGenesisConversationJSONFromSession(http.StatusAccepted, progressedSession, progressedConv, hostedGenesisProjectionOptions{
		RegistrationID:  convCtx.reg.ID,
		RequestID:       strings.TrimSpace(ctx.RequestID),
		CollapseCreated: true,
	})
	if err != nil {
		return nil, err
	}
	s.recordSoulMintInstanceReadAudit(ctx, convCtx.key, convCtx.agentIDHex, convCtx.conversationID, soulMintInstanceReadRouteRecover, "retry_declaration_extraction", resp.Status, len(resp.Body), started)
	return resp, nil
}

func (s *Server) handleSoulInstanceAssistantTurnRetry(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext, started time.Time) (*apptheory.Response, error) {
	progressedSession, progressedConv, auditOutcome, progressErr := s.retryHostedGenesisAssistantTurn(ctx, convCtx)
	if progressErr != nil {
		return nil, soulInstanceBootstrapConversationErrorFromAppError(progressErr)
	}
	resp, err := hostedGenesisConversationJSONFromSession(http.StatusAccepted, progressedSession, progressedConv, hostedGenesisProjectionOptions{
		RegistrationID:  convCtx.reg.ID,
		RequestID:       strings.TrimSpace(ctx.RequestID),
		CollapseCreated: true,
	})
	if err != nil {
		return nil, err
	}
	s.recordSoulMintInstanceReadAudit(ctx, convCtx.key, convCtx.agentIDHex, convCtx.conversationID, soulMintInstanceReadRouteRecover, auditOutcome, resp.Status, len(resp.Body), started)
	return resp, nil
}

func (s *Server) handleSoulInstanceMicroVMUnavailableRetry(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext, started time.Time) (*apptheory.Response, error) {
	progressedSession, progressedConv, progressErr := s.retryHostedGenesisMicroVMUnavailable(ctx, convCtx)
	if progressErr != nil {
		return nil, soulInstanceBootstrapConversationErrorFromAppError(progressErr)
	}
	resp, err := hostedGenesisConversationJSONFromSession(http.StatusAccepted, progressedSession, progressedConv, hostedGenesisProjectionOptions{
		RegistrationID:  convCtx.reg.ID,
		RequestID:       strings.TrimSpace(ctx.RequestID),
		CollapseCreated: true,
	})
	if err != nil {
		return nil, err
	}
	s.recordSoulMintInstanceReadAudit(ctx, convCtx.key, convCtx.agentIDHex, convCtx.conversationID, soulMintInstanceReadRouteRecover, "retry_microvm_unavailable", resp.Status, len(resp.Body), started)
	return resp, nil
}

func (s *Server) retryHostedGenesisAssistantTurn(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, string, *apptheory.AppTheoryError) {
	if hostedGenesisSessionHasPendingAssistantRetryFailure(convCtx.session) {
		session, conv, appErr := s.retryHostedGenesisPendingAssistantTurn(ctx, convCtx)
		return session, conv, "retry_pending_assistant_turn", appErr
	}
	session, conv, appErr := s.retryHostedGenesisFailedAssistantTurn(ctx, convCtx)
	return session, conv, "retry_assistant_turn", appErr
}

func hostedGenesisSessionRequiresRestart(session *models.HostedGenesisSession) bool {
	if session == nil || hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusFailed || session.Failure == nil {
		return false
	}
	return session.Failure.Recovery.Action == hostedgenesis.RecoveryActionRestartSoulBootstrap ||
		hostedGenesisDeclarationExtractionRetriesExhausted(session) ||
		hostedGenesisAssistantTurnRetriesExhausted(session)
}

func hostedGenesisSessionCanRetryDeclarationExtraction(session *models.HostedGenesisSession) bool {
	return hostedGenesisSessionHasRetryableDeclarationExtractionFailure(session) &&
		session.Failure.Recovery.MaxAttempts > 0
}

func hostedGenesisDeclarationExtractionRetriesExhausted(session *models.HostedGenesisSession) bool {
	return hostedGenesisSessionHasDeclarationExtractionRetryFailure(session) &&
		(!session.Failure.Retryable || session.Failure.Recovery.MaxAttempts <= 0)
}

func hostedGenesisSessionHasRetryableDeclarationExtractionFailure(session *models.HostedGenesisSession) bool {
	return hostedGenesisSessionHasDeclarationExtractionRetryFailure(session) &&
		session.Failure.Retryable
}

func hostedGenesisSessionHasDeclarationExtractionRetryFailure(session *models.HostedGenesisSession) bool {
	if session == nil || hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusFailed || session.Failure == nil {
		return false
	}
	return session.Failure.Code == hostedgenesis.FailureCodeDeclarationExtractionFailed &&
		session.Failure.Recovery.Action == hostedgenesis.RecoveryActionRetrySameStep
}

func hostedGenesisSessionCanRetryAssistantTurn(session *models.HostedGenesisSession) bool {
	return hostedGenesisSessionHasRetryableAssistantTurnFailure(session) &&
		session.Failure.Recovery.MaxAttempts > 0
}

func hostedGenesisAssistantTurnRetriesExhausted(session *models.HostedGenesisSession) bool {
	return hostedGenesisSessionHasAssistantTurnRetryFailure(session) &&
		(!session.Failure.Retryable || session.Failure.Recovery.MaxAttempts <= 0)
}

func hostedGenesisSessionHasRetryableAssistantTurnFailure(session *models.HostedGenesisSession) bool {
	return hostedGenesisSessionHasAssistantTurnRetryFailure(session) &&
		session.Failure.Retryable
}

func hostedGenesisSessionHasAssistantTurnRetryFailure(session *models.HostedGenesisSession) bool {
	if session == nil || hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusFailed || session.Failure == nil {
		return false
	}
	return session.Failure.Code == hostedgenesis.FailureCodeAssistantTurnFailed &&
		session.Failure.Recovery.Action == hostedgenesis.RecoveryActionRetrySameStep
}

func hostedGenesisSessionHasMicroVMUnavailableRetryFailure(session *models.HostedGenesisSession) bool {
	if session == nil || hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusFailed || session.Failure == nil {
		return false
	}
	return session.Failure.Code == hostedgenesis.FailureCodeMicroVMUnavailable &&
		session.Failure.Recovery.Action == hostedgenesis.RecoveryActionRetrySameStep
}

func validateHostedGenesisMicroVMRecoveryCheckpoint(session *models.HostedGenesisSession) error {
	if session == nil || session.VMCheckpoint == nil {
		return fmt.Errorf("missing vm checkpoint")
	}
	checkpoint := session.VMCheckpoint.Normalize()
	if err := checkpoint.Validate(); err != nil {
		return fmt.Errorf("invalid vm checkpoint: %w", err)
	}
	if err := validateHostedGenesisMicroVMRecoveryTurns(session, checkpoint); err != nil {
		return err
	}
	return validateHostedGenesisMicroVMRecoveryActorCheckpoint(session, checkpoint)
}

func validateHostedGenesisMicroVMRecoveryTurns(session *models.HostedGenesisSession, checkpoint hostedgenesis.VMCheckpointMetadata) error {
	currentTurnID := strings.TrimSpace(session.LatestTurnID)
	if currentTurnID == "" {
		return fmt.Errorf("missing current turn")
	}
	if err := hostedgenesis.ValidateTurnLedger(session.TurnLedger); err != nil {
		return fmt.Errorf("invalid durable turn ledger: %w", err)
	}
	currentTurnIndex := hostedGenesisTurnLedgerIndex(session.TurnLedger, currentTurnID)
	if currentTurnIndex < 0 || currentTurnIndex != len(session.TurnLedger)-1 {
		return fmt.Errorf("current turn %q is not the latest durable turn", currentTurnID)
	}
	checkpointTurnIndex := hostedGenesisTurnLedgerIndex(session.TurnLedger, checkpoint.LatestTurnID)
	if checkpointTurnIndex < 0 {
		return fmt.Errorf("checkpoint turn %q is not in the durable turn ledger", checkpoint.LatestTurnID)
	}
	if checkpointTurnIndex >= currentTurnIndex {
		return fmt.Errorf("checkpoint turn %q is not prior to current turn %q", checkpoint.LatestTurnID, currentTurnID)
	}
	expectedInputRef := hostedgenesis.CheckpointRef("input", session.ConversationID, currentTurnID)
	currentTurn := session.TurnLedger[currentTurnIndex].Normalize()
	if hostedgenesis.NormalizeCheckpointRef(session.InputCheckpointRef) != expectedInputRef ||
		currentTurn.InputCheckpointRef != expectedInputRef {
		return fmt.Errorf("current turn input checkpoint does not match the durable conversation binding")
	}
	return nil
}

func validateHostedGenesisMicroVMRecoveryActorCheckpoint(session *models.HostedGenesisSession, checkpoint hostedgenesis.VMCheckpointMetadata) error {
	expectedRef := hostedgenesis.CheckpointRef(
		"vm-actor",
		session.ConversationID,
		fmt.Sprintf("%s-%d-%s", firstNonEmpty(strings.TrimSpace(checkpoint.Step), strings.TrimSpace(checkpoint.Action), "step"), checkpoint.Sequence, checkpoint.LatestTurnID),
	)
	if strings.TrimSpace(checkpoint.Ref) != expectedRef {
		return fmt.Errorf("checkpoint ref does not match the durable conversation binding")
	}
	statusFrom := hostedgenesis.NormalizeStatus(checkpoint.StatusFrom)
	statusTo := hostedgenesis.NormalizeStatus(checkpoint.StatusTo)
	if statusTo != hostedgenesis.StatusAssistantTurnReady {
		return fmt.Errorf("checkpoint does not represent a completed assistant actor step")
	}
	if err := hostedgenesis.ValidateTransition(statusFrom, statusTo); err != nil {
		return err
	}
	if checkpoint.Sequence > session.Version {
		return fmt.Errorf("checkpoint sequence %d is ahead of session version %d", checkpoint.Sequence, session.Version)
	}
	return nil
}

func hostedGenesisTurnLedgerIndex(ledger []hostedgenesis.TurnLedgerEntry, turnID string) int {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return -1
	}
	for index, entry := range ledger {
		if strings.TrimSpace(entry.Normalize().TurnID) == turnID {
			return index
		}
	}
	return -1
}

func hostedGenesisSessionHasPendingAssistantRetryFailure(session *models.HostedGenesisSession) bool {
	if session == nil ||
		hostedgenesis.NormalizeStatus(session.Status) != hostedgenesis.StatusInProgress ||
		session.Failure == nil ||
		strings.TrimSpace(session.AssistantCheckpointRef) != "" {
		return false
	}
	return session.Failure.Code == hostedgenesis.FailureCodeAssistantTurnFailed &&
		session.Failure.Recovery.Action == hostedgenesis.RecoveryActionRetrySameStep
}

func hostedGenesisDeclarationExtractionRetryCarryForward(failure *hostedgenesis.Failure) *hostedgenesis.Failure {
	return hostedGenesisRetryCarryForward(failure)
}

func hostedGenesisAssistantTurnRetryCarryForward(failure *hostedgenesis.Failure) *hostedgenesis.Failure {
	return hostedGenesisRetryCarryForward(failure)
}

func hostedGenesisMicroVMUnavailableRetryCarryForward(failure *hostedgenesis.Failure) *hostedgenesis.Failure {
	return hostedGenesisRetryCarryForward(failure)
}

func hostedGenesisRetryCarryForward(failure *hostedgenesis.Failure) *hostedgenesis.Failure {
	if failure == nil {
		return nil
	}
	carried := *failure
	carried.Recovery.MaxAttempts--
	if carried.Recovery.MaxAttempts < 0 {
		carried.Recovery.MaxAttempts = 0
	}
	if carried.Recovery.MaxAttempts == 0 {
		carried.Retryable = false
	}
	return &carried
}

func (s *Server) retryHostedGenesisFailedAssistantTurn(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || convCtx.session == nil || convCtx.conv == nil {
		return nil, nil, newAppTheoryError("app.internal", "internal error")
	}
	if !hostedGenesisSessionCanRetryAssistantTurn(convCtx.session) {
		return nil, nil, newAppTheoryError(appErrCodeConflict, "conversation recovery requires a fresh soul bootstrap")
	}
	turnSession, _, buildErr := hostedGenesisRecoveryTurnSession(convCtx)
	if buildErr != nil {
		return nil, nil, buildErr
	}
	if s.hostedGenesisMicroVMDispatcher == nil {
		return nil, nil, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM recovery dispatch is unavailable")
	}
	now := time.Now().UTC()
	progressedSession := cloneHostedGenesisSession(convCtx.session)
	progressedConv := cloneSoulAgentMintConversation(convCtx.conv)
	progressedSession.Status = string(hostedgenesis.StatusInProgress)
	progressedSession.Failure = hostedGenesisAssistantTurnRetryCarryForward(convCtx.session.Failure)
	progressedSession.AssistantCheckpointRef = ""
	progressedSession.ExecutionStateRef = ""
	progressedSession.MicroVMExecutionID = ""
	progressedSession.MicroVMLifecycleRef = nil
	progressedSession.DeclarationCheckpoint = nil
	progressedSession.RequestID = strings.TrimSpace(ctx.RequestID)
	progressedSession.UpdatedAt = now
	progressedSession.CompletedAt = time.Time{}
	progressedConv.Status = models.SoulMintConversationStatusInProgress
	progressedConv.StatusReason = ""
	progressedConv.LatestTurnID = turnSession.turnID
	progressedConv.RequestID = strings.TrimSpace(ctx.RequestID)
	progressedConv.UpdatedAt = now
	progressedConv.CompletedAt = time.Time{}
	binding := progressedSession.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		log.Printf("controlplane: hosted genesis assistant retry binding invalid agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), err)
		return nil, nil, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM recovery dispatch binding is invalid")
	}
	freshDispatcher, freshErr := s.prepareHostedGenesisProviderTimeoutRuntime(ctx, convCtx, binding, progressedSession)
	if freshErr != nil {
		return nil, nil, freshErr
	}
	if appErr := s.persistHostedGenesisAssistantRetryPending(ctx.Context(), convCtx.session, progressedSession, progressedConv, now); appErr != nil {
		return nil, nil, appErr
	}
	progressedSession.Version = convCtx.session.Version + 1
	if invokeErr := invokeHostedGenesisFreshRuntime(ctx, freshDispatcher, binding); invokeErr != nil {
		return nil, nil, invokeErr
	}
	if freshDispatcher != nil {
		return progressedSession, progressedConv, nil
	}
	return s.dispatchHostedGenesisPersistedAssistantRetry(ctx, convCtx, progressedSession, progressedConv)
}

func (s *Server) retryHostedGenesisMicroVMUnavailable(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || convCtx.session == nil || convCtx.conv == nil {
		return nil, nil, newAppTheoryError("app.internal", "internal error")
	}
	if !hostedGenesisSessionHasMicroVMUnavailableRetryFailure(convCtx.session) ||
		!convCtx.session.Failure.Retryable ||
		convCtx.session.Failure.Recovery.MaxAttempts <= 0 {
		return nil, nil, newAppTheoryError(appErrCodeConflict, "conversation recovery requires a fresh soul bootstrap")
	}
	if err := validateHostedGenesisMicroVMRecoveryCheckpoint(convCtx.session); err != nil {
		return nil, nil, newAppTheoryError(appErrCodeConflict, "conversation recovery checkpoint is unavailable")
	}
	if s.hostedGenesisMicroVMDispatcher == nil {
		return nil, nil, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM recovery dispatch is unavailable")
	}
	now := time.Now().UTC()
	progressedSession := cloneHostedGenesisSession(convCtx.session)
	progressedConv := cloneSoulAgentMintConversation(convCtx.conv)
	progressedSession.Status = string(hostedgenesis.StatusInProgress)
	progressedSession.Failure = hostedGenesisMicroVMUnavailableRetryCarryForward(convCtx.session.Failure)
	progressedSession.AssistantCheckpointRef = ""
	progressedSession.ExecutionStateRef = ""
	progressedSession.MicroVMExecutionID = ""
	progressedSession.MicroVMLifecycleRef = nil
	progressedSession.DeclarationCheckpoint = nil
	progressedSession.RequestID = strings.TrimSpace(ctx.RequestID)
	progressedSession.UpdatedAt = now
	progressedSession.CompletedAt = time.Time{}
	progressedConv.Status = models.SoulMintConversationStatusInProgress
	progressedConv.StatusReason = ""
	progressedConv.LatestTurnID = firstNonEmpty(progressedSession.LatestTurnID, progressedConv.LatestTurnID)
	progressedConv.RequestID = strings.TrimSpace(ctx.RequestID)
	progressedConv.UpdatedAt = now
	progressedConv.CompletedAt = time.Time{}
	binding := progressedSession.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		log.Printf("controlplane: hosted genesis microvm retry binding invalid agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), err)
		return nil, nil, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM recovery dispatch binding is invalid")
	}
	if appErr := s.persistHostedGenesisMicroVMRetryPending(ctx.Context(), convCtx.session, progressedSession, progressedConv, now); appErr != nil {
		return nil, nil, appErr
	}
	progressedSession.Version = convCtx.session.Version + 1
	return s.dispatchHostedGenesisPersistedMicroVMRetry(ctx, convCtx, progressedSession, progressedConv)
}

func (s *Server) retryHostedGenesisPendingAssistantTurn(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || convCtx.session == nil || convCtx.conv == nil {
		return nil, nil, newAppTheoryError("app.internal", "internal error")
	}
	if !hostedGenesisSessionHasPendingAssistantRetryFailure(convCtx.session) {
		return nil, nil, newAppTheoryError(appErrCodeConflict, "conversation recovery requires a fresh soul bootstrap")
	}
	if !convCtx.session.Failure.Retryable || convCtx.session.Failure.Recovery.MaxAttempts <= 0 {
		failedSession, failedConv, appErr := s.persistHostedGenesisAssistantRetryDispatchFailure(ctx.Context(), convCtx.session, convCtx.conv, strings.TrimSpace(ctx.RequestID), time.Now().UTC())
		if appErr != nil {
			return nil, nil, appErr
		}
		return failedSession, failedConv, newAppTheoryError(appErrCodeConflict, "conversation recovery requires a fresh soul bootstrap")
	}
	if s.hostedGenesisMicroVMDispatcher == nil {
		if _, _, appErr := s.persistHostedGenesisAssistantRetryDispatchFailure(ctx.Context(), convCtx.session, convCtx.conv, strings.TrimSpace(ctx.RequestID), time.Now().UTC()); appErr != nil {
			return nil, nil, appErr
		}
		return nil, nil, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM recovery dispatch is unavailable")
	}
	now := time.Now().UTC()
	progressedSession := cloneHostedGenesisSession(convCtx.session)
	progressedConv := cloneSoulAgentMintConversation(convCtx.conv)
	progressedSession.Status = string(hostedgenesis.StatusInProgress)
	progressedSession.Failure = hostedGenesisAssistantTurnRetryCarryForward(convCtx.session.Failure)
	progressedSession.AssistantCheckpointRef = ""
	progressedSession.ExecutionStateRef = ""
	progressedSession.MicroVMExecutionID = ""
	progressedSession.MicroVMLifecycleRef = nil
	progressedSession.DeclarationCheckpoint = nil
	progressedSession.RequestID = strings.TrimSpace(ctx.RequestID)
	progressedSession.UpdatedAt = now
	progressedSession.CompletedAt = time.Time{}
	progressedConv.Status = models.SoulMintConversationStatusInProgress
	progressedConv.StatusReason = ""
	progressedConv.LatestTurnID = firstNonEmpty(progressedSession.LatestTurnID, progressedConv.LatestTurnID)
	progressedConv.RequestID = strings.TrimSpace(ctx.RequestID)
	progressedConv.UpdatedAt = now
	progressedConv.CompletedAt = time.Time{}
	binding := progressedSession.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		log.Printf("controlplane: hosted genesis pending assistant retry binding invalid agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), err)
		return nil, nil, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM recovery dispatch binding is invalid")
	}
	if appErr := s.persistHostedGenesisPendingAssistantRetryTransition(ctx.Context(), convCtx.session, progressedSession, progressedConv, now); appErr != nil {
		return nil, nil, appErr
	}
	progressedSession.Version = convCtx.session.Version + 1
	return s.dispatchHostedGenesisPersistedAssistantRetry(ctx, convCtx, progressedSession, progressedConv)
}

func (s *Server) dispatchHostedGenesisPersistedAssistantRetry(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext, pending *models.HostedGenesisSession, conv *models.SoulAgentMintConversation) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	return s.dispatchHostedGenesisPersistedConversationRetry(ctx, convCtx, pending, conv, hostedGenesisFailureAssistantTurnFailed)
}

func (s *Server) dispatchHostedGenesisPersistedMicroVMRetry(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext, pending *models.HostedGenesisSession, conv *models.SoulAgentMintConversation) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	return s.dispatchHostedGenesisPersistedConversationRetry(ctx, convCtx, pending, conv, hostedGenesisFailureMicroVMUnavailable)
}

func (s *Server) dispatchHostedGenesisPersistedConversationRetry(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext, pending *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, failureReason string) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || pending == nil || conv == nil {
		return nil, nil, newAppTheoryError("app.internal", "internal error")
	}
	if s.hostedGenesisMicroVMDispatcher == nil {
		if _, _, appErr := s.persistHostedGenesisRetryDispatchFailure(ctx.Context(), pending, conv, strings.TrimSpace(ctx.RequestID), time.Now().UTC(), failureReason); appErr != nil {
			return nil, nil, appErr
		}
		return nil, nil, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM recovery dispatch is unavailable")
	}
	binding := pending.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		log.Printf("controlplane: hosted genesis assistant retry binding invalid agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), err)
		return nil, nil, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM recovery dispatch binding is invalid")
	}
	runCtx, cancel := context.WithTimeout(detachedMintConversationContext(ctx.Context()), hostedGenesisAcceptedTurnDispatchTimeout)
	defer cancel()
	dispatch, dispatchErr := s.hostedGenesisMicroVMDispatcher.DispatchMicroVMRun(runCtx, strings.TrimSpace(ctx.RequestID), binding)
	if dispatchErr != nil {
		log.Printf("controlplane: hosted genesis assistant retry dispatch failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), dispatchErr)
		if _, _, appErr := s.persistHostedGenesisRetryDispatchFailure(ctx.Context(), pending, conv, strings.TrimSpace(ctx.RequestID), time.Now().UTC(), failureReason); appErr != nil {
			return nil, nil, appErr
		}
		return nil, nil, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM recovery dispatch failed")
	}
	progressedSession := cloneHostedGenesisSession(pending)
	if err := progressedSession.ApplyMicroVMLifecycleRef(dispatch.LifecycleRef); err != nil {
		log.Printf("controlplane: hosted genesis assistant retry lifecycle ref rejected agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), err)
		if _, _, appErr := s.persistHostedGenesisRetryDispatchFailure(ctx.Context(), pending, conv, strings.TrimSpace(ctx.RequestID), time.Now().UTC(), failureReason); appErr != nil {
			return nil, nil, appErr
		}
		return nil, nil, newAppTheoryError("app.internal", "failed to record microvm recovery dispatch")
	}
	progressedSession.RequestID = strings.TrimSpace(ctx.RequestID)
	progressedSession.UpdatedAt = time.Now().UTC()
	if appErr := s.persistHostedGenesisAssistantRetryLifecycle(ctx.Context(), pending, progressedSession); appErr != nil {
		return nil, nil, appErr
	}
	progressedSession.Version = pending.Version + 1
	return progressedSession, conv, nil
}

func (s *Server) persistHostedGenesisAssistantRetryPending(ctx context.Context, failed *models.HostedGenesisSession, progressed *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, now time.Time) *apptheory.AppTheoryError {
	return s.persistHostedGenesisFailedRetryPending(ctx, failed, progressed, conv, now, hostedgenesis.FailureCodeAssistantTurnFailed)
}

func (s *Server) persistHostedGenesisMicroVMRetryPending(ctx context.Context, failed *models.HostedGenesisSession, progressed *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, now time.Time) *apptheory.AppTheoryError {
	return s.persistHostedGenesisFailedRetryPending(ctx, failed, progressed, conv, now, hostedgenesis.FailureCodeMicroVMUnavailable)
}

func (s *Server) persistHostedGenesisFailedRetryPending(ctx context.Context, failed *models.HostedGenesisSession, progressed *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, now time.Time, failureCode hostedgenesis.FailureCode) *apptheory.AppTheoryError {
	if s == nil || s.store == nil || s.store.DB == nil || failed == nil || progressed == nil || conv == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	updateConv := &models.SoulAgentMintConversation{
		AgentID:        conv.AgentID,
		ConversationID: conv.ConversationID,
		Status:         conv.Status,
		StatusReason:   conv.StatusReason,
		LatestTurnID:   conv.LatestTurnID,
		RequestID:      conv.RequestID,
		UpdatedAt:      conv.UpdatedAt,
		CompletedAt:    conv.CompletedAt,
	}
	_ = updateConv.UpdateKeys()
	if err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		if err := addHostedGenesisSessionRecoveryRetryWrite(tx, progressed, failed.Version, failureCode); err != nil {
			return err
		}
		tx.UpdateWithBuilder(updateConv, func(ub core.UpdateBuilder) error {
			ub.Set("Status", updateConv.Status)
			ub.Set("StatusReason", "")
			ub.Set("LatestTurnID", updateConv.LatestTurnID)
			ub.Set("RequestID", strings.TrimSpace(updateConv.RequestID))
			ub.Set("UpdatedAt", now)
			ub.Set("CompletedAt", time.Time{})
			return nil
		}, tabletheory.IfExists(), tabletheory.Condition("Status", "=", models.SoulMintConversationStatusFailed))
		return nil
	}); err != nil {
		log.Printf("controlplane: hosted genesis assistant retry pending persist failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(progressed.AgentID), soulMintInstanceReadAuditHash(progressed.ConversationID), err)
		return newAppTheoryError("app.internal", "failed to persist assistant retry")
	}
	return nil
}

func (s *Server) persistHostedGenesisPendingAssistantRetryTransition(ctx context.Context, prior *models.HostedGenesisSession, progressed *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, now time.Time) *apptheory.AppTheoryError {
	if s == nil || s.store == nil || s.store.DB == nil || prior == nil || progressed == nil || conv == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	updateConv := &models.SoulAgentMintConversation{
		AgentID:        conv.AgentID,
		ConversationID: conv.ConversationID,
		Status:         conv.Status,
		StatusReason:   conv.StatusReason,
		LatestTurnID:   conv.LatestTurnID,
		RequestID:      conv.RequestID,
		UpdatedAt:      conv.UpdatedAt,
		CompletedAt:    conv.CompletedAt,
	}
	_ = updateConv.UpdateKeys()
	if err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		if err := addHostedGenesisSessionWrite(tx, progressed, false, prior.Version, hostedgenesis.StatusInProgress); err != nil {
			return err
		}
		tx.UpdateWithBuilder(updateConv, func(ub core.UpdateBuilder) error {
			ub.Set("Status", updateConv.Status)
			ub.Set("StatusReason", "")
			ub.Set("LatestTurnID", updateConv.LatestTurnID)
			ub.Set("RequestID", strings.TrimSpace(updateConv.RequestID))
			ub.Set("UpdatedAt", now)
			ub.Set("CompletedAt", time.Time{})
			return nil
		}, tabletheory.IfExists(), tabletheory.Condition("Status", "=", models.SoulMintConversationStatusInProgress))
		return nil
	}); err != nil {
		log.Printf("controlplane: hosted genesis pending assistant retry persist failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(progressed.AgentID), soulMintInstanceReadAuditHash(progressed.ConversationID), err)
		return newAppTheoryError("app.internal", "failed to persist assistant retry")
	}
	return nil
}

func (s *Server) persistHostedGenesisAssistantRetryLifecycle(ctx context.Context, pending *models.HostedGenesisSession, progressed *models.HostedGenesisSession) *apptheory.AppTheoryError {
	if s == nil || s.store == nil || s.store.DB == nil || pending == nil || progressed == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		return addHostedGenesisSessionWrite(tx, progressed, false, pending.Version, hostedgenesis.StatusInProgress)
	}); err != nil {
		log.Printf("controlplane: hosted genesis assistant retry lifecycle persist failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(progressed.AgentID), soulMintInstanceReadAuditHash(progressed.ConversationID), err)
		return newAppTheoryError("app.internal", "failed to persist assistant retry dispatch")
	}
	return nil
}

func (s *Server) persistHostedGenesisAssistantRetryDispatchFailure(ctx context.Context, pending *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, requestID string, now time.Time) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	return s.persistHostedGenesisRetryDispatchFailure(ctx, pending, conv, requestID, now, hostedGenesisFailureAssistantTurnFailed)
}

func (s *Server) persistHostedGenesisRetryDispatchFailure(ctx context.Context, pending *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, requestID string, now time.Time, failureReason string) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || pending == nil || conv == nil {
		return nil, nil, newAppTheoryError("app.internal", "internal error")
	}
	failedSession := cloneHostedGenesisSession(pending)
	failedConv := cloneSoulAgentMintConversation(conv)
	failedSession.Status = string(hostedgenesis.StatusFailed)
	if failedSession.Failure == nil {
		failedSession.Failure = hostedGenesisSessionFailureFromReason(failureReason)
	}
	failedSession.RequestID = strings.TrimSpace(requestID)
	failedSession.UpdatedAt = now
	failedSession.CompletedAt = now
	failedConv.Status = models.SoulMintConversationStatusFailed
	failedConv.StatusReason = strings.TrimSpace(failureReason)
	failedConv.LatestTurnID = firstNonEmpty(failedSession.LatestTurnID, failedConv.LatestTurnID)
	failedConv.RequestID = strings.TrimSpace(requestID)
	failedConv.UpdatedAt = now
	failedConv.CompletedAt = now
	updateConv := &models.SoulAgentMintConversation{
		AgentID:        failedConv.AgentID,
		ConversationID: failedConv.ConversationID,
	}
	_ = updateConv.UpdateKeys()
	if err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		if err := addHostedGenesisSessionWrite(tx, failedSession, false, pending.Version, hostedgenesis.StatusInProgress); err != nil {
			return err
		}
		tx.UpdateWithBuilder(updateConv, func(ub core.UpdateBuilder) error {
			ub.Set("Status", failedConv.Status)
			ub.Set("StatusReason", failedConv.StatusReason)
			ub.Set("LatestTurnID", failedConv.LatestTurnID)
			ub.Set("RequestID", failedConv.RequestID)
			ub.Set("UpdatedAt", now)
			ub.Set("CompletedAt", failedConv.CompletedAt)
			return nil
		}, tabletheory.IfExists(), tabletheory.Condition("Status", "=", models.SoulMintConversationStatusInProgress))
		return nil
	}); err != nil {
		log.Printf("controlplane: hosted genesis assistant retry failure persist failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(failedSession.AgentID), soulMintInstanceReadAuditHash(failedSession.ConversationID), err)
		return nil, nil, newAppTheoryError("app.internal", "failed to persist assistant retry failure")
	}
	return failedSession, failedConv, nil
}

func addHostedGenesisSessionRecoveryRetryWrite(tx core.TransactionBuilder, session *models.HostedGenesisSession, expectedVersion int64, failureCode hostedgenesis.FailureCode) error {
	return addHostedGenesisSessionFailedRetryWrite(
		tx,
		session,
		expectedVersion,
		hostedgenesis.StatusInProgress,
		failureCode,
		"hosted genesis assistant retry write is invalid",
		"hosted genesis recovery retry write requires in-progress retry",
	)
}

func (s *Server) retryHostedGenesisFailedDeclarationExtraction(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || convCtx.session == nil || convCtx.conv == nil {
		return nil, nil, newAppTheoryError("app.internal", "internal error")
	}
	if !hostedGenesisSessionCanRetryDeclarationExtraction(convCtx.session) {
		return nil, nil, newAppTheoryError(appErrCodeConflict, "conversation recovery requires a fresh soul bootstrap")
	}
	now := time.Now().UTC()
	expectedVersion := convCtx.session.Version
	progressedSession := cloneHostedGenesisSession(convCtx.session)
	progressedConv := cloneSoulAgentMintConversation(convCtx.conv)
	progressedSession.Status = string(hostedgenesis.StatusDeclarationExtractionPending)
	progressedSession.Failure = hostedGenesisDeclarationExtractionRetryCarryForward(convCtx.session.Failure)
	progressedSession.DeclarationCheckpoint = nil
	progressedSession.RequestID = strings.TrimSpace(ctx.RequestID)
	progressedSession.UpdatedAt = now
	progressedSession.CompletedAt = time.Time{}
	progressedConv.Status = models.SoulMintConversationStatusDeclarationExtractionPending
	progressedConv.StatusReason = ""
	progressedConv.RequestID = strings.TrimSpace(ctx.RequestID)
	progressedConv.UpdatedAt = now
	progressedConv.CompletedAt = time.Time{}
	if s.hostedGenesisMicroVMDispatcher == nil {
		return nil, nil, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM extraction dispatch is unavailable")
	}
	binding := progressedSession.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		return nil, nil, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM extraction binding is invalid")
	}
	freshDispatcher, freshErr := s.prepareHostedGenesisProviderTimeoutRuntime(ctx, convCtx, binding, progressedSession)
	if freshErr != nil {
		return nil, nil, freshErr
	}
	creditsDebited, appErr := s.debitSoulMintConversationCredits(
		ctx.Context(),
		convCtx.inst,
		soulMintConversationExtractModule,
		convCtx.conversationID,
		firstNonEmpty(progressedConv.IdempotencyKey, ctx.RequestID),
		soulMintConversationExtractBaseCredits,
		now,
		func(tx core.TransactionBuilder, creditsRequested int64) error {
			if err := addHostedGenesisSessionDeclarationExtractionRetryWrite(tx, progressedSession, expectedVersion); err != nil {
				return err
			}
			update := &models.SoulAgentMintConversation{AgentID: convCtx.agentIDHex, ConversationID: convCtx.conversationID}
			_ = update.UpdateKeys()
			tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
				ub.Add("ChargedCredits", creditsRequested)
				ub.Set("Status", models.SoulMintConversationStatusDeclarationExtractionPending)
				ub.Set("StatusReason", "")
				ub.Set("RequestID", strings.TrimSpace(ctx.RequestID))
				ub.Set("UpdatedAt", now)
				ub.Set("CompletedAt", time.Time{})
				return nil
			}, tabletheory.IfExists(), tabletheory.Condition("Status", "=", models.SoulMintConversationStatusFailed))
			return nil
		},
	)
	if appErr != nil {
		return nil, nil, appErr
	}
	progressedSession.Version = expectedVersion + 1
	progressedConv.ChargedCredits += creditsDebited
	return s.dispatchHostedGenesisRetriedDeclarationExtraction(ctx, convCtx, progressedSession, progressedConv, freshDispatcher, binding, now)
}

// hostedGenesisFreshRecoveryRequestID is deterministic for one failed Host
// session version. Concurrent recovery requests therefore present the same
// AppTheory/AWS Run idempotency token while the TableTheory version guard still
// permits only one retry/debit transaction. The digest contains only opaque
// lifecycle identifiers and is safe to log as request correlation.
func hostedGenesisFreshRecoveryRequestID(binding hostedgenesis.MicroVMSessionBinding, version int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("provider-timeout-recovery\x00%s\x00%s\x00%d", binding.TenantID(), strings.TrimSpace(binding.ConversationID), version)))
	return fmt.Sprintf("hg-recover-%x", sum[:16])
}

func (s *Server) prepareHostedGenesisProviderTimeoutRuntime(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext, binding hostedgenesis.MicroVMSessionBinding, progressed *models.HostedGenesisSession) (hostedgenesis.FreshMicroVMDispatcher, *apptheory.AppTheoryError) {
	if convCtx.session == nil || convCtx.session.Failure == nil || convCtx.session.Failure.Class != hostedgenesis.FailureClassProviderTimeout {
		return nil, nil
	}
	freshDispatcher, ok := s.hostedGenesisMicroVMDispatcher.(hostedgenesis.FreshMicroVMDispatcher)
	if !ok || convCtx.session.MicroVMLifecycleRef == nil || progressed == nil {
		return nil, newAppTheoryError(appErrCodeMicroVMUnavailable, "fresh MicroVM recovery is unavailable")
	}
	prepareCtx, cancel := context.WithTimeout(detachedMintConversationContext(ctx.Context()), hostedGenesisAcceptedTurnDispatchTimeout)
	prepared, err := freshDispatcher.PrepareFreshMicroVMRun(
		prepareCtx,
		hostedGenesisFreshRecoveryRequestID(binding, convCtx.session.Version),
		binding,
		*convCtx.session.MicroVMLifecycleRef,
	)
	cancel()
	if err != nil {
		log.Printf("controlplane: hosted genesis provider-timeout fresh runtime preparation failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), err)
		return nil, newAppTheoryError(appErrCodeMicroVMUnavailable, "fresh MicroVM recovery failed before dispatch")
	}
	if err := progressed.ApplyMicroVMLifecycleRef(prepared.LifecycleRef); err != nil {
		return nil, newAppTheoryError(appErrCodeMicroVMUnavailable, "fresh MicroVM lifecycle identity is invalid")
	}
	return freshDispatcher, nil
}

func (s *Server) dispatchHostedGenesisRetriedDeclarationExtraction(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext, progressedSession *models.HostedGenesisSession, progressedConv *models.SoulAgentMintConversation, freshDispatcher hostedgenesis.FreshMicroVMDispatcher, binding hostedgenesis.MicroVMSessionBinding, now time.Time) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	if invokeErr := invokeHostedGenesisFreshRuntime(ctx, freshDispatcher, binding); invokeErr != nil {
		return nil, nil, invokeErr
	}
	if freshDispatcher != nil {
		return progressedSession, progressedConv, nil
	}
	retryCtx := convCtx
	retryCtx.session = progressedSession
	retryCtx.conv = progressedConv
	if err := s.dispatchHostedGenesisDeclarationExtraction(ctx, retryCtx, now); err != nil {
		if appErr, ok := err.(*apptheory.AppTheoryError); ok {
			return nil, nil, appErr
		}
		return nil, nil, newAppTheoryError("app.internal", "failed to retry declaration extraction")
	}
	return retryCtx.session, retryCtx.conv, nil
}

func invokeHostedGenesisFreshRuntime(ctx *apptheory.Context, dispatcher hostedgenesis.FreshMicroVMDispatcher, binding hostedgenesis.MicroVMSessionBinding) *apptheory.AppTheoryError {
	if dispatcher == nil {
		return nil
	}
	invokeCtx, cancel := context.WithTimeout(detachedMintConversationContext(ctx.Context()), hostedGenesisAcceptedTurnDispatchTimeout)
	err := dispatcher.InvokeMicroVMTurn(invokeCtx, strings.TrimSpace(ctx.RequestID), binding)
	cancel()
	if err != nil {
		return newAppTheoryError(appErrCodeMicroVMUnavailable, "fresh MicroVM recovery dispatch failed")
	}
	return nil
}

func addHostedGenesisSessionDeclarationExtractionRetryWrite(tx core.TransactionBuilder, session *models.HostedGenesisSession, expectedVersion int64) error {
	return addHostedGenesisSessionFailedRetryWrite(
		tx,
		session,
		expectedVersion,
		hostedgenesis.StatusDeclarationExtractionPending,
		hostedgenesis.FailureCodeDeclarationExtractionFailed,
		"hosted genesis declaration extraction retry write is invalid",
		"hosted genesis declaration extraction retry write requires pending declaration extraction",
	)
}

func addHostedGenesisSessionFailedRetryWrite(tx core.TransactionBuilder, session *models.HostedGenesisSession, expectedVersion int64, targetStatus hostedgenesis.Status, failureCode hostedgenesis.FailureCode, invalidMessage string, requirementMessage string) error {
	if tx == nil || session == nil {
		return fmt.Errorf("%s", invalidMessage)
	}
	if hostedgenesis.NormalizeStatus(session.Status) != targetStatus ||
		session.Failure == nil ||
		session.Failure.Code != failureCode {
		return fmt.Errorf("%s", requirementMessage)
	}
	if err := session.Failure.Validate(); err != nil {
		return err
	}
	if err := session.BeforeUpdate(); err != nil {
		return err
	}
	tx.UpdateWithBuilder(session, func(ub core.UpdateBuilder) error {
		for _, field := range hostedGenesisDeclarationExtractionRetrySessionFields(session) {
			ub.Set(field.name, field.value)
		}
		ub.Add("Version", int64(1))
		return nil
	}, tabletheory.IfExists(), tabletheory.AtVersion(expectedVersion), tabletheory.Condition("Status", "=", string(hostedgenesis.StatusFailed)))
	return nil
}

type hostedGenesisSessionUpdateValue struct {
	name  string
	value any
}

func hostedGenesisDeclarationExtractionRetrySessionFields(session *models.HostedGenesisSession) []hostedGenesisSessionUpdateValue {
	return []hostedGenesisSessionUpdateValue{
		{name: "InstanceSlug", value: session.InstanceSlug},
		{name: "RegistrationID", value: session.RegistrationID},
		{name: "AgentID", value: session.AgentID},
		{name: "ConversationID", value: session.ConversationID},
		{name: "GSI1PK", value: session.GSI1PK},
		{name: "GSI1SK", value: session.GSI1SK},
		{name: "GSI2PK", value: session.GSI2PK},
		{name: "GSI2SK", value: session.GSI2SK},
		{name: "Status", value: session.Status},
		{name: "Model", value: session.Model},
		{name: "LatestTurnID", value: session.LatestTurnID},
		{name: "MessageCount", value: session.MessageCount},
		{name: "TurnLedger", value: session.TurnLedger},
		{name: "InputCheckpointRef", value: session.InputCheckpointRef},
		{name: "AssistantCheckpointRef", value: session.AssistantCheckpointRef},
		{name: "ExecutionStateRef", value: session.ExecutionStateRef},
		{name: "MicroVMExecutionID", value: session.MicroVMExecutionID},
		{name: "MicroVMLifecycleRef", value: session.MicroVMLifecycleRef},
		{name: "DeclarationCheckpoint", value: session.DeclarationCheckpoint},
		{name: "Failure", value: session.Failure},
		{name: "TraceIDs", value: session.TraceIDs},
		{name: "VMCheckpoint", value: session.VMCheckpoint},
		{name: "RequestID", value: session.RequestID},
		{name: "UpdatedAt", value: session.UpdatedAt},
		{name: "CompletedAt", value: session.CompletedAt},
	}
}

// dispatchHostedGenesisRecoveryTurn re-dispatches a stuck accepted turn through
// the MicroVM controller run command on the production recover path. It is the
// dispatch-only recovery site for sessions that predate the H1.2 lifecycle-ref
// wiring (no MicroVMLifecycleRef). H1.4 makes recovery never re-run a turn
// synchronously: the retained sync assistant runner is never consulted here, so
// production recovery cannot fall back to a control-plane LLM call. An unwired
// dispatcher, an invalid binding, or a rejected dispatch is a loud retryable
// microvm_unavailable failed session — never a silent 200 and never a sync LLM.
// A successful dispatch persists the durable in_progress session with the
// refreshed MicroVM lifecycle ref and returns 202 accepted-pending.
func (s *Server) dispatchHostedGenesisRecoveryTurn(ctx *apptheory.Context, regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, conv *models.SoulAgentMintConversation, acceptedMessages []soulMintConversationMessage) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, int, *apptheory.AppTheoryError) {
	if session.session == nil || conv == nil {
		return nil, nil, 0, newAppTheoryError("app.internal", "internal error")
	}
	if s.hostedGenesisMicroVMDispatcher == nil {
		log.Printf("controlplane: hosted genesis recovery dispatch unavailable agent_hash=%s conversation_hash=%s", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID))
		failedSession, failedConv, appErr := s.persistHostedGenesisAcceptedTurnFailure(ctx.Context(), session, conv, acceptedMessages, hostedGenesisFailureMicroVMUnavailable, strings.TrimSpace(ctx.RequestID), time.Now().UTC())
		if appErr != nil {
			return nil, nil, 0, appErr
		}
		return failedSession, failedConv, http.StatusOK, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM recovery dispatch is unavailable")
	}
	binding := session.session.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		log.Printf("controlplane: hosted genesis recovery dispatch binding invalid agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID), err)
		failedSession, failedConv, appErr := s.persistHostedGenesisAcceptedTurnFailure(ctx.Context(), session, conv, acceptedMessages, hostedGenesisFailureMicroVMUnavailable, strings.TrimSpace(ctx.RequestID), time.Now().UTC())
		if appErr != nil {
			return nil, nil, 0, appErr
		}
		return failedSession, failedConv, http.StatusOK, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM recovery dispatch binding is invalid")
	}
	runCtx, cancel := context.WithTimeout(detachedMintConversationContext(ctx.Context()), hostedGenesisAcceptedTurnDispatchTimeout)
	defer cancel()
	dispatch, dispatchErr := s.hostedGenesisMicroVMDispatcher.DispatchMicroVMRun(runCtx, strings.TrimSpace(ctx.RequestID), binding)
	if dispatchErr != nil {
		log.Printf("controlplane: hosted genesis recovery dispatch failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID), dispatchErr)
		failedSession, failedConv, appErr := s.persistHostedGenesisAcceptedTurnFailure(ctx.Context(), session, conv, acceptedMessages, hostedGenesisFailureMicroVMUnavailable, strings.TrimSpace(ctx.RequestID), time.Now().UTC())
		if appErr != nil {
			return nil, nil, 0, appErr
		}
		return failedSession, failedConv, http.StatusOK, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM recovery dispatch failed")
	}
	progressedSession, progressedConv, appErr := s.persistHostedGenesisAcceptedMicroVMDispatch(ctx.Context(), session, conv, acceptedMessages, dispatch, strings.TrimSpace(ctx.RequestID), time.Now().UTC())
	if appErr != nil {
		return nil, nil, 0, appErr
	}
	return progressedSession, progressedConv, http.StatusAccepted, nil
}

// hostedGenesisMicroVMRecoveryResult is the bounded outcome of a production
// recover-path reconstruction. terminalFailure is true when the MicroVM was
// observed in a terminal state and the session was persisted as a loud failed
// turn; otherwise the session reflects the reconciled (still-pending or
// workload-advanced) durable status.
type hostedGenesisMicroVMRecoveryResult struct {
	session         *models.HostedGenesisSession
	conv            *models.SoulAgentMintConversation
	httpStatus      int
	terminalFailure bool
}

// hostedGenesisSessionNeedsMicroVMReconciliation reports whether the recover
// path should reach production reconstruction via the M16 controller get
// command. A session needs reconstruction when it sits in a MicroVM-serviced
// pending state (in_progress assistant turn, or declaration_extraction_pending)
// and already carries the populated lifecycle ref the H1.2 accept path (or H1.3
// extraction dispatch) recorded. Sessions without a lifecycle ref fall through
// to the re-dispatch path.
func hostedGenesisSessionNeedsMicroVMReconciliation(session *models.HostedGenesisSession) bool {
	if session == nil || session.MicroVMLifecycleRef == nil {
		return false
	}
	status := hostedgenesis.NormalizeStatus(session.Status)
	if status != hostedgenesis.StatusInProgress && status != hostedgenesis.StatusDeclarationExtractionPending {
		return false
	}
	return strings.TrimSpace(session.ExecutionStateRef) != "" && strings.TrimSpace(session.MicroVMExecutionID) != ""
}

// reconcileHostedGenesisMicroVMRecovery is the production reconstruction
// reachability site (kills G6). It queries real VM state through the M16
// controller get command via the MicroVMDispatcher seam, using the lifecycle
// ref the accept path populated, and reconciles the durable HostedGenesisSession:
//
//   - A dead/expired (terminal) VM is mapped to a loud retryable
//     microvm_unavailable failed session. Reconstruction no longer no-ops.
//   - A live VM whose durable status has not advanced is returned as
//     still-pending (the H1.1 workload owns the
//     assistant_turn_ready / declaration_ready / failed transitions).
//
// An unwired dispatcher is fail-closed and loud: reconstruction is never a
// silent no-op, and the recover path never falls back to a non-MicroVM path.
func (s *Server) reconcileHostedGenesisMicroVMRecovery(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext) (hostedGenesisMicroVMRecoveryResult, *apptheory.AppTheoryError) {
	if convCtx.session == nil || convCtx.session.MicroVMLifecycleRef == nil {
		return hostedGenesisMicroVMRecoveryResult{}, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM execution state is unavailable for recovery")
	}
	if s.hostedGenesisMicroVMDispatcher == nil {
		log.Printf("controlplane: hosted genesis microvm reconciliation unavailable agent_hash=%s conversation_hash=%s", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID))
		return hostedGenesisMicroVMRecoveryResult{}, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM reconstruction is unavailable")
	}
	binding := convCtx.session.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		log.Printf("controlplane: hosted genesis microvm recovery binding invalid agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), err)
		return hostedGenesisMicroVMRecoveryResult{}, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM execution binding is invalid for recovery")
	}
	ref := *convCtx.session.MicroVMLifecycleRef
	reconcileCtx, cancel := context.WithTimeout(detachedMintConversationContext(ctx.Context()), hostedGenesisRecoveryReconcileTimeout)
	defer cancel()
	result, reconcileErr := s.hostedGenesisMicroVMDispatcher.ReconcileMicroVM(reconcileCtx, strings.TrimSpace(ctx.RequestID), binding, ref)
	if reconcileErr != nil {
		log.Printf("controlplane: hosted genesis microvm reconciliation failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), reconcileErr)
		return hostedGenesisMicroVMRecoveryResult{}, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM reconstruction failed")
	}
	if result.CannotCompletePendingTurn {
		// The VM is dead/expired while the durable Host status has not advanced
		// to a workload completion. This is the silent no-op reconstruction had
		// before H1.3; it is now a loud retryable failure so the operator sees
		// the stuck turn and the recover path can re-dispatch on retry.
		failedSession, failedConv, appErr := s.persistHostedGenesisMicroVMRecoveryFailure(ctx, convCtx, hostedGenesisFailureMicroVMUnavailable)
		if appErr != nil {
			return hostedGenesisMicroVMRecoveryResult{}, appErr
		}
		return hostedGenesisMicroVMRecoveryResult{session: failedSession, conv: failedConv, httpStatus: http.StatusOK, terminalFailure: true}, nil
	}
	// Live VM: apply the reconciled lifecycle ref idempotently and return the
	// current durable status. The HostedGenesisSession status is authoritative;
	// the reconciled ref only refreshes non-authoritative execution/cache state.
	reconciledSession := cloneHostedGenesisSession(convCtx.session)
	if err := reconciledSession.ApplyMicroVMLifecycleRef(result.LifecycleRef); err != nil {
		log.Printf("controlplane: hosted genesis microvm reconciled ref rejected agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), err)
		return hostedGenesisMicroVMRecoveryResult{}, newAppTheoryError(appErrCodeMicroVMUnavailable, "MicroVM reconciled state is invalid for recovery")
	}
	reconciledSession.RequestID = strings.TrimSpace(ctx.RequestID)
	reconciledSession.UpdatedAt = time.Now().UTC()
	return hostedGenesisMicroVMRecoveryResult{session: reconciledSession, conv: convCtx.conv, httpStatus: http.StatusOK}, nil
}

// persistHostedGenesisMicroVMRecoveryFailure records a loud retryable failed
// session when production reconstruction observes a terminal (dead/expired)
// MicroVM. Unlike the accept-path failure helper, the expected status is the
// session's actual pending status (in_progress or declaration_extraction_pending)
// so the TableTheory status condition matches the authoritative row and the
// transition to failed is validated against the real prior state. The stored
// transcript is preserved (no re-run of the assistant); only the failure and
// status advance are written.
func (s *Server) persistHostedGenesisMicroVMRecoveryFailure(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext, reason string) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || convCtx.session == nil || convCtx.conv == nil {
		return nil, nil, newAppTheoryError("app.internal", "internal error")
	}
	now := time.Now().UTC()
	expectedStatus := hostedgenesis.NormalizeStatus(convCtx.session.Status)
	failedSession := cloneHostedGenesisSession(convCtx.session)
	failedConv := cloneSoulAgentMintConversation(convCtx.conv)
	failedSession.Status = string(hostedgenesis.StatusFailed)
	failedSession.Failure = hostedGenesisSessionFailureFromReason(reason)
	failedSession.RequestID = strings.TrimSpace(ctx.RequestID)
	failedSession.UpdatedAt = now
	failedSession.CompletedAt = now
	failedConv.Status = models.SoulMintConversationStatusFailed
	failedConv.StatusReason = strings.TrimSpace(reason)
	failedConv.RequestID = strings.TrimSpace(ctx.RequestID)
	failedConv.UpdatedAt = now
	failedConv.CompletedAt = now
	updateConv := &models.SoulAgentMintConversation{
		AgentID:        failedConv.AgentID,
		ConversationID: failedConv.ConversationID,
	}
	_ = updateConv.UpdateKeys()
	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		if err := addHostedGenesisSessionWrite(tx, failedSession, false, convCtx.session.Version, expectedStatus); err != nil {
			return err
		}
		tx.UpdateWithBuilder(updateConv, func(ub core.UpdateBuilder) error {
			ub.Set("Status", failedConv.Status)
			ub.Set("StatusReason", strings.TrimSpace(reason))
			ub.Set("RequestID", failedConv.RequestID)
			ub.Set("UpdatedAt", now)
			ub.Set("CompletedAt", failedConv.CompletedAt)
			return nil
		}, tabletheory.IfExists())
		return nil
	}); err != nil {
		log.Printf("controlplane: hosted genesis microvm recovery failure persist failed agent_hash=%s conversation_hash=%s status=%s err=%v", soulMintInstanceReadAuditHash(failedConv.AgentID), soulMintInstanceReadAuditHash(failedConv.ConversationID), failedSession.Status, err)
		return nil, nil, newAppTheoryError("app.internal", "failed to persist recovery failure")
	}
	return failedSession, failedConv, nil
}

func hostedGenesisSessionNeedsAssistantRecovery(session *models.HostedGenesisSession) bool {
	if session == nil {
		return false
	}
	status := hostedgenesis.NormalizeStatus(session.Status)
	// H1.3: the recover path covers declaration_extraction_pending as well as
	// in_progress assistant turns. Both are MicroVM-serviced pending states;
	// routing them through the recover path kills the permanent trap where
	// declaration_extraction_pending could never be serviced or failed loudly.
	if status == hostedgenesis.StatusDeclarationExtractionPending {
		return true
	}
	return status == hostedgenesis.StatusInProgress &&
		strings.TrimSpace(session.AssistantCheckpointRef) == ""
}

func hostedGenesisRecoveryTurnSession(convCtx soulInstanceBootstrapConversationContext) (hostedGenesisTurnSession, []soulMintConversationMessage, *apptheory.AppTheoryError) {
	if convCtx.session == nil || convCtx.conv == nil {
		return hostedGenesisTurnSession{}, nil, newAppTheoryError(soulMintAppErrCodeNotFound, "conversation not found")
	}
	if !hostedGenesisConversationMatchesSession(convCtx.session, convCtx.conv) ||
		!strings.EqualFold(strings.TrimSpace(convCtx.session.RegistrationID), strings.TrimSpace(convCtx.reg.ID)) {
		return hostedGenesisTurnSession{}, nil, newAppTheoryError(appErrCodeForbidden, "conversation is outside the registration boundary")
	}
	messages, appErr := hostedGenesisRecoveryMessages(convCtx.conv)
	if appErr != nil {
		return hostedGenesisTurnSession{}, nil, appErr
	}
	turnID := firstNonEmpty(convCtx.session.LatestTurnID, convCtx.conv.LatestTurnID)
	if turnID == "" && len(convCtx.session.TurnLedger) > 0 {
		turnID = convCtx.session.TurnLedger[len(convCtx.session.TurnLedger)-1].Normalize().TurnID
	}
	if turnID == "" {
		return hostedGenesisTurnSession{}, nil, newAppTheoryError(soulMintAppErrCodeConflict, "conversation cannot recover without an accepted turn")
	}
	modelSet := firstNonEmpty(convCtx.session.Model, convCtx.conv.Model, defaultSoulMintConversationModel)
	session := hostedGenesisTurnSession{
		conversationID:     strings.TrimSpace(convCtx.session.ConversationID),
		turnID:             strings.TrimSpace(turnID),
		modelSet:           strings.TrimSpace(modelSet),
		existingMessages:   append([]soulMintConversationMessage(nil), messages...),
		existingUsage:      convCtx.conv.Usage,
		expectedStatus:     hostedgenesis.StatusInProgress,
		expectedVersion:    convCtx.session.Version,
		progressVersion:    convCtx.session.Version,
		hasProgressVersion: true,
		conv:               convCtx.conv,
		session:            convCtx.session,
	}
	return session, messages, nil
}

func hostedGenesisRecoveryMessages(conv *models.SoulAgentMintConversation) ([]soulMintConversationMessage, *apptheory.AppTheoryError) {
	if conv == nil {
		return nil, newAppTheoryError(soulMintAppErrCodeNotFound, "conversation not found")
	}
	raw := strings.TrimSpace(models.DecodeSoulMintConversationBlob(conv.Messages))
	if raw == "" {
		return nil, newAppTheoryError(soulMintAppErrCodeConflict, "conversation cannot recover without stored messages")
	}
	var messages []soulMintConversationMessage
	if err := json.Unmarshal([]byte(raw), &messages); err != nil || len(messages) == 0 {
		return nil, newAppTheoryError(soulMintAppErrCodeConflict, "conversation cannot recover without stored messages")
	}
	for i := range messages {
		messages[i].Role = strings.ToLower(strings.TrimSpace(messages[i].Role))
		messages[i].Content = strings.TrimSpace(messages[i].Content)
	}
	return messages, nil
}
