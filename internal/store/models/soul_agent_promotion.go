package models

import (
	"fmt"
	"strings"
	"time"
)

const (
	SoulAgentPromotionStageRequested       = "requested"
	SoulAgentPromotionStageApproved        = "approved"
	SoulAgentPromotionStageMinted          = "minted"
	SoulAgentPromotionStageReviewing       = "reviewing"
	SoulAgentPromotionStageReadyToFinalize = "ready_to_finalize"
	SoulAgentPromotionStageGraduated       = "graduated"
)

const (
	SoulAgentPromotionRequestStatusRequested = "requested"
	SoulAgentPromotionRequestStatusVerified  = "verified"
	SoulAgentPromotionRequestStatusMinted    = "minted"
	SoulAgentPromotionRequestStatusGraduated = "graduated"
)

const (
	SoulAgentPromotionReviewStatusNotStarted             = "not_started"
	SoulAgentPromotionReviewStatusConversationInProgress = "conversation_in_progress"
	SoulAgentPromotionReviewStatusDraftReady             = "draft_ready"
	SoulAgentPromotionReviewStatusPublished              = "published"
)

const (
	SoulAgentPromotionApprovalStatusPending  = "pending"
	SoulAgentPromotionApprovalStatusApproved = "approved"
)

const (
	SoulAgentPromotionReadinessAwaitingVerification = "awaiting_verification"
	SoulAgentPromotionReadinessAwaitingMint         = "awaiting_mint"
	SoulAgentPromotionReadinessReadyForConversation = "ready_for_conversation"
	SoulAgentPromotionReadinessReadyForFinalize     = "ready_for_finalize"
	SoulAgentPromotionReadinessGraduated            = "graduated"
)

// SoulAgentPromotion stores the durable, agent-centered promotion workflow state
// for moving from request through review and graduation.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: PROMOTION
type SoulAgentPromotion struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`
	GSI2PK string `theorydb:"index:gsi2,pk,attr:gsi2PK" json:"-"`
	GSI2SK string `theorydb:"index:gsi2,sk,attr:gsi2SK" json:"-"`

	AgentID string `theorydb:"attr:agentId" json:"agent_id"`

	RegistrationID string `theorydb:"attr:registrationId" json:"registration_id,omitempty"`
	RequestedBy    string `theorydb:"attr:requestedBy" json:"requested_by,omitempty"`

	Domain  string `theorydb:"attr:domain" json:"domain"`
	LocalID string `theorydb:"attr:localId" json:"local_id"`
	Wallet  string `theorydb:"attr:wallet" json:"wallet"`

	Stage           string `theorydb:"attr:stage" json:"stage"`
	RequestStatus   string `theorydb:"attr:requestStatus" json:"request_status"`
	ReviewStatus    string `theorydb:"attr:reviewStatus" json:"review_status"`
	ApprovalStatus  string `theorydb:"attr:approvalStatus" json:"approval_status"`
	ReadinessStatus string `theorydb:"attr:readinessStatus" json:"readiness_status"`

	MintOperationID     string `theorydb:"attr:mintOperationId" json:"mint_operation_id,omitempty"`
	MintOperationStatus string `theorydb:"attr:mintOperationStatus" json:"mint_operation_status,omitempty"`

	PrincipalAddress string `theorydb:"attr:principalAddress" json:"principal_address,omitempty"`

	LatestConversationID     string `theorydb:"attr:latestConversationId" json:"latest_conversation_id,omitempty"`
	LatestConversationStatus string `theorydb:"attr:latestConversationStatus" json:"latest_conversation_status,omitempty"`
	LatestReviewSHA256       string `theorydb:"attr:latestReviewSha256" json:"latest_review_sha256,omitempty"`
	LatestBoundaryCount      int    `theorydb:"attr:latestBoundaryCount" json:"latest_boundary_count,omitempty"`
	LatestCapabilityCount    int    `theorydb:"attr:latestCapabilityCount" json:"latest_capability_count,omitempty"`

	PublishedVersion int `theorydb:"attr:publishedVersion" json:"published_version,omitempty"`

	RequestedAt     time.Time `theorydb:"attr:requestedAt" json:"requested_at,omitempty"`
	VerifiedAt      time.Time `theorydb:"attr:verifiedAt" json:"verified_at,omitempty"`
	ApprovedAt      time.Time `theorydb:"attr:approvedAt" json:"approved_at,omitempty"`
	MintedAt        time.Time `theorydb:"attr:mintedAt" json:"minted_at,omitempty"`
	ReviewStartedAt time.Time `theorydb:"attr:reviewStartedAt" json:"review_started_at,omitempty"`
	ReviewReadyAt   time.Time `theorydb:"attr:reviewReadyAt" json:"review_ready_at,omitempty"`
	GraduatedAt     time.Time `theorydb:"attr:graduatedAt" json:"graduated_at,omitempty"`

	CreatedAt time.Time `theorydb:"attr:createdAt" json:"created_at"`
	UpdatedAt time.Time `theorydb:"attr:updatedAt" json:"updated_at"`
}

func (SoulAgentPromotion) TableName() string { return MainTableName() }

func (p *SoulAgentPromotion) BeforeCreate() error {
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	if strings.TrimSpace(p.Stage) == "" {
		p.Stage = SoulAgentPromotionStageRequested
	}
	if strings.TrimSpace(p.RequestStatus) == "" {
		p.RequestStatus = SoulAgentPromotionRequestStatusRequested
	}
	if strings.TrimSpace(p.ReviewStatus) == "" {
		p.ReviewStatus = SoulAgentPromotionReviewStatusNotStarted
	}
	if strings.TrimSpace(p.ApprovalStatus) == "" {
		p.ApprovalStatus = SoulAgentPromotionApprovalStatusPending
	}
	if strings.TrimSpace(p.ReadinessStatus) == "" {
		p.ReadinessStatus = SoulAgentPromotionReadinessAwaitingVerification
	}
	if p.RequestedAt.IsZero() {
		p.RequestedAt = p.CreatedAt
	}
	return p.UpdateKeys()
}

func (p *SoulAgentPromotion) BeforeUpdate() error {
	p.UpdatedAt = time.Now().UTC()
	return p.UpdateKeys()
}

func (p *SoulAgentPromotion) UpdateKeys() error {
	p.AgentID = strings.ToLower(strings.TrimSpace(p.AgentID))
	p.RegistrationID = strings.TrimSpace(p.RegistrationID)
	p.RequestedBy = strings.TrimSpace(p.RequestedBy)
	p.Domain = strings.ToLower(strings.TrimSpace(p.Domain))
	p.LocalID = normalizeSoulLocalID(p.LocalID)
	p.Wallet = strings.ToLower(strings.TrimSpace(p.Wallet))
	p.Stage = strings.ToLower(strings.TrimSpace(p.Stage))
	p.RequestStatus = strings.ToLower(strings.TrimSpace(p.RequestStatus))
	p.ReviewStatus = strings.ToLower(strings.TrimSpace(p.ReviewStatus))
	p.ApprovalStatus = strings.ToLower(strings.TrimSpace(p.ApprovalStatus))
	p.ReadinessStatus = strings.ToLower(strings.TrimSpace(p.ReadinessStatus))
	p.MintOperationID = strings.TrimSpace(p.MintOperationID)
	p.MintOperationStatus = strings.ToLower(strings.TrimSpace(p.MintOperationStatus))
	p.PrincipalAddress = strings.ToLower(strings.TrimSpace(p.PrincipalAddress))
	p.LatestConversationID = strings.TrimSpace(p.LatestConversationID)
	p.LatestConversationStatus = strings.ToLower(strings.TrimSpace(p.LatestConversationStatus))
	p.LatestReviewSHA256 = strings.ToLower(strings.TrimSpace(p.LatestReviewSHA256))

	p.PK = fmt.Sprintf("SOUL#AGENT#%s", p.AgentID)
	p.SK = "PROMOTION"
	p.updateGSI1()
	p.updateGSI2()
	return nil
}

func (p *SoulAgentPromotion) GetPK() string { return p.PK }

func (p *SoulAgentPromotion) GetSK() string { return p.SK }

func (p *SoulAgentPromotion) updateGSI1() {
	if p == nil {
		return
	}
	stage := strings.ToLower(strings.TrimSpace(p.Stage))
	if stage == "" {
		p.GSI1PK = ""
		p.GSI1SK = ""
		return
	}
	requestedAt := p.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = time.Now().UTC()
	}
	p.GSI1PK = fmt.Sprintf("SOUL_PROMOTION_STAGE#%s", stage)
	p.GSI1SK = fmt.Sprintf("%s#%s", requestedAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(p.AgentID))
}

func (p *SoulAgentPromotion) updateGSI2() {
	if p == nil {
		return
	}
	requestedBy := strings.TrimSpace(p.RequestedBy)
	if requestedBy == "" {
		p.GSI2PK = ""
		p.GSI2SK = ""
		return
	}
	requestedAt := p.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = time.Now().UTC()
	}
	p.GSI2PK = fmt.Sprintf("SOUL_PROMOTION_REQUESTER#%s", requestedBy)
	p.GSI2SK = fmt.Sprintf("%s#%s", requestedAt.UTC().Format(time.RFC3339Nano), strings.TrimSpace(p.AgentID))
}
