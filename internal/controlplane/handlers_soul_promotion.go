package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v3/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulAgentPromotionPrerequisites struct {
	PrincipalDeclarationRecorded bool `json:"principal_declaration_recorded"`
	MintOperationCreated         bool `json:"mint_operation_created"`
	MintExecuted                 bool `json:"mint_executed"`
	ConversationStarted          bool `json:"conversation_started"`
	ConversationCompleted        bool `json:"conversation_completed"`
	ReviewDraftReady             bool `json:"review_draft_ready"`
	ReadyForFinalize             bool `json:"ready_for_finalize"`
	Graduated                    bool `json:"graduated"`
}

type soulAgentPromotionView struct {
	AgentID string `json:"agent_id"`

	RegistrationID string `json:"registration_id,omitempty"`
	RequestedBy    string `json:"requested_by,omitempty"`

	Domain  string `json:"domain"`
	LocalID string `json:"local_id"`
	Wallet  string `json:"wallet,omitempty"`

	AuthorityModel string `json:"authority_model,omitempty"`

	Stage           string `json:"stage"`
	RequestStatus   string `json:"request_status"`
	ReviewStatus    string `json:"review_status"`
	ApprovalStatus  string `json:"approval_status"`
	ReadinessStatus string `json:"readiness_status"`

	MintOperationID           string `json:"mint_operation_id,omitempty"`
	MintOperationStatus       string `json:"mint_operation_status,omitempty"`
	AnchorState               string `json:"anchor_state,omitempty"`
	OnchainBindingStatus      string `json:"onchain_binding_status,omitempty"`
	OnchainBindingAvailable   bool   `json:"onchain_binding_available,omitempty"`
	HostedOffchainFinalizable bool   `json:"hosted_offchain_finalizable,omitempty"`
	PrincipalAddress          string `json:"principal_address,omitempty"`

	LatestConversationID     string `json:"latest_conversation_id,omitempty"`
	LatestConversationStatus string `json:"latest_conversation_status,omitempty"`
	LatestReviewSHA256       string `json:"latest_review_sha256,omitempty"`
	LatestBoundaryCount      int    `json:"latest_boundary_count,omitempty"`
	LatestCapabilityCount    int    `json:"latest_capability_count,omitempty"`

	PublishedVersion int `json:"published_version,omitempty"`

	RequestedAt     time.Time `json:"requested_at,omitempty"`
	VerifiedAt      time.Time `json:"verified_at,omitempty"`
	ApprovedAt      time.Time `json:"approved_at,omitempty"`
	MintedAt        time.Time `json:"minted_at,omitempty"`
	ReviewStartedAt time.Time `json:"review_started_at,omitempty"`
	ReviewReadyAt   time.Time `json:"review_ready_at,omitempty"`
	GraduatedAt     time.Time `json:"graduated_at,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	Prerequisites soulAgentPromotionPrerequisites `json:"prerequisites"`
	NextActions   []string                        `json:"next_actions,omitempty"`
}

type soulAgentPromotionResponse struct {
	Version   string                 `json:"version"`
	Promotion soulAgentPromotionView `json:"promotion"`
}

type soulAgentPromotionListResponse struct {
	Version    string                   `json:"version"`
	Promotions []soulAgentPromotionView `json:"promotions"`
	Count      int                      `json:"count"`
	HasMore    bool                     `json:"has_more"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

func ptrTo[T any](v T) *T { return &v }

func (s *Server) getSoulAgentPromotion(ctx context.Context, agentIDHex string) (*models.SoulAgentPromotion, error) {
	return getSoulAgentItemBySK[models.SoulAgentPromotion](s, ctx, agentIDHex, "PROMOTION")
}

func (s *Server) saveSoulAgentPromotion(ctx context.Context, promotion *models.SoulAgentPromotion) *apptheory.AppTheoryError {
	if s == nil || s.store == nil || s.store.DB == nil || promotion == nil {
		return newAppTheoryError("app.internal", "internal error")
	}
	_ = promotion.UpdateKeys()
	if err := s.store.DB.WithContext(ctx).Model(promotion).CreateOrUpdate(); err != nil {
		return newAppTheoryError("app.internal", "failed to save promotion state")
	}
	return nil
}

func (s *Server) loadOrFallbackSoulAgentPromotion(ctx context.Context, agentIDHex string, fallback *models.SoulAgentPromotion) *models.SoulAgentPromotion {
	promotion, err := s.getSoulAgentPromotion(ctx, agentIDHex)
	if err == nil && promotion != nil {
		return promotion
	}
	return fallback
}

func buildSoulAgentPromotionFromRegistration(reg *models.SoulAgentRegistration, now time.Time) *models.SoulAgentPromotion {
	if reg == nil {
		return nil
	}
	return &models.SoulAgentPromotion{
		AgentID:         reg.AgentID,
		RegistrationID:  reg.ID,
		RequestedBy:     strings.TrimSpace(reg.Username),
		Domain:          reg.DomainNormalized,
		LocalID:         reg.LocalID,
		Wallet:          reg.Wallet,
		AuthorityModel:  soulRegistrationAuthorityModel(reg),
		Stage:           models.SoulAgentPromotionStageRequested,
		RequestStatus:   models.SoulAgentPromotionRequestStatusRequested,
		ReviewStatus:    models.SoulAgentPromotionReviewStatusNotStarted,
		ApprovalStatus:  models.SoulAgentPromotionApprovalStatusPending,
		ReadinessStatus: models.SoulAgentPromotionReadinessAwaitingVerification,
		AnchorState:     models.SoulAnchorStateHostedOffchain,
		RequestedAt:     now.UTC(),
		CreatedAt:       now.UTC(),
		UpdatedAt:       now.UTC(),
	}
}

func updateSoulAgentPromotionForVerification(promotion *models.SoulAgentPromotion, reg *models.SoulAgentRegistration, op *models.SoulOperation, principalAddress string, now time.Time) *models.SoulAgentPromotion {
	if promotion == nil {
		promotion = &models.SoulAgentPromotion{}
	}
	if reg != nil {
		promotion.AgentID = reg.AgentID
		promotion.RegistrationID = reg.ID
		promotion.RequestedBy = strings.TrimSpace(reg.Username)
		promotion.Domain = reg.DomainNormalized
		promotion.LocalID = reg.LocalID
		promotion.Wallet = reg.Wallet
		promotion.AuthorityModel = soulRegistrationAuthorityModel(reg)
	}
	promotion.Stage = models.SoulAgentPromotionStageApproved
	promotion.RequestStatus = models.SoulAgentPromotionRequestStatusVerified
	promotion.ReviewStatus = models.SoulAgentPromotionReviewStatusNotStarted
	promotion.ApprovalStatus = models.SoulAgentPromotionApprovalStatusApproved
	promotion.ReadinessStatus = models.SoulAgentPromotionReadinessReadyForConversation
	promotion.AnchorState = models.SoulAnchorStateHostedOffchain
	promotion.PrincipalAddress = strings.ToLower(strings.TrimSpace(principalAddress))
	if op != nil {
		promotion.MintOperationID = strings.TrimSpace(op.OperationID)
		promotion.MintOperationStatus = strings.ToLower(strings.TrimSpace(op.Status))
	}
	if promotion.RequestedAt.IsZero() {
		promotion.RequestedAt = now.UTC()
	}
	promotion.VerifiedAt = now.UTC()
	promotion.ApprovedAt = now.UTC()
	if promotion.CreatedAt.IsZero() {
		promotion.CreatedAt = now.UTC()
	}
	promotion.UpdatedAt = now.UTC()
	return promotion
}

func updateSoulAgentPromotionForInstanceTrustBegin(promotion *models.SoulAgentPromotion, now time.Time) *models.SoulAgentPromotion {
	if promotion == nil {
		return nil
	}
	promotion.AuthorityModel = models.SoulAuthorityModelInstanceTrust
	promotion.Stage = models.SoulAgentPromotionStageApproved
	promotion.RequestStatus = models.SoulAgentPromotionRequestStatusVerified
	promotion.ReviewStatus = models.SoulAgentPromotionReviewStatusNotStarted
	promotion.ApprovalStatus = models.SoulAgentPromotionApprovalStatusApproved
	promotion.ReadinessStatus = models.SoulAgentPromotionReadinessReadyForConversation
	promotion.AnchorState = models.SoulAnchorStateHostedOffchain
	if promotion.RequestedAt.IsZero() {
		promotion.RequestedAt = now.UTC()
	}
	promotion.VerifiedAt = now.UTC()
	promotion.ApprovedAt = now.UTC()
	if promotion.CreatedAt.IsZero() {
		promotion.CreatedAt = now.UTC()
	}
	promotion.UpdatedAt = now.UTC()
	return promotion
}

func updateSoulAgentPromotionForMintExecution(promotion *models.SoulAgentPromotion, op *models.SoulOperation, now time.Time) *models.SoulAgentPromotion {
	if promotion == nil {
		return nil
	}
	if !soulAgentPromotionIsGraduated(promotion) {
		promotion.RequestStatus = models.SoulAgentPromotionRequestStatusMinted
	}
	if !soulAgentPromotionHasReviewProgress(promotion) && !soulAgentPromotionIsGraduated(promotion) {
		promotion.Stage = models.SoulAgentPromotionStageMinted
		promotion.ReadinessStatus = models.SoulAgentPromotionReadinessReadyForConversation
	}
	promotion.AnchorState = models.SoulAnchorStateImmutableOnchain
	promotion.MintedAt = now.UTC()
	if op != nil {
		promotion.MintOperationID = strings.TrimSpace(op.OperationID)
		promotion.MintOperationStatus = strings.ToLower(strings.TrimSpace(op.Status))
	}
	promotion.UpdatedAt = now.UTC()
	return promotion
}

func soulAgentPromotionHasReviewProgress(promotion *models.SoulAgentPromotion) bool {
	if promotion == nil {
		return false
	}
	if strings.TrimSpace(promotion.LatestConversationID) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(promotion.ReviewStatus)) {
	case models.SoulAgentPromotionReviewStatusConversationInProgress,
		models.SoulAgentPromotionReviewStatusDraftReady,
		models.SoulAgentPromotionReviewStatusPublished:
		return true
	}
	switch strings.ToLower(strings.TrimSpace(promotion.ReadinessStatus)) {
	case models.SoulAgentPromotionReadinessReadyForFinalize,
		models.SoulAgentPromotionReadinessGraduated:
		return true
	}
	return false
}

func soulAgentPromotionIsGraduated(promotion *models.SoulAgentPromotion) bool {
	return promotion != nil && (promotion.PublishedVersion > 0 ||
		strings.EqualFold(strings.TrimSpace(promotion.Stage), models.SoulAgentPromotionStageGraduated) ||
		strings.EqualFold(strings.TrimSpace(promotion.ReadinessStatus), models.SoulAgentPromotionReadinessGraduated))
}

func updateSoulAgentPromotionForConversation(promotion *models.SoulAgentPromotion, conversationID string, conversationStatus string, now time.Time) *models.SoulAgentPromotion {
	if promotion == nil {
		return nil
	}
	stage := models.SoulAgentPromotionStageReviewing
	reviewStatus := models.SoulAgentPromotionReviewStatusConversationInProgress
	readinessStatus := models.SoulAgentPromotionReadinessReadyForConversation
	if strings.EqualFold(strings.TrimSpace(conversationStatus), models.SoulMintConversationStatusCompleted) ||
		strings.EqualFold(strings.TrimSpace(conversationStatus), models.SoulMintConversationStatusPublished) {
		stage = models.SoulAgentPromotionStageReadyToFinalize
		reviewStatus = models.SoulAgentPromotionReviewStatusDraftReady
		readinessStatus = models.SoulAgentPromotionReadinessReadyForFinalize
		promotion.ReviewReadyAt = now.UTC()
	}
	promotion.Stage = stage
	promotion.ReviewStatus = reviewStatus
	promotion.ReadinessStatus = readinessStatus
	promotion.LatestConversationID = strings.TrimSpace(conversationID)
	promotion.LatestConversationStatus = strings.ToLower(strings.TrimSpace(conversationStatus))
	if promotion.ReviewStartedAt.IsZero() {
		promotion.ReviewStartedAt = now.UTC()
	}
	promotion.UpdatedAt = now.UTC()
	return promotion
}

func updateSoulAgentPromotionForGraduation(promotion *models.SoulAgentPromotion, publishedVersion int, now time.Time) *models.SoulAgentPromotion {
	if promotion == nil {
		return nil
	}
	promotion.Stage = models.SoulAgentPromotionStageGraduated
	promotion.RequestStatus = models.SoulAgentPromotionRequestStatusGraduated
	promotion.ReviewStatus = models.SoulAgentPromotionReviewStatusPublished
	promotion.ReadinessStatus = models.SoulAgentPromotionReadinessGraduated
	if strings.TrimSpace(promotion.AnchorState) == "" {
		promotion.AnchorState = models.SoulAnchorStateHostedOffchain
	}
	promotion.PublishedVersion = publishedVersion
	promotion.GraduatedAt = now.UTC()
	promotion.UpdatedAt = now.UTC()
	return promotion
}

func updateSoulAgentPromotionReviewDigest(promotion *models.SoulAgentPromotion, declarationsJSON string) *models.SoulAgentPromotion {
	if promotion == nil {
		return nil
	}
	trimmed := strings.TrimSpace(declarationsJSON)
	if trimmed == "" {
		promotion.LatestReviewSHA256 = ""
		promotion.LatestBoundaryCount = 0
		promotion.LatestCapabilityCount = 0
		return promotion
	}
	sum := sha256.Sum256([]byte(trimmed))
	promotion.LatestReviewSHA256 = hex.EncodeToString(sum[:])
	if decl, appErr := parseAndValidateMintConversationDeclarations(trimmed); appErr == nil {
		promotion.LatestBoundaryCount = len(decl.Boundaries)
		promotion.LatestCapabilityCount = len(decl.Capabilities)
	}
	return promotion
}

func (s *Server) buildSoulAgentPromotionView(promotion *models.SoulAgentPromotion) soulAgentPromotionView {
	if promotion == nil {
		return soulAgentPromotionView{}
	}
	authorityModel := soulPromotionAuthorityModel(promotion)
	principalRecorded := strings.TrimSpace(promotion.PrincipalAddress) != ""
	if authorityModel == models.SoulAuthorityModelInstanceTrust {
		principalRecorded = true
	}
	prereqs := soulAgentPromotionPrerequisites{
		PrincipalDeclarationRecorded: principalRecorded,
		MintOperationCreated:         strings.TrimSpace(promotion.MintOperationID) != "",
		MintExecuted:                 strings.EqualFold(strings.TrimSpace(promotion.MintOperationStatus), models.SoulOperationStatusExecuted),
		ConversationStarted:          strings.TrimSpace(promotion.LatestConversationID) != "",
		ConversationCompleted: strings.EqualFold(strings.TrimSpace(promotion.LatestConversationStatus), models.SoulMintConversationStatusCompleted) ||
			strings.EqualFold(strings.TrimSpace(promotion.LatestConversationStatus), models.SoulMintConversationStatusPublished),
		ReviewDraftReady: strings.TrimSpace(promotion.LatestReviewSHA256) != "",
		ReadyForFinalize: strings.EqualFold(strings.TrimSpace(promotion.ReadinessStatus), models.SoulAgentPromotionReadinessReadyForFinalize) || promotion.PublishedVersion > 0,
		Graduated:        promotion.PublishedVersion > 0 || strings.EqualFold(strings.TrimSpace(promotion.Stage), models.SoulAgentPromotionStageGraduated),
	}
	anchorState := soulAgentPromotionAnchorState(promotion)
	onchainStatus := soulAgentPromotionOnchainBindingStatus(promotion)
	return soulAgentPromotionView{
		AgentID:                   promotion.AgentID,
		RegistrationID:            promotion.RegistrationID,
		RequestedBy:               promotion.RequestedBy,
		Domain:                    promotion.Domain,
		LocalID:                   promotion.LocalID,
		Wallet:                    promotion.Wallet,
		AuthorityModel:            authorityModel,
		Stage:                     promotion.Stage,
		RequestStatus:             promotion.RequestStatus,
		ReviewStatus:              promotion.ReviewStatus,
		ApprovalStatus:            promotion.ApprovalStatus,
		ReadinessStatus:           promotion.ReadinessStatus,
		MintOperationID:           promotion.MintOperationID,
		MintOperationStatus:       promotion.MintOperationStatus,
		AnchorState:               anchorState,
		OnchainBindingStatus:      onchainStatus,
		OnchainBindingAvailable:   soulAgentPromotionOnchainBindingAvailable(promotion),
		HostedOffchainFinalizable: soulAgentPromotionHostedOffchainFinalizable(promotion),
		PrincipalAddress:          promotion.PrincipalAddress,
		LatestConversationID:      promotion.LatestConversationID,
		LatestConversationStatus:  promotion.LatestConversationStatus,
		LatestReviewSHA256:        promotion.LatestReviewSHA256,
		LatestBoundaryCount:       promotion.LatestBoundaryCount,
		LatestCapabilityCount:     promotion.LatestCapabilityCount,
		PublishedVersion:          promotion.PublishedVersion,
		RequestedAt:               promotion.RequestedAt,
		VerifiedAt:                promotion.VerifiedAt,
		ApprovedAt:                promotion.ApprovedAt,
		MintedAt:                  promotion.MintedAt,
		ReviewStartedAt:           promotion.ReviewStartedAt,
		ReviewReadyAt:             promotion.ReviewReadyAt,
		GraduatedAt:               promotion.GraduatedAt,
		CreatedAt:                 promotion.CreatedAt,
		UpdatedAt:                 promotion.UpdatedAt,
		Prerequisites:             prereqs,
		NextActions:               soulAgentPromotionNextActions(promotion),
	}
}

func soulAgentPromotionNextActions(promotion *models.SoulAgentPromotion) []string {
	if promotion == nil {
		return nil
	}
	actions := make([]string, 0, 4)
	switch strings.TrimSpace(promotion.ReadinessStatus) {
	case models.SoulAgentPromotionReadinessAwaitingVerification:
		actions = append(actions, "verify_request")
	case models.SoulAgentPromotionReadinessAwaitingMint:
		actions = append(actions, "record_mint_execution")
	case models.SoulAgentPromotionReadinessReadyForConversation:
		if strings.EqualFold(strings.TrimSpace(promotion.ReviewStatus), models.SoulAgentPromotionReviewStatusConversationInProgress) {
			actions = append(actions, "complete_review_conversation")
		} else {
			actions = append(actions, "start_review_conversation")
		}
		actions = appendPendingOnchainBindingAction(actions, promotion)
	case models.SoulAgentPromotionReadinessReadyForFinalize:
		actions = append(actions, "begin_finalize")
		actions = appendPendingOnchainBindingAction(actions, promotion)
	}
	if soulAgentPromotionIsGraduated(promotion) {
		actions = actions[:0]
		actions = appendPendingOnchainBindingAction(actions, promotion)
		if len(actions) == 0 {
			return nil
		}
	}
	sort.Strings(actions)
	return actions
}

func soulPromotionAuthorityModel(promotion *models.SoulAgentPromotion) string {
	if promotion == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(promotion.AuthorityModel)) {
	case models.SoulAuthorityModelInstanceTrust:
		return models.SoulAuthorityModelInstanceTrust
	case models.SoulAuthorityModelWalletPrincipal:
		return models.SoulAuthorityModelWalletPrincipal
	default:
		if strings.TrimSpace(promotion.Wallet) != "" || strings.TrimSpace(promotion.PrincipalAddress) != "" || strings.TrimSpace(promotion.MintOperationID) != "" {
			return models.SoulAuthorityModelWalletPrincipal
		}
		return ""
	}
}

func soulAgentPromotionAnchorState(promotion *models.SoulAgentPromotion) string {
	if promotion == nil {
		return models.SoulAnchorStateHostedOffchain
	}
	switch strings.ToLower(strings.TrimSpace(promotion.AnchorState)) {
	case models.SoulAnchorStateImmutableOnchain:
		return models.SoulAnchorStateImmutableOnchain
	default:
		return models.SoulAnchorStateHostedOffchain
	}
}

func soulAgentPromotionOnchainBindingStatus(promotion *models.SoulAgentPromotion) string {
	if promotion == nil {
		return "unavailable"
	}
	if soulAgentPromotionAnchorState(promotion) == models.SoulAnchorStateImmutableOnchain ||
		strings.EqualFold(strings.TrimSpace(promotion.MintOperationStatus), models.SoulOperationStatusExecuted) {
		return models.SoulOperationStatusExecuted
	}
	if strings.TrimSpace(promotion.MintOperationID) == "" {
		return "unavailable"
	}
	switch strings.ToLower(strings.TrimSpace(promotion.MintOperationStatus)) {
	case models.SoulOperationStatusFailed:
		return models.SoulOperationStatusFailed
	case models.SoulOperationStatusProposed:
		return models.SoulOperationStatusProposed
	default:
		return models.SoulOperationStatusPending
	}
}

func soulAgentPromotionOnchainBindingAvailable(promotion *models.SoulAgentPromotion) bool {
	return promotion != nil && strings.TrimSpace(promotion.MintOperationID) != ""
}

func soulAgentPromotionHostedOffchainFinalizable(promotion *models.SoulAgentPromotion) bool {
	return promotion != nil &&
		soulAgentPromotionAnchorState(promotion) == models.SoulAnchorStateHostedOffchain &&
		strings.EqualFold(strings.TrimSpace(promotion.ReadinessStatus), models.SoulAgentPromotionReadinessReadyForFinalize)
}

func appendPendingOnchainBindingAction(actions []string, promotion *models.SoulAgentPromotion) []string {
	if promotion == nil {
		return actions
	}
	if soulAgentPromotionAnchorState(promotion) == models.SoulAnchorStateImmutableOnchain {
		return actions
	}
	switch soulAgentPromotionOnchainBindingStatus(promotion) {
	case models.SoulOperationStatusPending, models.SoulOperationStatusProposed, models.SoulOperationStatusFailed:
		return append(actions, "record_mint_execution")
	default:
		return actions
	}
}

func (s *Server) loadSoulAgentPromotionForAccess(ctx *apptheory.Context, agentIDHex string) (*models.SoulAgentPromotion, *apptheory.AppTheoryError) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	promotion, err := s.getSoulAgentPromotion(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil, newAppTheoryError("app.not_found", "promotion not found")
	}
	if err != nil {
		return nil, newAppTheoryError("app.internal", "internal error")
	}

	if !isOperator(ctx) {
		if _, _, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(promotion.Domain)); accessErr != nil {
			return nil, accessErr
		}
	}
	return promotion, nil
}

func (s *Server) handleSoulAgentGetPromotion(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}
	promotion, appErr := s.loadSoulAgentPromotionForAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(http.StatusOK, soulAgentPromotionResponse{
		Version:   "1",
		Promotion: s.buildSoulAgentPromotionView(promotion),
	})
}

func (s *Server) handleSoulListMyPromotions(ctx *apptheory.Context) (*apptheory.Response, error) {
	items, nextCursor, hasMore, appErr := listSoulRequesterScopedItems[models.SoulAgentPromotion, soulAgentPromotionView](
		s,
		ctx,
		&models.SoulAgentPromotion{},
		"gsi2",
		"GSI2PK",
		"SOUL_PROMOTION_REQUESTER#",
		"GSI2SK",
		"failed to list promotions",
		s.buildSoulAgentPromotionView,
	)
	if appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(http.StatusOK, soulAgentPromotionListResponse{
		Version:    "1",
		Promotions: items,
		Count:      len(items),
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
}

func (s *Server) handleSoulAgentPromotionVerify(ctx *apptheory.Context) (*apptheory.Response, error) {
	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}
	promotion, appErr := s.loadSoulAgentPromotionForAccess(ctx, agentIDHex)
	if appErr != nil {
		return nil, appErr
	}
	if strings.TrimSpace(promotion.RegistrationID) == "" {
		return nil, newAppTheoryError("app.conflict", "promotion has no registration to verify")
	}

	ctx.Params["id"] = promotion.RegistrationID
	return s.handleSoulAgentRegistrationVerify(ctx)
}
