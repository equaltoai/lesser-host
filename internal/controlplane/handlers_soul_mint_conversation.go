package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
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
	defaultSoulMintConversationModel  = "anthropic:claude-sonnet-4-6"
	mintConversationBlobPrefix        = "b64:"

	soulMintConversationAlreadyPublishedMessage = "registration is already published"
)

// --- Request / Response types ---

type soulMintConversationRequest struct {
	ConversationID string `json:"conversation_id,omitempty"` // Empty = start new conversation.
	Model          string `json:"model,omitempty"`           // e.g. "anthropic:claude-sonnet-4-6"
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

type soulMintConversationFinalizeBoundaryRequirement struct {
	BoundaryID      string `json:"boundary_id"`
	Category        string `json:"category"`
	Statement       string `json:"statement"`
	Rationale       string `json:"rationale,omitempty"`
	Supersedes      string `json:"supersedes,omitempty"`
	SignatureHex    string `json:"signature_hex,omitempty"`
	SignerWallet    string `json:"signer_wallet"`
	SigningMethod   string `json:"signing_method"`
	MessageEncoding string `json:"message_encoding"`
	Message         string `json:"message"`
	DigestHex       string `json:"digest_hex"`
}

type soulMintConversationFinalizeSigningInput struct {
	SignerWallet    string `json:"signer_wallet"`
	SigningMethod   string `json:"signing_method"`
	MessageEncoding string `json:"message_encoding"`
	MessageHex      string `json:"message_hex"`
	DigestHex       string `json:"digest_hex"`
	CanonicalJSON   string `json:"canonical_json"`
}

type soulMintConversationFinalizeRequestTemplate struct {
	BoundarySignatures map[string]string `json:"boundary_signatures"`
	IssuedAt           string            `json:"issued_at"`
	ExpectedVersion    int               `json:"expected_version"`
	SelfAttestation    string            `json:"self_attestation"`
}

type soulMintConversationFinalizeBeginResponse struct {
	Version                 string                                            `json:"version"`
	DigestHex               string                                            `json:"digest_hex"`
	IssuedAt                string                                            `json:"issued_at"`
	ExpectedVersion         int                                               `json:"expected_version"`
	NextVersion             int                                               `json:"next_version"`
	DeclarationsPreview     soulMintConversationProducedDeclarations          `json:"declarations_preview"`
	BoundaryRequirements    []soulMintConversationFinalizeBoundaryRequirement `json:"boundary_requirements,omitempty"`
	SelfAttestationSigning  soulMintConversationFinalizeSigningInput          `json:"self_attestation_signing"`
	FinalizeRequestTemplate soulMintConversationFinalizeRequestTemplate       `json:"finalize_request_template"`
	RegistrationPreview     map[string]any                                    `json:"registration_preview,omitempty"`
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

type soulAgentMintConversationsResponse struct {
	Version       string                              `json:"version"`
	Conversations []*models.SoulAgentMintConversation `json:"conversations"`
	Count         int                                 `json:"count"`
}

type mintConversationSession struct {
	conversationID   string
	modelSet         string
	existingMessages []soulMintConversationMessage
	existingUsage    models.AIUsage
	isNew            bool
}

type mintConversationRegistrationContext struct {
	reg        *models.SoulAgentRegistration
	inst       *models.Instance
	agentIDHex string
}

type mintConversationAgentContext struct {
	reg        *models.SoulAgentRegistration
	inst       *models.Instance
	identity   *models.SoulAgentIdentity
	agentIDHex string
}

type mintConversationFinalizeContext struct {
	reg            *models.SoulAgentRegistration
	inst           *models.Instance
	identity       *models.SoulAgentIdentity
	conv           *models.SoulAgentMintConversation
	agentIDHex     string
	conversationID string
}

type mintConversationDebitParams struct {
	instanceSlug string
	module       string
	target       string
	requestID    string
}

func (s *Server) requireMintConversationRegistrationContext(ctx *apptheory.Context, requirePacks bool) (mintConversationRegistrationContext, *apptheory.AppError) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return mintConversationRegistrationContext{}, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return mintConversationRegistrationContext{}, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return mintConversationRegistrationContext{}, appErr
	}
	if requirePacks && (s == nil || s.soulPacks == nil || strings.TrimSpace(s.cfg.SoulPackBucketName) == "") {
		return mintConversationRegistrationContext{}, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}

	regID := strings.TrimSpace(ctx.Param("id"))
	if regID == "" {
		return mintConversationRegistrationContext{}, &apptheory.AppError{Code: "app.bad_request", Message: "registration id is required"}
	}
	reg, err := s.getSoulAgentRegistration(ctx.Context(), regID)
	if err != nil {
		return mintConversationRegistrationContext{}, &apptheory.AppError{Code: "app.not_found", Message: "registration not found"}
	}
	if !isOperator(ctx) && strings.TrimSpace(reg.Username) != strings.TrimSpace(ctx.AuthIdentity) {
		return mintConversationRegistrationContext{}, &apptheory.AppError{Code: "app.forbidden", Message: "forbidden"}
	}

	_, inst, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(reg.DomainNormalized))
	if accessErr != nil {
		return mintConversationRegistrationContext{}, accessErr
	}
	return mintConversationRegistrationContext{
		reg:        reg,
		inst:       inst,
		agentIDHex: strings.ToLower(strings.TrimSpace(reg.AgentID)),
	}, nil
}

func mintConversationRegistrationFromIdentity(identity *models.SoulAgentIdentity) *models.SoulAgentRegistration {
	if identity == nil {
		return nil
	}
	return &models.SoulAgentRegistration{
		DomainNormalized: strings.TrimSpace(identity.Domain),
		LocalID:          strings.TrimSpace(identity.LocalID),
		AgentID:          strings.TrimSpace(identity.AgentID),
		Wallet:           strings.TrimSpace(identity.Wallet),
		Capabilities:     append([]string(nil), identity.Capabilities...),
	}
}

func (s *Server) requireMintConversationAgentContext(ctx *apptheory.Context, requirePacks bool) (mintConversationAgentContext, *apptheory.AppError) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return mintConversationAgentContext{}, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return mintConversationAgentContext{}, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return mintConversationAgentContext{}, appErr
	}
	if requirePacks && (s == nil || s.soulPacks == nil || strings.TrimSpace(s.cfg.SoulPackBucketName) == "") {
		return mintConversationAgentContext{}, &apptheory.AppError{Code: "app.conflict", Message: "soul registry bucket is not configured"}
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return mintConversationAgentContext{}, appErr
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return mintConversationAgentContext{}, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if err != nil {
		return mintConversationAgentContext{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	_, inst, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain))
	if accessErr != nil {
		return mintConversationAgentContext{}, accessErr
	}

	return mintConversationAgentContext{
		reg:        mintConversationRegistrationFromIdentity(identity),
		inst:       inst,
		identity:   identity,
		agentIDHex: agentIDHex,
	}, nil
}

func requireMintConversationMessage(ctx *apptheory.Context) (soulMintConversationRequest, string, error) {
	var req soulMintConversationRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return req, "", parseErr
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return req, "", &apptheory.AppError{Code: "app.bad_request", Message: "message is required"}
	}
	if len(message) > 8192 {
		return req, "", &apptheory.AppError{Code: "app.bad_request", Message: "message is too long"}
	}
	return req, message, nil
}

func requireMintConversationID(ctx *apptheory.Context) (string, *apptheory.AppError) {
	conversationID := strings.TrimSpace(ctx.Param("conversationId"))
	if conversationID == "" {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "conversationId is required"}
	}
	return conversationID, nil
}

func (s *Server) listSoulAgentMintConversations(ctx context.Context, agentIDHex string, limit int) ([]*models.SoulAgentMintConversation, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	var items []*models.SoulAgentMintConversation
	if err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentMintConversation{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "MINT_CONVERSATION#").
		All(&items); err != nil && !theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list mint conversations"}
	}

	sort.Slice(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		if left == nil || right == nil {
			return right == nil
		}
		if left.CreatedAt.Equal(right.CreatedAt) {
			return strings.TrimSpace(left.ConversationID) > strings.TrimSpace(right.ConversationID)
		}
		return left.CreatedAt.After(right.CreatedAt)
	})

	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	for _, item := range items {
		decodeMintConversationFields(item)
	}
	return items, nil
}

func (s *Server) loadMintConversationByStatus(ctx context.Context, agentIDHex string, conversationID string, expectedStatus string, statusMessage string, emptyDeclMessage string) (*models.SoulAgentMintConversation, *apptheory.AppError) {
	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx, agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "conversation not found"}
	}
	decodeMintConversationFields(conv)
	if conv.Status != expectedStatus {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: statusMessage}
	}
	if emptyDeclMessage != "" && strings.TrimSpace(conv.ProducedDeclarations) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: emptyDeclMessage}
	}
	return conv, nil
}

func (s *Server) loadMintConversationFinalizeContext(ctx *apptheory.Context) (mintConversationFinalizeContext, *apptheory.AppError) {
	regCtx, appErr := s.requireMintConversationRegistrationContext(ctx, true)
	if appErr != nil {
		return mintConversationFinalizeContext{}, appErr
	}
	conversationID, appErr := requireMintConversationID(ctx)
	if appErr != nil {
		return mintConversationFinalizeContext{}, appErr
	}
	conv, appErr := s.loadMintConversationByStatus(ctx.Context(), regCtx.agentIDHex, conversationID, models.SoulMintConversationStatusCompleted, "conversation is not completed", "conversation has no produced declarations")
	if appErr != nil {
		return mintConversationFinalizeContext{}, appErr
	}
	identity, err := s.getSoulAgentIdentity(ctx.Context(), regCtx.agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return mintConversationFinalizeContext{}, &apptheory.AppError{Code: "app.conflict", Message: "registration is not yet verified"}
	}
	if err != nil {
		return mintConversationFinalizeContext{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.TrimSpace(identity.PrincipalAddress) == "" ||
		strings.TrimSpace(identity.PrincipalSignature) == "" ||
		strings.TrimSpace(identity.PrincipalDeclaration) == "" ||
		strings.TrimSpace(identity.PrincipalDeclaredAt) == "" {
		return mintConversationFinalizeContext{}, &apptheory.AppError{Code: "app.conflict", Message: "principal declaration is missing; re-verify registration"}
	}
	return mintConversationFinalizeContext{
		reg:            regCtx.reg,
		inst:           regCtx.inst,
		identity:       identity,
		conv:           conv,
		agentIDHex:     regCtx.agentIDHex,
		conversationID: conversationID,
	}, nil
}

func (s *Server) ensureMintConversationAgentNotPublished(ctx context.Context, agentIDHex string) *apptheory.AppError {
	identity, err := s.getSoulAgentIdentity(ctx, agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if identity != nil && identity.SelfDescriptionVersion > 0 {
		return &apptheory.AppError{Code: "app.conflict", Message: soulMintConversationAlreadyPublishedMessage}
	}
	return nil
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
	params, appErr := validateMintConversationDebitParams(s, inst, module, target, requestID)
	if appErr != nil {
		return 0, appErr
	}

	pricingMultiplierBps := effectiveAIPricingMultiplierBps(inst.AIPricingMultiplierBps)
	creditsRequested = billing.PricedCredits(listCredits, pricingMultiplierBps)
	if creditsRequested <= 0 {
		return 0, nil
	}

	month := now.UTC().Format("2006-01")
	budget, appErr := s.loadMintConversationBudget(ctx, params.instanceSlug, month)
	if appErr != nil {
		return 0, appErr
	}

	allowOverage := strings.EqualFold(strings.TrimSpace(inst.OveragePolicy), "allow")
	if mintConversationCreditsInsufficient(budget, creditsRequested, allowOverage) {
		return 0, &apptheory.AppError{Code: "app.conflict", Message: "insufficient credits"}
	}

	includedDebited, overageDebited := billing.PartsForDebit(budget.IncludedCredits, budget.UsedCredits, creditsRequested)
	billingType := billing.TypeFromParts(includedDebited, overageDebited)

	entry := &models.UsageLedgerEntry{
		ID:                     billing.UsageLedgerEntryID(params.instanceSlug, month, params.requestID, params.module, params.target, creditsRequested),
		InstanceSlug:           params.instanceSlug,
		Month:                  month,
		Module:                 params.module,
		Target:                 params.target,
		Cached:                 false,
		Reason:                 billingType,
		RequestID:              params.requestID,
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
	err := s.applyMintConversationCreditDebit(ctx, budget, entry, creditsRequested, allowOverage, now, extraWrites)
	if theoryErrors.IsConditionFailed(err) {
		return 0, &apptheory.AppError{Code: "app.conflict", Message: "insufficient credits"}
	}
	if err != nil {
		return 0, &apptheory.AppError{Code: "app.internal", Message: "failed to debit credits"}
	}

	return creditsRequested, nil
}

func validateMintConversationDebitParams(s *Server, inst *models.Instance, module string, target string, requestID string) (mintConversationDebitParams, *apptheory.AppError) {
	if s == nil || s.store == nil || s.store.DB == nil || inst == nil {
		return mintConversationDebitParams{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	instanceSlug := strings.ToLower(strings.TrimSpace(inst.Slug))
	module = strings.ToLower(strings.TrimSpace(module))
	if instanceSlug == "" || module == "" {
		return mintConversationDebitParams{}, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	target = strings.TrimSpace(target)
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		requestID = target
	}
	return mintConversationDebitParams{
		instanceSlug: instanceSlug,
		module:       module,
		target:       target,
		requestID:    requestID,
	}, nil
}

func (s *Server) loadMintConversationBudget(ctx context.Context, instanceSlug string, month string) (models.InstanceBudgetMonth, *apptheory.AppError) {
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
			return models.InstanceBudgetMonth{}, &apptheory.AppError{Code: "app.conflict", Message: "credits are not configured for this instance; purchase credits first"}
		}
		return models.InstanceBudgetMonth{}, &apptheory.AppError{Code: "app.internal", Message: "failed to load credits budget"}
	}
	return budget, nil
}

func mintConversationCreditsInsufficient(budget models.InstanceBudgetMonth, creditsRequested int64, allowOverage bool) bool {
	remaining := budget.IncludedCredits - budget.UsedCredits
	return remaining < creditsRequested && !allowOverage
}

func (s *Server) applyMintConversationCreditDebit(
	ctx context.Context,
	budget models.InstanceBudgetMonth,
	entry *models.UsageLedgerEntry,
	creditsRequested int64,
	allowOverage bool,
	now time.Time,
	extraWrites func(tx core.TransactionBuilder, creditsRequested int64) error,
) error {
	updateBudget := &models.InstanceBudgetMonth{
		InstanceSlug: budget.InstanceSlug,
		Month:        budget.Month,
		UpdatedAt:    now.UTC(),
	}
	_ = updateBudget.UpdateKeys()
	maxUsed := budget.IncludedCredits - creditsRequested

	return s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.Put(entry)
		if extraWrites != nil {
			if err := extraWrites(tx, creditsRequested); err != nil {
				return err
			}
		}
		return applyMintConversationBudgetUpdate(tx, updateBudget, creditsRequested, allowOverage, now, maxUsed)
	})
}

func applyMintConversationBudgetUpdate(tx core.TransactionBuilder, updateBudget *models.InstanceBudgetMonth, creditsRequested int64, allowOverage bool, now time.Time, maxUsed int64) error {
	builder := func(ub core.UpdateBuilder) error {
		ub.Add("UsedCredits", creditsRequested)
		ub.Set("UpdatedAt", now.UTC())
		return nil
	}
	if allowOverage {
		tx.UpdateWithBuilder(updateBudget, builder, tabletheory.IfExists())
		return nil
	}
	tx.UpdateWithBuilder(updateBudget, builder,
		tabletheory.IfExists(),
		tabletheory.ConditionExpression(
			"attribute_not_exists(usedCredits) OR usedCredits <= :max",
			map[string]any{":max": maxUsed},
		),
	)
	return nil
}

// --- Handler ---

func (s *Server) handleSoulAgentListMintConversations(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentCtx, appErr := s.requireMintConversationAgentContext(ctx, false)
	if appErr != nil {
		return nil, appErr
	}

	items, appErr := s.listSoulAgentMintConversations(ctx.Context(), agentCtx.agentIDHex, parseLimit(queryFirst(ctx, "limit"), 20, 1, 100))
	if appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, soulAgentMintConversationsResponse{
		Version:       "1",
		Conversations: items,
		Count:         len(items),
	})
}

// handleSoulMintConversation conducts a streaming LLM-assisted minting conversation.
// Each call sends one user message and streams the assistant response via SSE.
func (s *Server) handleSoulMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	regCtx, appErr := s.requireMintConversationRegistrationContext(ctx, false)
	if appErr != nil {
		return nil, appErr
	}
	if publishGuardErr := s.ensureMintConversationAgentNotPublished(ctx.Context(), regCtx.agentIDHex); publishGuardErr != nil {
		return nil, publishGuardErr
	}
	req, message, err := requireMintConversationMessage(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	// Load or create conversation record.
	session, appErr := s.loadMintConversationSession(ctx.Context(), regCtx.agentIDHex, req.ConversationID, req.Model)
	if appErr != nil {
		return nil, appErr
	}

	if session.modelSet == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "model is required"}
	}
	apiKey, appErr := s.apiKeyForMintConversationModel(ctx.Context(), session.modelSet)
	if appErr != nil {
		return nil, appErr
	}

	// Debit credits for this LLM call (fail closed if insufficient credits).
	if appErr := s.debitMintConversationStreamCredits(ctx.Context(), regCtx.inst, regCtx.agentIDHex, session, strings.TrimSpace(ctx.RequestID), now); appErr != nil {
		return nil, appErr
	}
	promotion := s.loadOrFallbackSoulAgentPromotion(ctx.Context(), regCtx.agentIDHex, buildSoulAgentPromotionFromRegistration(regCtx.reg, now))
	previousPromotion := cloneSoulAgentPromotion(promotion)
	promotion = updateSoulAgentPromotionForConversation(promotion, session.conversationID, models.SoulMintConversationStatusInProgress, now)
	if appErr := s.saveSoulAgentPromotion(ctx.Context(), promotion); appErr != nil {
		return nil, appErr
	}
	if shouldEmitSoulPromotionReviewStartedEvent(previousPromotion, promotion, session.conversationID) {
		if appErr := s.saveSoulAgentPromotionLifecycleEvent(ctx.Context(), buildSoulAgentPromotionLifecycleEvent(promotion, soulAgentPromotionLifecycleEventInput{
			EventType:      models.SoulAgentPromotionEventTypeReviewStarted,
			RequestID:      strings.TrimSpace(ctx.RequestID),
			ConversationID: session.conversationID,
			OccurredAt:     now,
		})); appErr != nil {
			return nil, appErr
		}
	}

	// Build provider messages from conversation history + new user message.
	existingMessages := append(session.existingMessages, soulMintConversationMessage{Role: "user", Content: message})
	systemPrompt := buildMintConversationSystemPrompt(regCtx.reg)

	// Create SSE event channel and start streaming.
	eventCh := make(chan apptheory.SSEEvent, 16)

	go s.streamMintConversation(ctx.Context(), eventCh, streamMintConversationParams{
		apiKey:           apiKey,
		modelSet:         session.modelSet,
		systemPrompt:     systemPrompt,
		existingMessages: existingMessages,
		existingUsage:    session.existingUsage,
		agentIDHex:       regCtx.agentIDHex,
		conversationID:   session.conversationID,
	})

	return apptheory.SSEStreamResponse(ctx.Context(), http.StatusOK, eventCh)
}

func (s *Server) handleSoulAgentMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentCtx, appErr := s.requireMintConversationAgentContext(ctx, false)
	if appErr != nil {
		return nil, appErr
	}
	if agentCtx.identity != nil && agentCtx.identity.SelfDescriptionVersion > 0 {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: soulMintConversationAlreadyPublishedMessage}
	}

	req, message, err := requireMintConversationMessage(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	session, appErr := s.loadMintConversationSession(ctx.Context(), agentCtx.agentIDHex, req.ConversationID, req.Model)
	if appErr != nil {
		return nil, appErr
	}
	if session.modelSet == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "model is required"}
	}
	apiKey, appErr := s.apiKeyForMintConversationModel(ctx.Context(), session.modelSet)
	if appErr != nil {
		return nil, appErr
	}
	if appErr := s.debitMintConversationStreamCredits(ctx.Context(), agentCtx.inst, agentCtx.agentIDHex, session, strings.TrimSpace(ctx.RequestID), now); appErr != nil {
		return nil, appErr
	}
	promotion := s.loadOrFallbackSoulAgentPromotion(ctx.Context(), agentCtx.agentIDHex, buildSoulAgentPromotionFromRegistration(agentCtx.reg, now))
	previousPromotion := cloneSoulAgentPromotion(promotion)
	promotion = updateSoulAgentPromotionForConversation(promotion, session.conversationID, models.SoulMintConversationStatusInProgress, now)
	if appErr := s.saveSoulAgentPromotion(ctx.Context(), promotion); appErr != nil {
		return nil, appErr
	}
	if shouldEmitSoulPromotionReviewStartedEvent(previousPromotion, promotion, session.conversationID) {
		if appErr := s.saveSoulAgentPromotionLifecycleEvent(ctx.Context(), buildSoulAgentPromotionLifecycleEvent(promotion, soulAgentPromotionLifecycleEventInput{
			EventType:      models.SoulAgentPromotionEventTypeReviewStarted,
			RequestID:      strings.TrimSpace(ctx.RequestID),
			ConversationID: session.conversationID,
			OccurredAt:     now,
		})); appErr != nil {
			return nil, appErr
		}
	}

	existingMessages := append(session.existingMessages, soulMintConversationMessage{Role: "user", Content: message})
	systemPrompt := buildMintConversationSystemPrompt(agentCtx.reg)
	eventCh := make(chan apptheory.SSEEvent, 16)

	go s.streamMintConversation(ctx.Context(), eventCh, streamMintConversationParams{
		apiKey:           apiKey,
		modelSet:         session.modelSet,
		systemPrompt:     systemPrompt,
		existingMessages: existingMessages,
		existingUsage:    session.existingUsage,
		agentIDHex:       agentCtx.agentIDHex,
		conversationID:   session.conversationID,
	})

	return apptheory.SSEStreamResponse(ctx.Context(), http.StatusOK, eventCh)
}

func (s *Server) loadMintConversationSession(ctx context.Context, agentIDHex string, requestedConversationID string, requestedModel string) (mintConversationSession, *apptheory.AppError) {
	session := mintConversationSession{
		conversationID: strings.TrimSpace(requestedConversationID),
		modelSet:       strings.TrimSpace(requestedModel),
	}
	if session.conversationID == "" {
		if session.modelSet == "" {
			session.modelSet = defaultSoulMintConversationModel
		}
		token, err := newToken(16)
		if err != nil {
			return mintConversationSession{}, &apptheory.AppError{Code: "app.internal", Message: "failed to create conversation id"}
		}
		session.conversationID = token
		session.isNew = true
		return session, nil
	}

	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx, agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", session.conversationID))
	if err != nil {
		return mintConversationSession{}, &apptheory.AppError{Code: "app.not_found", Message: "conversation not found"}
	}
	decodeMintConversationFields(conv)
	if conv.Status != models.SoulMintConversationStatusInProgress {
		return mintConversationSession{}, &apptheory.AppError{Code: "app.conflict", Message: "conversation is not in progress"}
	}

	storedModel := strings.TrimSpace(conv.Model)
	if storedModel != "" {
		if session.modelSet != "" && !strings.EqualFold(storedModel, session.modelSet) {
			return mintConversationSession{}, &apptheory.AppError{Code: "app.conflict", Message: "cannot change model for an existing conversation"}
		}
		session.modelSet = storedModel
	}
	if strings.TrimSpace(conv.Messages) != "" {
		_ = json.Unmarshal([]byte(conv.Messages), &session.existingMessages)
	}
	session.existingUsage = conv.Usage
	return session, nil
}

func (s *Server) debitMintConversationStreamCredits(ctx context.Context, inst *models.Instance, agentIDHex string, session mintConversationSession, requestID string, now time.Time) *apptheory.AppError {
	extraWrites := func(tx core.TransactionBuilder, creditsRequested int64) error {
		if session.isNew {
			conv := &models.SoulAgentMintConversation{
				AgentID:        agentIDHex,
				ConversationID: session.conversationID,
				Model:          session.modelSet,
				Status:         models.SoulMintConversationStatusInProgress,
				CreatedAt:      now,
				ChargedCredits: creditsRequested,
			}
			_ = conv.UpdateKeys()
			tx.Create(conv)
			return nil
		}

		update := &models.SoulAgentMintConversation{
			AgentID:        agentIDHex,
			ConversationID: session.conversationID,
		}
		_ = update.UpdateKeys()
		tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
			ub.Add("ChargedCredits", creditsRequested)
			return nil
		}, tabletheory.IfExists())
		return nil
	}

	if _, appErr := s.debitSoulMintConversationCredits(
		ctx,
		inst,
		soulMintConversationStreamModule,
		session.conversationID,
		requestID,
		soulMintConversationStreamBaseCredits,
		now,
		extraWrites,
	); appErr != nil {
		return appErr
	}
	return nil
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

const mintConversationRunTimeout = 2 * time.Minute

func detachedMintConversationContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithoutCancel(ctx)
}

func emitMintConversationEvent(ctx context.Context, eventCh chan<- apptheory.SSEEvent, event apptheory.SSEEvent) bool {
	if eventCh == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case eventCh <- event:
		return true
	case <-ctx.Done():
		return false
	default:
		return false
	}
}

func (s *Server) streamMintConversation(ctx context.Context, eventCh chan<- apptheory.SSEEvent, p streamMintConversationParams) {
	defer close(eventCh)
	runCtx, cancel := context.WithTimeout(detachedMintConversationContext(ctx), mintConversationRunTimeout)
	defer cancel()

	// Emit start event.
	emitMintConversationEvent(ctx, eventCh, apptheory.SSEEvent{
		Event: "conversation_start",
		Data: soulMintConversationStartEvent{
			ConversationID: p.conversationID,
			Model:          p.modelSet,
		},
	})

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
		emitMintConversationEvent(ctx, eventCh, apptheory.SSEEvent{
			Event: "delta",
			Data: soulMintConversationDeltaEvent{
				Text: delta,
			},
		})
	}

	switch {
	case strings.HasPrefix(strings.ToLower(strings.TrimSpace(p.modelSet)), "openai:"):
		fullResponse, llmUsage, err = llm.StreamMintConversationOpenAI(runCtx, p.apiKey, p.modelSet, p.systemPrompt, llmMessages, onDelta)
	case strings.HasPrefix(strings.ToLower(strings.TrimSpace(p.modelSet)), "anthropic:"):
		fullResponse, llmUsage, err = llm.StreamMintConversationAnthropic(runCtx, p.apiKey, p.modelSet, p.systemPrompt, llmMessages, onDelta)
	default:
		err = fmt.Errorf("unsupported model set %q", p.modelSet)
	}

	if err != nil {
		emitMintConversationEvent(ctx, eventCh, apptheory.SSEEvent{
			Event: "error",
			Data: soulMintConversationErrorEvent{
				Error: "failed to generate response",
			},
		})
		// Update conversation status to failed.
		s.updateMintConversationStatus(runCtx, p.agentIDHex, p.conversationID, models.SoulMintConversationStatusFailed, p.existingMessages, "")
		return
	}

	// Append assistant response to messages and persist.
	updatedMessages := append(p.existingMessages, soulMintConversationMessage{Role: "assistant", Content: fullResponse})
	s.updateMintConversationTurn(runCtx, p.agentIDHex, p.conversationID, updatedMessages, addAIUsage(p.existingUsage, llmUsage))

	// Emit done event.
	emitMintConversationEvent(ctx, eventCh, apptheory.SSEEvent{
		Event: "conversation_done",
		Data: soulMintConversationDoneEvent{
			ConversationID: p.conversationID,
			FullResponse:   fullResponse,
		},
	})
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
		Messages:       encodeMintConversationBlob(string(messagesJSON)),
		Status:         models.SoulMintConversationStatusInProgress,
	}
	_ = conv.UpdateKeys()
	if err := s.store.DB.WithContext(ctx).Model(conv).IfExists().Update("Messages"); err != nil {
		log.Printf("controlplane: update mint conversation messages failed: agent=%s conversation=%s err=%v", conv.AgentID, conv.ConversationID, err)
	}
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
		Messages:       encodeMintConversationBlob(string(messagesJSON)),
		Usage:          usage,
		Status:         models.SoulMintConversationStatusInProgress,
	}
	_ = conv.UpdateKeys()
	if err := s.store.DB.WithContext(ctx).Model(conv).IfExists().Update("Messages", "Usage"); err != nil {
		log.Printf("controlplane: update mint conversation turn failed: agent=%s conversation=%s err=%v", conv.AgentID, conv.ConversationID, err)
	}
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
		Messages:             encodeMintConversationBlob(string(messagesJSON)),
		ProducedDeclarations: encodeMintConversationBlob(declarations),
		Status:               status,
		CompletedAt:          now,
	}
	_ = conv.UpdateKeys()
	if err := s.store.DB.WithContext(ctx).Model(conv).IfExists().Update("Messages", "ProducedDeclarations", "Status", "CompletedAt"); err != nil {
		log.Printf("controlplane: update mint conversation status failed: agent=%s conversation=%s status=%s err=%v", conv.AgentID, conv.ConversationID, conv.Status, err)
	}
}

// handleSoulCompleteMintConversation marks a conversation as completed and extracts declarations.
func (s *Server) handleSoulCompleteMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	regCtx, appErr := s.requireMintConversationRegistrationContext(ctx, false)
	if appErr != nil {
		return nil, appErr
	}
	if publishGuardErr := s.ensureMintConversationAgentNotPublished(ctx.Context(), regCtx.agentIDHex); publishGuardErr != nil {
		return nil, publishGuardErr
	}
	conversationID, appErr := requireMintConversationID(ctx)
	if appErr != nil {
		return nil, appErr
	}
	conv, appErr := s.loadMintConversationByStatus(ctx.Context(), regCtx.agentIDHex, conversationID, models.SoulMintConversationStatusInProgress, "conversation is not in progress", "")
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	declarationsJSON, extractUsage, appErr := s.resolveMintConversationCompletion(ctx, regCtx, conv, conversationID, now)
	if appErr != nil {
		return nil, appErr
	}
	if appErr := s.persistCompletedMintConversation(ctx.Context(), conv, declarationsJSON, extractUsage, now); appErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to complete conversation"}
	}
	promotion := s.loadOrFallbackSoulAgentPromotion(ctx.Context(), regCtx.agentIDHex, buildSoulAgentPromotionFromRegistration(regCtx.reg, now))
	promotion = updateSoulAgentPromotionForConversation(promotion, conversationID, models.SoulMintConversationStatusCompleted, now)
	promotion = updateSoulAgentPromotionReviewDigest(promotion, declarationsJSON)
	if appErr := s.saveSoulAgentPromotion(ctx.Context(), promotion); appErr != nil {
		return nil, appErr
	}
	if appErr := s.saveSoulAgentPromotionLifecycleEvent(ctx.Context(), buildSoulAgentPromotionLifecycleEvent(promotion, soulAgentPromotionLifecycleEventInput{
		EventType:      models.SoulAgentPromotionEventTypeFinalizeReady,
		RequestID:      strings.TrimSpace(ctx.RequestID),
		ConversationID: conversationID,
		OccurredAt:     now,
	})); appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, conv)
}

func (s *Server) handleSoulAgentCompleteMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentCtx, appErr := s.requireMintConversationAgentContext(ctx, false)
	if appErr != nil {
		return nil, appErr
	}
	if agentCtx.identity != nil && agentCtx.identity.SelfDescriptionVersion > 0 {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: soulMintConversationAlreadyPublishedMessage}
	}
	conversationID, appErr := requireMintConversationID(ctx)
	if appErr != nil {
		return nil, appErr
	}
	conv, appErr := s.loadMintConversationByStatus(ctx.Context(), agentCtx.agentIDHex, conversationID, models.SoulMintConversationStatusInProgress, "conversation is not in progress", "")
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	declarationsJSON, extractUsage, appErr := s.resolveMintConversationCompletion(ctx, mintConversationRegistrationContext{
		reg:        agentCtx.reg,
		inst:       agentCtx.inst,
		agentIDHex: agentCtx.agentIDHex,
	}, conv, conversationID, now)
	if appErr != nil {
		return nil, appErr
	}
	if appErr := s.persistCompletedMintConversation(ctx.Context(), conv, declarationsJSON, extractUsage, now); appErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to complete conversation"}
	}
	promotion := s.loadOrFallbackSoulAgentPromotion(ctx.Context(), agentCtx.agentIDHex, buildSoulAgentPromotionFromRegistration(agentCtx.reg, now))
	promotion = updateSoulAgentPromotionForConversation(promotion, conversationID, models.SoulMintConversationStatusCompleted, now)
	promotion = updateSoulAgentPromotionReviewDigest(promotion, declarationsJSON)
	if appErr := s.saveSoulAgentPromotion(ctx.Context(), promotion); appErr != nil {
		return nil, appErr
	}
	if appErr := s.saveSoulAgentPromotionLifecycleEvent(ctx.Context(), buildSoulAgentPromotionLifecycleEvent(promotion, soulAgentPromotionLifecycleEventInput{
		EventType:      models.SoulAgentPromotionEventTypeFinalizeReady,
		RequestID:      strings.TrimSpace(ctx.RequestID),
		ConversationID: conversationID,
		OccurredAt:     now,
	})); appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, conv)
}

// handleSoulGetMintConversation retrieves a mint conversation record.
func (s *Server) handleSoulGetMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	regCtx, appErr := s.requireMintConversationRegistrationContext(ctx, false)
	if appErr != nil {
		return nil, appErr
	}

	conversationID := strings.TrimSpace(ctx.Param("conversationId"))
	if conversationID == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "conversationId is required"}
	}

	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), regCtx.agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "conversation not found"}
	}
	decodeMintConversationFields(conv)

	return apptheory.JSON(http.StatusOK, conv)
}

func (s *Server) handleSoulAgentGetMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentCtx, appErr := s.requireMintConversationAgentContext(ctx, false)
	if appErr != nil {
		return nil, appErr
	}
	conversationID, appErr := requireMintConversationID(ctx)
	if appErr != nil {
		return nil, appErr
	}

	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), agentCtx.agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "conversation not found"}
	}
	decodeMintConversationFields(conv)

	return apptheory.JSON(http.StatusOK, conv)
}

// handleSoulBeginFinalizeMintConversation prepares a mint conversation output to be published as a v2 registration
// by verifying boundary signatures and returning the v2 self-attestation digest for the full document.
func (s *Server) handleSoulBeginFinalizeMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	finalizeCtx, appErr := s.loadMintConversationFinalizeContext(ctx)
	if appErr != nil {
		return nil, appErr
	}
	if finalizeCtx.identity.SelfDescriptionVersion > 0 {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: soulMintConversationAlreadyPublishedMessage}
	}
	req, err := parseMintConversationFinalizeBeginRequestBody(ctx)
	if err != nil {
		return nil, err
	}

	decl, appErr := parseAndValidateMintConversationDeclarations(finalizeCtx.conv.ProducedDeclarations)
	if appErr != nil {
		return nil, appErr
	}
	if verifyErr := verifyMintConversationBoundarySignatures(finalizeCtx.identity.Wallet, decl.Boundaries, req.BoundarySignatures); verifyErr != nil {
		return nil, verifyErr
	}

	now := time.Now().UTC()
	expectedVersion := finalizeCtx.identity.SelfDescriptionVersion
	nextVersion := expectedVersion + 1

	regMap, _, digest, _, _, appErr := s.buildMintConversationFinalizeV2Registration(finalizeCtx.agentIDHex, finalizeCtx.identity, decl, req.BoundarySignatures, now, nextVersion, "0x00")
	if appErr != nil {
		return nil, appErr
	}
	return s.respondMintConversationFinalizePreflight(finalizeCtx.identity, decl, req.BoundarySignatures, regMap, digest, now, expectedVersion, nextVersion)
}

func (s *Server) handleSoulAgentBeginFinalizeMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentCtx, appErr := s.requireMintConversationAgentContext(ctx, true)
	if appErr != nil {
		return nil, appErr
	}
	conversationID, appErr := requireMintConversationID(ctx)
	if appErr != nil {
		return nil, appErr
	}
	conv, appErr := s.loadMintConversationByStatus(ctx.Context(), agentCtx.agentIDHex, conversationID, models.SoulMintConversationStatusCompleted, "conversation is not completed", "conversation has no produced declarations")
	if appErr != nil {
		return nil, appErr
	}
	if agentCtx.identity.SelfDescriptionVersion > 0 {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: soulMintConversationAlreadyPublishedMessage}
	}
	if strings.TrimSpace(agentCtx.identity.PrincipalAddress) == "" ||
		strings.TrimSpace(agentCtx.identity.PrincipalSignature) == "" ||
		strings.TrimSpace(agentCtx.identity.PrincipalDeclaration) == "" ||
		strings.TrimSpace(agentCtx.identity.PrincipalDeclaredAt) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "principal declaration is missing; re-verify registration"}
	}

	req, err := parseMintConversationFinalizeBeginRequestBody(ctx)
	if err != nil {
		return nil, err
	}
	decl, appErr := parseAndValidateMintConversationDeclarations(conv.ProducedDeclarations)
	if appErr != nil {
		return nil, appErr
	}
	if verifyErr := verifyMintConversationBoundarySignatures(agentCtx.identity.Wallet, decl.Boundaries, req.BoundarySignatures); verifyErr != nil {
		return nil, verifyErr
	}

	now := time.Now().UTC()
	expectedVersion := agentCtx.identity.SelfDescriptionVersion
	nextVersion := expectedVersion + 1
	regMap, _, digest, _, _, appErr := s.buildMintConversationFinalizeV2Registration(agentCtx.agentIDHex, agentCtx.identity, decl, req.BoundarySignatures, now, nextVersion, "0x00")
	if appErr != nil {
		return nil, appErr
	}
	return s.respondMintConversationFinalizePreflight(agentCtx.identity, decl, req.BoundarySignatures, regMap, digest, now, expectedVersion, nextVersion)
}

func (s *Server) handleSoulFinalizeMintConversationPreflight(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleSoulBeginFinalizeMintConversation(ctx)
}

func (s *Server) handleSoulAgentFinalizeMintConversationPreflight(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleSoulAgentBeginFinalizeMintConversation(ctx)
}

// handleSoulFinalizeMintConversation publishes the v2 registration version derived from a completed mint conversation.
func (s *Server) handleSoulFinalizeMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	finalizeCtx, appErr := s.loadMintConversationFinalizeContext(ctx)
	if appErr != nil {
		return nil, appErr
	}
	req, issuedAt, expectedVersion, selfSig, err := parseMintConversationFinalizeRequestBody(ctx)
	if err != nil {
		return nil, err
	}

	nextVersion := *expectedVersion + 1
	if finalizeCtx.identity.SelfDescriptionVersion > nextVersion {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent has advanced beyond this version"}
	}
	if finalizeCtx.identity.SelfDescriptionVersion < *expectedVersion {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}
	decl, appErr := parseAndValidateMintConversationDeclarations(finalizeCtx.conv.ProducedDeclarations)
	if appErr != nil {
		return nil, appErr
	}
	if verifyErr := verifyMintConversationBoundarySignatures(finalizeCtx.identity.Wallet, decl.Boundaries, req.BoundarySignatures); verifyErr != nil {
		return nil, verifyErr
	}

	regMap, regV2, digest, capsNorm, claimLevels, appErr := s.buildMintConversationFinalizeV2Registration(finalizeCtx.agentIDHex, finalizeCtx.identity, decl, req.BoundarySignatures, issuedAt.UTC(), nextVersion, selfSig)
	if appErr != nil {
		return nil, appErr
	}
	if sigErr := verifyEthereumSignatureBytes(finalizeCtx.identity.Wallet, digest, selfSig); sigErr != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration signature"}
	}
	return s.finalizeMintConversationPublish(ctx, finalizeCtx, regV2, regMap, decl, req.BoundarySignatures, capsNorm, claimLevels, issuedAt, expectedVersion, selfSig)
}

func (s *Server) handleSoulAgentFinalizeMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentCtx, appErr := s.requireMintConversationAgentContext(ctx, true)
	if appErr != nil {
		return nil, appErr
	}
	conversationID, appErr := requireMintConversationID(ctx)
	if appErr != nil {
		return nil, appErr
	}
	conv, appErr := s.loadMintConversationByStatus(ctx.Context(), agentCtx.agentIDHex, conversationID, models.SoulMintConversationStatusCompleted, "conversation is not completed", "conversation has no produced declarations")
	if appErr != nil {
		return nil, appErr
	}
	if strings.TrimSpace(agentCtx.identity.PrincipalAddress) == "" ||
		strings.TrimSpace(agentCtx.identity.PrincipalSignature) == "" ||
		strings.TrimSpace(agentCtx.identity.PrincipalDeclaration) == "" ||
		strings.TrimSpace(agentCtx.identity.PrincipalDeclaredAt) == "" {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "principal declaration is missing; re-verify registration"}
	}

	req, issuedAt, expectedVersion, selfSig, err := parseMintConversationFinalizeRequestBody(ctx)
	if err != nil {
		return nil, err
	}

	finalizeCtx := mintConversationFinalizeContext{
		reg:            agentCtx.reg,
		inst:           agentCtx.inst,
		identity:       agentCtx.identity,
		conv:           conv,
		agentIDHex:     agentCtx.agentIDHex,
		conversationID: conversationID,
	}

	nextVersion := *expectedVersion + 1
	if finalizeCtx.identity.SelfDescriptionVersion > nextVersion {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "agent has advanced beyond this version"}
	}
	if finalizeCtx.identity.SelfDescriptionVersion < *expectedVersion {
		return nil, &apptheory.AppError{Code: "app.conflict", Message: "version conflict; reload and try again"}
	}

	decl, appErr := parseAndValidateMintConversationDeclarations(finalizeCtx.conv.ProducedDeclarations)
	if appErr != nil {
		return nil, appErr
	}
	if verifyErr := verifyMintConversationBoundarySignatures(finalizeCtx.identity.Wallet, decl.Boundaries, req.BoundarySignatures); verifyErr != nil {
		return nil, verifyErr
	}

	regMap, regV2, digest, capsNorm, claimLevels, appErr := s.buildMintConversationFinalizeV2Registration(finalizeCtx.agentIDHex, finalizeCtx.identity, decl, req.BoundarySignatures, issuedAt.UTC(), nextVersion, selfSig)
	if appErr != nil {
		return nil, appErr
	}
	if sigErr := verifyEthereumSignatureBytes(finalizeCtx.identity.Wallet, digest, selfSig); sigErr != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration signature"}
	}
	return s.finalizeMintConversationPublish(ctx, finalizeCtx, regV2, regMap, decl, req.BoundarySignatures, capsNorm, claimLevels, issuedAt, expectedVersion, selfSig)
}

// --- Helpers ---

func (s *Server) respondMintConversationFinalizePreflight(
	identity *models.SoulAgentIdentity,
	decl soulMintConversationProducedDeclarations,
	boundarySignatures map[string]string,
	regMap map[string]any,
	digest []byte,
	issuedAt time.Time,
	expectedVersion int,
	nextVersion int,
) (*apptheory.Response, error) {
	canonicalJSON, appErr := buildMintConversationFinalizeCanonicalJSON(regMap)
	if appErr != nil {
		return nil, appErr
	}

	issuedAtStr := issuedAt.UTC().Format(time.RFC3339Nano)
	digestHex := "0x" + hex.EncodeToString(digest)
	return apptheory.JSON(http.StatusOK, soulMintConversationFinalizeBeginResponse{
		Version:             "1",
		DigestHex:           digestHex,
		IssuedAt:            issuedAtStr,
		ExpectedVersion:     expectedVersion,
		NextVersion:         nextVersion,
		DeclarationsPreview: decl,
		BoundaryRequirements: buildMintConversationFinalizeBoundaryRequirements(
			strings.TrimSpace(identity.Wallet),
			decl.Boundaries,
			boundarySignatures,
		),
		SelfAttestationSigning: soulMintConversationFinalizeSigningInput{
			SignerWallet:    strings.TrimSpace(identity.Wallet),
			SigningMethod:   "eip191_personal_sign",
			MessageEncoding: "hex_bytes",
			MessageHex:      digestHex,
			DigestHex:       digestHex,
			CanonicalJSON:   canonicalJSON,
		},
		FinalizeRequestTemplate: soulMintConversationFinalizeRequestTemplate{
			BoundarySignatures: copyMintConversationBoundarySignatures(boundarySignatures),
			IssuedAt:           issuedAtStr,
			ExpectedVersion:    expectedVersion,
			SelfAttestation:    "",
		},
		RegistrationPreview: regMap,
	})
}

func buildMintConversationFinalizeBoundaryRequirements(
	wallet string,
	boundaries []soul.BoundaryV2,
	boundarySignatures map[string]string,
) []soulMintConversationFinalizeBoundaryRequirement {
	out := make([]soulMintConversationFinalizeBoundaryRequirement, 0, len(boundaries))
	for i := range boundaries {
		b := boundaries[i]
		requirement := soulMintConversationFinalizeBoundaryRequirement{
			BoundaryID:      strings.TrimSpace(b.ID),
			Category:        strings.ToLower(strings.TrimSpace(b.Category)),
			Statement:       strings.TrimSpace(b.Statement),
			SignatureHex:    strings.TrimSpace(boundarySignatures[strings.TrimSpace(b.ID)]),
			SignerWallet:    strings.TrimSpace(wallet),
			SigningMethod:   "eip191_personal_sign",
			MessageEncoding: "utf8",
			Message:         strings.TrimSpace(b.Statement),
			DigestHex:       "0x" + hex.EncodeToString(crypto.Keccak256([]byte(strings.TrimSpace(b.Statement)))),
		}
		if strings.TrimSpace(b.Rationale) != "" {
			requirement.Rationale = strings.TrimSpace(b.Rationale)
		}
		if b.Supersedes != nil && strings.TrimSpace(*b.Supersedes) != "" {
			requirement.Supersedes = strings.TrimSpace(*b.Supersedes)
		}
		out = append(out, requirement)
	}
	return out
}

func buildMintConversationFinalizeCanonicalJSON(regMap map[string]any) (string, *apptheory.AppError) {
	unsigned := cloneSoulRegistrationMap(regMap)
	att := map[string]any{}
	if attAny, ok := regMap["attestations"].(map[string]any); ok {
		for key, value := range attAny {
			if strings.TrimSpace(key) == "selfAttestation" {
				continue
			}
			att[key] = value
		}
	}
	unsigned["attestations"] = att

	canonical, err := canonicalJSON(unsigned)
	if err != nil {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	return string(canonical), nil
}

func copyMintConversationBoundarySignatures(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

func parseMintConversationCompleteDeclarations(ctx *apptheory.Context) string {
	var reqBody struct {
		Declarations json.RawMessage `json:"declarations,omitempty"`
	}
	_ = httpx.ParseJSON(ctx, &reqBody)

	raw := bytes.TrimSpace(reqBody.Declarations)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] != '"' {
		return strings.TrimSpace(string(raw))
	}

	var wrapped string
	if decodeErr := json.Unmarshal(raw, &wrapped); decodeErr != nil {
		return ""
	}
	return strings.TrimSpace(wrapped)
}

func (s *Server) resolveMintConversationCompletion(
	ctx *apptheory.Context,
	regCtx mintConversationRegistrationContext,
	conv *models.SoulAgentMintConversation,
	conversationID string,
	now time.Time,
) (string, models.AIUsage, *apptheory.AppError) {
	declarationsJSON := parseMintConversationCompleteDeclarations(ctx)
	if declarationsJSON != "" {
		if _, appErr := parseAndValidateMintConversationDeclarations(declarationsJSON); appErr != nil {
			return "", models.AIUsage{}, appErr
		}
		return declarationsJSON, models.AIUsage{}, nil
	}

	creditsDebited, appErr := s.debitSoulMintConversationCredits(
		ctx.Context(),
		regCtx.inst,
		soulMintConversationExtractModule,
		conversationID,
		strings.TrimSpace(ctx.RequestID),
		soulMintConversationExtractBaseCredits,
		now,
		func(tx core.TransactionBuilder, creditsRequested int64) error {
			update := &models.SoulAgentMintConversation{AgentID: regCtx.agentIDHex, ConversationID: conversationID}
			_ = update.UpdateKeys()
			tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
				ub.Add("ChargedCredits", creditsRequested)
				return nil
			}, tabletheory.IfExists())
			return nil
		},
	)
	if appErr != nil {
		return "", models.AIUsage{}, appErr
	}
	conv.ChargedCredits += creditsDebited

	decl, usage, appErr := s.extractMintConversationDeclarations(ctx.Context(), regCtx.reg, conv, now)
	if appErr != nil {
		return "", models.AIUsage{}, appErr
	}
	b, err := json.Marshal(decl)
	if err != nil {
		return "", models.AIUsage{}, &apptheory.AppError{Code: "app.internal", Message: "failed to serialize declarations"}
	}
	return strings.TrimSpace(string(b)), usage, nil
}

func (s *Server) persistCompletedMintConversation(ctx context.Context, conv *models.SoulAgentMintConversation, declarationsJSON string, extractUsage models.AIUsage, now time.Time) *apptheory.AppError {
	if extractUsage != (models.AIUsage{}) {
		conv.Usage = addAIUsage(conv.Usage, extractUsage)
	}
	conv.Status = models.SoulMintConversationStatusCompleted
	conv.ProducedDeclarations = declarationsJSON
	conv.CompletedAt = now
	update := &models.SoulAgentMintConversation{
		AgentID:              conv.AgentID,
		ConversationID:       conv.ConversationID,
		Status:               conv.Status,
		ProducedDeclarations: encodeMintConversationBlob(declarationsJSON),
		CompletedAt:          conv.CompletedAt,
		Usage:                conv.Usage,
	}
	_ = update.UpdateKeys()
	if err := s.store.DB.WithContext(ctx).Model(update).IfExists().Update("Status", "ProducedDeclarations", "CompletedAt", "Usage"); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to complete conversation"}
	}
	return nil
}

func encodeMintConversationBlob(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return mintConversationBlobPrefix + base64.StdEncoding.EncodeToString([]byte(trimmed))
}

func decodeMintConversationBlob(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, mintConversationBlobPrefix) {
		return trimmed
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(trimmed, mintConversationBlobPrefix))
	if err != nil {
		return trimmed
	}
	return strings.TrimSpace(string(decoded))
}

func decodeMintConversationFields(conv *models.SoulAgentMintConversation) {
	if conv == nil {
		return
	}
	conv.Messages = decodeMintConversationBlob(conv.Messages)
	conv.ProducedDeclarations = decodeMintConversationBlob(conv.ProducedDeclarations)
}

func parseMintConversationFinalizeBeginRequestBody(ctx *apptheory.Context) (soulMintConversationFinalizeBeginRequest, error) {
	var req soulMintConversationFinalizeBeginRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return req, parseErr
	}
	if len(req.BoundarySignatures) == 0 {
		return req, &apptheory.AppError{Code: "app.bad_request", Message: "boundary_signatures is required"}
	}
	return req, nil
}

func parseMintConversationFinalizeRequestBody(ctx *apptheory.Context) (soulMintConversationFinalizeRequest, time.Time, *int, string, error) {
	var req soulMintConversationFinalizeRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return req, time.Time{}, nil, "", parseErr
	}
	if len(req.BoundarySignatures) == 0 {
		return req, time.Time{}, nil, "", &apptheory.AppError{Code: "app.bad_request", Message: "boundary_signatures is required"}
	}
	issuedAtRaw := strings.TrimSpace(req.IssuedAt)
	if issuedAtRaw == "" {
		return req, time.Time{}, nil, "", &apptheory.AppError{Code: "app.bad_request", Message: "issued_at is required"}
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, issuedAtRaw)
	if err != nil {
		issuedAt, err = time.Parse(time.RFC3339, issuedAtRaw)
	}
	if err != nil {
		return req, time.Time{}, nil, "", &apptheory.AppError{Code: "app.bad_request", Message: "issued_at must be an RFC3339 timestamp"}
	}
	if req.ExpectedVersion == nil {
		return req, time.Time{}, nil, "", &apptheory.AppError{Code: "app.bad_request", Message: "expected_version is required"}
	}
	if *req.ExpectedVersion < 0 {
		return req, time.Time{}, nil, "", &apptheory.AppError{Code: "app.bad_request", Message: "expected_version is invalid"}
	}
	selfSig := strings.TrimSpace(req.SelfAttestation)
	if selfSig == "" {
		return req, time.Time{}, nil, "", &apptheory.AppError{Code: "app.bad_request", Message: "self_attestation is required"}
	}
	return req, issuedAt, req.ExpectedVersion, selfSig, nil
}

func verifyMintConversationBoundarySignatures(wallet string, boundaries []soul.BoundaryV2, signatures map[string]string) *apptheory.AppError {
	for i := range boundaries {
		b := boundaries[i]
		sig := strings.TrimSpace(signatures[strings.TrimSpace(b.ID)])
		if sig == "" {
			return &apptheory.AppError{Code: "app.bad_request", Message: "missing boundary signature for " + strings.TrimSpace(b.ID)}
		}
		statementDigest := crypto.Keccak256([]byte(strings.TrimSpace(b.Statement)))
		if sigErr := verifyEthereumSignatureBytes(wallet, statementDigest, sig); sigErr != nil {
			return &apptheory.AppError{Code: "app.bad_request", Message: "invalid boundary signature for " + strings.TrimSpace(b.ID)}
		}
	}
	return nil
}

func (s *Server) finalizeMintConversationPublish(
	ctx *apptheory.Context,
	finalizeCtx mintConversationFinalizeContext,
	regV2 *soul.RegistrationFileV2,
	regMap map[string]any,
	decl soulMintConversationProducedDeclarations,
	boundarySignatures map[string]string,
	capsNorm []string,
	claimLevels map[string]string,
	issuedAt time.Time,
	expectedVersion *int,
	selfSig string,
) (*apptheory.Response, error) {
	regBytes, err := json.Marshal(regMap)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	sum := sha256.Sum256(regBytes)
	regSHA256 := hex.EncodeToString(sum[:])
	changeSummary := extractStringField(regMap, "changeSummary")
	bounds := buildMintConversationBoundaryModels(finalizeCtx.agentIDHex, decl.Boundaries, boundarySignatures, issuedAt, *expectedVersion+1)

	now := time.Now().UTC()
	publishedVersion, pubErr := s.publishSoulAgentRegistrationV2(ctx.Context(), finalizeCtx.agentIDHex, finalizeCtx.identity, regV2, regBytes, regSHA256, selfSig, changeSummary, capsNorm, claimLevels, expectedVersion, now)
	if pubErr != nil {
		return nil, pubErr
	}
	if appErr := s.persistMintConversationBoundaries(ctx.Context(), finalizeCtx.identity, bounds); appErr != nil {
		return nil, appErr
	}
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     strings.TrimSpace(ctx.AuthIdentity),
		Action:    "soul.mint_conversation.finalize",
		Target:    fmt.Sprintf("soul_agent_identity:%s", finalizeCtx.agentIDHex),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	})
	promotion := s.loadOrFallbackSoulAgentPromotion(ctx.Context(), finalizeCtx.agentIDHex, buildSoulAgentPromotionFromRegistration(finalizeCtx.reg, now))
	promotion = updateSoulAgentPromotionForConversation(promotion, finalizeCtx.conversationID, models.SoulMintConversationStatusCompleted, now)
	promotion = updateSoulAgentPromotionForGraduation(promotion, publishedVersion, now)
	if appErr := s.saveSoulAgentPromotion(ctx.Context(), promotion); appErr != nil {
		return nil, appErr
	}
	if appErr := s.saveSoulAgentPromotionLifecycleEvent(ctx.Context(), buildSoulAgentPromotionLifecycleEvent(promotion, soulAgentPromotionLifecycleEventInput{
		EventType:      models.SoulAgentPromotionEventTypeGraduated,
		RequestID:      strings.TrimSpace(ctx.RequestID),
		ConversationID: finalizeCtx.conversationID,
		OccurredAt:     now,
	})); appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(http.StatusOK, soulMintConversationFinalizeResponse{
		Version:          "1",
		Agent:            *finalizeCtx.identity,
		PublishedVersion: publishedVersion,
	})
}

func buildMintConversationBoundaryModels(agentIDHex string, boundaries []soul.BoundaryV2, boundarySignatures map[string]string, issuedAt time.Time, nextVersion int) []*models.SoulAgentBoundary {
	out := make([]*models.SoulAgentBoundary, 0, len(boundaries))
	for i := range boundaries {
		b := boundaries[i]
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
			Signature:      strings.TrimSpace(boundarySignatures[strings.TrimSpace(b.ID)]),
			AddedAt:        issuedAt.UTC(),
		}
		_ = m.UpdateKeys()
		out = append(out, m)
	}
	return out
}

func (s *Server) persistMintConversationBoundaries(ctx context.Context, identity *models.SoulAgentIdentity, bounds []*models.SoulAgentBoundary) *apptheory.AppError {
	for _, b := range bounds {
		if b == nil {
			continue
		}
		if err := s.store.DB.WithContext(ctx).Model(b).IfNotExists().Create(); err != nil {
			if theoryErrors.IsConditionFailed(err) {
				s.tryWriteSoulBoundaryKeywordIndexForBoundary(ctx, identity, b)
				continue
			}
			return &apptheory.AppError{Code: "app.internal", Message: "failed to persist boundaries"}
		}
		s.tryWriteSoulBoundaryKeywordIndexForBoundary(ctx, identity, b)
	}
	return nil
}

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
	lifecycleStatus := mintConversationFinalizeLifecycleStatus(identity)
	principal := buildMintConversationFinalizePrincipal(identity)
	selfDesc := buildMintConversationFinalizeSelfDescription(decl.SelfDescription)
	capsAny := buildMintConversationFinalizeCapabilities(decl.Capabilities)
	boundsAny := buildMintConversationFinalizeBoundaries(decl.Boundaries, boundarySignatures, issuedAtStr, nextVersion)
	changeSummary := fmt.Sprintf("Publish mint conversation declarations (v%d)", nextVersion)
	transparency := nonNilMintConversationTransparency(decl.Transparency)

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
		"transparency":    transparency,
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

	regBytes, err := json.Marshal(reg)
	if err != nil {
		return nil, nil, nil, nil, nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid registration JSON"}
	}
	parsed, appErr := parseMintConversationFinalizeV2Registration(regBytes)
	if appErr != nil {
		return nil, nil, nil, nil, nil, appErr
	}

	digest, appErr = computeSoulRegistrationSelfAttestationDigest(reg)
	if appErr != nil {
		return nil, nil, nil, nil, nil, appErr
	}

	// Capability indexing inputs.
	caps := extractCapabilityNames(reg)
	capsNorm = normalizeSoulCapabilitiesLoose(caps)
	claimLevels = extractCapabilityClaimLevels(reg)

	return reg, parsed, digest, capsNorm, claimLevels, nil
}

func mintConversationFinalizeLifecycleStatus(identity *models.SoulAgentIdentity) string {
	lifecycleStatus := strings.ToLower(strings.TrimSpace(identity.LifecycleStatus))
	if lifecycleStatus == "" {
		lifecycleStatus = strings.ToLower(strings.TrimSpace(identity.Status))
	}
	if lifecycleStatus == models.SoulAgentStatusPending || lifecycleStatus == "" {
		return models.SoulAgentStatusActive
	}
	return lifecycleStatus
}

func buildMintConversationFinalizePrincipal(identity *models.SoulAgentIdentity) map[string]any {
	return map[string]any{
		"type":        "individual",
		"identifier":  strings.TrimSpace(identity.PrincipalAddress),
		"declaration": strings.TrimSpace(identity.PrincipalDeclaration),
		"signature":   strings.TrimSpace(identity.PrincipalSignature),
		"declaredAt":  strings.TrimSpace(identity.PrincipalDeclaredAt),
	}
}

func buildMintConversationFinalizeSelfDescription(selfDesc soul.SelfDescriptionV2) map[string]any {
	out := map[string]any{
		"purpose":    strings.TrimSpace(selfDesc.Purpose),
		"authoredBy": strings.ToLower(strings.TrimSpace(selfDesc.AuthoredBy)),
	}
	if strings.TrimSpace(selfDesc.Constraints) != "" {
		out["constraints"] = strings.TrimSpace(selfDesc.Constraints)
	}
	if strings.TrimSpace(selfDesc.Commitments) != "" {
		out["commitments"] = strings.TrimSpace(selfDesc.Commitments)
	}
	if strings.TrimSpace(selfDesc.Limitations) != "" {
		out["limitations"] = strings.TrimSpace(selfDesc.Limitations)
	}
	if strings.TrimSpace(selfDesc.MintingModel) != "" {
		out["mintingModel"] = strings.TrimSpace(selfDesc.MintingModel)
	}
	return out
}

func buildMintConversationFinalizeCapabilities(capabilities []soul.CapabilityV2) []any {
	out := make([]any, 0, len(capabilities))
	for i := range capabilities {
		c := capabilities[i]
		item := map[string]any{
			"capability": strings.TrimSpace(c.Capability),
			"scope":      strings.TrimSpace(c.Scope),
			"claimLevel": strings.ToLower(strings.TrimSpace(c.ClaimLevel)),
		}
		if len(c.Constraints) > 0 {
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
		out = append(out, item)
	}
	return out
}

func buildMintConversationFinalizeBoundaries(boundaries []soul.BoundaryV2, boundarySignatures map[string]string, issuedAt string, nextVersion int) []any {
	out := make([]any, 0, len(boundaries))
	for i := range boundaries {
		b := boundaries[i]
		item := map[string]any{
			"id":             strings.TrimSpace(b.ID),
			"category":       strings.ToLower(strings.TrimSpace(b.Category)),
			"statement":      strings.TrimSpace(b.Statement),
			"addedAt":        issuedAt,
			"addedInVersion": strconv.Itoa(nextVersion),
			"signature":      strings.TrimSpace(boundarySignatures[strings.TrimSpace(b.ID)]),
		}
		if strings.TrimSpace(b.Rationale) != "" {
			item["rationale"] = strings.TrimSpace(b.Rationale)
		}
		if b.Supersedes != nil && strings.TrimSpace(*b.Supersedes) != "" {
			item["supersedes"] = strings.TrimSpace(*b.Supersedes)
		}
		out = append(out, item)
	}
	return out
}

func nonNilMintConversationTransparency(transparency map[string]any) map[string]any {
	if transparency == nil {
		return map[string]any{}
	}
	return transparency
}

func parseMintConversationFinalizeV2Registration(regBytes []byte) (*soul.RegistrationFileV2, *apptheory.AppError) {
	parsed, err := soul.ParseRegistrationFileV2(regBytes)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid v2 registration schema"}
	}
	if err := parsed.Validate(); err != nil {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: err.Error()}
	}
	return parsed, nil
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
		fmt.Fprintf(&sb, "- Domain: %s\n", quoteMintConversationPromptValue(reg.DomainNormalized))
	}
	if reg.LocalID != "" {
		fmt.Fprintf(&sb, "- Local ID: %s\n", quoteMintConversationPromptValue(reg.LocalID))
	}
	if len(reg.Capabilities) > 0 {
		caps := make([]string, 0, len(reg.Capabilities))
		for _, c := range reg.Capabilities {
			c = sanitizeMintConversationPromptInline(c, 128)
			if c != "" {
				caps = append(caps, c)
			}
		}
		b, _ := json.Marshal(caps)
		fmt.Fprintf(&sb, "- Declared capabilities: %s\n", string(b))
	}

	return sb.String()
}

func sanitizeMintConversationPromptInline(raw string, maxLen int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
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

func quoteMintConversationPromptValue(raw string) string {
	raw = sanitizeMintConversationPromptInline(raw, 256)
	if raw == "" {
		return ""
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return strconv.Quote(raw)
	}
	return string(b)
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
			log.Printf("controlplane: mint conversation declaration extraction failed: provider=openai model=%s err=%v", modelSet, err)
			return soulMintConversationProducedDeclarations{}, models.AIUsage{}, &apptheory.AppError{Code: "app.internal", Message: "failed to extract declarations"}
		}
		draft = out
		usage = u
	case strings.HasPrefix(strings.ToLower(modelSet), "anthropic:"):
		out, u, err := llm.MintConversationDeclarationsAnthropic(ctx, apiKey, modelSet, in)
		if err != nil {
			log.Printf("controlplane: mint conversation declaration extraction failed: provider=anthropic model=%s err=%v", modelSet, err)
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
		c.ClaimLevel = soulClaimLevelSelfDeclared
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
