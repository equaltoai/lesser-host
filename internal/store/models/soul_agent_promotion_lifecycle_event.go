package models

import (
	"fmt"
	"strings"
	"time"
)

const (
	SoulAgentPromotionEventTypeRequestCreated  = "request_created"
	SoulAgentPromotionEventTypeRequestApproved = "request_approved"
	SoulAgentPromotionEventTypeMintExecuted    = "mint_executed"
	SoulAgentPromotionEventTypeReviewStarted   = "review_started"
	SoulAgentPromotionEventTypeFinalizeReady   = "finalize_ready"
	SoulAgentPromotionEventTypeGraduated       = "graduated"
)

// SoulAgentPromotionLifecycleEvent stores a durable lifecycle transition snapshot for a promotion workflow.
//
// Keys:
//
//	PK: SOUL#AGENT#{agentId}
//	SK: EVENT#{timestamp}#{eventId}
//
// Indexes:
//
//	GSI1PK: SOUL_PROMOTION_EVENT_REQUESTER#{requestedBy}
//	GSI1SK: {timestamp}#{agentId}#{eventId}
type SoulAgentPromotionLifecycleEvent struct {
	_ struct{} `theorydb:"naming:camelCase"`

	PK string `theorydb:"pk,attr:PK" json:"-"`
	SK string `theorydb:"sk,attr:SK" json:"-"`

	GSI1PK string `theorydb:"index:gsi1,pk,attr:gsi1PK" json:"-"`
	GSI1SK string `theorydb:"index:gsi1,sk,attr:gsi1SK" json:"-"`

	AgentID        string `theorydb:"attr:agentId" json:"agent_id"`
	EventID        string `theorydb:"attr:eventId" json:"event_id"`
	EventType      string `theorydb:"attr:eventType" json:"event_type"`
	Summary        string `theorydb:"attr:summary" json:"summary,omitempty"`
	RequestedBy    string `theorydb:"attr:requestedBy" json:"requested_by,omitempty"`
	RegistrationID string `theorydb:"attr:registrationId" json:"registration_id,omitempty"`

	RequestID      string `theorydb:"attr:requestId" json:"request_id,omitempty"`
	OperationID    string `theorydb:"attr:operationId" json:"operation_id,omitempty"`
	ConversationID string `theorydb:"attr:conversationId" json:"conversation_id,omitempty"`

	Domain  string `theorydb:"attr:domain" json:"domain,omitempty"`
	LocalID string `theorydb:"attr:localId" json:"local_id,omitempty"`
	Wallet  string `theorydb:"attr:wallet" json:"wallet,omitempty"`

	Stage           string `theorydb:"attr:stage" json:"stage,omitempty"`
	RequestStatus   string `theorydb:"attr:requestStatus" json:"request_status,omitempty"`
	ReviewStatus    string `theorydb:"attr:reviewStatus" json:"review_status,omitempty"`
	ApprovalStatus  string `theorydb:"attr:approvalStatus" json:"approval_status,omitempty"`
	ReadinessStatus string `theorydb:"attr:readinessStatus" json:"readiness_status,omitempty"`

	MintOperationID     string `theorydb:"attr:mintOperationId" json:"mint_operation_id,omitempty"`
	MintOperationStatus string `theorydb:"attr:mintOperationStatus" json:"mint_operation_status,omitempty"`
	PrincipalAddress    string `theorydb:"attr:principalAddress" json:"principal_address,omitempty"`

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
	CreatedAt       time.Time `theorydb:"attr:createdAt" json:"created_at,omitempty"`
	UpdatedAt       time.Time `theorydb:"attr:updatedAt" json:"updated_at,omitempty"`
	OccurredAt      time.Time `theorydb:"attr:occurredAt" json:"occurred_at"`
}

func (SoulAgentPromotionLifecycleEvent) TableName() string { return MainTableName() }

func (e *SoulAgentPromotionLifecycleEvent) BeforeCreate() error {
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	if strings.TrimSpace(e.EventID) == "" {
		e.EventID = DefaultSoulAgentPromotionLifecycleEventID(e.EventType, e.OccurredAt, e.AgentID, e.ConversationID, e.OperationID)
	}
	if err := e.UpdateKeys(); err != nil {
		return err
	}
	if err := requireNonEmpty("agentId", e.AgentID); err != nil {
		return err
	}
	if err := requireNonEmpty("eventId", e.EventID); err != nil {
		return err
	}
	if err := requireOneOf(
		"eventType",
		e.EventType,
		SoulAgentPromotionEventTypeRequestCreated,
		SoulAgentPromotionEventTypeRequestApproved,
		SoulAgentPromotionEventTypeMintExecuted,
		SoulAgentPromotionEventTypeReviewStarted,
		SoulAgentPromotionEventTypeFinalizeReady,
		SoulAgentPromotionEventTypeGraduated,
	); err != nil {
		return err
	}
	return nil
}

func (e *SoulAgentPromotionLifecycleEvent) UpdateKeys() error {
	if e == nil {
		return nil
	}

	e.AgentID = strings.ToLower(strings.TrimSpace(e.AgentID))
	e.EventID = strings.TrimSpace(e.EventID)
	e.EventType = strings.ToLower(strings.TrimSpace(e.EventType))
	e.Summary = strings.TrimSpace(e.Summary)
	e.RequestedBy = strings.TrimSpace(e.RequestedBy)
	e.RegistrationID = strings.TrimSpace(e.RegistrationID)
	e.RequestID = strings.TrimSpace(e.RequestID)
	e.OperationID = strings.TrimSpace(e.OperationID)
	e.ConversationID = strings.TrimSpace(e.ConversationID)
	e.Domain = strings.ToLower(strings.TrimSpace(e.Domain))
	e.LocalID = strings.TrimSpace(e.LocalID)
	e.Wallet = strings.TrimSpace(e.Wallet)
	e.Stage = strings.ToLower(strings.TrimSpace(e.Stage))
	e.RequestStatus = strings.ToLower(strings.TrimSpace(e.RequestStatus))
	e.ReviewStatus = strings.ToLower(strings.TrimSpace(e.ReviewStatus))
	e.ApprovalStatus = strings.ToLower(strings.TrimSpace(e.ApprovalStatus))
	e.ReadinessStatus = strings.ToLower(strings.TrimSpace(e.ReadinessStatus))
	e.MintOperationID = strings.TrimSpace(e.MintOperationID)
	e.MintOperationStatus = strings.ToLower(strings.TrimSpace(e.MintOperationStatus))
	e.PrincipalAddress = strings.TrimSpace(e.PrincipalAddress)
	e.LatestConversationID = strings.TrimSpace(e.LatestConversationID)
	e.LatestConversationStatus = strings.ToLower(strings.TrimSpace(e.LatestConversationStatus))
	e.LatestReviewSHA256 = strings.ToLower(strings.TrimSpace(e.LatestReviewSHA256))

	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	if e.EventID == "" {
		e.EventID = DefaultSoulAgentPromotionLifecycleEventID(e.EventType, e.OccurredAt, e.AgentID, e.ConversationID, e.OperationID)
	}

	ts := e.OccurredAt.UTC().Format(time.RFC3339Nano)
	e.PK = fmt.Sprintf("SOUL#AGENT#%s", e.AgentID)
	e.SK = fmt.Sprintf("EVENT#%s#%s", ts, e.EventID)

	if e.RequestedBy == "" {
		e.GSI1PK = ""
		e.GSI1SK = ""
		return nil
	}
	e.GSI1PK = fmt.Sprintf("SOUL_PROMOTION_EVENT_REQUESTER#%s", e.RequestedBy)
	e.GSI1SK = fmt.Sprintf("%s#%s#%s", ts, e.AgentID, e.EventID)
	return nil
}

func (e *SoulAgentPromotionLifecycleEvent) GetPK() string { return e.PK }

func (e *SoulAgentPromotionLifecycleEvent) GetSK() string { return e.SK }

func DefaultSoulAgentPromotionLifecycleEventID(eventType string, occurredAt time.Time, agentID string, conversationID string, operationID string) string {
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	suffix := strings.TrimSpace(conversationID)
	if suffix == "" {
		suffix = strings.TrimSpace(operationID)
	}
	if suffix == "" {
		suffix = strings.TrimSpace(agentID)
	}
	if suffix == "" {
		suffix = "promotion"
	}
	return fmt.Sprintf("%s#%s#%s", eventType, occurredAt.UTC().Format("20060102T150405.000000000Z"), suffix)
}
