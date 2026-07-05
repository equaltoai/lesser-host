package controlplane

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"

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
func (s *Server) dispatchHostedGenesisRecoveryTurn(ctx *apptheory.Context, regCtx mintConversationRegistrationContext, session hostedGenesisTurnSession, conv *models.SoulAgentMintConversation, acceptedMessages []soulMintConversationMessage) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, int, *apptheory.AppError) {
	if session.session == nil || conv == nil {
		return nil, nil, 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if s.hostedGenesisMicroVMDispatcher == nil {
		log.Printf("controlplane: hosted genesis recovery dispatch unavailable agent_hash=%s conversation_hash=%s", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID))
		failedSession, failedConv, appErr := s.persistHostedGenesisAcceptedTurnFailure(ctx.Context(), session, conv, acceptedMessages, hostedGenesisFailureMicroVMUnavailable, strings.TrimSpace(ctx.RequestID), time.Now().UTC())
		if appErr != nil {
			return nil, nil, 0, appErr
		}
		return failedSession, failedConv, http.StatusOK, &apptheory.AppError{Code: appErrCodeMicroVMUnavailable, Message: "MicroVM recovery dispatch is unavailable"}
	}
	binding := session.session.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		log.Printf("controlplane: hosted genesis recovery dispatch binding invalid agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(regCtx.agentIDHex), soulMintInstanceReadAuditHash(session.conversationID), err)
		failedSession, failedConv, appErr := s.persistHostedGenesisAcceptedTurnFailure(ctx.Context(), session, conv, acceptedMessages, hostedGenesisFailureMicroVMUnavailable, strings.TrimSpace(ctx.RequestID), time.Now().UTC())
		if appErr != nil {
			return nil, nil, 0, appErr
		}
		return failedSession, failedConv, http.StatusOK, &apptheory.AppError{Code: appErrCodeMicroVMUnavailable, Message: "MicroVM recovery dispatch binding is invalid"}
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
		return failedSession, failedConv, http.StatusOK, &apptheory.AppError{Code: appErrCodeMicroVMUnavailable, Message: "MicroVM recovery dispatch failed"}
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
func (s *Server) reconcileHostedGenesisMicroVMRecovery(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext) (hostedGenesisMicroVMRecoveryResult, *apptheory.AppError) {
	if convCtx.session == nil || convCtx.session.MicroVMLifecycleRef == nil {
		return hostedGenesisMicroVMRecoveryResult{}, &apptheory.AppError{Code: appErrCodeMicroVMUnavailable, Message: "MicroVM execution state is unavailable for recovery"}
	}
	if s.hostedGenesisMicroVMDispatcher == nil {
		log.Printf("controlplane: hosted genesis microvm reconciliation unavailable agent_hash=%s conversation_hash=%s", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID))
		return hostedGenesisMicroVMRecoveryResult{}, &apptheory.AppError{Code: appErrCodeMicroVMUnavailable, Message: "MicroVM reconstruction is unavailable"}
	}
	binding := convCtx.session.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		log.Printf("controlplane: hosted genesis microvm recovery binding invalid agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), err)
		return hostedGenesisMicroVMRecoveryResult{}, &apptheory.AppError{Code: appErrCodeMicroVMUnavailable, Message: "MicroVM execution binding is invalid for recovery"}
	}
	ref := *convCtx.session.MicroVMLifecycleRef
	reconcileCtx, cancel := context.WithTimeout(detachedMintConversationContext(ctx.Context()), hostedGenesisRecoveryReconcileTimeout)
	defer cancel()
	result, reconcileErr := s.hostedGenesisMicroVMDispatcher.ReconcileMicroVM(reconcileCtx, strings.TrimSpace(ctx.RequestID), binding, ref)
	if reconcileErr != nil {
		log.Printf("controlplane: hosted genesis microvm reconciliation failed agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(convCtx.agentIDHex), soulMintInstanceReadAuditHash(convCtx.conversationID), reconcileErr)
		return hostedGenesisMicroVMRecoveryResult{}, &apptheory.AppError{Code: appErrCodeMicroVMUnavailable, Message: "MicroVM reconstruction failed"}
	}
	if result.Terminal {
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
		return hostedGenesisMicroVMRecoveryResult{}, &apptheory.AppError{Code: appErrCodeMicroVMUnavailable, Message: "MicroVM reconciled state is invalid for recovery"}
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
func (s *Server) persistHostedGenesisMicroVMRecoveryFailure(ctx *apptheory.Context, convCtx soulInstanceBootstrapConversationContext, reason string) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil || convCtx.session == nil || convCtx.conv == nil {
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
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
		return nil, nil, &apptheory.AppError{Code: "app.internal", Message: "failed to persist recovery failure"}
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

func hostedGenesisRecoveryTurnSession(convCtx soulInstanceBootstrapConversationContext) (hostedGenesisTurnSession, []soulMintConversationMessage, *apptheory.AppError) {
	if convCtx.session == nil || convCtx.conv == nil {
		return hostedGenesisTurnSession{}, nil, &apptheory.AppError{Code: soulMintAppErrCodeNotFound, Message: "conversation not found"}
	}
	if !hostedGenesisConversationMatchesSession(convCtx.session, convCtx.conv) ||
		!strings.EqualFold(strings.TrimSpace(convCtx.session.RegistrationID), strings.TrimSpace(convCtx.reg.ID)) {
		return hostedGenesisTurnSession{}, nil, &apptheory.AppError{Code: appErrCodeForbidden, Message: "conversation is outside the registration boundary"}
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
		return hostedGenesisTurnSession{}, nil, &apptheory.AppError{Code: soulMintAppErrCodeConflict, Message: "conversation cannot recover without an accepted turn"}
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

func hostedGenesisRecoveryMessages(conv *models.SoulAgentMintConversation) ([]soulMintConversationMessage, *apptheory.AppError) {
	if conv == nil {
		return nil, &apptheory.AppError{Code: soulMintAppErrCodeNotFound, Message: "conversation not found"}
	}
	raw := strings.TrimSpace(models.DecodeSoulMintConversationBlob(conv.Messages))
	if raw == "" {
		return nil, &apptheory.AppError{Code: soulMintAppErrCodeConflict, Message: "conversation cannot recover without stored messages"}
	}
	var messages []soulMintConversationMessage
	if err := json.Unmarshal([]byte(raw), &messages); err != nil || len(messages) == 0 {
		return nil, &apptheory.AppError{Code: soulMintAppErrCodeConflict, Message: "conversation cannot recover without stored messages"}
	}
	for i := range messages {
		messages[i].Role = strings.ToLower(strings.TrimSpace(messages[i].Role))
		messages[i].Content = strings.TrimSpace(messages[i].Content)
	}
	return messages, nil
}
