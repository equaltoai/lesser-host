package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/httpx"
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

	var req soulMintConversationRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}

	message := strings.TrimSpace(req.Message)
	if message == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "message is required"}
	}

	modelSet := strings.TrimSpace(req.Model)
	if modelSet == "" {
		modelSet = "anthropic:claude-sonnet-4-20250514"
	}

	// Resolve Anthropic model.
	if !strings.HasPrefix(strings.ToLower(modelSet), "anthropic:") {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "only anthropic models are supported for minting conversations"}
	}
	modelName := strings.TrimSpace(strings.TrimPrefix(modelSet, "anthropic:"))

	apiKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if apiKey == "" {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "LLM provider not configured"}
	}

	agentIDHex := strings.ToLower(strings.TrimSpace(reg.AgentID))

	// Load or create conversation record.
	conversationID := strings.TrimSpace(req.ConversationID)
	var existingMessages []soulMintConversationMessage
	if conversationID != "" {
		conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.not_found", Message: "conversation not found"}
		}
		if conv.Status != models.SoulMintConversationStatusInProgress {
			return nil, &apptheory.AppError{Code: "app.conflict", Message: "conversation is not in progress"}
		}
		if strings.TrimSpace(conv.Messages) != "" {
			_ = json.Unmarshal([]byte(conv.Messages), &existingMessages)
		}
	} else {
		conversationID = fmt.Sprintf("%d", time.Now().UnixNano())
		conv := &models.SoulAgentMintConversation{
			AgentID:        agentIDHex,
			ConversationID: conversationID,
			Model:          modelSet,
			Status:         models.SoulMintConversationStatusInProgress,
		}
		_ = conv.UpdateKeys()
		_ = s.store.DB.WithContext(ctx.Context()).Model(conv).Create()
	}

	// Build Anthropic messages from conversation history + new user message.
	existingMessages = append(existingMessages, soulMintConversationMessage{Role: "user", Content: message})
	anthropicMessages := buildAnthropicMessages(existingMessages)

	systemPrompt := buildMintConversationSystemPrompt(reg)

	// Create SSE event channel and start streaming.
	eventCh := make(chan apptheory.SSEEvent, 16)

	go s.streamMintConversation(ctx.Context(), eventCh, streamMintConversationParams{
		apiKey:           apiKey,
		modelName:        modelName,
		modelSet:         modelSet,
		systemPrompt:     systemPrompt,
		messages:         anthropicMessages,
		existingMessages: existingMessages,
		agentIDHex:       agentIDHex,
		conversationID:   conversationID,
	})

	return apptheory.SSEStreamResponse(ctx.Context(), http.StatusOK, eventCh)
}

type streamMintConversationParams struct {
	apiKey           string
	modelName        string
	modelSet         string
	systemPrompt     string
	messages         []anthropic.MessageParam
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

	// Call Anthropic streaming API.
	client := anthropic.NewClient(option.WithAPIKey(p.apiKey))
	stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(p.modelName),
		MaxTokens: 4096,
		System:    []anthropic.TextBlockParam{{Text: p.systemPrompt}},
		Messages:  p.messages,
	})
	defer stream.Close()

	var fullResponse strings.Builder

	for stream.Next() {
		event := stream.Current()

		switch delta := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			textDelta := delta.Delta.AsTextDelta()
			if textDelta.Text != "" {
				fullResponse.WriteString(textDelta.Text)
				eventCh <- apptheory.SSEEvent{
					Event: "delta",
					Data: soulMintConversationDeltaEvent{
						Text: textDelta.Text,
					},
				}
			}
		}
	}

	if stream.Err() != nil {
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

	responseText := fullResponse.String()

	// Append assistant response to messages and persist.
	updatedMessages := append(p.existingMessages, soulMintConversationMessage{Role: "assistant", Content: responseText})
	s.updateMintConversationMessages(ctx, p.agentIDHex, p.conversationID, updatedMessages)

	// Emit done event.
	eventCh <- apptheory.SSEEvent{
		Event: "conversation_done",
		Data: soulMintConversationDoneEvent{
			ConversationID: p.conversationID,
			FullResponse:   responseText,
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

	var reqBody struct {
		Declarations string `json:"declarations,omitempty"` // Optional: JSON object of produced declarations.
	}
	_ = httpx.ParseJSON(ctx, &reqBody)

	now := time.Now().UTC()
	conv.Status = models.SoulMintConversationStatusCompleted
	conv.ProducedDeclarations = strings.TrimSpace(reqBody.Declarations)
	conv.CompletedAt = now
	_ = conv.UpdateKeys()
	_ = s.store.DB.WithContext(ctx.Context()).Model(conv).IfExists().Update("Status", "ProducedDeclarations", "CompletedAt")

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

func buildAnthropicMessages(messages []soulMintConversationMessage) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(messages))
	for _, m := range messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		switch role {
		case "user":
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(content)))
		case "assistant":
			out = append(out, anthropic.NewAssistantMessage(anthropic.NewTextBlock(content)))
		}
	}
	return out
}

func buildMintConversationSystemPrompt(reg *models.SoulAgentRegistration) string {
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
		sb.WriteString(fmt.Sprintf("- Domain: %s\n", reg.DomainNormalized))
	}
	if reg.LocalID != "" {
		sb.WriteString(fmt.Sprintf("- Local ID: %s\n", reg.LocalID))
	}
	if len(reg.Capabilities) > 0 {
		sb.WriteString(fmt.Sprintf("- Declared capabilities: %s\n", strings.Join(reg.Capabilities, ", ")))
	}

	return sb.String()
}
