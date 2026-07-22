package controlplane

import (
	"context"
	"errors"
	"log"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"

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
		log.Printf("controlplane: hosted genesis wait observation failed agent_hash=%s conversation_hash=%s", soulMintInstanceReadAuditHash(session.AgentID), soulMintInstanceReadAuditHash(session.ConversationID))
		return session, conv, nil
	}
	if !result.CannotCompletePendingTurn {
		return session, conv, nil
	}

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
			// A workload completion won the version race. Reload and project the
			// authoritative result instead of overwriting it with stale death.
			return s.reloadHostedGenesisAfterObservation(ctx.Context(), session, conv)
		}
		log.Printf("controlplane: hosted genesis terminal wait observation persist failed agent_hash=%s conversation_hash=%s", soulMintInstanceReadAuditHash(session.AgentID), soulMintInstanceReadAuditHash(session.ConversationID))
		return nil, nil, soulMintInstanceReadError(soulMintInstanceReadCodeInternal, "internal error", 500, nil)
	}
	reloadedSession, reloadedConv, reloadErr := s.reloadHostedGenesisAfterObservation(ctx.Context(), failed, conv)
	if reloadErr != nil {
		return nil, nil, reloadErr
	}
	return reloadedSession, reloadedConv, nil
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
