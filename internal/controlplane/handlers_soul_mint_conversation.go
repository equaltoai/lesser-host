package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/billing"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	soulMintConversationStreamBaseCredits  = int64(10)
	soulMintConversationExtractBaseCredits = int64(10)

	soulMintConversationStreamModule  = "soul.mint_conversation.stream"
	soulMintConversationExtractModule = "soul.mint_conversation.extract"
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

type soulMintConversationFinalizeBeginRequest struct {
	BoundarySignatures map[string]string `json:"boundary_signatures"`
}

type soulMintConversationFinalizeBeginResponse struct {
	Version             string         `json:"version"`
	DigestHex           string         `json:"digest_hex"`
	IssuedAt            string         `json:"issued_at"`
	ExpectedVersion     int            `json:"expected_version"`
	NextVersion         int            `json:"next_version"`
	RegistrationPreview map[string]any `json:"registration_preview,omitempty"`
}

type soulMintConversationFinalizeRequest struct {
	BoundarySignatures map[string]string `json:"boundary_signatures"`
	IssuedAt           string            `json:"issued_at"`
	ExpectedVersion    *int              `json:"expected_version,omitempty"`
	SelfAttestation    string            `json:"self_attestation"`
}

type soulMintConversationFinalizeResponse struct {
	Version          string                   `json:"version"`
	Agent            models.SoulAgentIdentity `json:"agent"`
	PublishedVersion int                      `json:"published_version"`
}

func (s *Server) debitSoulMintConversationCredits(
	ctx context.Context,
	inst *models.Instance,
	module string,
	target string,
	requestID string,
	listCredits int64,
	now time.Time,
	extraWrites func(tx core.TransactionBuilder, creditsRequested int64) error,
) (creditsRequested int64, appErr *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if inst == nil {
		return 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	instanceSlug := strings.ToLower(strings.TrimSpace(inst.Slug))
	if instanceSlug == "" {
		return 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	module = strings.ToLower(strings.TrimSpace(module))
	if module == "" {
		return 0, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	target = strings.TrimSpace(target)
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		requestID = target
	}

	pricingMultiplierBps := effectiveAIPricingMultiplierBps(inst.AIPricingMultiplierBps)
	creditsRequested = billing.PricedCredits(listCredits, pricingMultiplierBps)
	if creditsRequested <= 0 {
		return 0, nil
	}

	month := now.UTC().Format("2006-01")

	pk := fmt.Sprintf("INSTANCE#%s", instanceSlug)
	sk := fmt.Sprintf("BUDGET#%s", month)
	var budget models.InstanceBudgetMonth
	if err := s.store.DB.WithContext(ctx).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&budget); err != nil {
		if theoryErrors.IsNotFound(err) {
			return 0, &apptheory.AppError{Code: "app.conflict", Message: "credits are not configured for this instance; purchase credits first"}
		}
		return 0, &apptheory.AppError{Code: "app.internal", Message: "failed to load credits budget"}
	}

	allowOverage := strings.EqualFold(strings.TrimSpace(inst.OveragePolicy), "allow")
	remaining := budget.IncludedCredits - budget.UsedCredits
	if remaining < creditsRequested && !allowOverage {
		return 0, &apptheory.AppError{Code: "app.conflict", Message: "insufficient credits"}
	}

	includedDebited, overageDebited := billing.PartsForDebit(budget.IncludedCredits, budget.UsedCredits, creditsRequested)
	billingType := billing.TypeFromParts(includedDebited, overageDebited)

	entry := &models.UsageLedgerEntry{
		ID:                     billing.UsageLedgerEntryID(instanceSlug, month, requestID, module, target, creditsRequested),
		InstanceSlug:           instanceSlug,
		Month:                  month,
		Module:                 module,
		Target:                 target,
		Cached:                 false,
		Reason:                 billingType,
		RequestID:              requestID,
		RequestedCredits:       creditsRequested,
		ListCredits:            listCredits,
		PricingMultiplierBps:   pricingMultiplierBps,
		DebitedCredits:         creditsRequested,
		IncludedDebitedCredits: includedDebited,
		OverageDebitedCredits:  overageDebited,
		BillingType:            billingType,
		CreatedAt:              now.UTC(),
	}
	_ = entry.UpdateKeys()

	updateBudget := &models.InstanceBudgetMonth{
		InstanceSlug: instanceSlug,
		Month:        month,
		UpdatedAt:    now.UTC(),
	}
	_ = updateBudget.UpdateKeys()

	maxUsed := budget.IncludedCredits - creditsRequested

	err := s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.Put(entry)
		if extraWrites != nil {
			if err := extraWrites(tx, creditsRequested); err != nil {
				return err
			}
		}
		if allowOverage {
			tx.UpdateWithBuilder(updateBudget, func(ub core.UpdateBuilder) error {
				ub.Add("UsedCredits", creditsRequested)
				ub.Set("UpdatedAt", now.UTC())
				return nil
			}, tabletheory.IfExists())
			return nil
		}
		tx.UpdateWithBuilder(updateBudget, func(ub core.UpdateBuilder) error {
			ub.Add("UsedCredits", creditsRequested)
			ub.Set("UpdatedAt", now.UTC())
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.ConditionExpression(
				"attribute_not_exists(usedCredits) OR usedCredits <= :max",
				map[string]any{
					":max": maxUsed,
				},
			),
		)
		return nil
	})
	if theoryErrors.IsConditionFailed(err) {
		return 0, &apptheory.AppError{Code: "app.conflict", Message: "insufficient credits"}
	}
	if err != nil {
		return 0, &apptheory.AppError{Code: "app.internal", Message: "failed to debit credits"}
	}

	return creditsRequested, nil
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

	// Determine instance config for billing/metering.
	_, inst, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(reg.DomainNormalized))
	if accessErr != nil {
		return nil, accessErr
	}

	now := time.Now().UTC()

	// Load or create conversation record.
	conversationID := strings.TrimSpace(req.ConversationID)
	modelSet := strings.TrimSpace(req.Model)
	var existingMessages []soulMintConversationMessage
	var existingUsage models.AIUsage
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
		existingUsage = conv.Usage
	} else {
		if modelSet == "" {
			modelSet = "anthropic:claude-sonnet-4-20250514"
		}
		token, err := newToken(16)
		if err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to create conversation id"}
		}
		conversationID = token
	}

	if modelSet == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "model is required"}
	}
	apiKey, appErr := s.apiKeyForMintConversationModel(ctx.Context(), modelSet)
	if appErr != nil {
		return nil, appErr
	}

	// Debit credits for this LLM call (fail closed if insufficient credits).
	if strings.TrimSpace(req.ConversationID) == "" {
		conv := &models.SoulAgentMintConversation{
			AgentID:        agentIDHex,
			ConversationID: conversationID,
			Model:          modelSet,
			Status:         models.SoulMintConversationStatusInProgress,
			CreatedAt:      now,
		}
		_ = conv.UpdateKeys()

		if _, appErr := s.debitSoulMintConversationCredits(
			ctx.Context(),
			inst,
			soulMintConversationStreamModule,
			conversationID,
			strings.TrimSpace(ctx.RequestID),
			soulMintConversationStreamBaseCredits,
			now,
			func(tx core.TransactionBuilder, creditsRequested int64) error {
				conv.ChargedCredits = creditsRequested
				tx.Create(conv)
				return nil
			},
		); appErr != nil {
			return nil, appErr
		}
	} else {
		if _, appErr := s.debitSoulMintConversationCredits(
			ctx.Context(),
			inst,
			soulMintConversationStreamModule,
			conversationID,
			strings.TrimSpace(ctx.RequestID),
			soulMintConversationStreamBaseCredits,
			now,
			func(tx core.TransactionBuilder, creditsRequested int64) error {
				update := &models.SoulAgentMintConversation{
					AgentID:        agentIDHex,
					ConversationID: conversationID,
				}
				_ = update.UpdateKeys()
				tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
					ub.Add("ChargedCredits", creditsRequested)
					return nil
				}, tabletheory.IfExists())
				return nil
			},
		); appErr != nil {
			return nil, appErr
		}
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
		existingUsage:    existingUsage,
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
	existingUsage    models.AIUsage
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
	s.updateMintConversationTurn(ctx, p.agentIDHex, p.conversationID, updatedMessages, addAIUsage(p.existingUsage, llmUsage))

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

func addAIUsage(existing models.AIUsage, delta models.AIUsage) models.AIUsage {
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

func (s *Server) updateMintConversationTurn(ctx context.Context, agentIDHex string, conversationID string, messages []soulMintConversationMessage, usage models.AIUsage) {
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
		Usage:          usage,
	}
	_ = conv.UpdateKeys()
	_ = s.store.DB.WithContext(ctx).Model(conv).IfExists().Update("Messages", "Usage")
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

	// Determine instance config for billing/metering.
	_, inst, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(reg.DomainNormalized))
	if accessErr != nil {
		return nil, accessErr
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
	var extractUsage models.AIUsage
	if declarationsJSON == "" {
		// Debit credits for the extraction call (fail closed if insufficient credits).
		creditsDebited, appErr := s.debitSoulMintConversationCredits(
			ctx.Context(),
			inst,
			soulMintConversationExtractModule,
			conversationID,
			strings.TrimSpace(ctx.RequestID),
			soulMintConversationExtractBaseCredits,
			now,
			func(tx core.TransactionBuilder, creditsRequested int64) error {
				update := &models.SoulAgentMintConversation{
					AgentID:        agentIDHex,
					ConversationID: conversationID,
				}
				_ = update.UpdateKeys()
				tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
					ub.Add("ChargedCredits", creditsRequested)
					return nil
				}, tabletheory.IfExists())
				return nil
			},
		)
		if appErr != nil {
			return nil, appErr
		}
		conv.ChargedCredits += creditsDebited

		decl, usage, appErr := s.extractMintConversationDeclarations(ctx.Context(), reg, conv, now)
		if appErr != nil {
			return nil, appErr
		}
		extractUsage = usage
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

	// Persist total usage (streaming turns + extraction) on the conversation record.
	if extractUsage != (models.AIUsage{}) {
		conv.Usage = addAIUsage(conv.Usage, extractUsage)
	}

	conv.Status = models.SoulMintConversationStatusCompleted
	conv.ProducedDeclarations = declarationsJSON
	conv.CompletedAt = now
	_ = conv.UpdateKeys()
	if err := s.store.DB.WithContext(ctx.Context()).Model(conv).IfExists().Update("Status", "ProducedDeclarations", "CompletedAt", "Usage"); err != nil {
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

// handleSoulBeginFinalizeMintConversation prepares a mint conversation output to be published as a v2 registration
// by verifying boundary signatures and returning the v2 self-attestation digest for the full document.
func (s *Server) handleSoulBeginFinalizeMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if s == nil || s.soulPacks == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}
	if strings.TrimSpace(s.cfg.SoulPackBucketName) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
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

	// Enforce domain ownership (instance access).
	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(reg.DomainNormalized)); accessErr != nil {
		return nil, accessErr
	}

	conversationID := strings.TrimSpace(ctx.Param("conversationId"))
	if conversationID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "conversationId is required"}
	}

	var req soulMintConversationFinalizeBeginRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}
	if len(req.BoundarySignatures) == 0 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "boundary_signatures is required"}
	}

	agentIDHex := strings.ToLower(strings.TrimSpace(reg.AgentID))
	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "conversation not found"}
	}
	if conv.Status != models.SoulMintConversationStatusCompleted {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "conversation is not completed"}
	}
	if strings.TrimSpace(conv.ProducedDeclarations) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "conversation has no produced declarations"}
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "registration is not yet verified"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	// Only allow the initial publish via mint conversation.
	if identity.SelfDescriptionVersion > 0 {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "registration is already published"}
	}

	// Require principal declaration metadata to build a valid v2 registration.
	if strings.TrimSpace(identity.PrincipalAddress) == "" ||
		strings.TrimSpace(identity.PrincipalSignature) == "" ||
		strings.TrimSpace(identity.PrincipalDeclaration) == "" ||
		strings.TrimSpace(identity.PrincipalDeclaredAt) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "principal declaration is missing; re-verify registration"}
	}

	decl, appErr := parseAndValidateMintConversationDeclarations(conv.ProducedDeclarations)
	if appErr != nil {
		return nil, appErr
	}

	// Verify all boundary signatures (EIP-191 over keccak256(bytes(statement))).
	for i := range decl.Boundaries {
		b := decl.Boundaries[i]
		sig := strings.TrimSpace(req.BoundarySignatures[strings.TrimSpace(b.ID)])
		if sig == "" {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "missing boundary signature for " + strings.TrimSpace(b.ID)}
		}
		statementDigest := crypto.Keccak256([]byte(strings.TrimSpace(b.Statement)))
		if err := verifyEthereumSignatureBytes(identity.Wallet, statementDigest, sig); err != nil {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid boundary signature for " + strings.TrimSpace(b.ID)}
		}
	}

	now := time.Now().UTC()
	expectedVersion := identity.SelfDescriptionVersion
	nextVersion := expectedVersion + 1

	regMap, _, digest, _, _, appErr := s.buildMintConversationFinalizeV2Registration(agentIDHex, identity, decl, req.BoundarySignatures, now, nextVersion, "0x00")
	if appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, soulMintConversationFinalizeBeginResponse{
		Version:             "1",
		DigestHex:           "0x" + hex.EncodeToString(digest),
		IssuedAt:            now.Format(time.RFC3339Nano),
		ExpectedVersion:     expectedVersion,
		NextVersion:         nextVersion,
		RegistrationPreview: regMap,
	})
}

// handleSoulFinalizeMintConversation publishes the v2 registration version derived from a completed mint conversation.
func (s *Server) handleSoulFinalizeMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}
	if s == nil || s.soulPacks == nil {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}
	if strings.TrimSpace(s.cfg.SoulPackBucketName) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
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

	// Enforce domain ownership (instance access).
	if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(reg.DomainNormalized)); accessErr != nil {
		return nil, accessErr
	}

	conversationID := strings.TrimSpace(ctx.Param("conversationId"))
	if conversationID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "conversationId is required"}
	}

	var req soulMintConversationFinalizeRequest
	if err := httpx.ParseJSON(ctx, &req); err != nil {
		return nil, err
	}
	if len(req.BoundarySignatures) == 0 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "boundary_signatures is required"}
	}

	issuedAtRaw := strings.TrimSpace(req.IssuedAt)
	if issuedAtRaw == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "issued_at is required"}
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, issuedAtRaw)
	if err != nil {
		issuedAt, err = time.Parse(time.RFC3339, issuedAtRaw)
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "issued_at must be an RFC3339 timestamp"}
	}

	expectedVersion := req.ExpectedVersion
	if expectedVersion == nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "expected_version is required"}
	}
	if *expectedVersion < 0 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "expected_version is invalid"}
	}

	selfSig := strings.TrimSpace(req.SelfAttestation)
	if selfSig == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "self_attestation is required"}
	}

	agentIDHex := strings.ToLower(strings.TrimSpace(reg.AgentID))
	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "conversation not found"}
	}
	if conv.Status != models.SoulMintConversationStatusCompleted {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "conversation is not completed"}
	}
	if strings.TrimSpace(conv.ProducedDeclarations) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "conversation has no produced declarations"}
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "registration is not yet verified"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	nextVersion := *expectedVersion + 1
	if identity.SelfDescriptionVersion > nextVersion {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent has advanced beyond this version"}
	}
	if identity.SelfDescriptionVersion < *expectedVersion {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}
	// Allow idempotent retries:
	// - first-time publish: identity.SelfDescriptionVersion == expectedVersion
	// - retry after publish: identity.SelfDescriptionVersion == nextVersion

	// Require principal declaration metadata to build a valid v2 registration.
	if strings.TrimSpace(identity.PrincipalAddress) == "" ||
		strings.TrimSpace(identity.PrincipalSignature) == "" ||
		strings.TrimSpace(identity.PrincipalDeclaration) == "" ||
		strings.TrimSpace(identity.PrincipalDeclaredAt) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "principal declaration is missing; re-verify registration"}
	}

	decl, appErr := parseAndValidateMintConversationDeclarations(conv.ProducedDeclarations)
	if appErr != nil {
		return nil, appErr
	}

	// Verify all boundary signatures (EIP-191 over keccak256(bytes(statement))).
	for i := range decl.Boundaries {
		b := decl.Boundaries[i]
		sig := strings.TrimSpace(req.BoundarySignatures[strings.TrimSpace(b.ID)])
		if sig == "" {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "missing boundary signature for " + strings.TrimSpace(b.ID)}
		}
		statementDigest := crypto.Keccak256([]byte(strings.TrimSpace(b.Statement)))
		if err := verifyEthereumSignatureBytes(identity.Wallet, statementDigest, sig); err != nil {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid boundary signature for " + strings.TrimSpace(b.ID)}
		}
	}

	regMap, regV2, digest, capsNorm, claimLevels, appErr := s.buildMintConversationFinalizeV2Registration(agentIDHex, identity, decl, req.BoundarySignatures, issuedAt.UTC(), nextVersion, selfSig)
	if appErr != nil {
		return nil, appErr
	}
	if err := verifyEthereumSignatureBytes(identity.Wallet, digest, selfSig); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration signature"}
	}

	regBytes, err := json.Marshal(regMap)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	regSHA256 := func() string {
		sum := sha256.Sum256(regBytes)
		return hex.EncodeToString(sum[:])
	}()
	changeSummary := extractStringField(regMap, "changeSummary")

	// Persist boundary records for boundary reads.
	bounds := make([]*models.SoulAgentBoundary, 0, len(decl.Boundaries))
	for i := range decl.Boundaries {
		b := decl.Boundaries[i]
		sig := strings.TrimSpace(req.BoundarySignatures[strings.TrimSpace(b.ID)])

		supersedes := ""
		if b.Supersedes != nil {
			supersedes = strings.TrimSpace(*b.Supersedes)
		}

		m := &models.SoulAgentBoundary{
			AgentID:        agentIDHex,
			BoundaryID:     strings.TrimSpace(b.ID),
			Category:       strings.ToLower(strings.TrimSpace(b.Category)),
			Statement:      strings.TrimSpace(b.Statement),
			Rationale:      strings.TrimSpace(b.Rationale),
			AddedInVersion: nextVersion,
			Supersedes:     supersedes,
			Signature:      sig,
			AddedAt:        issuedAt.UTC(),
		}
		_ = m.UpdateKeys()
		bounds = append(bounds, m)
	}

	now := time.Now().UTC()
	publishedVersion, pubErr := s.publishSoulAgentRegistrationV2(ctx.Context(), agentIDHex, identity, regV2, regBytes, regSHA256, selfSig, changeSummary, capsNorm, claimLevels, expectedVersion, now)
	if pubErr != nil {
		return nil, pubErr
	}

	for _, b := range bounds {
		if b == nil {
			continue
		}
		if err := s.store.DB.WithContext(ctx.Context()).Model(b).IfNotExists().Create(); err != nil {
			if theoryErrors.IsConditionFailed(err) {
				s.tryWriteSoulBoundaryKeywordIndexForBoundary(ctx.Context(), identity, b)
				continue
			}
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to persist boundaries"}
		}
		s.tryWriteSoulBoundaryKeywordIndexForBoundary(ctx.Context(), identity, b)
	}

	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.mint_conversation.finalize",
		Target:    fmt.Sprintf("soul_agent_identity:%s", agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})

	return apptheory.JSON(http.StatusOK, soulMintConversationFinalizeResponse{
		Version:          "1",
		Agent:            *identity,
		PublishedVersion: publishedVersion,
	})
}

// --- Helpers ---

func (s *Server) buildMintConversationFinalizeV2Registration(
	agentIDHex string,
	identity *models.SoulAgentIdentity,
	decl soulMintConversationProducedDeclarations,
	boundarySignatures map[string]string,
	issuedAt time.Time,
	nextVersion int,
	selfAttestation string,
) (reg map[string]any, regV2 *soul.RegistrationFileV2, digest []byte, capsNorm []string, claimLevels map[string]string, appErr *apptheory.AppError) {
	if s == nil || identity == nil {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	if agentIDHex == "" {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if nextVersion <= 0 {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid version"}
	}

	issuedAt = issuedAt.UTC()
	issuedAtStr := issuedAt.Format(time.RFC3339Nano)

	// Map pending -> active for v2 lifecycle (spec has no "pending").
	lifecycleStatus := strings.ToLower(strings.TrimSpace(identity.LifecycleStatus))
	if lifecycleStatus == "" {
		lifecycleStatus = strings.ToLower(strings.TrimSpace(identity.Status))
	}
	if lifecycleStatus == models.SoulAgentStatusPending {
		lifecycleStatus = models.SoulAgentStatusActive
	}
	if lifecycleStatus == "" {
		lifecycleStatus = models.SoulAgentStatusActive
	}

	principal := map[string]any{
		"type":        "individual",
		"identifier":  strings.TrimSpace(identity.PrincipalAddress),
		"declaration": strings.TrimSpace(identity.PrincipalDeclaration),
		"signature":   strings.TrimSpace(identity.PrincipalSignature),
		"declaredAt":  strings.TrimSpace(identity.PrincipalDeclaredAt),
	}

	selfDesc := map[string]any{
		"purpose":    strings.TrimSpace(decl.SelfDescription.Purpose),
		"authoredBy": strings.ToLower(strings.TrimSpace(decl.SelfDescription.AuthoredBy)),
	}
	if strings.TrimSpace(decl.SelfDescription.Constraints) != "" {
		selfDesc["constraints"] = strings.TrimSpace(decl.SelfDescription.Constraints)
	}
	if strings.TrimSpace(decl.SelfDescription.Commitments) != "" {
		selfDesc["commitments"] = strings.TrimSpace(decl.SelfDescription.Commitments)
	}
	if strings.TrimSpace(decl.SelfDescription.Limitations) != "" {
		selfDesc["limitations"] = strings.TrimSpace(decl.SelfDescription.Limitations)
	}
	if strings.TrimSpace(decl.SelfDescription.MintingModel) != "" {
		selfDesc["mintingModel"] = strings.TrimSpace(decl.SelfDescription.MintingModel)
	}

	capsAny := make([]any, 0, len(decl.Capabilities))
	for i := range decl.Capabilities {
		c := decl.Capabilities[i]
		item := map[string]any{
			"capability": strings.TrimSpace(c.Capability),
			"scope":      strings.TrimSpace(c.Scope),
			"claimLevel": strings.ToLower(strings.TrimSpace(c.ClaimLevel)),
		}
		if c.Constraints != nil && len(c.Constraints) > 0 {
			item["constraints"] = c.Constraints
		}
		if strings.TrimSpace(c.LastValidated) != "" {
			item["lastValidated"] = strings.TrimSpace(c.LastValidated)
		}
		if strings.TrimSpace(c.ValidationRef) != "" {
			item["validationRef"] = strings.TrimSpace(c.ValidationRef)
		}
		if strings.TrimSpace(c.DegradesTo) != "" {
			item["degradesTo"] = strings.TrimSpace(c.DegradesTo)
		}
		capsAny = append(capsAny, item)
	}

	boundsAny := make([]any, 0, len(decl.Boundaries))
	for i := range decl.Boundaries {
		b := decl.Boundaries[i]
		sig := strings.TrimSpace(boundarySignatures[strings.TrimSpace(b.ID)])
		item := map[string]any{
			"id":             strings.TrimSpace(b.ID),
			"category":       strings.ToLower(strings.TrimSpace(b.Category)),
			"statement":      strings.TrimSpace(b.Statement),
			"addedAt":        issuedAtStr,
			"addedInVersion": strconv.Itoa(nextVersion),
			"signature":      sig,
		}
		if strings.TrimSpace(b.Rationale) != "" {
			item["rationale"] = strings.TrimSpace(b.Rationale)
		}
		if b.Supersedes != nil && strings.TrimSpace(*b.Supersedes) != "" {
			item["supersedes"] = strings.TrimSpace(*b.Supersedes)
		}
		boundsAny = append(boundsAny, item)
	}

	changeSummary := fmt.Sprintf("Publish mint conversation declarations (v%d)", nextVersion)

	reg = map[string]any{
		"version":         "2",
		"agentId":         agentIDHex,
		"domain":          strings.TrimSpace(identity.Domain),
		"localId":         strings.TrimSpace(identity.LocalID),
		"wallet":          strings.TrimSpace(identity.Wallet),
		"principal":       principal,
		"selfDescription": selfDesc,
		"capabilities":    capsAny,
		"boundaries":      boundsAny,
		"transparency":    decl.Transparency,
		"endpoints":       map[string]any{},
		"lifecycle": map[string]any{
			"status":          lifecycleStatus,
			"statusChangedAt": issuedAtStr,
		},
		"attestations": map[string]any{
			"selfAttestation": strings.TrimSpace(selfAttestation),
		},
		"created":       issuedAtStr,
		"updated":       issuedAtStr,
		"changeSummary": changeSummary,
	}
	if nextVersion > 1 {
		prevKey := soulRegistrationVersionedS3Key(agentIDHex, nextVersion-1)
		reg["previousVersionUri"] = fmt.Sprintf("s3://%s/%s", strings.TrimSpace(s.cfg.SoulPackBucketName), prevKey)
	}

	if reg["transparency"] == nil {
		reg["transparency"] = map[string]any{}
	}

	regBytes, err := json.Marshal(reg)
	if err != nil {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	parsed, err := soul.ParseRegistrationFileV2(regBytes)
	if err != nil {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid v2 registration schema"}
	}
	if err := parsed.Validate(); err != nil {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}

	digest, appErr = computeSoulRegistrationSelfAttestationDigest(reg)
	if appErr != nil {
		return nil, nil, nil, nil, nil, appErr
	}

	// Capability indexing inputs.
	caps := extractCapabilityNames(reg)
	capsNorm, appErr = normalizeSoulCapabilitiesStrict(s.cfg.SoulSupportedCapabilities, caps)
	if appErr != nil {
		return nil, nil, nil, nil, nil, appErr
	}
	claimLevels = extractCapabilityClaimLevels(reg)

	return reg, parsed, digest, capsNorm, claimLevels, nil
}

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

func (s *Server) extractMintConversationDeclarations(ctx context.Context, reg *models.SoulAgentRegistration, conv *models.SoulAgentMintConversation, now time.Time) (soulMintConversationProducedDeclarations, models.AIUsage, *apptheory.AppError) {
	if s == nil || reg == nil || conv == nil {
		return soulMintConversationProducedDeclarations{}, models.AIUsage{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	modelSet := strings.TrimSpace(conv.Model)
	if modelSet == "" {
		return soulMintConversationProducedDeclarations{}, models.AIUsage{}, &apptheory.AppError{Code: "app.bad_request", Message: "conversation model is missing"}
	}

	var transcript []soulMintConversationMessage
	if strings.TrimSpace(conv.Messages) != "" {
		_ = json.Unmarshal([]byte(conv.Messages), &transcript)
	}
	if len(transcript) == 0 {
		return soulMintConversationProducedDeclarations{}, models.AIUsage{}, &apptheory.AppError{Code: "app.bad_request", Message: "conversation has no messages"}
	}

	apiKey, appErr := s.apiKeyForMintConversationModel(ctx, modelSet)
	if appErr != nil {
		return soulMintConversationProducedDeclarations{}, models.AIUsage{}, appErr
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
	var usage models.AIUsage
	switch {
	case strings.HasPrefix(strings.ToLower(modelSet), "openai:"):
		out, u, err := llm.MintConversationDeclarationsOpenAI(ctx, apiKey, modelSet, in)
		if err != nil {
			return soulMintConversationProducedDeclarations{}, models.AIUsage{}, &apptheory.AppError{Code: "app.internal", Message: "failed to extract declarations"}
		}
		draft = out
		usage = u
	case strings.HasPrefix(strings.ToLower(modelSet), "anthropic:"):
		out, u, err := llm.MintConversationDeclarationsAnthropic(ctx, apiKey, modelSet, in)
		if err != nil {
			return soulMintConversationProducedDeclarations{}, models.AIUsage{}, &apptheory.AppError{Code: "app.internal", Message: "failed to extract declarations"}
		}
		draft = out
		usage = u
	default:
		return soulMintConversationProducedDeclarations{}, models.AIUsage{}, &apptheory.AppError{Code: "app.bad_request", Message: "unsupported model set"}
	}

	decl, appErr := buildMintConversationProducedDeclarations(draft, now, modelSet)
	if appErr != nil {
		return soulMintConversationProducedDeclarations{}, models.AIUsage{}, appErr
	}
	return decl, usage, nil
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
