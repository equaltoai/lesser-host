package aiworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	apptheory "github.com/theory-cloud/apptheory/v2/runtime"

	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/manageddomain"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	hostedGenesisFailureLLMUnavailable              = "llm_unavailable"
	hostedGenesisFailureAssistantTurnFailed         = "assistant_turn_failed"
	hostedGenesisFailureMicroVMUnavailable          = "microvm_unavailable"
	hostedGenesisFailureInvalidCompletionState      = "invalid_completion_state"
	hostedGenesisFailureMissingProducedDeclarations = "missing_produced_declarations"
	hostedGenesisFailureInvalidProducedDeclarations = "invalid_produced_declarations"
	hostedGenesisFailureTenantBoundaryViolation     = "tenant_boundary_violation"
	hostedGenesisFailureOperatorActionRequired      = "operator_action_required"

	hostedGenesisSQSApproximateReceiveCount = "ApproximateReceiveCount"
	// Keep this limit contract-tested against HostedGenesisQueue's CDK redrive
	// policy. The final source-queue delivery must terminalize durable state
	// before SQS transfers the message to the alarm-only DLQ.
	hostedGenesisQueueMaxReceiveCount = 3
)

type hostedGenesisStore interface {
	GetSoulAgentRegistration(ctx context.Context, id string) (*models.SoulAgentRegistration, error)
	GetDomain(ctx context.Context, domain string) (*models.Domain, error)
	GetInstance(ctx context.Context, slug string) (*models.Instance, error)
	GetHostedGenesisSession(ctx context.Context, instanceSlug string, conversationID string) (*models.HostedGenesisSession, error)
	UpdateHostedGenesisSession(ctx context.Context, item *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status) error
	FailHostedGenesisSessionAndConversation(ctx context.Context, session *models.HostedGenesisSession, expectedVersion int64, expectedStatus hostedgenesis.Status, conversation *models.SoulAgentMintConversation) error
	GetSoulAgentMintConversation(ctx context.Context, agentID string, conversationID string) (*models.SoulAgentMintConversation, error)
	PutSoulAgentMintConversation(ctx context.Context, item *models.SoulAgentMintConversation) error
	GetSoulMintConversationIdempotency(ctx context.Context, instanceSlug string, registrationID string, idempotencyKey string) (*models.SoulMintConversationIdempotency, error)
}

func (s *Server) handleHostedGenesisQueueMessage(ctx *apptheory.EventContext, msg events.SQSMessage) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}
	if ctx == nil {
		return fmt.Errorf("event context is nil")
	}
	var qm hostedgenesis.QueueMessage
	if err := json.Unmarshal([]byte(msg.Body), &qm); err != nil {
		return nil
	}
	if strings.TrimSpace(qm.Kind) != hostedgenesis.QueueMessageKind {
		return nil
	}
	switch strings.TrimSpace(qm.Step) {
	case hostedgenesis.StepMicroVMDispatch:
		receiveCount, err := hostedGenesisSQSReceiveCount(msg)
		if err != nil {
			return err
		}
		return s.processHostedGenesisMicroVMDispatch(ctx.Context(), ctx.RequestID, qm, receiveCount)
	default:
		return nil
	}
}

func hostedGenesisSQSReceiveCount(msg events.SQSMessage) (int, error) {
	raw := strings.TrimSpace(msg.Attributes[hostedGenesisSQSApproximateReceiveCount])
	receiveCount, err := strconv.Atoi(raw)
	if err != nil || receiveCount < 1 {
		return 0, fmt.Errorf("hosted genesis SQS receive count is invalid")
	}
	return receiveCount, nil
}

func (s *Server) hostedGenesisStore() (hostedGenesisStore, bool) {
	if s == nil || s.store == nil {
		return nil, false
	}
	st, ok := s.store.(hostedGenesisStore)
	return st, ok
}

func (s *Server) loadAndValidateHostedGenesisJob(ctx context.Context, st hostedGenesisStore, msg hostedgenesis.QueueMessage) (*models.SoulAgentRegistration, *models.SoulAgentMintConversation, *models.HostedGenesisSession, error) {
	reg, err := loadHostedGenesisRegistrationForJob(ctx, st, msg.RegistrationID)
	if err != nil || reg == nil {
		return nil, nil, nil, err
	}
	if !hostedGenesisRegistrationMatchesJob(reg, msg) {
		return nil, nil, nil, nil
	}
	session, err := loadHostedGenesisSessionForJob(ctx, st, msg)
	if err != nil || session == nil {
		return reg, nil, nil, err
	}
	valid, err := s.validateHostedGenesisSessionIdentity(ctx, st, session, msg)
	if err != nil || !valid {
		return nil, nil, nil, err
	}
	valid, err = s.validateHostedGenesisBoundary(ctx, st, reg, session, msg)
	if err != nil || !valid {
		return nil, nil, nil, err
	}
	valid, err = s.validateHostedGenesisIdempotency(ctx, st, session, msg)
	if err != nil || !valid {
		return nil, nil, nil, err
	}
	conv, err := loadHostedGenesisConversationForJob(ctx, st, msg)
	if err != nil {
		return nil, nil, nil, err
	}
	if conv == nil {
		persistErr := s.markHostedGenesisSessionFailed(ctx, st, session, hostedGenesisFailureInvalidCompletionState, msg.RequestID)
		return nil, nil, nil, persistErr
	}
	conv.Messages = models.DecodeSoulMintConversationBlob(conv.Messages)
	conv.ProducedDeclarations = models.DecodeSoulMintConversationBlob(conv.ProducedDeclarations)
	return reg, conv, session, nil
}

func loadHostedGenesisRegistrationForJob(ctx context.Context, st hostedGenesisStore, registrationID string) (*models.SoulAgentRegistration, error) {
	reg, err := st.GetSoulAgentRegistration(ctx, registrationID)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("load hosted genesis registration: %w", err)
	}
	return reg, nil
}

func loadHostedGenesisSessionForJob(ctx context.Context, st hostedGenesisStore, msg hostedgenesis.QueueMessage) (*models.HostedGenesisSession, error) {
	session, err := st.GetHostedGenesisSession(ctx, msg.InstanceSlug, msg.ConversationID)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("load hosted genesis session: %w", err)
	}
	return session, nil
}

func loadHostedGenesisConversationForJob(ctx context.Context, st hostedGenesisStore, msg hostedgenesis.QueueMessage) (*models.SoulAgentMintConversation, error) {
	conv, err := st.GetSoulAgentMintConversation(ctx, msg.AgentID, msg.ConversationID)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("load hosted genesis conversation: %w", err)
	}
	return conv, nil
}

func (s *Server) validateHostedGenesisSessionIdentity(ctx context.Context, st hostedGenesisStore, session *models.HostedGenesisSession, msg hostedgenesis.QueueMessage) (bool, error) {
	if strings.EqualFold(strings.TrimSpace(session.RegistrationID), strings.TrimSpace(msg.RegistrationID)) &&
		strings.EqualFold(strings.TrimSpace(session.AgentID), strings.TrimSpace(msg.AgentID)) {
		return true, nil
	}
	authoritative := msg
	authoritative.AgentID = session.AgentID
	authoritative.ConversationID = session.ConversationID
	conv, err := loadHostedGenesisConversationForJob(ctx, st, authoritative)
	if err != nil {
		return false, err
	}
	return false, s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureTenantBoundaryViolation, msg.RequestID)
}

func (s *Server) validateHostedGenesisBoundary(ctx context.Context, st hostedGenesisStore, reg *models.SoulAgentRegistration, session *models.HostedGenesisSession, msg hostedgenesis.QueueMessage) (bool, error) {
	valid, err := s.hostedGenesisJobBoundaryValid(ctx, st, reg, msg)
	if err != nil || valid {
		return valid, err
	}
	conv, err := loadHostedGenesisConversationForJob(ctx, st, msg)
	if err != nil {
		return false, err
	}
	return false, s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureTenantBoundaryViolation, msg.RequestID)
}

func (s *Server) validateHostedGenesisIdempotency(ctx context.Context, st hostedGenesisStore, session *models.HostedGenesisSession, msg hostedgenesis.QueueMessage) (bool, error) {
	valid, err := hostedGenesisJobIdempotencyValid(ctx, st, msg)
	if err != nil || valid {
		return valid, err
	}
	conv, err := loadHostedGenesisConversationForJob(ctx, st, msg)
	if err != nil {
		return false, err
	}
	return false, s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureInvalidCompletionState, msg.RequestID)
}

func hostedGenesisRegistrationMatchesJob(reg *models.SoulAgentRegistration, msg hostedgenesis.QueueMessage) bool {
	return reg != nil && strings.EqualFold(strings.TrimSpace(reg.AgentID), strings.TrimSpace(msg.AgentID))
}

func (s *Server) hostedGenesisJobBoundaryValid(ctx context.Context, st hostedGenesisStore, reg *models.SoulAgentRegistration, msg hostedgenesis.QueueMessage) (bool, error) {
	domain, inst, ok, err := s.hostedGenesisJobBoundaryDomainAndInstance(ctx, st, strings.TrimSpace(reg.DomainNormalized), strings.TrimSpace(msg.InstanceSlug))
	if err != nil || !ok || domain == nil || inst == nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(domain.InstanceSlug), strings.TrimSpace(msg.InstanceSlug)) &&
		strings.EqualFold(strings.TrimSpace(inst.Slug), strings.TrimSpace(msg.InstanceSlug)), nil
}

func (s *Server) hostedGenesisJobBoundaryDomainAndInstance(ctx context.Context, st hostedGenesisStore, normalizedDomain string, instanceSlug string) (*models.Domain, *models.Instance, bool, error) {
	if st == nil {
		return nil, nil, false, fmt.Errorf("hosted genesis store not initialized")
	}
	normalizedDomain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(normalizedDomain), "."))
	instanceSlug = strings.TrimSpace(instanceSlug)
	if normalizedDomain == "" || instanceSlug == "" {
		return nil, nil, false, nil
	}
	domain, inst, ok, err := hostedGenesisDirectDomainAndInstance(ctx, st, normalizedDomain, instanceSlug)
	if err != nil {
		return nil, nil, false, err
	}
	if ok {
		return domain, inst, true, nil
	}
	stage := ""
	if s != nil {
		stage = s.cfg.Stage
	}
	baseDomain, ok := manageddomain.BaseDomainFromStageDomain(stage, normalizedDomain)
	if !ok {
		return nil, nil, false, nil
	}
	domain, inst, ok, err = hostedGenesisDirectDomainAndInstance(ctx, st, baseDomain, instanceSlug)
	if err != nil {
		return nil, nil, false, err
	}
	if !ok || domain == nil || inst == nil {
		return nil, nil, false, nil
	}
	if strings.TrimSpace(domain.Type) != models.DomainTypePrimary ||
		!strings.EqualFold(strings.TrimSpace(domain.VerificationMethod), "managed") ||
		!strings.EqualFold(strings.TrimSpace(inst.HostedBaseDomain), strings.TrimSpace(domain.Domain)) {
		return nil, nil, false, nil
	}
	return domain, inst, true, nil
}

func hostedGenesisDirectDomainAndInstance(ctx context.Context, st hostedGenesisStore, normalizedDomain string, instanceSlug string) (*models.Domain, *models.Instance, bool, error) {
	domain, domainErr := st.GetDomain(ctx, normalizedDomain)
	if domainErr != nil {
		if store.IsNotFound(domainErr) {
			return nil, nil, false, nil
		}
		return nil, nil, false, fmt.Errorf("load hosted genesis domain: %w", domainErr)
	}
	if domain == nil || !hostedGenesisDomainActive(domain) || !strings.EqualFold(strings.TrimSpace(domain.InstanceSlug), instanceSlug) {
		return nil, nil, false, nil
	}
	inst, instErr := st.GetInstance(ctx, instanceSlug)
	if instErr != nil {
		if store.IsNotFound(instErr) {
			return nil, nil, false, nil
		}
		return nil, nil, false, fmt.Errorf("load hosted genesis instance: %w", instErr)
	}
	if inst == nil || !strings.EqualFold(strings.TrimSpace(inst.Slug), instanceSlug) {
		return nil, nil, false, nil
	}
	return domain, inst, true, nil
}

func hostedGenesisJobIdempotencyValid(ctx context.Context, st hostedGenesisStore, msg hostedgenesis.QueueMessage) (bool, error) {
	if strings.TrimSpace(msg.IdempotencyKey) == "" {
		return true, nil
	}
	idem, idemErr := st.GetSoulMintConversationIdempotency(ctx, msg.InstanceSlug, msg.RegistrationID, msg.IdempotencyKey)
	if idemErr != nil {
		if store.IsNotFound(idemErr) {
			return false, nil
		}
		return false, fmt.Errorf("load hosted genesis idempotency reservation: %w", idemErr)
	}
	return idem != nil &&
		strings.EqualFold(strings.TrimSpace(idem.ConversationID), strings.TrimSpace(msg.ConversationID)) &&
		strings.EqualFold(strings.TrimSpace(idem.TurnID), strings.TrimSpace(msg.TurnID)), nil
}

func (s *Server) markHostedGenesisConversationFailed(ctx context.Context, st hostedGenesisStore, conv *models.SoulAgentMintConversation, session *models.HostedGenesisSession, reason string, requestID string) error {
	return s.markHostedGenesisConversationFailedWithDetail(ctx, st, conv, session, reason, "", requestID)
}

func (s *Server) markHostedGenesisConversationFailedWithDetail(ctx context.Context, st hostedGenesisStore, conv *models.SoulAgentMintConversation, session *models.HostedGenesisSession, reason string, detail string, requestID string) error {
	if st == nil {
		return fmt.Errorf("hosted genesis store not initialized")
	}
	if conv == nil {
		return s.markHostedGenesisSessionFailedWithDetail(ctx, st, session, reason, detail, requestID)
	}
	now := time.Now().UTC()
	conv.Status = models.SoulMintConversationStatusFailed
	conv.StatusReason = hostedGenesisFailureReasonForStorage(reason, detail)
	conv.RequestID = firstNonEmptyWorker(requestID, conv.RequestID)
	conv.UpdatedAt = now
	conv.CompletedAt = now
	encodeHostedGenesisPrivateFields(conv)
	if err := conv.UpdateKeys(); err != nil {
		return err
	}
	if session == nil {
		return st.PutSoulAgentMintConversation(ctx, conv)
	}
	expectedStatus := hostedgenesis.NormalizeStatus(session.Status)
	session.Status = string(hostedgenesis.StatusFailed)
	session.Failure = hostedGenesisFailureFromWorkerReasonWithDetail(reason, detail)
	session.RequestID = firstNonEmptyWorker(requestID, session.RequestID)
	session.UpdatedAt = now
	session.CompletedAt = now
	return st.FailHostedGenesisSessionAndConversation(ctx, session, session.Version, expectedStatus, conv)
}

func (s *Server) markHostedGenesisSessionFailed(ctx context.Context, st hostedGenesisStore, session *models.HostedGenesisSession, reason string, requestID string) error {
	return s.markHostedGenesisSessionFailedWithDetail(ctx, st, session, reason, "", requestID)
}

func (s *Server) markHostedGenesisSessionFailedWithDetail(ctx context.Context, st hostedGenesisStore, session *models.HostedGenesisSession, reason string, detail string, requestID string) error {
	if st == nil {
		return fmt.Errorf("hosted genesis store not initialized")
	}
	if session == nil {
		return nil
	}
	now := time.Now().UTC()
	expectedStatus := hostedgenesis.NormalizeStatus(session.Status)
	session.Status = string(hostedgenesis.StatusFailed)
	session.Failure = hostedGenesisFailureFromWorkerReasonWithDetail(reason, detail)
	session.RequestID = firstNonEmptyWorker(requestID, session.RequestID)
	session.UpdatedAt = now
	session.CompletedAt = now
	return st.UpdateHostedGenesisSession(ctx, session, session.Version, expectedStatus)
}

func encodeHostedGenesisPrivateFields(conv *models.SoulAgentMintConversation) {
	if conv == nil {
		return
	}
	conv.Messages = models.EncodeSoulMintConversationBlob(models.DecodeSoulMintConversationBlob(conv.Messages))
	conv.ProducedDeclarations = models.EncodeSoulMintConversationBlob(models.DecodeSoulMintConversationBlob(conv.ProducedDeclarations))
}

func hostedGenesisFailureFromWorkerReason(reason string) *hostedgenesis.Failure {
	return hostedGenesisFailureFromWorkerReasonWithDetail(reason, "")
}

var hostedGenesisWorkerFailureCodes = map[string]hostedgenesis.FailureCode{
	hostedGenesisFailureLLMUnavailable:              hostedgenesis.FailureCodeLLMUnavailable,
	hostedGenesisFailureAssistantTurnFailed:         hostedgenesis.FailureCodeAssistantTurnFailed,
	hostedGenesisFailureMicroVMUnavailable:          hostedgenesis.FailureCodeMicroVMUnavailable,
	hostedGenesisFailureMissingProducedDeclarations: hostedgenesis.FailureCodeMissingProducedDeclarations,
	hostedGenesisFailureInvalidProducedDeclarations: hostedgenesis.FailureCodeInvalidProducedDeclarations,
	hostedGenesisFailureTenantBoundaryViolation:     hostedgenesis.FailureCodeTenantBoundaryViolation,
	hostedGenesisFailureOperatorActionRequired:      hostedgenesis.FailureCodeOperatorActionRequired,
}

func workerRecoveryForFailureCode(code hostedgenesis.FailureCode) (hostedgenesis.RecoveryAction, bool) {
	switch code {
	case hostedgenesis.FailureCodeLLMUnavailable, hostedgenesis.FailureCodeAssistantTurnFailed, hostedgenesis.FailureCodeMicroVMUnavailable:
		return hostedgenesis.RecoveryActionRetrySameStep, true
	case hostedgenesis.FailureCodeTenantBoundaryViolation, hostedgenesis.FailureCodeOperatorActionRequired:
		return hostedgenesis.RecoveryActionOperatorAction, false
	case hostedgenesis.FailureCodeMissingProducedDeclarations, hostedgenesis.FailureCodeInvalidProducedDeclarations:
		return hostedgenesis.RecoveryActionRestartSoulBootstrap, false
	default:
		return hostedgenesis.RecoveryActionRefreshState, false
	}
}

func hostedGenesisFailureFromWorkerReasonWithDetail(reason string, detail string) *hostedgenesis.Failure {
	code, ok := hostedGenesisWorkerFailureCodes[strings.TrimSpace(reason)]
	if !ok {
		code = hostedgenesis.FailureCodeInvalidCompletionState
	}
	action, retryable := workerRecoveryForFailureCode(code)
	recoveryReason := hostedgenesis.SanitizeFailureReason(code, detail)
	return &hostedgenesis.Failure{
		Code:      code,
		Message:   hostedgenesis.FailureMessage(code),
		Retryable: retryable,
		Recovery: hostedgenesis.Recovery{
			Action:            action,
			MaxAttempts:       maxAttemptsFromWorkerRecovery(action),
			RetryAfterSeconds: retryAfterFromWorkerRecovery(action),
			Reason:            recoveryReason,
		},
	}
}

func hostedGenesisFailureReasonForStorage(reason string, detail string) string {
	code := hostedGenesisFailureFromWorkerReason(reason).Code
	return hostedgenesis.SanitizeFailureReason(code, detail)
}

func maxAttemptsFromWorkerRecovery(action hostedgenesis.RecoveryAction) int {
	if action == hostedgenesis.RecoveryActionRetrySameStep {
		return 3
	}
	return 0
}

func retryAfterFromWorkerRecovery(action hostedgenesis.RecoveryAction) int {
	if action == hostedgenesis.RecoveryActionRetrySameStep {
		return 30
	}
	return 0
}

func hostedGenesisDomainActive(domain *models.Domain) bool {
	if domain == nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(domain.Status))
	return status == models.DomainStatusVerified || status == models.DomainStatusActive
}

func firstNonEmptyWorker(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func hostedGenesisAuditHash(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}
