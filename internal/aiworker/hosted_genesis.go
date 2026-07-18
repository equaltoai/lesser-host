package aiworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/mintprompt"
	"github.com/equaltoai/lesser-host/internal/manageddomain"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const hostedGenesisRunTimeout = 2 * time.Minute

const (
	hostedGenesisFailureLLMUnavailable              = "llm_unavailable"
	hostedGenesisFailureAssistantTurnFailed         = "assistant_turn_failed"
	hostedGenesisFailureDeclarationExtractionFailed = "declaration_extraction_failed"
	hostedGenesisFailureMicroVMUnavailable          = "microvm_unavailable"
	hostedGenesisFailureInvalidCompletionState      = "invalid_completion_state"
	hostedGenesisFailureMissingProducedDeclarations = "missing_produced_declarations"
	hostedGenesisFailureInvalidProducedDeclarations = "invalid_produced_declarations"
	hostedGenesisFailureTenantBoundaryViolation     = "tenant_boundary_violation"
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

type hostedGenesisMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type hostedGenesisProducedDeclarations struct {
	SelfDescription soul.SelfDescriptionV2 `json:"selfDescription"`
	Capabilities    []soul.CapabilityV2    `json:"capabilities"`
	Boundaries      []soul.BoundaryV2      `json:"boundaries"`
	Transparency    map[string]any         `json:"transparency"`
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
		return s.processHostedGenesisMicroVMDispatch(ctx.Context(), ctx.RequestID, qm)
	case hostedgenesis.StepAssistantTurn:
		return s.processHostedGenesisAssistantTurn(ctx.Context(), ctx.RequestID, qm)
	case hostedgenesis.StepDeclarationExtraction:
		return s.processHostedGenesisDeclarationExtraction(ctx.Context(), ctx.RequestID, qm)
	default:
		return nil
	}
}

func (s *Server) hostedGenesisStore() (hostedGenesisStore, bool) {
	if s == nil || s.store == nil {
		return nil, false
	}
	st, ok := s.store.(hostedGenesisStore)
	return st, ok
}

func (s *Server) processHostedGenesisAssistantTurn(ctx context.Context, workerRequestID string, msg hostedgenesis.QueueMessage) error {
	st, ok := s.hostedGenesisStore()
	if !ok {
		return fmt.Errorf("hosted genesis store not initialized")
	}
	reg, conv, session, err := s.loadAndValidateHostedGenesisJob(ctx, st, msg)
	if err != nil {
		return err
	}
	if !hostedGenesisAssistantJobReady(reg, conv, session, msg) {
		return nil
	}
	run, ok, err := s.prepareHostedGenesisAssistantRun(ctx, st, conv, session, workerRequestID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return s.runAndPersistHostedGenesisAssistant(ctx, st, reg, conv, session, msg, workerRequestID, run)
}

type hostedGenesisAssistantRun struct {
	messages    []hostedGenesisMessage
	llmMessages []llm.MintConversationMessage
	modelSet    string
	apiKey      string
}

func hostedGenesisAssistantJobReady(reg *models.SoulAgentRegistration, conv *models.SoulAgentMintConversation, session *models.HostedGenesisSession, msg hostedgenesis.QueueMessage) bool {
	if reg == nil || conv == nil || session == nil {
		return false
	}
	return strings.TrimSpace(session.LatestTurnID) == strings.TrimSpace(msg.TurnID) && hostedgenesis.NormalizeStatus(session.Status) == hostedgenesis.StatusInProgress
}

func (s *Server) prepareHostedGenesisAssistantRun(ctx context.Context, st hostedGenesisStore, conv *models.SoulAgentMintConversation, session *models.HostedGenesisSession, workerRequestID string) (hostedGenesisAssistantRun, bool, error) {
	messages, err := decodeHostedGenesisMessages(conv.Messages)
	if err != nil || len(messages) == 0 {
		persistErr := s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureInvalidCompletionState, workerRequestID)
		return hostedGenesisAssistantRun{}, false, persistErr
	}
	modelSet := firstNonEmptyWorker(session.Model, conv.Model)
	apiKey, appErr := hostedGenesisAPIKey(ctx, modelSet)
	if appErr != nil {
		persistErr := s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureLLMUnavailable, workerRequestID)
		return hostedGenesisAssistantRun{}, false, persistErr
	}
	return hostedGenesisAssistantRun{
		messages:    messages,
		llmMessages: hostedGenesisLLMMessages(messages),
		modelSet:    modelSet,
		apiKey:      apiKey,
	}, true, nil
}

func (s *Server) runAndPersistHostedGenesisAssistant(ctx context.Context, st hostedGenesisStore, reg *models.SoulAgentRegistration, conv *models.SoulAgentMintConversation, session *models.HostedGenesisSession, msg hostedgenesis.QueueMessage, workerRequestID string, run hostedGenesisAssistantRun) error {
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hostedGenesisRunTimeout)
	defer cancel()
	fullResponse, usage, err := runHostedGenesisAssistantModel(runCtx, run.apiKey, run.modelSet, hostedGenesisSystemPrompt(reg), run.llmMessages)
	if err != nil || strings.TrimSpace(fullResponse) == "" {
		log.Printf("aiworker: hosted genesis assistant turn failed agent_hash=%s conversation_hash=%s provider=%s failure_code=%s", hostedGenesisAuditHash(conv.AgentID), hostedGenesisAuditHash(conv.ConversationID), hostedGenesisProvider(run.modelSet), hostedGenesisFailureAssistantTurnFailed)
		if persistErr := s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureAssistantTurnFailed, workerRequestID); persistErr != nil {
			return persistErr
		}
		return err
	}
	run.messages = append(run.messages, hostedGenesisMessage{Role: "assistant", Content: fullResponse})
	messagesJSON, err := json.Marshal(run.messages)
	if err != nil {
		return s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureAssistantTurnFailed, workerRequestID)
	}
	return persistHostedGenesisAssistantResponse(ctx, st, conv, session, msg, workerRequestID, string(messagesJSON), usage, len(run.messages))
}

func persistHostedGenesisAssistantResponse(ctx context.Context, st hostedGenesisStore, conv *models.SoulAgentMintConversation, session *models.HostedGenesisSession, msg hostedgenesis.QueueMessage, workerRequestID string, messagesJSON string, usage models.AIUsage, messageCount int) error {
	now := time.Now().UTC()
	conv.Messages = models.EncodeSoulMintConversationBlob(messagesJSON)
	conv.Usage = addAIUsageWorker(conv.Usage, usage)
	conv.Status = models.SoulMintConversationStatusAssistantTurnReady
	conv.StatusReason = ""
	conv.RequestID = firstNonEmptyWorker(msg.RequestID, workerRequestID, conv.RequestID)
	conv.UpdatedAt = now
	encodeHostedGenesisPrivateFields(conv)
	_ = conv.UpdateKeys()
	if err := st.PutSoulAgentMintConversation(ctx, conv); err != nil {
		return err
	}
	session.Status = string(hostedgenesis.StatusAssistantTurnReady)
	session.MessageCount = maxInt(session.MessageCount, messageCount)
	session.AssistantCheckpointRef = hostedgenesis.CheckpointRef("assistant", session.ConversationID, msg.TurnID)
	session.RequestID = conv.RequestID
	session.UpdatedAt = now
	return st.UpdateHostedGenesisSession(ctx, session, session.Version, hostedgenesis.StatusInProgress)
}

func (s *Server) processHostedGenesisDeclarationExtraction(ctx context.Context, workerRequestID string, msg hostedgenesis.QueueMessage) error {
	st, ok := s.hostedGenesisStore()
	if !ok {
		return fmt.Errorf("hosted genesis store not initialized")
	}
	reg, conv, session, err := s.loadAndValidateHostedGenesisJob(ctx, st, msg)
	if err != nil {
		return err
	}
	if !hostedGenesisDeclarationJobReady(reg, conv, session) {
		return nil
	}
	run, ok, err := s.prepareHostedGenesisDeclarationRun(ctx, st, reg, conv, session, workerRequestID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return s.runAndPersistHostedGenesisDeclaration(ctx, st, conv, session, msg, workerRequestID, run)
}

type hostedGenesisDeclarationRun struct {
	input    llm.MintConversationDeclarationsInput
	modelSet string
	apiKey   string
}

func hostedGenesisDeclarationJobReady(reg *models.SoulAgentRegistration, conv *models.SoulAgentMintConversation, session *models.HostedGenesisSession) bool {
	return reg != nil && conv != nil && session != nil && hostedgenesis.NormalizeStatus(session.Status) == hostedgenesis.StatusDeclarationExtractionPending
}

func (s *Server) prepareHostedGenesisDeclarationRun(ctx context.Context, st hostedGenesisStore, reg *models.SoulAgentRegistration, conv *models.SoulAgentMintConversation, session *models.HostedGenesisSession, workerRequestID string) (hostedGenesisDeclarationRun, bool, error) {
	messages, err := decodeHostedGenesisMessages(conv.Messages)
	if err != nil || len(messages) == 0 || !hostedGenesisHasAssistant(messages) {
		persistErr := s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureInvalidCompletionState, workerRequestID)
		return hostedGenesisDeclarationRun{}, false, persistErr
	}
	modelSet := firstNonEmptyWorker(session.Model, conv.Model)
	apiKey, appErr := hostedGenesisAPIKey(ctx, modelSet)
	if appErr != nil {
		persistErr := s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureLLMUnavailable, workerRequestID)
		return hostedGenesisDeclarationRun{}, false, persistErr
	}
	return hostedGenesisDeclarationRun{
		input:    hostedGenesisDeclarationInput(reg, messages),
		modelSet: modelSet,
		apiKey:   apiKey,
	}, true, nil
}

func hostedGenesisDeclarationInput(reg *models.SoulAgentRegistration, messages []hostedGenesisMessage) llm.MintConversationDeclarationsInput {
	in := llm.MintConversationDeclarationsInput{
		Registration: llm.MintConversationRegistrationContext{
			Domain:               strings.TrimSpace(reg.DomainNormalized),
			LocalID:              strings.TrimSpace(reg.LocalID),
			AgentID:              strings.TrimSpace(reg.AgentID),
			DeclaredCapabilities: hostedgenesis.FilterDeclaredCapabilitiesForPrompt(reg.Capabilities),
		},
		Messages: hostedGenesisLLMMessages(messages),
	}
	return in
}

func hostedGenesisLLMMessages(messages []hostedGenesisMessage) []llm.MintConversationMessage {
	out := make([]llm.MintConversationMessage, 0, len(messages))
	for _, m := range messages {
		out = append(out, llm.MintConversationMessage{Role: strings.ToLower(strings.TrimSpace(m.Role)), Content: strings.TrimSpace(m.Content)})
	}
	return out
}

func (s *Server) runAndPersistHostedGenesisDeclaration(ctx context.Context, st hostedGenesisStore, conv *models.SoulAgentMintConversation, session *models.HostedGenesisSession, msg hostedgenesis.QueueMessage, workerRequestID string, run hostedGenesisDeclarationRun) error {
	draft, usage, err := runHostedGenesisDeclarationModel(ctx, run.apiKey, run.modelSet, run.input)
	if err != nil {
		log.Printf("aiworker: hosted genesis declaration extraction failed agent_hash=%s conversation_hash=%s provider=%s failure_code=%s", hostedGenesisAuditHash(conv.AgentID), hostedGenesisAuditHash(conv.ConversationID), hostedGenesisProvider(run.modelSet), hostedGenesisFailureDeclarationExtractionFailed)
		if persistErr := s.markHostedGenesisConversationFailed(ctx, st, conv, session, hostedGenesisFailureDeclarationExtractionFailed, workerRequestID); persistErr != nil {
			return persistErr
		}
		return err
	}
	decl, err := buildHostedGenesisDeclarationsDraft(draft, time.Now().UTC(), run.modelSet, run.input.Registration.DeclaredCapabilities)
	if err != nil {
		detail := string(hostedgenesis.DeclarationValidationCodeFromError(err))
		log.Printf("aiworker: hosted genesis produced declarations rejected agent_hash=%s conversation_hash=%s failure_code=%s reason_code=%s", hostedGenesisAuditHash(conv.AgentID), hostedGenesisAuditHash(conv.ConversationID), hostedGenesisFailureInvalidProducedDeclarations, detail)
		return s.markHostedGenesisConversationFailedWithDetail(ctx, st, conv, session, hostedGenesisFailureInvalidProducedDeclarations, detail, workerRequestID)
	}
	b, err := json.Marshal(decl)
	if err != nil {
		return s.markHostedGenesisConversationFailedWithDetail(ctx, st, conv, session, hostedGenesisFailureInvalidProducedDeclarations, string(hostedgenesis.DeclarationCodeInvalid), workerRequestID)
	}
	return persistHostedGenesisDeclarationResponse(ctx, st, conv, session, msg, workerRequestID, string(b), usage, run.modelSet)
}

func persistHostedGenesisDeclarationResponse(ctx context.Context, st hostedGenesisStore, conv *models.SoulAgentMintConversation, session *models.HostedGenesisSession, msg hostedgenesis.QueueMessage, workerRequestID string, declarationsJSON string, usage models.AIUsage, modelSet string) error {
	now := time.Now().UTC()
	conv.ProducedDeclarations = models.EncodeSoulMintConversationBlob(declarationsJSON)
	conv.Usage = addAIUsageWorker(conv.Usage, usage)
	conv.Status = models.SoulMintConversationStatusDeclarationReady
	conv.StatusReason = ""
	conv.RequestID = firstNonEmptyWorker(msg.RequestID, workerRequestID, conv.RequestID)
	conv.CompletedAt = now
	conv.UpdatedAt = now
	encodeHostedGenesisPrivateFields(conv)
	_ = conv.UpdateKeys()
	if err := st.PutSoulAgentMintConversation(ctx, conv); err != nil {
		return err
	}
	checkpoint := hostedGenesisDeclarationCheckpointFromWorker(session, conv, declarationsJSON, now, modelSet, conv.RequestID)
	session.Status = string(hostedgenesis.StatusDeclarationReady)
	session.DeclarationCheckpoint = &checkpoint
	session.Failure = nil
	session.RequestID = conv.RequestID
	session.CompletedAt = now
	session.UpdatedAt = now
	return st.UpdateHostedGenesisSession(ctx, session, session.Version, hostedgenesis.StatusDeclarationExtractionPending)
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

var runHostedGenesisAssistantModel = defaultRunHostedGenesisAssistantModel

func defaultRunHostedGenesisAssistantModel(ctx context.Context, apiKey string, modelSet string, systemPrompt string, messages []llm.MintConversationMessage) (string, models.AIUsage, error) {
	switch {
	case strings.HasPrefix(strings.ToLower(modelSet), "openai:"):
		return llm.StreamMintConversationOpenAI(ctx, apiKey, modelSet, systemPrompt, messages, func(string) {})
	case strings.HasPrefix(strings.ToLower(modelSet), "anthropic:"):
		return llm.StreamMintConversationAnthropic(ctx, apiKey, modelSet, systemPrompt, messages, func(string) {})
	default:
		return "", models.AIUsage{}, fmt.Errorf("unsupported model set")
	}
}

var runHostedGenesisDeclarationModel = defaultRunHostedGenesisDeclarationModel

func defaultRunHostedGenesisDeclarationModel(ctx context.Context, apiKey string, modelSet string, in llm.MintConversationDeclarationsInput) (llm.MintConversationDeclarationsDraft, models.AIUsage, error) {
	switch {
	case strings.HasPrefix(strings.ToLower(modelSet), "openai:"):
		return llm.MintConversationDeclarationsOpenAI(ctx, apiKey, modelSet, in)
	case strings.HasPrefix(strings.ToLower(modelSet), "anthropic:"):
		return llm.MintConversationDeclarationsAnthropic(ctx, apiKey, modelSet, in)
	default:
		return llm.MintConversationDeclarationsDraft{}, models.AIUsage{}, fmt.Errorf("unsupported model set")
	}
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

func decodeHostedGenesisMessages(raw string) ([]hostedGenesisMessage, error) {
	raw = models.DecodeSoulMintConversationBlob(raw)
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var messages []hostedGenesisMessage
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func hostedGenesisHasAssistant(messages []hostedGenesisMessage) bool {
	for _, msg := range messages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") && strings.TrimSpace(msg.Content) != "" {
			return true
		}
	}
	return false
}

func hostedGenesisDeclarationCheckpointFromWorker(session *models.HostedGenesisSession, conv *models.SoulAgentMintConversation, declarationsJSON string, now time.Time, modelSet string, requestID string) hostedgenesis.DeclarationCheckpoint {
	sum := sha256.Sum256([]byte(strings.TrimSpace(declarationsJSON)))
	hashHex := hex.EncodeToString(sum[:])
	messageCount := 0
	registrationID := ""
	if session != nil {
		messageCount = session.MessageCount
		registrationID = session.RegistrationID
	}
	if messageCount <= 0 {
		messageCount = mintConversationMessageCountWorker(conv)
	}
	return hostedgenesis.DeclarationCheckpoint{
		DeclarationID:   "decl_" + hashHex[:16],
		DeclarationHash: "sha256:" + hashHex,
		CheckpointRef:   hostedgenesis.CheckpointRef("declaration", conv.ConversationID, hashHex[:16]),
		ProducedAt:      now.UTC(),
		RegistrationID:  strings.TrimSpace(registrationID),
		ConversationID:  strings.TrimSpace(conv.ConversationID),
		AgentID:         strings.ToLower(strings.TrimSpace(conv.AgentID)),
		MessageCount:    messageCount,
		Model:           strings.TrimSpace(modelSet),
		RequestID:       strings.TrimSpace(requestID),
	}
}

func hostedGenesisFailureFromWorkerReason(reason string) *hostedgenesis.Failure {
	return hostedGenesisFailureFromWorkerReasonWithDetail(reason, "")
}

func hostedGenesisFailureFromWorkerReasonWithDetail(reason string, detail string) *hostedgenesis.Failure {
	code := hostedgenesis.FailureCodeInvalidCompletionState
	switch strings.TrimSpace(reason) {
	case hostedGenesisFailureLLMUnavailable:
		code = hostedgenesis.FailureCodeLLMUnavailable
	case hostedGenesisFailureAssistantTurnFailed:
		code = hostedgenesis.FailureCodeAssistantTurnFailed
	case hostedGenesisFailureMicroVMUnavailable:
		code = hostedgenesis.FailureCodeMicroVMUnavailable
	case hostedGenesisFailureDeclarationExtractionFailed:
		code = hostedgenesis.FailureCodeDeclarationExtractionFailed
	case hostedGenesisFailureMissingProducedDeclarations:
		code = hostedgenesis.FailureCodeMissingProducedDeclarations
	case hostedGenesisFailureInvalidProducedDeclarations:
		code = hostedgenesis.FailureCodeInvalidProducedDeclarations
	case hostedGenesisFailureTenantBoundaryViolation:
		code = hostedgenesis.FailureCodeTenantBoundaryViolation
	}
	action := hostedgenesis.RecoveryActionRefreshState
	retryable := false
	if code == hostedgenesis.FailureCodeLLMUnavailable || code == hostedgenesis.FailureCodeAssistantTurnFailed || code == hostedgenesis.FailureCodeDeclarationExtractionFailed || code == hostedgenesis.FailureCodeMicroVMUnavailable {
		action = hostedgenesis.RecoveryActionRetrySameStep
		retryable = true
	}
	if code == hostedgenesis.FailureCodeTenantBoundaryViolation {
		action = hostedgenesis.RecoveryActionOperatorAction
	}
	if code == hostedgenesis.FailureCodeMissingProducedDeclarations || code == hostedgenesis.FailureCodeInvalidProducedDeclarations {
		action = hostedgenesis.RecoveryActionRestartSoulBootstrap
		retryable = false
	}
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

func mintConversationMessageCountWorker(conv *models.SoulAgentMintConversation) int {
	if conv == nil {
		return 0
	}
	messages, err := decodeHostedGenesisMessages(conv.Messages)
	if err != nil {
		return 0
	}
	return len(messages)
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func hostedGenesisDomainActive(domain *models.Domain) bool {
	if domain == nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(domain.Status))
	return status == models.DomainStatusVerified || status == models.DomainStatusActive
}

func hostedGenesisAPIKey(ctx context.Context, modelSet string) (string, error) {
	modelSetNorm := strings.ToLower(strings.TrimSpace(modelSet))
	switch {
	case strings.HasPrefix(modelSetNorm, "openai:"):
		if k := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); k != "" {
			return k, nil
		}
		return secrets.OpenAIServiceKey(ctx, nil)
	case strings.HasPrefix(modelSetNorm, "anthropic:"):
		if k := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); k != "" {
			return k, nil
		}
		if k := strings.TrimSpace(os.Getenv("CLAUDE_API_KEY")); k != "" {
			return k, nil
		}
		return secrets.ClaudeAPIKey(ctx, nil)
	default:
		return "", fmt.Errorf("unsupported model set")
	}
}

func hostedGenesisSystemPrompt(reg *models.SoulAgentRegistration) string {
	return mintprompt.MintConversationSystemPrompt(reg)
}

func buildHostedGenesisDeclarationsDraft(draft llm.MintConversationDeclarationsDraft, now time.Time, modelSet string, _ ...[]string) (hostedGenesisProducedDeclarations, error) {
	decl := hostedGenesisProducedDeclarations{
		SelfDescription: draft.SelfDescription,
		Capabilities:    []soul.CapabilityV2{},
		Boundaries:      []soul.BoundaryV2{},
		Transparency:    draft.Transparency,
	}
	decl.SelfDescription.AuthoredBy = "agent"
	decl.SelfDescription.MintingModel = strings.TrimSpace(modelSet)
	if err := decl.SelfDescription.Validate(); err != nil {
		return hostedGenesisProducedDeclarations{}, hostedgenesis.NewDeclarationValidationError(hostedgenesis.DeclarationCodeSelfDescription)
	}
	var capErr error
	decl.Capabilities, capErr = hostedgenesis.ValidateAndNormalizeProducedCapabilities(draft.Capabilities)
	if capErr != nil {
		return hostedGenesisProducedDeclarations{}, capErr
	}
	invalidBoundary := false
	for i, b := range draft.Boundaries {
		entry := soul.BoundaryV2{
			ID:             fmt.Sprintf("mint-%d-%02d", now.Unix(), i+1),
			Category:       strings.ToLower(strings.TrimSpace(b.Category)),
			Statement:      strings.TrimSpace(b.Statement),
			Rationale:      strings.TrimSpace(b.Rationale),
			AddedAt:        now.UTC().Format(time.RFC3339),
			AddedInVersion: "1",
			Signature:      "0x00",
		}
		if err := entry.Validate(); err != nil {
			invalidBoundary = true
			continue
		}
		decl.Boundaries = append(decl.Boundaries, entry)
	}
	if invalidBoundary {
		return hostedGenesisProducedDeclarations{}, hostedgenesis.NewDeclarationValidationError(hostedgenesis.DeclarationCodeBoundariesBad)
	}
	if len(decl.Boundaries) == 0 {
		if len(draft.Boundaries) > 0 {
			return hostedGenesisProducedDeclarations{}, hostedgenesis.NewDeclarationValidationError(hostedgenesis.DeclarationCodeBoundariesBad)
		}
		return hostedGenesisProducedDeclarations{}, hostedgenesis.NewDeclarationValidationError(hostedgenesis.DeclarationCodeBoundaries)
	}
	if decl.Transparency == nil {
		decl.Transparency = map[string]any{}
	}
	return decl, nil
}

func addAIUsageWorker(existing models.AIUsage, delta models.AIUsage) models.AIUsage {
	out := existing
	if strings.TrimSpace(out.Provider) == "" {
		out.Provider = strings.TrimSpace(delta.Provider)
	}
	if strings.TrimSpace(out.Model) == "" {
		out.Model = strings.TrimSpace(delta.Model)
	}
	out.InputTokens += delta.InputTokens
	out.OutputTokens += delta.OutputTokens
	total := delta.TotalTokens
	if total == 0 && (delta.InputTokens != 0 || delta.OutputTokens != 0) {
		total = delta.InputTokens + delta.OutputTokens
	}
	out.TotalTokens += total
	out.DurationMs += delta.DurationMs
	out.ToolCalls += delta.ToolCalls
	return out
}

func firstNonEmptyWorker(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func hostedGenesisProvider(modelSet string) string {
	if i := strings.Index(strings.TrimSpace(modelSet), ":"); i > 0 {
		return strings.ToLower(strings.TrimSpace(modelSet[:i]))
	}
	return "unknown"
}

func hostedGenesisAuditHash(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}
