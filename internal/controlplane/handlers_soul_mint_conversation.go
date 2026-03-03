package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

// --- Request / Response types ---

type soulMintConversationRequest struct {
	ConversationID string `json:"conversation_id,omitempty"` // Empty = start new conversation.
	Model          string `json:"model,omitempty"`           // e.g. "anthropic:claude-sonnet-4-20250514"
	Message        string `json:"message"`                   // User's message for this turn.
}

type soulMintConversationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// SSE event payloads.
type soulMintConversationStartEvent struct {
	ConversationID string `json:"conversation_id"`
	Model          string `json:"model"`
}

type soulMintConversationDeltaEvent struct {
	Text string `json:"text"`
}

type soulMintConversationDoneEvent struct {
	ConversationID string `json:"conversation_id"`
	FullResponse   string `json:"full_response"`
}

type soulMintConversationErrorEvent struct {
	Error string `json:"error"`
}

type soulMintConversationProducedDeclarations struct {
	SelfDescription soul.SelfDescriptionV2 `json:"selfDescription"`
	Capabilities    []soul.CapabilityV2    `json:"capabilities"`
	Boundaries      []soul.BoundaryV2      `json:"boundaries"`
	Transparency    map[string]any         `json:"transparency"`
}

// --- Handler ---

// handleSoulMintConversation conducts a streaming LLM-assisted minting conversation.
// Each call sends one user message and streams the assistant response via SSE.
func (s *Server) handleSoulMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	regID := strings.TrimSpace(ctx.Param("id"))
	if regID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "registration id is required"}
	}

	// Load the registration to provide context to the LLM.
	reg, err := s.getSoulAgentRegistration(ctx.Context(), regID)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "registration not found"}
	}
	if !isOperator(ctx) && strings.TrimSpace(reg.Username) != strings.TrimSpace(ctx.AuthIdentity) {
		return nil, &apptheory.AppError{Code: "app.forbidden", Message: "forbidden"}
	}

	var req soulMintConversationRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	message := strings.TrimSpace(req.Message)
	if message == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "message is required"}
	}
	if len(message) > 8192 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "message is too long"}
	}

	agentIDHex := strings.ToLower(strings.TrimSpace(reg.AgentID))

	// Load or create conversation record.
	conversationID := strings.TrimSpace(req.ConversationID)
	modelSet := strings.TrimSpace(req.Model)
	var existingMessages []soulMintConversationMessage
	if conversationID != "" {
		conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.not_found", Message: "conversation not found"}
		}
		if conv.Status != models.SoulMintConversationStatusInProgress {
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "conversation is not in progress"}
		}

		// Prevent model switching mid-conversation: use the stored model.
		storedModel := strings.TrimSpace(conv.Model)
		if storedModel != "" {
			if modelSet != "" && !strings.EqualFold(storedModel, modelSet) {
				return nil, &apptheory.AppError{Code: "app.conflict", Message: "cannot change model for an existing conversation"}
			}
			modelSet = storedModel
		}

		if strings.TrimSpace(conv.Messages) != "" {
			_ = json.Unmarshal([]byte(conv.Messages), &existingMessages)
		}
	} else {
		if modelSet == "" {
			modelSet = "anthropic:claude-sonnet-4-20250514"
		}
		token, err := newToken(16)
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create conversation id"}
		}
		conversationID = token
		conv := &models.SoulAgentMintConversation{
			AgentID:        agentIDHex,
			ConversationID: conversationID,
			Model:          modelSet,
			Status:         models.SoulMintConversationStatusInProgress,
		}
		_ = conv.UpdateKeys()
		if err := s.store.DB.WithContext(ctx.Context()).Model(conv).Create(); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create conversation"}
		}
	}

	if modelSet == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "model is required"}
	}
	apiKey, appErr := s.apiKeyForMintConversationModel(ctx.Context(), modelSet)
	if appErr != nil {
		return nil, appErr
	}

	// Build provider messages from conversation history + new user message.
	existingMessages = append(existingMessages, soulMintConversationMessage{Role: "user", Content: message})
	systemPrompt := buildMintConversationSystemPrompt(reg)

	// Create SSE event channel and start streaming.
	eventCh := make(chan apptheory.SSEEvent, 16)

	go s.streamMintConversation(ctx.Context(), eventCh, streamMintConversationParams{
		apiKey:           apiKey,
		modelSet:         modelSet,
		systemPrompt:     systemPrompt,
		existingMessages: existingMessages,
		agentIDHex:       agentIDHex,
		conversationID:   conversationID,
	})

	return apptheory.SSEStreamResponse(ctx.Context(), http.StatusOK, eventCh)
}

type streamMintConversationParams struct {
	apiKey           string
	modelSet         string
	systemPrompt     string
	existingMessages []soulMintConversationMessage
	agentIDHex       string
	conversationID   string
}

func (s *Server) streamMintConversation(ctx context.Context, eventCh chan<- apptheory.SSEEvent, p streamMintConversationParams) {
	defer close(eventCh)

	// Emit start event.
	eventCh <- apptheory.SSEEvent{
		Event: "conversation_start",
		Data: soulMintConversationStartEvent{
			ConversationID: p.conversationID,
			Model:          p.modelSet,
		},
	}

	// Stream from provider via internal/ai adapters.
	var fullResponse string
	var llmUsage models.AIUsage
	var err error

	llmMessages := make([]llm.MintConversationMessage, 0, len(p.existingMessages))
	for _, m := range p.existingMessages {
		llmMessages = append(llmMessages, llm.MintConversationMessage{
			Role:    strings.TrimSpace(m.Role),
			Content: strings.TrimSpace(m.Content),
		})
	}

	onDelta := func(delta string) {
		eventCh <- apptheory.SSEEvent{
			Event: "delta",
			Data: soulMintConversationDeltaEvent{
				Text: delta,
			},
		}
	}

	switch {
	case strings.HasPrefix(strings.ToLower(strings.TrimSpace(p.modelSet)), "openai:"):
		fullResponse, llmUsage, err = llm.StreamMintConversationOpenAI(ctx, p.apiKey, p.modelSet, p.systemPrompt, llmMessages, onDelta)
	case strings.HasPrefix(strings.ToLower(strings.TrimSpace(p.modelSet)), "anthropic:"):
		fullResponse, llmUsage, err = llm.StreamMintConversationAnthropic(ctx, p.apiKey, p.modelSet, p.systemPrompt, llmMessages, onDelta)
	default:
		err = fmt.Errorf("unsupported model set %q", p.modelSet)
	}

	_ = llmUsage

	if err != nil {
		eventCh <- apptheory.SSEEvent{
			Event: "error",
			Data: soulMintConversationErrorEvent{
				Error: "failed to generate response",
			},
		}
		// Update conversation status to failed.
		s.updateMintConversationStatus(ctx, p.agentIDHex, p.conversationID, models.SoulMintConversationStatusFailed, p.existingMessages, "")
		return
	}

	// Append assistant response to messages and persist.
	updatedMessages := append(p.existingMessages, soulMintConversationMessage{Role: "assistant", Content: fullResponse})
	s.updateMintConversationMessages(ctx, p.agentIDHex, p.conversationID, updatedMessages)

	// Emit done event.
	eventCh <- apptheory.SSEEvent{
		Event: "conversation_done",
		Data: soulMintConversationDoneEvent{
			ConversationID: p.conversationID,
			FullResponse:   fullResponse,
		},
	}
}

func (s *Server) updateMintConversationMessages(ctx context.Context, agentIDHex string, conversationID string, messages []soulMintConversationMessage) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return
	}
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		return
	}
	conv := &models.SoulAgentMintConversation{
		AgentID:        agentIDHex,
		ConversationID: conversationID,
		Messages:       string(messagesJSON),
	}
	_ = conv.UpdateKeys()
	_ = s.store.DB.WithContext(ctx).Model(conv).IfExists().Update("Messages")
}

func (s *Server) updateMintConversationStatus(ctx context.Context, agentIDHex string, conversationID string, status string, messages []soulMintConversationMessage, declarations string) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return
	}
	messagesJSON, _ := json.Marshal(messages)
	now := time.Now().UTC()
	conv := &models.SoulAgentMintConversation{
		AgentID:              agentIDHex,
		ConversationID:       conversationID,
		Messages:             string(messagesJSON),
		ProducedDeclarations: declarations,
		Status:               status,
		CompletedAt:          now,
	}
	_ = conv.UpdateKeys()
	_ = s.store.DB.WithContext(ctx).Model(conv).IfExists().Update("Messages", "ProducedDeclarations", "Status", "CompletedAt")
}

// handleSoulCompleteMintConversation marks a conversation as completed and extracts declarations.
func (s *Server) handleSoulCompleteMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	regID := strings.TrimSpace(ctx.Param("id"))
	if regID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "registration id is required"}
	}

	reg, err := s.getSoulAgentRegistration(ctx.Context(), regID)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "registration not found"}
	}
	if !isOperator(ctx) && strings.TrimSpace(reg.Username) != strings.TrimSpace(ctx.AuthIdentity) {
		return nil, &apptheory.AppError{Code: "app.forbidden", Message: "forbidden"}
	}

	conversationID := strings.TrimSpace(ctx.Param("conversationId"))
	if conversationID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "conversationId is required"}
	}

	agentIDHex := strings.ToLower(strings.TrimSpace(reg.AgentID))
	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "conversation not found"}
	}
	if conv.Status != models.SoulMintConversationStatusInProgress {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "conversation is not in progress"}
	}

	now := time.Now().UTC()

	// Optional: accept produced declarations (either as a JSON string or as a raw JSON object).
	var reqBody struct {
		Declarations json.RawMessage `json:"declarations,omitempty"`
	}
	_ = httpx.ParseJSON(ctx, &reqBody)

	declarationsJSON := ""
	raw := bytes.TrimSpace(reqBody.Declarations)
	if len(raw) > 0 {
		if raw[0] == '"' {
			// JSON string wrapper.
			var s string
			if err := json.Unmarshal(raw, &s); err == nil {
				declarationsJSON = strings.TrimSpace(s)
			}
		} else {
			declarationsJSON = strings.TrimSpace(string(raw))
		}
	}

	// If declarations were not provided, extract + validate them from the transcript using the configured model.
	if declarationsJSON == "" {
		decl, appErr := s.extractMintConversationDeclarations(ctx.Context(), reg, conv, now)
		if appErr != nil {
			return nil, appErr
		}
		b, err := json.Marshal(decl)
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to serialize declarations"}
		}
		declarationsJSON = strings.TrimSpace(string(b))
	} else {
		if _, appErr := parseAndValidateMintConversationDeclarations(declarationsJSON); appErr != nil {
			return nil, appErr
		}
	}

	conv.Status = models.SoulMintConversationStatusCompleted
	conv.ProducedDeclarations = declarationsJSON
	conv.CompletedAt = now
	_ = conv.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(conv).IfExists().Update("Status", "ProducedDeclarations", "CompletedAt"); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to complete conversation"}
	}

	return apptheory.JSON(http.StatusOK, conv)
}

// handleSoulGetMintConversation retrieves a mint conversation record.
func (s *Server) handleSoulGetMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	regID := strings.TrimSpace(ctx.Param("id"))
	if regID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "registration id is required"}
	}

	reg, err := s.getSoulAgentRegistration(ctx.Context(), regID)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "registration not found"}
	}
	if !isOperator(ctx) && strings.TrimSpace(reg.Username) != strings.TrimSpace(ctx.AuthIdentity) {
		return nil, &apptheory.AppError{Code: "app.forbidden", Message: "forbidden"}
	}

	conversationID := strings.TrimSpace(ctx.Param("conversationId"))
	if conversationID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "conversationId is required"}
	}

	agentIDHex := strings.ToLower(strings.TrimSpace(reg.AgentID))
	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "conversation not found"}
	}

	return apptheory.JSON(http.StatusOK, conv)
}

// --- Helpers ---

func buildMintConversationSystemPrompt(reg *models.SoulAgentRegistration) string {
	sanitizeInline := func(raw string, maxLen int) string {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return ""
		}
		// Remove control characters / newlines to reduce prompt injection surface.
		raw = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return ' '
			}
			return r
		}, raw)
		if maxLen > 0 && len(raw) > maxLen {
			raw = raw[:maxLen]
		}
		return raw
	}
	formatQuoted := func(raw string) string {
		raw = sanitizeInline(raw, 256)
		if raw == "" {
			return ""
		}
		// JSON-string quoting is a simple, robust way to avoid untrusted text being interpreted
		// as new instructions in the prompt.
		b, err := json.Marshal(raw)
		if err != nil {
			return strconv.Quote(raw)
		}
		return string(b)
	}

	var sb strings.Builder
	sb.WriteString(`You are a Soul Registry minting assistant. Your role is to help an AI agent define its identity through structured conversation before its soul is minted on-chain.

You are conducting a minting conversation with an agent that wants to register in the Soul Registry. Your goal is to help the agent articulate:

1. **Self-Description**: A clear, honest description of what the agent is, its purpose, and its primary function.
2. **Capabilities**: What the agent can do, with appropriate claim levels (self-declared, peer-validated, operator-attested).
3. **Boundaries**: What the agent will NOT do — ethical limits, operational constraints, and refusal conditions.
4. **Transparency**: How the agent makes decisions, what models it uses, and its known limitations.

Guidelines:
- Ask probing questions to help the agent articulate its identity clearly.
- Challenge vague or overly broad claims.
- Encourage honesty about limitations and potential failure modes.
- Help distinguish between capabilities the agent has vs. aspirations.
- Ensure boundaries are concrete and actionable, not just platitudes.
- The conversation should feel collaborative, not interrogative.

When you feel the conversation has covered all four areas sufficiently, summarize the proposed declarations in a structured format.

`)

	sb.WriteString("Agent registration context:\n")
	if reg.DomainNormalized != "" {
		sb.WriteString(fmt.Sprintf("- Domain: %s\n", formatQuoted(reg.DomainNormalized)))
	}
	if reg.LocalID != "" {
		sb.WriteString(fmt.Sprintf("- Local ID: %s\n", formatQuoted(reg.LocalID)))
	}
	if len(reg.Capabilities) > 0 {
		caps := make([]string, 0, len(reg.Capabilities))
		for _, c := range reg.Capabilities {
			c = sanitizeInline(c, 128)
			if c != "" {
				caps = append(caps, c)
			}
		}
		b, _ := json.Marshal(caps)
		sb.WriteString(fmt.Sprintf("- Declared capabilities: %s\n", string(b)))
	}

	return sb.String()
}

func (s *Server) apiKeyForMintConversationModel(ctx context.Context, modelSet string) (string, *apptheory.AppError) {
	modelSetNorm := strings.ToLower(strings.TrimSpace(modelSet))
	switch {
	case strings.HasPrefix(modelSetNorm, "openai:"):
		if k := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); k != "" {
			return k, nil
		}
		k, err := secrets.OpenAIServiceKey(ctx, nil)
		if err != nil {
			return "", &apptheory.AppError{Code: "app.internal", Message: "LLM provider not configured"}
		}
		return k, nil
	case strings.HasPrefix(modelSetNorm, "anthropic:"):
		if k := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); k != "" {
			return k, nil
		}
		if k := strings.TrimSpace(os.Getenv("CLAUDE_API_KEY")); k != "" {
			return k, nil
		}
		k, err := secrets.ClaudeAPIKey(ctx, nil)
		if err != nil {
			return "", &apptheory.AppError{Code: "app.internal", Message: "LLM provider not configured"}
		}
		return k, nil
	default:
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "unsupported model set"}
	}
}

func (s *Server) extractMintConversationDeclarations(ctx context.Context, reg *models.SoulAgentRegistration, conv *models.SoulAgentMintConversation, now time.Time) (soulMintConversationProducedDeclarations, *apptheory.AppError) {
	if s == nil || reg == nil || conv == nil {
		return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	modelSet := strings.TrimSpace(conv.Model)
	if modelSet == "" {
		return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.bad_request", Message: "conversation model is missing"}
	}

	var transcript []soulMintConversationMessage
	if strings.TrimSpace(conv.Messages) != "" {
		_ = json.Unmarshal([]byte(conv.Messages), &transcript)
	}
	if len(transcript) == 0 {
		return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.bad_request", Message: "conversation has no messages"}
	}

	apiKey, appErr := s.apiKeyForMintConversationModel(ctx, modelSet)
	if appErr != nil {
		return soulMintConversationProducedDeclarations{}, appErr
	}

	in := llm.MintConversationDeclarationsInput{
		Registration: llm.MintConversationRegistrationContext{
			Domain:               strings.TrimSpace(reg.DomainNormalized),
			LocalID:              strings.TrimSpace(reg.LocalID),
			AgentID:              strings.TrimSpace(reg.AgentID),
			DeclaredCapabilities: append([]string(nil), reg.Capabilities...),
		},
		Messages: make([]llm.MintConversationMessage, 0, len(transcript)),
	}
	for _, m := range transcript {
		in.Messages = append(in.Messages, llm.MintConversationMessage{
			Role:    strings.ToLower(strings.TrimSpace(m.Role)),
			Content: strings.TrimSpace(m.Content),
		})
	}

	var draft llm.MintConversationDeclarationsDraft
	switch {
	case strings.HasPrefix(strings.ToLower(modelSet), "openai:"):
		out, _, err := llm.MintConversationDeclarationsOpenAI(ctx, apiKey, modelSet, in)
		if err != nil {
			return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.internal", Message: "failed to extract declarations"}
		}
		draft = out
	case strings.HasPrefix(strings.ToLower(modelSet), "anthropic:"):
		out, _, err := llm.MintConversationDeclarationsAnthropic(ctx, apiKey, modelSet, in)
		if err != nil {
			return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.internal", Message: "failed to extract declarations"}
		}
		draft = out
	default:
		return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.bad_request", Message: "unsupported model set"}
	}

	decl, appErr := buildMintConversationProducedDeclarations(draft, now, modelSet)
	if appErr != nil {
		return soulMintConversationProducedDeclarations{}, appErr
	}
	return decl, nil
}

func buildMintConversationProducedDeclarations(draft llm.MintConversationDeclarationsDraft, now time.Time, modelSet string) (soulMintConversationProducedDeclarations, *apptheory.AppError) {
	decl := soulMintConversationProducedDeclarations{
		SelfDescription: draft.SelfDescription,
		Capabilities:    nil,
		Boundaries:      nil,
		Transparency:    draft.Transparency,
	}

	decl.SelfDescription.AuthoredBy = "agent"
	decl.SelfDescription.MintingModel = strings.TrimSpace(modelSet)
	if err := decl.SelfDescription.Validate(); err != nil {
		return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.internal", Message: "invalid extracted selfDescription"}
	}

	caps := make([]soul.CapabilityV2, 0, len(draft.Capabilities))
	for _, c := range draft.Capabilities {
		c.ClaimLevel = "self-declared"
		if strings.TrimSpace(c.Capability) == "" || strings.TrimSpace(c.Scope) == "" {
			continue
		}
		if err := c.Validate(); err != nil {
			continue
		}
		caps = append(caps, c)
	}
	if len(caps) == 0 {
		return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.internal", Message: "invalid extracted capabilities"}
	}
	decl.Capabilities = caps

	bounds := make([]soul.BoundaryV2, 0, len(draft.Boundaries))
	for i, b := range draft.Boundaries {
		id := fmt.Sprintf("mint-%d-%02d", now.Unix(), i+1)
		entry := soul.BoundaryV2{
			ID:             id,
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
		bounds = append(bounds, entry)
	}
	if len(bounds) == 0 {
		return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.internal", Message: "invalid extracted boundaries"}
	}
	decl.Boundaries = bounds

	if decl.Transparency == nil {
		decl.Transparency = map[string]any{}
	}

	return decl, nil
}

func parseAndValidateMintConversationDeclarations(raw string) (soulMintConversationProducedDeclarations, *apptheory.AppError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.bad_request", Message: "declarations is required"}
	}

	var decl soulMintConversationProducedDeclarations
	if err := json.Unmarshal([]byte(raw), &decl); err != nil {
		return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.bad_request", Message: "invalid declarations JSON"}
	}

	if err := decl.SelfDescription.Validate(); err != nil {
		return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.bad_request", Message: "invalid selfDescription: " + err.Error()}
	}
	if len(decl.Capabilities) == 0 {
		return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.bad_request", Message: "capabilities is required"}
	}
	for i := range decl.Capabilities {
		if err := decl.Capabilities[i].Validate(); err != nil {
			return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.bad_request", Message: fmt.Sprintf("invalid capabilities[%d]: %s", i, err.Error())}
		}
	}
	if len(decl.Boundaries) == 0 {
		return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.bad_request", Message: "boundaries is required"}
	}
	for i := range decl.Boundaries {
		if err := decl.Boundaries[i].Validate(); err != nil {
			return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.bad_request", Message: fmt.Sprintf("invalid boundaries[%d]: %s", i, err.Error())}
		}
	}
	if decl.Transparency == nil {
		return soulMintConversationProducedDeclarations{}, &apptheory.AppError{Code: "app.bad_request", Message: "transparency is required"}
	}

	return decl, nil
}
