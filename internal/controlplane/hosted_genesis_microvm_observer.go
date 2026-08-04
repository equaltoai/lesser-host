package controlplane

import (
	"context"
	"errors"
	"log"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	runtimemicrovm "github.com/theory-cloud/apptheory/v3/runtime/microvm"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/completion"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// observeHostedGenesisMicroVMOnRead makes normal wait-only polling an
// authoritative lifecycle observation rather than a nudge/recovery action. It
// uses AppTheory's canonical controller GET through ReconcileMicroVM; it never
// invokes, resumes, restarts, or locally models a MicroVM lifecycle.
//
// A live VM leaves Host truth untouched. A provider-suspended, terminated,
// failed, or maximum-duration-expired VM is atomically projected to the
// existing typed microvm_unavailable failure through CompletionWriter, whose
// TableTheory transaction is guarded by exact turn, status, and optimistic-lock
// version. The observer never resumes uncertain in-flight provider work.
func (s *Server) observeHostedGenesisMicroVMOnRead(ctx *apptheory.Context, session *models.HostedGenesisSession, conv *models.SoulAgentMintConversation) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	if !hostedGenesisSessionNeedsMicroVMReconciliation(session) {
		return session, conv, nil
	}
	if s == nil || s.hostedGenesisMicroVMDispatcher == nil || s.store == nil || conv == nil {
		log.Printf("controlplane: hosted genesis wait observation unavailable agent_hash=%s conversation_hash=%s", soulMintInstanceReadAuditHash(session.AgentID), soulMintInstanceReadAuditHash(session.ConversationID))
		return session, conv, nil
	}
	binding := session.MicroVMSessionBinding()
	if err := binding.Validate(); err != nil {
		log.Printf("controlplane: hosted genesis wait observation binding invalid agent_hash=%s conversation_hash=%s", soulMintInstanceReadAuditHash(session.AgentID), soulMintInstanceReadAuditHash(session.ConversationID))
		return session, conv, nil
	}
	requestID := firstNonEmpty(strings.TrimSpace(ctx.RequestID), strings.TrimSpace(session.RequestID))
	observeCtx, cancel := context.WithTimeout(detachedMintConversationContext(ctx.Context()), hostedGenesisRecoveryReconcileTimeout)
	defer cancel()
	result, err := s.hostedGenesisMicroVMDispatcher.ReconcileMicroVM(observeCtx, requestID, binding, *session.MicroVMLifecycleRef)
	if err != nil {
		logHostedGenesisTerminalObservation(session, "unknown", "reconcile_failed", "none")
		return session, conv, nil
	}
	if !result.CannotCompletePendingTurn {
		return session, conv, nil
	}
	lifecycleClass := hostedGenesisTerminalObservationLifecycleClass(result)
	logHostedGenesisTerminalObservation(session, lifecycleClass, "terminal_observed", "none")

	writer := completion.NewCompletionWriter(s.store, nil)
	failed, err := writer.RecordFailure(ctx.Context(), completion.CompletionTurn{
		InstanceSlug:   session.InstanceSlug,
		ConversationID: session.ConversationID,
		TurnID:         session.LatestTurnID,
		RequestID:      requestID,
	}, completion.CompletionFailure{
		Code:      hostedgenesis.FailureCodeMicroVMUnavailable,
		Retryable: true,
		Recovery: hostedgenesis.Recovery{
			Action:            hostedgenesis.RecoveryActionRetrySameStep,
			MaxAttempts:       3,
			RetryAfterSeconds: 5,
			Reason:            string(hostedgenesis.FailureCodeMicroVMUnavailable),
		},
	})
	if err != nil {
		if errors.Is(err, completion.ErrCompletionConflict) {
			reloadedSession, reloadedConv, reloadErr := s.reloadHostedGenesisAfterObservation(ctx.Context(), session, conv)
			if reloadErr != nil {
				logHostedGenesisTerminalObservation(session, lifecycleClass, "reload_failed", "completion_conflict")
				return nil, nil, reloadErr
			}
			classification := hostedGenesisObservationConflictClassification(session, reloadedSession)
			logHostedGenesisTerminalObservation(reloadedSession, lifecycleClass, "authoritative_reload", classification)
			if classification != hostedGenesisObservationConflictWorkloadWon {
				// Only a recognized workload progression wins this race. An exact
				// unchanged pending row (including a conflicting idempotency guard),
				// or an unrelated state advance, is not evidence of completion. Fail
				// loudly instead of swallowing the conditional failure.
				return nil, nil, soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", 500, nil)
			}
			return reloadedSession, reloadedConv, nil
		}
		logHostedGenesisTerminalObservation(session, lifecycleClass, "persist_failed", "non_conditional")
		return nil, nil, soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", 500, nil)
	}
	reloadedSession, reloadedConv, reloadErr := s.reloadHostedGenesisAfterObservation(ctx.Context(), failed, conv)
	if reloadErr != nil {
		logHostedGenesisTerminalObservation(failed, lifecycleClass, "reload_failed", "none")
		return nil, nil, reloadErr
	}
	logHostedGenesisTerminalObservation(reloadedSession, lifecycleClass, "converged", "none")
	return reloadedSession, reloadedConv, nil
}

const (
	hostedGenesisObservationConflictUnchangedPending = "unchanged_pending"
	hostedGenesisObservationConflictWorkloadWon      = "workload_won"
	hostedGenesisObservationConflictStateAdvanced    = "authoritative_state_advanced"
)

func hostedGenesisTerminalObservationLifecycleClass(result hostedgenesis.MicroVMReconcileResult) string {
	switch result.LifecycleRef.LifecycleState {
	case runtimemicrovm.StateSuspended:
		return "suspended"
	case runtimemicrovm.StateTerminated:
		return "terminated"
	case runtimemicrovm.StateFailed:
		return "failed"
	default:
		// CannotCompletePendingTurn with a non-terminal, non-suspended
		// lifecycle is the controller-expiry path.
		return "expired"
	}
}

func hostedGenesisObservationConflictClassification(expected *models.HostedGenesisSession, actual *models.HostedGenesisSession) string {
	if expected != nil && actual != nil &&
		expected.Version == actual.Version &&
		hostedgenesis.NormalizeStatus(expected.Status) == hostedgenesis.NormalizeStatus(actual.Status) &&
		strings.TrimSpace(expected.LatestTurnID) == strings.TrimSpace(actual.LatestTurnID) {
		return hostedGenesisObservationConflictUnchangedPending
	}
	if expected != nil && actual != nil &&
		actual.Version > expected.Version &&
		strings.TrimSpace(actual.LatestTurnID) == strings.TrimSpace(expected.LatestTurnID) {
		actualStatus := hostedgenesis.NormalizeStatus(actual.Status)
		if actualStatus == hostedgenesis.StatusAssistantTurnReady ||
			actualStatus == hostedgenesis.StatusDeclarationReady ||
			actualStatus == hostedgenesis.StatusFailed {
			return hostedGenesisObservationConflictWorkloadWon
		}
	}
	return hostedGenesisObservationConflictStateAdvanced
}

func logHostedGenesisTerminalObservation(session *models.HostedGenesisSession, lifecycleClass string, outcome string, conflict string) {
	if session == nil {
		return
	}
	log.Printf("controlplane: hosted genesis terminal wait observation agent_hash=%s conversation_hash=%s turn_hash=%s session_version=%d durable_status=%s lifecycle_class=%s outcome=%s conflict=%s",
		soulMintInstanceReadAuditHash(session.AgentID),
		soulMintInstanceReadAuditHash(session.ConversationID),
		soulMintInstanceReadAuditHash(session.LatestTurnID),
		session.Version,
		hostedgenesis.NormalizeStatus(session.Status),
		lifecycleClass,
		outcome,
		conflict,
	)
}

func (s *Server) reloadHostedGenesisAfterObservation(ctx context.Context, session *models.HostedGenesisSession, conv *models.SoulAgentMintConversation) (*models.HostedGenesisSession, *models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	reloadedSession, err := s.store.GetHostedGenesisSession(ctx, session.InstanceSlug, session.ConversationID)
	if err != nil || reloadedSession == nil {
		return nil, nil, soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", 500, nil)
	}
	reloadedConv, err := s.store.GetSoulAgentMintConversation(ctx, session.AgentID, session.ConversationID)
	if err != nil || reloadedConv == nil {
		return nil, nil, soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", 500, nil)
	}
	decodeMintConversationFields(reloadedConv)
	return reloadedSession, reloadedConv, nil
}
