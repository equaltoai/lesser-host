package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/v2"
	"github.com/theory-cloud/tabletheory/v2/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/ai/llm"
	"github.com/equaltoai/lesser-host/internal/billing"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis"
	"github.com/equaltoai/lesser-host/internal/hostedgenesis/mintprompt"
	"github.com/equaltoai/lesser-host/internal/httpx"
	"github.com/equaltoai/lesser-host/internal/secrets"
	"github.com/equaltoai/lesser-host/internal/soul"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	soulMintConversationStreamBaseCredits  = int64(10)
	soulMintConversationExtractBaseCredits = int64(10)

	soulMintConversationStreamModule           = "soul.mint_conversation.stream"
	soulMintConversationExtractModule          = "soul.mint_conversation.extract"
	defaultSoulMintConversationModel           = "anthropic:claude-sonnet-4-6"
	mintConversationUnsupportedModelSetMessage = "unsupported model set"

	soulMintConversationAlreadyPublishedMessage = "registration is already published"

	soulMintConversationCompleteConflictMessage           = "conversation cannot be completed from current state"
	soulMintConversationCompleteReasonInvalidState        = "invalid_completion_state"
	soulMintConversationCompleteReasonMissingDeclarations = "missing_produced_declarations"
	soulMintConversationCompleteReasonInvalidDeclarations = "invalid_produced_declarations"
	soulMintConversationCompleteDetailReason              = "reason"
	soulMintConversationCompleteDetailStatus              = "conversation_status"
	soulMintConversationCompleteDetailExpectedStatus      = "expected_status"
	soulMintConversationCompleteDetailDeclarationsPresent = "produced_declarations_present"
	soulMintConversationCompleteDetailDeclarationsValid   = "produced_declarations_valid"
)

// --- Request / Response types ---

type soulMintConversationRequest struct {
	ConversationID  string `json:"conversation_id,omitempty"` // Empty = start new conversation.
	Model           string `json:"model,omitempty"`           // e.g. "anthropic:claude-sonnet-4-6"
	Message         string `json:"message"`                   // User's message for this turn.
	IdempotencyKey  string `json:"idempotency_key,omitempty"`
	CorrelationID   string `json:"correlation_id,omitempty"`
	LesserRequestID string `json:"lesser_request_id,omitempty"`
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
	SignerWallet    string `json:"signer_wallet,omitempty"`
	SigningMethod   string `json:"signing_method"`
	MessageEncoding string `json:"message_encoding"`
	Message         string `json:"message"`
	DigestHex       string `json:"digest_hex,omitempty"`
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
	SelfAttestation    string            `json:"self_attestation,omitempty"`
}

type soulMintConversationFinalizeBeginResponse struct {
	Version                 string                                            `json:"version"`
	AuthorityModel          string                                            `json:"authority_model,omitempty"`
	AnchorState             string                                            `json:"anchor_state,omitempty"`
	DigestHex               string                                            `json:"digest_hex,omitempty"`
	IssuedAt                string                                            `json:"issued_at"`
	ExpectedVersion         int                                               `json:"expected_version"`
	NextVersion             int                                               `json:"next_version"`
	DeclarationsPreview     soulMintConversationProducedDeclarations          `json:"declarations_preview"`
	BoundaryRequirements    []soulMintConversationFinalizeBoundaryRequirement `json:"boundary_requirements,omitempty"`
	SelfAttestationSigning  *soulMintConversationFinalizeSigningInput         `json:"self_attestation_signing,omitempty"`
	FinalizeRequestTemplate soulMintConversationFinalizeRequestTemplate       `json:"finalize_request_template"`
	RegistrationPreview     map[string]any                                    `json:"registration_preview,omitempty"`
}

type soulMintConversationFinalizeRequest struct {
	BoundarySignatures map[string]string `json:"boundary_signatures"`
	IssuedAt           string            `json:"issued_at"`
	ExpectedVersion    *int              `json:"expected_version,omitempty"`
	SelfAttestation    string            `json:"self_attestation"`
}

type soulMintConversationPublicationEvidence struct {
	AgentID                    string `json:"agent_id"`
	PublishedVersion           int    `json:"published_version"`
	RegistrationURI            string `json:"registration_uri,omitempty"`
	RegistrationS3Key          string `json:"registration_s3_key,omitempty"`
	VersionedRegistrationURI   string `json:"versioned_registration_uri,omitempty"`
	VersionedRegistrationS3Key string `json:"versioned_registration_s3_key,omitempty"`
	AuthorityModel             string `json:"authority_model,omitempty"`
	AnchorState                string `json:"anchor_state,omitempty"`
	PublishedAt                string `json:"published_at,omitempty"`
}

type soulMintConversationPromotionEvidence struct {
	AgentID                  string `json:"agent_id"`
	RegistrationID           string `json:"registration_id,omitempty"`
	Stage                    string `json:"stage,omitempty"`
	RequestStatus            string `json:"request_status,omitempty"`
	ReviewStatus             string `json:"review_status,omitempty"`
	ReadinessStatus          string `json:"readiness_status,omitempty"`
	AuthorityModel           string `json:"authority_model,omitempty"`
	AnchorState              string `json:"anchor_state,omitempty"`
	LatestConversationID     string `json:"latest_conversation_id,omitempty"`
	LatestConversationStatus string `json:"latest_conversation_status,omitempty"`
	PublishedVersion         int    `json:"published_version,omitempty"`
	GraduatedAt              string `json:"graduated_at,omitempty"`
}

type soulMintConversationFinalizeResponse struct {
	Version          string                                  `json:"version"`
	AgentID          string                                  `json:"agent_id"`
	Agent            models.SoulAgentIdentity                `json:"agent"`
	PublishedVersion int                                     `json:"published_version"`
	Publication      soulMintConversationPublicationEvidence `json:"publication"`
	Promotion        *soulMintConversationPromotionEvidence  `json:"promotion,omitempty"`
}

func (r soulMintConversationFinalizeResponse) MarshalJSON() ([]byte, error) {
	agentBytes, err := json.Marshal(r.Agent)
	if err != nil {
		return nil, err
	}
	var agent map[string]any
	if err := json.Unmarshal(agentBytes, &agent); err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.Agent.MintTxHash) == "" {
		delete(agent, "mint_tx_hash")
	}
	if r.Agent.MintedAt.IsZero() {
		delete(agent, "minted_at")
	}
	return json.Marshal(struct {
		Version          string                                  `json:"version"`
		AgentID          string                                  `json:"agent_id"`
		Agent            map[string]any                          `json:"agent"`
		PublishedVersion int                                     `json:"published_version"`
		Publication      soulMintConversationPublicationEvidence `json:"publication"`
		Promotion        *soulMintConversationPromotionEvidence  `json:"promotion,omitempty"`
	}{
		Version:          r.Version,
		AgentID:          r.AgentID,
		Agent:            agent,
		PublishedVersion: r.PublishedVersion,
		Publication:      r.Publication,
		Promotion:        r.Promotion,
	})
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
	session        *models.HostedGenesisSession
	agentIDHex     string
	conversationID string
	auditActor     string
}

type mintConversationDebitParams struct {
	instanceSlug string
	module       string
	target       string
	requestID    string
}

func (s *Server) requireMintConversationRegistrationContext(ctx *apptheory.Context, requirePacks bool) (mintConversationRegistrationContext, *apptheory.AppTheoryError) {
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
		return mintConversationRegistrationContext{}, newAppTheoryError("app.conflict", "soul registry bucket is not configured")
	}

	regID := strings.TrimSpace(ctx.Param("id"))
	if regID == "" {
		return mintConversationRegistrationContext{}, newAppTheoryError("app.bad_request", "registration id is required")
	}
	reg, err := s.getSoulAgentRegistration(ctx.Context(), regID)
	if err != nil {
		return mintConversationRegistrationContext{}, newAppTheoryError("app.not_found", "registration not found")
	}
	if !isOperator(ctx) && strings.TrimSpace(reg.Username) != strings.TrimSpace(ctx.AuthIdentity) {
		return mintConversationRegistrationContext{}, newAppTheoryError("app.forbidden", "forbidden")
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
		AuthorityModel:   soulIdentityAuthorityModel(identity),
		Capabilities:     append([]string(nil), identity.Capabilities...),
	}
}

func (s *Server) requireMintConversationAgentContext(ctx *apptheory.Context, requirePacks bool) (mintConversationAgentContext, *apptheory.AppTheoryError) {
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
		return mintConversationAgentContext{}, newAppTheoryError("app.conflict", "soul registry bucket is not configured")
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return mintConversationAgentContext{}, appErr
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return mintConversationAgentContext{}, newAppTheoryError("app.not_found", "agent not found")
	}
	if err != nil {
		return mintConversationAgentContext{}, newAppTheoryError("app.internal", "internal error")
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
		return req, "", newAppTheoryError("app.bad_request", "message is required")
	}
	if len(message) > 8192 {
		return req, "", newAppTheoryError("app.bad_request", "message is too long")
	}
	return req, message, nil
}

func requireMintConversationID(ctx *apptheory.Context) (string, *apptheory.AppTheoryError) {
	conversationID := strings.TrimSpace(ctx.Param("conversationId"))
	if conversationID == "" {
		return "", newAppTheoryError("app.bad_request", "conversationId is required")
	}
	return conversationID, nil
}

func (s *Server) listSoulAgentMintConversations(ctx context.Context, agentIDHex string, limit int) ([]*models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	var items []*models.SoulAgentMintConversation
	if err := s.store.DB.WithContext(ctx).
		Model(&models.SoulAgentMintConversation{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "MINT_CONVERSATION#").
		All(&items); err != nil && !theoryErrors.IsNotFound(err) {
		return nil, newAppTheoryError("app.internal", "failed to list mint conversations")
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

func (s *Server) loadMintConversationForCompletion(ctx context.Context, agentIDHex string, conversationID string) (*models.SoulAgentMintConversation, *apptheory.AppTheoryError) {
	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx, agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
	if err != nil {
		return nil, newAppTheoryError("app.not_found", "conversation not found")
	}
	decodeMintConversationFields(conv)
	return conv, nil
}

func mintConversationCompletionReplayReady(conv *models.SoulAgentMintConversation) (bool, string) {
	if conv == nil {
		return false, soulMintConversationCompleteReasonInvalidState
	}
	decodeMintConversationFields(conv)
	switch strings.TrimSpace(conv.Status) {
	case models.SoulMintConversationStatusCompleted, models.SoulMintConversationStatusDeclarationReady:
		present, valid := mintConversationProducedDeclarationsState(conv)
		if valid {
			return true, ""
		}
		if !present {
			return false, soulMintConversationCompleteReasonMissingDeclarations
		}
		return false, soulMintConversationCompleteReasonInvalidDeclarations
	case models.SoulMintConversationStatusInProgress, models.SoulMintConversationStatusAssistantTurnReady, models.SoulMintConversationStatusDeclarationExtractionPending:
		return false, ""
	default:
		return false, soulMintConversationCompleteReasonInvalidState
	}
}

func mintConversationProducedDeclarationsState(conv *models.SoulAgentMintConversation) (present bool, valid bool) {
	if conv == nil {
		return false, false
	}
	decodeMintConversationFields(conv)
	raw := strings.TrimSpace(conv.ProducedDeclarations)
	if raw == "" {
		return false, false
	}
	_, appErr := parseAndValidateMintConversationDeclarations(raw)
	return true, appErr == nil
}

func mintConversationCompletionConflictDetails(conv *models.SoulAgentMintConversation, reason string) map[string]any {
	status := stackDriftUnknown
	if conv != nil {
		decodeMintConversationFields(conv)
		if trimmed := strings.TrimSpace(conv.Status); trimmed != "" {
			status = trimmed
		}
	}
	present, valid := mintConversationProducedDeclarationsState(conv)
	return map[string]any{
		soulMintConversationCompleteDetailReason:              strings.TrimSpace(reason),
		soulMintConversationCompleteDetailStatus:              status,
		soulMintConversationCompleteDetailExpectedStatus:      models.SoulMintConversationStatusInProgress,
		soulMintConversationCompleteDetailDeclarationsPresent: present,
		soulMintConversationCompleteDetailDeclarationsValid:   valid,
	}
}

func mintConversationCompletionStateConflict(conv *models.SoulAgentMintConversation, reason string) *apptheory.AppTheoryError {
	return apptheory.NewAppTheoryError(appErrCodeConflict, soulMintConversationCompleteConflictMessage).
		WithStatusCode(http.StatusConflict).
		WithDetails(mintConversationCompletionConflictDetails(conv, reason))
}

func requireMintConversationReadyForFinalize(conv *models.SoulAgentMintConversation, statusMessage string, emptyDeclMessage string) *apptheory.AppTheoryError {
	if conv == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	decodeMintConversationFields(conv)
	status := strings.TrimSpace(conv.Status)
	if status != models.SoulMintConversationStatusCompleted && status != models.SoulMintConversationStatusDeclarationReady {
		return newAppTheoryError("app.conflict", statusMessage)
	}
	present, valid := mintConversationProducedDeclarationsState(conv)
	if !present {
		return newAppTheoryError("app.conflict", emptyDeclMessage)
	}
	if !valid {
		return newAppTheoryError("app.conflict", "conversation has invalid produced declarations")
	}
	return nil
}

func requireMintConversationDurableAssistantTurn(conv *models.SoulAgentMintConversation) *apptheory.AppTheoryError {
	if conv == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	ok, reason := mintConversationHasDurableAssistantTurn(conv)
	if ok {
		return nil
	}
	return newAppTheoryError(appErrCodeConflict, reason)
}

func mintConversationHasDurableAssistantTurn(conv *models.SoulAgentMintConversation) (bool, string) {
	if conv == nil {
		return false, "conversation has no durable messages"
	}
	decodeMintConversationFields(conv)
	rawMessages := strings.TrimSpace(conv.Messages)
	if rawMessages == "" {
		return false, "conversation has no durable messages"
	}

	var messages []soulMintConversationMessage
	if err := json.Unmarshal([]byte(rawMessages), &messages); err != nil {
		return false, "conversation messages are invalid"
	}
	for _, msg := range messages {
		if strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") && strings.TrimSpace(msg.Content) != "" {
			return true, ""
		}
	}
	return false, "conversation has no completed assistant turn"
}

func (s *Server) loadMintConversationFinalizeContext(ctx *apptheory.Context) (mintConversationFinalizeContext, *apptheory.AppTheoryError) {
	regCtx, appErr := s.requireMintConversationRegistrationContext(ctx, true)
	if appErr != nil {
		return mintConversationFinalizeContext{}, appErr
	}
	conversationID, appErr := requireMintConversationID(ctx)
	if appErr != nil {
		return mintConversationFinalizeContext{}, appErr
	}
	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), regCtx.agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
	if err != nil {
		return mintConversationFinalizeContext{}, newAppTheoryError("app.not_found", "conversation not found")
	}
	if appErr := requireMintConversationReadyForFinalize(conv, "conversation is not completed", "conversation has no produced declarations"); appErr != nil {
		return mintConversationFinalizeContext{}, appErr
	}
	identity, err := s.getSoulAgentIdentity(ctx.Context(), regCtx.agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return mintConversationFinalizeContext{}, newAppTheoryError("app.conflict", "registration is not yet verified")
	}
	if err != nil {
		return mintConversationFinalizeContext{}, newAppTheoryError("app.internal", "internal error")
	}
	if strings.TrimSpace(identity.PrincipalAddress) == "" ||
		strings.TrimSpace(identity.PrincipalSignature) == "" ||
		strings.TrimSpace(identity.PrincipalDeclaration) == "" ||
		strings.TrimSpace(identity.PrincipalDeclaredAt) == "" {
		return mintConversationFinalizeContext{}, newAppTheoryError("app.conflict", "principal declaration is missing; re-verify registration")
	}
	return mintConversationFinalizeContext{
		reg:            regCtx.reg,
		inst:           regCtx.inst,
		identity:       identity,
		conv:           conv,
		agentIDHex:     regCtx.agentIDHex,
		conversationID: conversationID,
		auditActor:     strings.TrimSpace(ctx.AuthIdentity),
	}, nil
}

func (s *Server) ensureMintConversationAgentNotPublished(ctx context.Context, agentIDHex string) *apptheory.AppTheoryError {
	identity, err := s.getSoulAgentIdentity(ctx, agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if identity != nil && identity.SelfDescriptionVersion > 0 {
		return newAppTheoryError("app.conflict", soulMintConversationAlreadyPublishedMessage)
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
) (creditsRequested int64, appErr *apptheory.AppTheoryError) {
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
		return 0, newAppTheoryError("app.conflict", "insufficient credits")
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
		return 0, newAppTheoryError("app.conflict", "insufficient credits")
	}
	if err != nil {
		return 0, newAppTheoryError("app.internal", "failed to debit credits")
	}

	return creditsRequested, nil
}

func validateMintConversationDebitParams(s *Server, inst *models.Instance, module string, target string, requestID string) (mintConversationDebitParams, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || inst == nil {
		return mintConversationDebitParams{}, newAppTheoryError("app.internal", "internal error")
	}
	instanceSlug := strings.ToLower(strings.TrimSpace(inst.Slug))
	module = strings.ToLower(strings.TrimSpace(module))
	if instanceSlug == "" || module == "" {
		return mintConversationDebitParams{}, newAppTheoryError("app.internal", "internal error")
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

func (s *Server) loadMintConversationBudget(ctx context.Context, instanceSlug string, month string) (models.InstanceBudgetMonth, *apptheory.AppTheoryError) {
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
			return models.InstanceBudgetMonth{}, newAppTheoryError("app.conflict", "credits are not configured for this instance; purchase credits first")
		}
		return models.InstanceBudgetMonth{}, newAppTheoryError("app.internal", "failed to load credits budget")
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
	return s.handleSoulMintConversationForRegistration(ctx, regCtx)
}

func (s *Server) handleSoulMintConversationForRegistration(ctx *apptheory.Context, regCtx mintConversationRegistrationContext) (*apptheory.Response, error) {
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
		return nil, newAppTheoryError("app.bad_request", "model is required")
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

	s.startMintConversationStream(ctx.Context(), eventCh, streamMintConversationParams{
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
		return nil, newAppTheoryError("app.conflict", soulMintConversationAlreadyPublishedMessage)
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
		return nil, newAppTheoryError("app.bad_request", "model is required")
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

	s.startMintConversationStream(ctx.Context(), eventCh, streamMintConversationParams{
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

func (s *Server) loadMintConversationSession(ctx context.Context, agentIDHex string, requestedConversationID string, requestedModel string) (mintConversationSession, *apptheory.AppTheoryError) {
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
			return mintConversationSession{}, newAppTheoryError("app.internal", "failed to create conversation id")
		}
		session.conversationID = token
		session.isNew = true
		return session, nil
	}

	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx, agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", session.conversationID))
	if err != nil {
		return mintConversationSession{}, newAppTheoryError("app.not_found", "conversation not found")
	}
	decodeMintConversationFields(conv)
	if conv.Status != models.SoulMintConversationStatusInProgress {
		return mintConversationSession{}, newAppTheoryError("app.conflict", "conversation is not in progress")
	}

	storedModel := strings.TrimSpace(conv.Model)
	if storedModel != "" {
		if session.modelSet != "" && !strings.EqualFold(storedModel, session.modelSet) {
			return mintConversationSession{}, newAppTheoryError("app.conflict", "cannot change model for an existing conversation")
		}
		session.modelSet = storedModel
	}
	if strings.TrimSpace(conv.Messages) != "" {
		_ = json.Unmarshal([]byte(conv.Messages), &session.existingMessages)
	}
	session.existingUsage = conv.Usage
	return session, nil
}

func (s *Server) debitMintConversationStreamCredits(ctx context.Context, inst *models.Instance, agentIDHex string, session mintConversationSession, requestID string, now time.Time) *apptheory.AppTheoryError {
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

func (s *Server) startMintConversationStream(ctx context.Context, eventCh chan<- apptheory.SSEEvent, p streamMintConversationParams) {
	streamer := func(runCtx context.Context, ch chan<- apptheory.SSEEvent, params streamMintConversationParams) {
		s.streamMintConversation(runCtx, ch, params)
	}
	if s != nil && s.mintConversationStreamer != nil {
		streamer = s.mintConversationStreamer
	}
	go streamer(ctx, eventCh, p)
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
		log.Printf("controlplane: update mint conversation messages failed: agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(conv.AgentID), soulMintInstanceReadAuditHash(conv.ConversationID), err)
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
		log.Printf("controlplane: update mint conversation turn failed: agent_hash=%s conversation_hash=%s err=%v", soulMintInstanceReadAuditHash(conv.AgentID), soulMintInstanceReadAuditHash(conv.ConversationID), err)
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
		log.Printf("controlplane: update mint conversation status failed: agent_hash=%s conversation_hash=%s status=%s err=%v", soulMintInstanceReadAuditHash(conv.AgentID), soulMintInstanceReadAuditHash(conv.ConversationID), conv.Status, err)
	}
}

// handleSoulCompleteMintConversation marks a legacy conversation as completed
// and extracts declarations. The Hosted Genesis instance-key path is projected
// through handleSoulInstanceCompleteMintConversation; after Project 48 M11 that
// path is a polling/finalize gate and no longer decides final affirmation or
// starts Host-side extraction for ordinary actor turns.
func (s *Server) handleSoulCompleteMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	regCtx, appErr := s.requireMintConversationRegistrationContext(ctx, false)
	if appErr != nil {
		return nil, appErr
	}
	conversationID, appErr := requireMintConversationID(ctx)
	if appErr != nil {
		return nil, appErr
	}
	publishGuardErr := s.ensureMintConversationAgentNotPublished(ctx.Context(), regCtx.agentIDHex)
	conv, appErr := s.loadMintConversationForCompletion(ctx.Context(), regCtx.agentIDHex, conversationID)
	if appErr != nil {
		if publishGuardErr != nil {
			return nil, publishGuardErr
		}
		return nil, appErr
	}
	if replayReady, reason := mintConversationCompletionReplayReady(conv); replayReady {
		return apptheory.JSON(http.StatusOK, conv)
	} else if reason != "" {
		return nil, mintConversationCompletionStateConflict(conv, reason)
	}
	if publishGuardErr != nil {
		return nil, publishGuardErr
	}

	return s.completeSoulMintConversationForRegistration(ctx, regCtx, conv, conversationID)
}

func (s *Server) handleSoulAgentCompleteMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentCtx, appErr := s.requireMintConversationAgentContext(ctx, false)
	if appErr != nil {
		return nil, appErr
	}
	var publishGuardErr *apptheory.AppTheoryError
	if agentCtx.identity != nil && agentCtx.identity.SelfDescriptionVersion > 0 {
		publishGuardErr = newAppTheoryError("app.conflict", soulMintConversationAlreadyPublishedMessage)
	}
	conversationID, appErr := requireMintConversationID(ctx)
	if appErr != nil {
		return nil, appErr
	}
	conv, appErr := s.loadMintConversationForCompletion(ctx.Context(), agentCtx.agentIDHex, conversationID)
	if appErr != nil {
		if publishGuardErr != nil {
			return nil, publishGuardErr
		}
		return nil, appErr
	}
	if replayReady, reason := mintConversationCompletionReplayReady(conv); replayReady {
		return apptheory.JSON(http.StatusOK, conv)
	} else if reason != "" {
		return nil, mintConversationCompletionStateConflict(conv, reason)
	}
	if publishGuardErr != nil {
		return nil, publishGuardErr
	}

	return s.completeSoulMintConversationForRegistration(ctx, mintConversationRegistrationContext{
		reg:        agentCtx.reg,
		inst:       agentCtx.inst,
		agentIDHex: agentCtx.agentIDHex,
	}, conv, conversationID)
}

// handleSoulGetMintConversation retrieves a mint conversation record.
func (s *Server) handleSoulGetMintConversation(ctx *apptheory.Context) (*apptheory.Response, error) {
	regCtx, appErr := s.requireMintConversationRegistrationContext(ctx, false)
	if appErr != nil {
		return nil, appErr
	}

	conversationID := strings.TrimSpace(ctx.Param("conversationId"))
	if conversationID == "" {
		return nil, newAppTheoryError("app.bad_request", "conversationId is required")
	}

	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), regCtx.agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
	if err != nil {
		return nil, newAppTheoryError("app.not_found", "conversation not found")
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
		return nil, newAppTheoryError("app.not_found", "conversation not found")
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
	return s.beginFinalizeMintConversation(ctx, finalizeCtx)
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
	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), agentCtx.agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
	if err != nil {
		return nil, newAppTheoryError("app.not_found", "conversation not found")
	}
	if appErr := requireMintConversationReadyForFinalize(conv, "conversation is not completed", "conversation has no produced declarations"); appErr != nil {
		return nil, appErr
	}
	return s.beginFinalizeMintConversation(ctx, mintConversationFinalizeContext{
		reg:            agentCtx.reg,
		inst:           agentCtx.inst,
		identity:       agentCtx.identity,
		conv:           conv,
		agentIDHex:     agentCtx.agentIDHex,
		conversationID: conversationID,
		auditActor:     strings.TrimSpace(ctx.AuthIdentity),
	})
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
	return s.finalizeMintConversation(ctx, finalizeCtx)
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
	conv, err := getSoulAgentItemBySK[models.SoulAgentMintConversation](s, ctx.Context(), agentCtx.agentIDHex, fmt.Sprintf("MINT_CONVERSATION#%s", conversationID))
	if err != nil {
		return nil, newAppTheoryError("app.not_found", "conversation not found")
	}
	if appErr := requireMintConversationReadyForFinalize(conv, "conversation is not completed", "conversation has no produced declarations"); appErr != nil {
		return nil, appErr
	}

	finalizeCtx := mintConversationFinalizeContext{
		reg:            agentCtx.reg,
		inst:           agentCtx.inst,
		identity:       agentCtx.identity,
		conv:           conv,
		agentIDHex:     agentCtx.agentIDHex,
		conversationID: conversationID,
		auditActor:     strings.TrimSpace(ctx.AuthIdentity),
	}
	return s.finalizeMintConversation(ctx, finalizeCtx)
}

func (s *Server) beginFinalizeMintConversation(ctx *apptheory.Context, finalizeCtx mintConversationFinalizeContext) (*apptheory.Response, error) {
	if finalizeCtx.identity == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if appErr := requireHostedGenesisFinalizeDeclarationsMatchSession(finalizeCtx.session, finalizeCtx.conv); appErr != nil {
		return nil, appErr
	}
	if finalizeCtx.identity.SelfDescriptionVersion > 0 {
		return nil, newAppTheoryError("app.conflict", soulMintConversationAlreadyPublishedMessage)
	}
	if activeErr := requireMintConversationFinalizeActiveIdentity(finalizeCtx.identity); activeErr != nil {
		return nil, activeErr
	}
	if isExplicitInstanceTrustAuthority(finalizeCtx.reg, finalizeCtx.identity) {
		return s.beginFinalizeMintConversationInstanceTrust(ctx, finalizeCtx)
	}
	if strings.TrimSpace(finalizeCtx.identity.PrincipalAddress) == "" ||
		strings.TrimSpace(finalizeCtx.identity.PrincipalSignature) == "" ||
		strings.TrimSpace(finalizeCtx.identity.PrincipalDeclaration) == "" ||
		strings.TrimSpace(finalizeCtx.identity.PrincipalDeclaredAt) == "" {
		return nil, newAppTheoryError("app.conflict", "principal declaration is missing; re-verify registration")
	}
	publishIdentity := mintConversationFinalizeIdentityForPublication(finalizeCtx.identity)
	req, err := parseMintConversationFinalizeBeginRequestBody(ctx)
	if err != nil {
		return nil, err
	}

	decl, appErr := mintConversationFinalizeDeclarationsForContext(finalizeCtx)
	if appErr != nil {
		return nil, appErr
	}
	if verifyErr := verifyMintConversationBoundarySignatures(finalizeCtx.identity.Wallet, decl.Boundaries, req.BoundarySignatures); verifyErr != nil {
		return nil, verifyErr
	}

	now := time.Now().UTC()
	expectedVersion := finalizeCtx.identity.SelfDescriptionVersion
	nextVersion := expectedVersion + 1

	regMap, _, digest, _, _, appErr := s.buildMintConversationFinalizeV2Registration(finalizeCtx.agentIDHex, publishIdentity, decl, req.BoundarySignatures, now, nextVersion, "0x00")
	if appErr != nil {
		return nil, appErr
	}
	return s.respondMintConversationFinalizePreflight(publishIdentity, decl, req.BoundarySignatures, regMap, digest, now, expectedVersion, nextVersion)
}

func (s *Server) finalizeMintConversation(ctx *apptheory.Context, finalizeCtx mintConversationFinalizeContext) (*apptheory.Response, error) {
	if finalizeCtx.identity == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	if appErr := requireHostedGenesisFinalizeDeclarationsMatchSession(finalizeCtx.session, finalizeCtx.conv); appErr != nil {
		return nil, appErr
	}
	if isExplicitInstanceTrustAuthority(finalizeCtx.reg, finalizeCtx.identity) {
		return s.finalizeMintConversationInstanceTrust(ctx, finalizeCtx)
	}
	if strings.TrimSpace(finalizeCtx.identity.PrincipalAddress) == "" ||
		strings.TrimSpace(finalizeCtx.identity.PrincipalSignature) == "" ||
		strings.TrimSpace(finalizeCtx.identity.PrincipalDeclaration) == "" ||
		strings.TrimSpace(finalizeCtx.identity.PrincipalDeclaredAt) == "" {
		return nil, newAppTheoryError("app.conflict", "principal declaration is missing; re-verify registration")
	}
	req, issuedAt, expectedVersion, selfSig, err := parseMintConversationFinalizeRequestBody(ctx)
	if err != nil {
		return nil, err
	}

	nextVersion := *expectedVersion + 1
	if versionErr := requireMintConversationFinalizeVersionAndActive(finalizeCtx.identity, nextVersion, *expectedVersion); versionErr != nil {
		return nil, versionErr
	}
	publishIdentity := mintConversationFinalizeIdentityForPublication(finalizeCtx.identity)
	finalizeCtx.identity = publishIdentity

	decl, appErr := mintConversationFinalizeDeclarationsForContext(finalizeCtx)
	if appErr != nil {
		return nil, appErr
	}
	if verifyErr := verifyMintConversationBoundarySignatures(finalizeCtx.identity.Wallet, decl.Boundaries, req.BoundarySignatures); verifyErr != nil {
		return nil, verifyErr
	}

	regMap, regV2, digest, capsNorm, claimLevels, appErr := s.buildMintConversationFinalizeV2Registration(finalizeCtx.agentIDHex, publishIdentity, decl, req.BoundarySignatures, issuedAt.UTC(), nextVersion, selfSig)
	if appErr != nil {
		return nil, appErr
	}
	if sigErr := verifyEthereumSignatureBytes(finalizeCtx.identity.Wallet, digest, selfSig); sigErr != nil {
		return nil, newAppTheoryError("app.bad_request", "invalid registration signature")
	}
	return s.finalizeMintConversationPublish(ctx, finalizeCtx, regV2, regMap, decl, req.BoundarySignatures, capsNorm, claimLevels, issuedAt, expectedVersion, selfSig)
}

func (s *Server) beginFinalizeMintConversationInstanceTrust(ctx *apptheory.Context, finalizeCtx mintConversationFinalizeContext) (*apptheory.Response, error) {
	if appErr := requireExplicitInstanceTrustAuthority(finalizeCtx.reg, finalizeCtx.identity); appErr != nil {
		return nil, appErr
	}
	publishIdentity := mintConversationFinalizeIdentityForPublication(finalizeCtx.identity)
	req, err := parseMintConversationFinalizeBeginRequestBodyOptional(ctx)
	if err != nil {
		return nil, err
	}

	decl, appErr := mintConversationFinalizeDeclarationsForContext(finalizeCtx)
	if appErr != nil {
		return nil, appErr
	}

	now := time.Now().UTC()
	expectedVersion := finalizeCtx.identity.SelfDescriptionVersion
	nextVersion := expectedVersion + 1
	regMap, _, _, _, _, appErr := s.buildMintConversationFinalizeV2RegistrationWithOptions(finalizeCtx.agentIDHex, publishIdentity, decl, req.BoundarySignatures, now, nextVersion, "", mintConversationFinalizeRegistrationOptions{
		AuthorityModel:    models.SoulAuthorityModelInstanceTrust,
		AnchorState:       models.SoulAnchorStateHostedOffchain,
		IncludeSignatures: false,
	})
	if appErr != nil {
		return nil, appErr
	}
	return s.respondMintConversationFinalizePreflightInstanceTrust(decl, req.BoundarySignatures, regMap, now, expectedVersion, nextVersion)
}

func (s *Server) finalizeMintConversationInstanceTrust(ctx *apptheory.Context, finalizeCtx mintConversationFinalizeContext) (*apptheory.Response, error) {
	if appErr := requireExplicitInstanceTrustAuthority(finalizeCtx.reg, finalizeCtx.identity); appErr != nil {
		return nil, appErr
	}
	req, issuedAt, expectedVersion, err := parseMintConversationFinalizeInstanceTrustRequestBody(ctx, finalizeCtx.identity.SelfDescriptionVersion)
	if err != nil {
		return nil, err
	}

	nextVersion := *expectedVersion + 1
	if versionErr := requireMintConversationFinalizeVersionAndActive(finalizeCtx.identity, nextVersion, *expectedVersion); versionErr != nil {
		return nil, versionErr
	}
	publishIdentity := mintConversationFinalizeIdentityForPublication(finalizeCtx.identity)
	finalizeCtx.identity = publishIdentity

	decl, appErr := mintConversationFinalizeDeclarationsForContext(finalizeCtx)
	if appErr != nil {
		return nil, appErr
	}

	regMap, regV2, _, capsNorm, claimLevels, appErr := s.buildMintConversationFinalizeV2RegistrationWithOptions(finalizeCtx.agentIDHex, publishIdentity, decl, req.BoundarySignatures, issuedAt.UTC(), nextVersion, "", mintConversationFinalizeRegistrationOptions{
		AuthorityModel:    models.SoulAuthorityModelInstanceTrust,
		AnchorState:       models.SoulAnchorStateHostedOffchain,
		IncludeSignatures: false,
	})
	if appErr != nil {
		return nil, appErr
	}
	return s.finalizeMintConversationPublish(ctx, finalizeCtx, regV2, regMap, decl, req.BoundarySignatures, capsNorm, claimLevels, issuedAt, expectedVersion, "")
}

func mintConversationFinalizeDeclarationsForContext(finalizeCtx mintConversationFinalizeContext) (soulMintConversationProducedDeclarations, *apptheory.AppTheoryError) {
	if finalizeCtx.conv == nil {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.conflict", "conversation has no produced declarations")
	}
	decl, appErr := parseAndValidateMintConversationDeclarations(finalizeCtx.conv.ProducedDeclarations)
	if appErr != nil {
		return soulMintConversationProducedDeclarations{}, appErr
	}
	decl.Capabilities = hostedgenesis.NormalizeProducedCapabilities(decl.Capabilities)
	instanceTrust := isExplicitInstanceTrustAuthority(finalizeCtx.reg, finalizeCtx.identity)
	if !instanceTrust {
		decl.Capabilities = hostedgenesis.MergeDeclaredCapabilities(decl.Capabilities, mintConversationFinalizeDeclaredCapabilities(finalizeCtx))
	}
	if !instanceTrust && len(decl.Capabilities) == 0 {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.conflict", string(hostedgenesis.DeclarationCodeCapabilities))
	}
	if len(decl.Boundaries) == 0 {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.conflict", string(hostedgenesis.DeclarationCodeBoundaries))
	}
	return decl, nil
}

func mintConversationFinalizeDeclaredCapabilities(finalizeCtx mintConversationFinalizeContext) []string {
	if finalizeCtx.reg != nil && len(finalizeCtx.reg.Capabilities) > 0 {
		return append([]string(nil), finalizeCtx.reg.Capabilities...)
	}
	if finalizeCtx.identity != nil {
		return append([]string(nil), finalizeCtx.identity.Capabilities...)
	}
	return nil
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
		AuthorityModel:      models.SoulAuthorityModelWalletPrincipal,
		AnchorState:         soulIdentityAnchorState(identity),
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
		SelfAttestationSigning: &soulMintConversationFinalizeSigningInput{
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

func (s *Server) respondMintConversationFinalizePreflightInstanceTrust(
	decl soulMintConversationProducedDeclarations,
	boundarySignatures map[string]string,
	regMap map[string]any,
	issuedAt time.Time,
	expectedVersion int,
	nextVersion int,
) (*apptheory.Response, error) {
	issuedAtStr := issuedAt.UTC().Format(time.RFC3339Nano)
	return apptheory.JSON(http.StatusOK, soulMintConversationFinalizeBeginResponse{
		Version:             "1",
		AuthorityModel:      models.SoulAuthorityModelInstanceTrust,
		AnchorState:         models.SoulAnchorStateHostedOffchain,
		IssuedAt:            issuedAtStr,
		ExpectedVersion:     expectedVersion,
		NextVersion:         nextVersion,
		DeclarationsPreview: decl,
		BoundaryRequirements: buildMintConversationFinalizeHostedBoundaryRequirements(
			decl.Boundaries,
			boundarySignatures,
		),
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

func buildMintConversationFinalizeHostedBoundaryRequirements(
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
			SigningMethod:   "instance_trust",
			MessageEncoding: "none",
			Message:         strings.TrimSpace(b.Statement),
			DigestHex:       "",
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

func buildMintConversationFinalizeCanonicalJSON(regMap map[string]any) (string, *apptheory.AppTheoryError) {
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
		return "", newAppTheoryError("app.bad_request", "invalid registration JSON")
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

func soulIdentityAnchorState(identity *models.SoulAgentIdentity) string {
	if identity == nil {
		return models.SoulAnchorStateHostedOffchain
	}
	switch strings.ToLower(strings.TrimSpace(identity.AnchorState)) {
	case models.SoulAnchorStateImmutableOnchain:
		return models.SoulAnchorStateImmutableOnchain
	default:
		return models.SoulAnchorStateHostedOffchain
	}
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
) (string, models.AIUsage, *apptheory.AppTheoryError) {
	declarationsJSON := parseMintConversationCompleteDeclarations(ctx)
	if declarationsJSON != "" {
		decl, appErr := parseAndValidateMintConversationDeclarations(declarationsJSON)
		if appErr != nil {
			return "", models.AIUsage{}, appErr
		}
		if !isRegistrationInstanceTrust(regCtx.reg) && len(decl.Capabilities) == 0 {
			return "", models.AIUsage{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeCapabilities))
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
		return "", models.AIUsage{}, newAppTheoryError("app.internal", "failed to serialize declarations")
	}
	return strings.TrimSpace(string(b)), usage, nil
}

func (s *Server) persistCompletedMintConversation(ctx context.Context, conv *models.SoulAgentMintConversation, declarationsJSON string, extractUsage models.AIUsage, now time.Time) *apptheory.AppTheoryError {
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
		return newAppTheoryError("app.internal", "failed to complete conversation")
	}
	return nil
}

func encodeMintConversationBlob(raw string) string {
	return models.EncodeSoulMintConversationBlob(raw)
}

func decodeMintConversationBlob(raw string) string {
	return models.DecodeSoulMintConversationBlob(raw)
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
		return req, newAppTheoryError("app.bad_request", "boundary_signatures is required")
	}
	return req, nil
}

func parseMintConversationFinalizeBeginRequestBodyOptional(ctx *apptheory.Context) (soulMintConversationFinalizeBeginRequest, error) {
	var req soulMintConversationFinalizeBeginRequest
	if ctx != nil && len(bytes.TrimSpace(ctx.Request.Body)) > 0 {
		if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
			return req, parseErr
		}
	}
	if req.BoundarySignatures == nil {
		req.BoundarySignatures = map[string]string{}
	}
	return req, nil
}

func parseMintConversationFinalizeRequestBody(ctx *apptheory.Context) (soulMintConversationFinalizeRequest, time.Time, *int, string, error) {
	var req soulMintConversationFinalizeRequest
	if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
		return req, time.Time{}, nil, "", parseErr
	}
	if len(req.BoundarySignatures) == 0 {
		return req, time.Time{}, nil, "", newAppTheoryError("app.bad_request", "boundary_signatures is required")
	}
	issuedAtRaw := strings.TrimSpace(req.IssuedAt)
	if issuedAtRaw == "" {
		return req, time.Time{}, nil, "", newAppTheoryError("app.bad_request", "issued_at is required")
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, issuedAtRaw)
	if err != nil {
		issuedAt, err = time.Parse(time.RFC3339, issuedAtRaw)
	}
	if err != nil {
		return req, time.Time{}, nil, "", newAppTheoryError("app.bad_request", "issued_at must be an RFC3339 timestamp")
	}
	if req.ExpectedVersion == nil {
		return req, time.Time{}, nil, "", newAppTheoryError("app.bad_request", "expected_version is required")
	}
	if *req.ExpectedVersion < 0 {
		return req, time.Time{}, nil, "", newAppTheoryError("app.bad_request", "expected_version is invalid")
	}
	selfSig := strings.TrimSpace(req.SelfAttestation)
	if selfSig == "" {
		return req, time.Time{}, nil, "", newAppTheoryError("app.bad_request", "self_attestation is required")
	}
	return req, issuedAt, req.ExpectedVersion, selfSig, nil
}

func parseMintConversationFinalizeInstanceTrustRequestBody(ctx *apptheory.Context, currentVersion int) (soulMintConversationFinalizeRequest, time.Time, *int, error) {
	var req soulMintConversationFinalizeRequest
	if ctx != nil && len(bytes.TrimSpace(ctx.Request.Body)) > 0 {
		if parseErr := httpx.ParseJSON(ctx, &req); parseErr != nil {
			return req, time.Time{}, nil, parseErr
		}
	}
	if req.BoundarySignatures == nil {
		req.BoundarySignatures = map[string]string{}
	}
	if strings.TrimSpace(req.SelfAttestation) != "" {
		return req, time.Time{}, nil, newAppTheoryError("app.bad_request", "self_attestation must be omitted for authority_model=instance_trust")
	}

	issuedAt := time.Now().UTC()
	issuedAtRaw := strings.TrimSpace(req.IssuedAt)
	if issuedAtRaw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, issuedAtRaw)
		if err != nil {
			parsed, err = time.Parse(time.RFC3339, issuedAtRaw)
		}
		if err != nil {
			return req, time.Time{}, nil, newAppTheoryError("app.bad_request", "issued_at must be an RFC3339 timestamp")
		}
		issuedAt = parsed.UTC()
	}

	if req.ExpectedVersion == nil {
		expected := currentVersion
		req.ExpectedVersion = &expected
	}
	if *req.ExpectedVersion < 0 {
		return req, time.Time{}, nil, newAppTheoryError("app.bad_request", "expected_version is invalid")
	}
	return req, issuedAt, req.ExpectedVersion, nil
}

func verifyMintConversationBoundarySignatures(wallet string, boundaries []soul.BoundaryV2, signatures map[string]string) *apptheory.AppTheoryError {
	for i := range boundaries {
		b := boundaries[i]
		sig := strings.TrimSpace(signatures[strings.TrimSpace(b.ID)])
		if sig == "" {
			return newAppTheoryError("app.bad_request", "missing boundary signature for "+strings.TrimSpace(b.ID))
		}
		statementDigest := crypto.Keccak256([]byte(strings.TrimSpace(b.Statement)))
		if sigErr := verifyEthereumSignatureBytes(wallet, statementDigest, sig); sigErr != nil {
			return newAppTheoryError("app.bad_request", "invalid boundary signature for "+strings.TrimSpace(b.ID))
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
		return nil, newAppTheoryError("app.bad_request", "invalid registration JSON")
	}
	sum := sha256.Sum256(regBytes)
	regSHA256 := hex.EncodeToString(sum[:])
	changeSummary := extractStringField(regMap, "changeSummary")
	bounds := buildMintConversationBoundaryModels(finalizeCtx.agentIDHex, decl.Boundaries, boundarySignatures, issuedAt, *expectedVersion+1)

	now := time.Now().UTC()
	ensMaterial, appErr := s.prepareMintConversationManagedENSMaterial(ctx.Context(), finalizeCtx.identity, finalizeCtx.inst, regV2, now)
	if appErr != nil {
		return nil, appErr
	}
	publishedVersion, pubErr := s.publishSoulAgentRegistrationV2(ctx.Context(), finalizeCtx.agentIDHex, finalizeCtx.identity, regV2, regBytes, regSHA256, selfSig, changeSummary, capsNorm, claimLevels, expectedVersion, now)
	if pubErr != nil {
		return nil, pubErr
	}
	if appErr := s.persistMintConversationManagedENSMaterial(ctx.Context(), ensMaterial); appErr != nil {
		return nil, appErr
	}
	if appErr := s.persistMintConversationBoundaries(ctx.Context(), finalizeCtx.identity, bounds); appErr != nil {
		return nil, appErr
	}
	auditActor := strings.TrimSpace(finalizeCtx.auditActor)
	if auditActor == "" {
		auditActor = strings.TrimSpace(ctx.AuthIdentity)
	}
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     auditActor,
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
		AnchorState:    soulAgentPromotionAnchorState(promotion),
		OccurredAt:     now,
	})); appErr != nil {
		return nil, appErr
	}
	promotionEvidence := buildMintConversationPromotionEvidence(promotion)
	publication := s.buildMintConversationPublicationEvidence(finalizeCtx.agentIDHex, publishedVersion, soulPromotionAuthorityModel(promotion), soulAgentPromotionAnchorState(promotion), now)
	return apptheory.JSON(http.StatusOK, soulMintConversationFinalizeResponse{
		Version:          "1",
		AgentID:          finalizeCtx.agentIDHex,
		Agent:            *finalizeCtx.identity,
		PublishedVersion: publishedVersion,
		Publication:      publication,
		Promotion:        promotionEvidence,
	})
}

func buildMintConversationPromotionEvidence(promotion *models.SoulAgentPromotion) *soulMintConversationPromotionEvidence {
	if promotion == nil {
		return nil
	}
	out := &soulMintConversationPromotionEvidence{
		AgentID:                  strings.TrimSpace(promotion.AgentID),
		RegistrationID:           strings.TrimSpace(promotion.RegistrationID),
		Stage:                    strings.TrimSpace(promotion.Stage),
		RequestStatus:            strings.TrimSpace(promotion.RequestStatus),
		ReviewStatus:             strings.TrimSpace(promotion.ReviewStatus),
		ReadinessStatus:          strings.TrimSpace(promotion.ReadinessStatus),
		AuthorityModel:           soulPromotionAuthorityModel(promotion),
		AnchorState:              soulAgentPromotionAnchorState(promotion),
		LatestConversationID:     strings.TrimSpace(promotion.LatestConversationID),
		LatestConversationStatus: strings.TrimSpace(promotion.LatestConversationStatus),
		PublishedVersion:         promotion.PublishedVersion,
	}
	if !promotion.GraduatedAt.IsZero() {
		out.GraduatedAt = promotion.GraduatedAt.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func (s *Server) buildMintConversationPublicationEvidence(agentIDHex string, publishedVersion int, authorityModel string, anchorState string, publishedAt time.Time) soulMintConversationPublicationEvidence {
	agentIDHex = strings.ToLower(strings.TrimSpace(agentIDHex))
	currentKey := soulRegistrationS3Key(agentIDHex)
	versionedKey := soulRegistrationVersionedS3Key(agentIDHex, publishedVersion)
	bucket := ""
	if s != nil {
		bucket = strings.TrimSpace(s.cfg.SoulPackBucketName)
	}
	registrationURI := ""
	versionedURI := ""
	if bucket != "" {
		registrationURI = fmt.Sprintf("s3://%s/%s", bucket, currentKey)
		versionedURI = fmt.Sprintf("s3://%s/%s", bucket, versionedKey)
	}
	return soulMintConversationPublicationEvidence{
		AgentID:                    agentIDHex,
		PublishedVersion:           publishedVersion,
		RegistrationURI:            registrationURI,
		RegistrationS3Key:          currentKey,
		VersionedRegistrationURI:   versionedURI,
		VersionedRegistrationS3Key: versionedKey,
		AuthorityModel:             normalizeSoulAuthorityModel(authorityModel),
		AnchorState:                strings.TrimSpace(anchorState),
		PublishedAt:                publishedAt.UTC().Format(time.RFC3339Nano),
	}
}

type mintConversationManagedENSMaterial struct {
	ensName    string
	channel    *models.SoulAgentChannel
	resolution *models.SoulAgentENSResolution
	existing   *models.SoulAgentChannel
}

func (s *Server) prepareMintConversationManagedENSMaterial(
	ctx context.Context,
	identity *models.SoulAgentIdentity,
	inst *models.Instance,
	regV2 *soul.RegistrationFileV2,
	now time.Time,
) (*mintConversationManagedENSMaterial, *apptheory.AppTheoryError) {
	if s == nil || identity == nil || inst == nil || regV2 == nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}
	canonicalLocal, err := soul.ValidateManagedHandle(identity.LocalID)
	if err != nil || canonicalLocal == "" {
		return nil, newAppTheoryError("app.conflict", "agent local id is invalid")
	}
	instanceSlug, err := soul.ValidateManagedInstanceSlug(inst.Slug)
	if err != nil {
		return nil, newAppTheoryError("app.conflict", "agent domain instance slug is invalid")
	}
	ensName, err := soul.ManagedENSName(canonicalLocal, instanceSlug)
	if err != nil {
		return nil, newAppTheoryError("app.conflict", "managed ens name is invalid")
	}

	channel := &models.SoulAgentChannel{
		AgentID:            strings.TrimSpace(identity.AgentID),
		ChannelType:        models.SoulChannelTypeENS,
		Identifier:         ensName,
		ENSResolverAddress: strings.TrimSpace(s.cfg.ENSGatewayResolverAddress),
		ENSChain:           strings.TrimSpace(s.provisionENSChainName()),
		Verified:           true,
		VerifiedAt:         now.UTC(),
		ProvisionedAt:      now.UTC(),
		Status:             models.SoulChannelStatusActive,
		UpdatedAt:          now.UTC(),
	}
	_ = channel.UpdateKeys()

	resolution := &models.SoulAgentENSResolution{
		ENSName:             ensName,
		AgentID:             strings.TrimSpace(identity.AgentID),
		Wallet:              strings.TrimSpace(identity.Wallet),
		LocalID:             strings.TrimSpace(identity.LocalID),
		Domain:              strings.TrimSpace(identity.Domain),
		SoulRegistrationURI: s.currentSoulRegistrationURI(identity.AgentID),
		MCPEndpoint:         strings.TrimSpace(regV2.Endpoints.MCP),
		ActivityPubURI:      strings.TrimSpace(regV2.Endpoints.ActivityPub),
		Description:         strings.TrimSpace(regV2.SelfDescription.Purpose),
		Status:              firstNonEmpty(regV2.Lifecycle.Status, identity.LifecycleStatus, identity.Status),
		UpdatedAt:           now.UTC(),
	}
	if createdAt, ok := parseRFC3339Loose(regV2.Created); ok {
		resolution.CreatedAt = createdAt
	}
	if resolution.CreatedAt.IsZero() {
		resolution.CreatedAt = now.UTC()
	}
	_ = resolution.UpdateKeys()

	if appErr := s.preflightSoulENSResolutionAssignable(ctx, resolution); appErr != nil {
		return nil, appErr
	}
	existing, appErr := s.loadExistingSoulChannel(ctx, strings.TrimSpace(identity.AgentID), models.SoulChannelTypeENS)
	if appErr != nil {
		return nil, appErr
	}
	return &mintConversationManagedENSMaterial{
		ensName:    ensName,
		channel:    channel,
		resolution: resolution,
		existing:   existing,
	}, nil
}

func (s *Server) persistMintConversationManagedENSMaterial(ctx context.Context, material *mintConversationManagedENSMaterial) *apptheory.AppTheoryError {
	if s == nil || material == nil || material.channel == nil || material.resolution == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	if material.existing != nil && strings.TrimSpace(material.existing.Identifier) != "" && !strings.EqualFold(strings.TrimSpace(material.existing.Identifier), material.ensName) {
		oldResolution := &models.SoulAgentENSResolution{ENSName: material.existing.Identifier, AgentID: material.channel.AgentID}
		_ = oldResolution.UpdateKeys()
		if err := s.deleteSoulENSResolutionIfOwned(ctx, oldResolution); err != nil {
			return newAppTheoryError("app.internal", "failed to delete ens resolution")
		}
	}
	if err := s.store.DB.WithContext(ctx).Model(material.channel).CreateOrUpdate(); err != nil {
		return newAppTheoryError("app.internal", "failed to update channel")
	}
	if appErr := s.ensureSoulENSResolution(ctx, material.resolution); appErr != nil {
		return appErr
	}
	return nil
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

func (s *Server) persistMintConversationBoundaries(ctx context.Context, identity *models.SoulAgentIdentity, bounds []*models.SoulAgentBoundary) *apptheory.AppTheoryError {
	for _, b := range bounds {
		if b == nil {
			continue
		}
		if err := s.store.DB.WithContext(ctx).Model(b).IfNotExists().Create(); err != nil {
			if theoryErrors.IsConditionFailed(err) {
				s.tryWriteSoulBoundaryKeywordIndexForBoundary(ctx, identity, b)
				continue
			}
			return newAppTheoryError("app.internal", "failed to persist boundaries")
		}
		s.tryWriteSoulBoundaryKeywordIndexForBoundary(ctx, identity, b)
	}
	return nil
}

// buildMintConversationSystemPrompt delegates to the shared
// hostedgenesis/mintprompt builder so the control-plane synchronous path and the
// in-VM MicroVM workload prompt the provider identically for a given
// registration (drift-free).
func buildMintConversationSystemPrompt(reg *models.SoulAgentRegistration) string {
	return mintprompt.MintConversationSystemPrompt(reg)
}

func (s *Server) apiKeyForMintConversationModel(ctx context.Context, modelSet string) (string, *apptheory.AppTheoryError) {
	modelSetNorm := strings.ToLower(strings.TrimSpace(modelSet))
	switch {
	case strings.HasPrefix(modelSetNorm, "openai:"):
		if k := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); k != "" {
			return k, nil
		}
		k, err := secrets.OpenAIServiceKey(ctx, nil)
		if err != nil {
			return "", newAppTheoryError("app.internal", "LLM provider not configured")
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
			return "", newAppTheoryError("app.internal", "LLM provider not configured")
		}
		return k, nil
	default:
		return "", newAppTheoryError("app.bad_request", mintConversationUnsupportedModelSetMessage)
	}
}

func (s *Server) extractMintConversationDeclarations(ctx context.Context, reg *models.SoulAgentRegistration, conv *models.SoulAgentMintConversation, now time.Time) (soulMintConversationProducedDeclarations, models.AIUsage, *apptheory.AppTheoryError) {
	if s == nil || reg == nil || conv == nil {
		return soulMintConversationProducedDeclarations{}, models.AIUsage{}, newAppTheoryError("app.internal", "internal error")
	}

	modelSet := strings.TrimSpace(conv.Model)
	if modelSet == "" {
		return soulMintConversationProducedDeclarations{}, models.AIUsage{}, newAppTheoryError("app.bad_request", "conversation model is missing")
	}

	var transcript []soulMintConversationMessage
	if strings.TrimSpace(conv.Messages) != "" {
		_ = json.Unmarshal([]byte(conv.Messages), &transcript)
	}
	if len(transcript) == 0 {
		return soulMintConversationProducedDeclarations{}, models.AIUsage{}, newAppTheoryError("app.bad_request", "conversation has no messages")
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
			DeclaredCapabilities: hostedgenesis.FilterDeclaredCapabilitiesForPrompt(reg.Capabilities),
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
			log.Printf("controlplane: mint conversation declaration extraction failed: provider=openai model=%s failure_code=%s", modelSet, hostedGenesisFailureDeclarationExtractionFailed)
			return soulMintConversationProducedDeclarations{}, models.AIUsage{}, newAppTheoryError("app.internal", "failed to extract declarations")
		}
		draft = out
		usage = u
	case strings.HasPrefix(strings.ToLower(modelSet), "anthropic:"):
		out, u, err := llm.MintConversationDeclarationsAnthropic(ctx, apiKey, modelSet, in)
		if err != nil {
			log.Printf("controlplane: mint conversation declaration extraction failed: provider=anthropic model=%s failure_code=%s", modelSet, hostedGenesisFailureDeclarationExtractionFailed)
			return soulMintConversationProducedDeclarations{}, models.AIUsage{}, newAppTheoryError("app.internal", "failed to extract declarations")
		}
		draft = out
		usage = u
	default:
		return soulMintConversationProducedDeclarations{}, models.AIUsage{}, newAppTheoryError("app.bad_request", mintConversationUnsupportedModelSetMessage)
	}

	declaredCapabilities := reg.Capabilities
	allowEmptyCapabilities := isRegistrationInstanceTrust(reg)
	if allowEmptyCapabilities {
		declaredCapabilities = nil
	}
	decl, appErr := buildMintConversationProducedDeclarationsWithOptions(draft, now, modelSet, declaredCapabilities, allowEmptyCapabilities)
	if appErr != nil {
		return soulMintConversationProducedDeclarations{}, models.AIUsage{}, appErr
	}
	return decl, usage, nil
}

func buildMintConversationProducedDeclarations(draft llm.MintConversationDeclarationsDraft, now time.Time, modelSet string, declaredCapabilities ...[]string) (soulMintConversationProducedDeclarations, *apptheory.AppTheoryError) {
	declared := []string(nil)
	if len(declaredCapabilities) > 0 {
		declared = declaredCapabilities[0]
	}
	return buildMintConversationProducedDeclarationsWithOptions(draft, now, modelSet, declared, false)
}

func buildMintConversationProducedDeclarationsWithOptions(draft llm.MintConversationDeclarationsDraft, now time.Time, modelSet string, declaredCapabilities []string, allowEmptyCapabilities bool) (soulMintConversationProducedDeclarations, *apptheory.AppTheoryError) {
	decl := soulMintConversationProducedDeclarations{
		SelfDescription: draft.SelfDescription,
		Capabilities:    []soul.CapabilityV2{},
		Boundaries:      []soul.BoundaryV2{},
		Transparency:    draft.Transparency,
	}

	decl.SelfDescription.AuthoredBy = "agent"
	decl.SelfDescription.MintingModel = strings.TrimSpace(modelSet)
	if err := decl.SelfDescription.Validate(); err != nil {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeSelfDescription))
	}

	if allowEmptyCapabilities {
		var capErr error
		decl.Capabilities, capErr = hostedgenesis.ValidateAndNormalizeProducedCapabilities(draft.Capabilities)
		if capErr != nil {
			return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeCapabilitiesBad))
		}
	} else {
		decl.Capabilities = hostedgenesis.NormalizeProducedCapabilities(draft.Capabilities)
	}
	if !allowEmptyCapabilities {
		decl.Capabilities = hostedgenesis.MergeDeclaredCapabilities(decl.Capabilities, declaredCapabilities)
	}
	if !allowEmptyCapabilities && len(decl.Capabilities) == 0 {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeCapabilities))
	}

	bounds := make([]soul.BoundaryV2, 0, len(draft.Boundaries))
	invalidBoundary := false
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
			invalidBoundary = true
			continue
		}
		bounds = append(bounds, entry)
	}
	decl.Boundaries = bounds
	if invalidBoundary {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeBoundariesBad))
	}
	if len(decl.Boundaries) == 0 {
		code := hostedgenesis.DeclarationCodeBoundaries
		if len(draft.Boundaries) > 0 {
			code = hostedgenesis.DeclarationCodeBoundariesBad
		}
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(code))
	}

	if decl.Transparency == nil {
		decl.Transparency = map[string]any{}
	}

	return decl, nil
}

func parseAndValidateMintConversationDeclarations(raw string) (soulMintConversationProducedDeclarations, *apptheory.AppTheoryError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", "declarations is required")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeInvalid))
	}
	if !mintConversationJSONFieldPresent(fields, "capabilities") {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeCapabilities))
	}
	if !mintConversationJSONFieldPresent(fields, "boundaries") {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeBoundaries))
	}
	if !mintConversationJSONFieldPresent(fields, "transparency") {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeTransparency))
	}
	if !mintConversationJSONFieldIsArray(fields["capabilities"]) {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeCapabilitiesBad))
	}
	if !mintConversationJSONFieldIsArray(fields["boundaries"]) {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeBoundariesBad))
	}

	var decl soulMintConversationProducedDeclarations
	if err := json.Unmarshal([]byte(raw), &decl); err != nil {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeInvalid))
	}

	if err := decl.SelfDescription.Validate(); err != nil {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeSelfDescription))
	}
	for i := range decl.Capabilities {
		if err := decl.Capabilities[i].Validate(); err != nil {
			return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeCapabilitiesBad))
		}
	}
	for i := range decl.Boundaries {
		if err := decl.Boundaries[i].Validate(); err != nil {
			return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeBoundariesBad))
		}
	}
	if decl.Transparency == nil {
		return soulMintConversationProducedDeclarations{}, newAppTheoryError("app.bad_request", string(hostedgenesis.DeclarationCodeTransparency))
	}

	return decl, nil
}
