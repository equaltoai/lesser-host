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
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const hostedGenesisRunTimeout = 2 * time.Minute

const (
	hostedGenesisFailureLLMUnavailable              = "llm_unavailable"
	hostedGenesisFailureAssistantTurnFailed         = "assistant_turn_failed"
	hostedGenesisFailureDeclarationExtractionFailed = "declaration_extraction_failed"
	hostedGenesisFailureInvalidCompletionState      = "invalid_completion_state"
	hostedGenesisFailureTenantBoundaryViolation     = "tenant_boundary_violation"
)

type hostedGenesisStore interface {
	GetSoulAgentRegistration(ctx context.Context, id string) (*models.SoulAgentRegistration, error)
	GetDomain(ctx context.Context, domain string) (*models.Domain, error)
	GetInstance(ctx context.Context, slug string) (*models.Instance, error)
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
	reg, conv, err := s.loadAndValidateHostedGenesisJob(ctx, st, msg)
	if err != nil {
		return err
	}
	if conv == nil || reg == nil {
		return nil
	}
	if strings.TrimSpace(conv.LatestTurnID) != strings.TrimSpace(msg.TurnID) || strings.TrimSpace(conv.Status) != models.SoulMintConversationStatusInProgress {
		return nil
	}

	messages, err := decodeHostedGenesisMessages(conv.Messages)
	if err != nil || len(messages) == 0 {
		s.markHostedGenesisConversationFailed(ctx, st, conv, hostedGenesisFailureInvalidCompletionState, workerRequestID)
		return nil
	}
	modelSet := strings.TrimSpace(conv.Model)
	apiKey, appErr := hostedGenesisAPIKey(ctx, modelSet)
	if appErr != nil {
		s.markHostedGenesisConversationFailed(ctx, st, conv, hostedGenesisFailureLLMUnavailable, workerRequestID)
		return nil
	}

	llmMessages := make([]llm.MintConversationMessage, 0, len(messages))
	for _, m := range messages {
		llmMessages = append(llmMessages, llm.MintConversationMessage{Role: strings.ToLower(strings.TrimSpace(m.Role)), Content: strings.TrimSpace(m.Content)})
	}
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), hostedGenesisRunTimeout)
	defer cancel()
	var fullResponse string
	var usage models.AIUsage
	switch {
	case strings.HasPrefix(strings.ToLower(modelSet), "openai:"):
		fullResponse, usage, err = llm.StreamMintConversationOpenAI(runCtx, apiKey, modelSet, hostedGenesisSystemPrompt(reg), llmMessages, func(string) {})
	case strings.HasPrefix(strings.ToLower(modelSet), "anthropic:"):
		fullResponse, usage, err = llm.StreamMintConversationAnthropic(runCtx, apiKey, modelSet, hostedGenesisSystemPrompt(reg), llmMessages, func(string) {})
	default:
		err = fmt.Errorf("unsupported model set")
	}
	if err != nil || strings.TrimSpace(fullResponse) == "" {
		log.Printf("aiworker: hosted genesis assistant turn failed agent_hash=%s conversation_hash=%s provider=%s err=%v", hostedGenesisAuditHash(conv.AgentID), hostedGenesisAuditHash(conv.ConversationID), hostedGenesisProvider(modelSet), err)
		s.markHostedGenesisConversationFailed(ctx, st, conv, hostedGenesisFailureAssistantTurnFailed, workerRequestID)
		return err
	}

	messages = append(messages, hostedGenesisMessage{Role: "assistant", Content: fullResponse})
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		s.markHostedGenesisConversationFailed(ctx, st, conv, hostedGenesisFailureAssistantTurnFailed, workerRequestID)
		return nil
	}
	now := time.Now().UTC()
	conv.Messages = models.EncodeSoulMintConversationBlob(string(messagesJSON))
	conv.Usage = addAIUsageWorker(conv.Usage, usage)
	conv.Status = models.SoulMintConversationStatusAssistantTurnReady
	conv.StatusReason = ""
	conv.RequestID = firstNonEmptyWorker(msg.RequestID, workerRequestID, conv.RequestID)
	conv.UpdatedAt = now
	encodeHostedGenesisPrivateFields(conv)
	_ = conv.UpdateKeys()
	return st.PutSoulAgentMintConversation(ctx, conv)
}

func (s *Server) processHostedGenesisDeclarationExtraction(ctx context.Context, workerRequestID string, msg hostedgenesis.QueueMessage) error {
	st, ok := s.hostedGenesisStore()
	if !ok {
		return fmt.Errorf("hosted genesis store not initialized")
	}
	reg, conv, err := s.loadAndValidateHostedGenesisJob(ctx, st, msg)
	if err != nil || reg == nil || conv == nil {
		return err
	}
	if strings.TrimSpace(conv.Status) != models.SoulMintConversationStatusDeclarationExtractionPending {
		return nil
	}
	messages, err := decodeHostedGenesisMessages(conv.Messages)
	if err != nil || len(messages) == 0 || !hostedGenesisHasAssistant(messages) {
		s.markHostedGenesisConversationFailed(ctx, st, conv, hostedGenesisFailureInvalidCompletionState, workerRequestID)
		return nil
	}
	modelSet := strings.TrimSpace(conv.Model)
	apiKey, appErr := hostedGenesisAPIKey(ctx, modelSet)
	if appErr != nil {
		s.markHostedGenesisConversationFailed(ctx, st, conv, hostedGenesisFailureLLMUnavailable, workerRequestID)
		return nil
	}
	in := llm.MintConversationDeclarationsInput{
		Registration: llm.MintConversationRegistrationContext{
			Domain:               strings.TrimSpace(reg.DomainNormalized),
			LocalID:              strings.TrimSpace(reg.LocalID),
			AgentID:              strings.TrimSpace(reg.AgentID),
			DeclaredCapabilities: append([]string(nil), reg.Capabilities...),
		},
		Messages: make([]llm.MintConversationMessage, 0, len(messages)),
	}
	for _, m := range messages {
		in.Messages = append(in.Messages, llm.MintConversationMessage{Role: strings.ToLower(strings.TrimSpace(m.Role)), Content: strings.TrimSpace(m.Content)})
	}
	var draft llm.MintConversationDeclarationsDraft
	var usage models.AIUsage
	switch {
	case strings.HasPrefix(strings.ToLower(modelSet), "openai:"):
		draft, usage, err = llm.MintConversationDeclarationsOpenAI(ctx, apiKey, modelSet, in)
	case strings.HasPrefix(strings.ToLower(modelSet), "anthropic:"):
		draft, usage, err = llm.MintConversationDeclarationsAnthropic(ctx, apiKey, modelSet, in)
	default:
		err = fmt.Errorf("unsupported model set")
	}
	if err != nil {
		log.Printf("aiworker: hosted genesis declaration extraction failed agent_hash=%s conversation_hash=%s provider=%s err=%v", hostedGenesisAuditHash(conv.AgentID), hostedGenesisAuditHash(conv.ConversationID), hostedGenesisProvider(modelSet), err)
		s.markHostedGenesisConversationFailed(ctx, st, conv, hostedGenesisFailureDeclarationExtractionFailed, workerRequestID)
		return err
	}
	decl, err := buildHostedGenesisDeclarationsDraft(draft, time.Now().UTC(), modelSet)
	if err != nil {
		s.markHostedGenesisConversationFailed(ctx, st, conv, hostedGenesisFailureDeclarationExtractionFailed, workerRequestID)
		return nil
	}
	b, err := json.Marshal(decl)
	if err != nil {
		s.markHostedGenesisConversationFailed(ctx, st, conv, hostedGenesisFailureDeclarationExtractionFailed, workerRequestID)
		return nil
	}
	now := time.Now().UTC()
	conv.ProducedDeclarations = models.EncodeSoulMintConversationBlob(string(b))
	conv.Usage = addAIUsageWorker(conv.Usage, usage)
	conv.Status = models.SoulMintConversationStatusDeclarationReady
	conv.StatusReason = ""
	conv.RequestID = firstNonEmptyWorker(msg.RequestID, workerRequestID, conv.RequestID)
	conv.CompletedAt = now
	conv.UpdatedAt = now
	encodeHostedGenesisPrivateFields(conv)
	_ = conv.UpdateKeys()
	return st.PutSoulAgentMintConversation(ctx, conv)
}

func (s *Server) loadAndValidateHostedGenesisJob(ctx context.Context, st hostedGenesisStore, msg hostedgenesis.QueueMessage) (*models.SoulAgentRegistration, *models.SoulAgentMintConversation, error) {
	reg, err := st.GetSoulAgentRegistration(ctx, msg.RegistrationID)
	if err != nil || reg == nil {
		return nil, nil, nil
	}
	if !strings.EqualFold(strings.TrimSpace(reg.AgentID), strings.TrimSpace(msg.AgentID)) {
		return nil, nil, nil
	}
	domain, err := st.GetDomain(ctx, reg.DomainNormalized)
	if err != nil || domain == nil || !hostedGenesisDomainActive(domain) || !strings.EqualFold(strings.TrimSpace(domain.InstanceSlug), strings.TrimSpace(msg.InstanceSlug)) {
		conv, _ := st.GetSoulAgentMintConversation(ctx, msg.AgentID, msg.ConversationID)
		s.markHostedGenesisConversationFailed(ctx, st, conv, hostedGenesisFailureTenantBoundaryViolation, msg.RequestID)
		return nil, nil, nil
	}
	inst, err := st.GetInstance(ctx, msg.InstanceSlug)
	if err != nil || inst == nil || !strings.EqualFold(strings.TrimSpace(inst.Slug), strings.TrimSpace(msg.InstanceSlug)) {
		conv, _ := st.GetSoulAgentMintConversation(ctx, msg.AgentID, msg.ConversationID)
		s.markHostedGenesisConversationFailed(ctx, st, conv, hostedGenesisFailureTenantBoundaryViolation, msg.RequestID)
		return nil, nil, nil
	}
	if strings.TrimSpace(msg.IdempotencyKey) != "" {
		idem, err := st.GetSoulMintConversationIdempotency(ctx, msg.InstanceSlug, msg.RegistrationID, msg.IdempotencyKey)
		if err != nil || idem == nil || !strings.EqualFold(strings.TrimSpace(idem.ConversationID), strings.TrimSpace(msg.ConversationID)) {
			conv, _ := st.GetSoulAgentMintConversation(ctx, msg.AgentID, msg.ConversationID)
			s.markHostedGenesisConversationFailed(ctx, st, conv, hostedGenesisFailureInvalidCompletionState, msg.RequestID)
			return nil, nil, nil
		}
	}
	conv, err := st.GetSoulAgentMintConversation(ctx, msg.AgentID, msg.ConversationID)
	if err != nil || conv == nil {
		return reg, nil, nil
	}
	conv.Messages = models.DecodeSoulMintConversationBlob(conv.Messages)
	conv.ProducedDeclarations = models.DecodeSoulMintConversationBlob(conv.ProducedDeclarations)
	return reg, conv, nil
}

func (s *Server) markHostedGenesisConversationFailed(ctx context.Context, st hostedGenesisStore, conv *models.SoulAgentMintConversation, reason string, requestID string) {
	if st == nil || conv == nil {
		return
	}
	now := time.Now().UTC()
	conv.Status = models.SoulMintConversationStatusFailed
	conv.StatusReason = strings.TrimSpace(reason)
	conv.RequestID = firstNonEmptyWorker(requestID, conv.RequestID)
	conv.UpdatedAt = now
	conv.CompletedAt = now
	encodeHostedGenesisPrivateFields(conv)
	_ = conv.UpdateKeys()
	_ = st.PutSoulAgentMintConversation(ctx, conv)
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
	var sb strings.Builder
	sb.WriteString("You are a Soul Registry minting assistant helping an AI agent define self-description, capabilities, boundaries, and transparency. Keep responses concise and safe; never ask for or reveal credentials.\n\n")
	if reg != nil {
		if strings.TrimSpace(reg.DomainNormalized) != "" {
			sb.WriteString("Domain: " + strings.TrimSpace(reg.DomainNormalized) + "\n")
		}
		if strings.TrimSpace(reg.LocalID) != "" {
			sb.WriteString("Local ID: " + strings.TrimSpace(reg.LocalID) + "\n")
		}
		if len(reg.Capabilities) > 0 {
			b, _ := json.Marshal(reg.Capabilities)
			sb.WriteString("Declared capabilities: " + string(b) + "\n")
		}
	}
	return sb.String()
}

func buildHostedGenesisDeclarationsDraft(draft llm.MintConversationDeclarationsDraft, now time.Time, modelSet string) (hostedGenesisProducedDeclarations, error) {
	decl := hostedGenesisProducedDeclarations{
		SelfDescription: draft.SelfDescription,
		Capabilities:    []soul.CapabilityV2{},
		Boundaries:      []soul.BoundaryV2{},
		Transparency:    draft.Transparency,
	}
	decl.SelfDescription.AuthoredBy = "agent"
	decl.SelfDescription.MintingModel = strings.TrimSpace(modelSet)
	if err := decl.SelfDescription.Validate(); err != nil {
		return hostedGenesisProducedDeclarations{}, err
	}
	for _, c := range draft.Capabilities {
		c.ClaimLevel = "self-declared"
		if strings.TrimSpace(c.Capability) == "" || strings.TrimSpace(c.Scope) == "" {
			continue
		}
		if err := c.Validate(); err != nil {
			continue
		}
		decl.Capabilities = append(decl.Capabilities, c)
	}
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
			continue
		}
		decl.Boundaries = append(decl.Boundaries, entry)
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
