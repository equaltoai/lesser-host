package controlplane

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulAgentPromotionLifecycleEventView struct {
	EventID        string                 `json:"event_id"`
	EventType      string                 `json:"event_type"`
	Summary        string                 `json:"summary,omitempty"`
	OccurredAt     time.Time              `json:"occurred_at"`
	RequestID      string                 `json:"request_id,omitempty"`
	OperationID    string                 `json:"operation_id,omitempty"`
	ConversationID string                 `json:"conversation_id,omitempty"`
	Promotion      soulAgentPromotionView `json:"promotion"`
}

type soulAgentPromotionLifecycleEventListResponse struct {
	Version    string                                 `json:"version"`
	Events     []soulAgentPromotionLifecycleEventView `json:"events"`
	Count      int                                    `json:"count"`
	HasMore    bool                                   `json:"has_more"`
	NextCursor string                                 `json:"next_cursor,omitempty"`
}

type soulAgentPromotionLifecycleEventInput struct {
	EventType      string
	Summary        string
	RequestID      string
	OperationID    string
	ConversationID string
	OccurredAt     time.Time
}

func cloneSoulAgentPromotion(promotion *models.SoulAgentPromotion) *models.SoulAgentPromotion {
	if promotion == nil {
		return nil
	}
	copy := *promotion
	return &copy
}

func buildSoulAgentPromotionLifecycleEvent(promotion *models.SoulAgentPromotion, input soulAgentPromotionLifecycleEventInput) *models.SoulAgentPromotionLifecycleEvent {
	if promotion == nil {
		return nil
	}
	occurredAt := input.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	event := &models.SoulAgentPromotionLifecycleEvent{
		AgentID:                  promotion.AgentID,
		EventType:                input.EventType,
		Summary:                  strings.TrimSpace(input.Summary),
		RequestedBy:              promotion.RequestedBy,
		RegistrationID:           promotion.RegistrationID,
		RequestID:                strings.TrimSpace(input.RequestID),
		OperationID:              firstNonBlank(input.OperationID, promotion.MintOperationID),
		ConversationID:           firstNonBlank(input.ConversationID, promotion.LatestConversationID),
		Domain:                   promotion.Domain,
		LocalID:                  promotion.LocalID,
		Wallet:                   promotion.Wallet,
		Stage:                    promotion.Stage,
		RequestStatus:            promotion.RequestStatus,
		ReviewStatus:             promotion.ReviewStatus,
		ApprovalStatus:           promotion.ApprovalStatus,
		ReadinessStatus:          promotion.ReadinessStatus,
		MintOperationID:          promotion.MintOperationID,
		MintOperationStatus:      promotion.MintOperationStatus,
		PrincipalAddress:         promotion.PrincipalAddress,
		LatestConversationID:     promotion.LatestConversationID,
		LatestConversationStatus: promotion.LatestConversationStatus,
		LatestReviewSHA256:       promotion.LatestReviewSHA256,
		LatestBoundaryCount:      promotion.LatestBoundaryCount,
		LatestCapabilityCount:    promotion.LatestCapabilityCount,
		PublishedVersion:         promotion.PublishedVersion,
		RequestedAt:              promotion.RequestedAt,
		VerifiedAt:               promotion.VerifiedAt,
		ApprovedAt:               promotion.ApprovedAt,
		MintedAt:                 promotion.MintedAt,
		ReviewStartedAt:          promotion.ReviewStartedAt,
		ReviewReadyAt:            promotion.ReviewReadyAt,
		GraduatedAt:              promotion.GraduatedAt,
		CreatedAt:                promotion.CreatedAt,
		UpdatedAt:                promotion.UpdatedAt,
		OccurredAt:               occurredAt,
	}
	if event.Summary == "" {
		event.Summary = soulAgentPromotionLifecycleEventSummary(event.EventType)
	}
	event.EventID = models.DefaultSoulAgentPromotionLifecycleEventID(event.EventType, occurredAt, event.AgentID, event.ConversationID, event.OperationID)
	return event
}

func soulAgentPromotionLifecycleEventSummary(eventType string) string {
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case models.SoulAgentPromotionEventTypeRequestCreated:
		return "promotion request created"
	case models.SoulAgentPromotionEventTypeRequestApproved:
		return "promotion request approved"
	case models.SoulAgentPromotionEventTypeMintExecuted:
		return "mint execution recorded"
	case models.SoulAgentPromotionEventTypeReviewStarted:
		return "mint conversation review started"
	case models.SoulAgentPromotionEventTypeFinalizeReady:
		return "review draft ready for finalize"
	case models.SoulAgentPromotionEventTypeGraduated:
		return "promotion graduated"
	default:
		return "promotion updated"
	}
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func (s *Server) saveSoulAgentPromotionLifecycleEvent(ctx context.Context, event *models.SoulAgentPromotionLifecycleEvent) *apptheory.AppError {
	if s == nil || s.store == nil || s.store.DB == nil || event == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err := event.UpdateKeys(); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to normalize promotion lifecycle event"}
	}
	if err := s.store.DB.WithContext(ctx).Model(event).Create(); err != nil {
		return &apptheory.AppError{Code: "app.internal", Message: "failed to emit promotion lifecycle event"}
	}
	return nil
}

func shouldEmitSoulPromotionReviewStartedEvent(previous *models.SoulAgentPromotion, next *models.SoulAgentPromotion, conversationID string) bool {
	if next == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(next.ReviewStatus), models.SoulAgentPromotionReviewStatusConversationInProgress) {
		return false
	}
	conversationID = strings.TrimSpace(conversationID)
	if previous == nil {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(previous.ReviewStatus), models.SoulAgentPromotionReviewStatusConversationInProgress) {
		return true
	}
	return strings.TrimSpace(previous.LatestConversationID) != conversationID
}

func promotionFromLifecycleEvent(event *models.SoulAgentPromotionLifecycleEvent) *models.SoulAgentPromotion {
	if event == nil {
		return nil
	}
	return &models.SoulAgentPromotion{
		AgentID:                  event.AgentID,
		RegistrationID:           event.RegistrationID,
		RequestedBy:              event.RequestedBy,
		Domain:                   event.Domain,
		LocalID:                  event.LocalID,
		Wallet:                   event.Wallet,
		Stage:                    event.Stage,
		RequestStatus:            event.RequestStatus,
		ReviewStatus:             event.ReviewStatus,
		ApprovalStatus:           event.ApprovalStatus,
		ReadinessStatus:          event.ReadinessStatus,
		MintOperationID:          event.MintOperationID,
		MintOperationStatus:      event.MintOperationStatus,
		PrincipalAddress:         event.PrincipalAddress,
		LatestConversationID:     event.LatestConversationID,
		LatestConversationStatus: event.LatestConversationStatus,
		LatestReviewSHA256:       event.LatestReviewSHA256,
		LatestBoundaryCount:      event.LatestBoundaryCount,
		LatestCapabilityCount:    event.LatestCapabilityCount,
		PublishedVersion:         event.PublishedVersion,
		RequestedAt:              event.RequestedAt,
		VerifiedAt:               event.VerifiedAt,
		ApprovedAt:               event.ApprovedAt,
		MintedAt:                 event.MintedAt,
		ReviewStartedAt:          event.ReviewStartedAt,
		ReviewReadyAt:            event.ReviewReadyAt,
		GraduatedAt:              event.GraduatedAt,
		CreatedAt:                event.CreatedAt,
		UpdatedAt:                event.UpdatedAt,
	}
}

func (s *Server) buildSoulAgentPromotionLifecycleEventView(event *models.SoulAgentPromotionLifecycleEvent) soulAgentPromotionLifecycleEventView {
	if event == nil {
		return soulAgentPromotionLifecycleEventView{}
	}
	return soulAgentPromotionLifecycleEventView{
		EventID:        strings.TrimSpace(event.EventID),
		EventType:      strings.TrimSpace(event.EventType),
		Summary:        strings.TrimSpace(event.Summary),
		OccurredAt:     event.OccurredAt,
		RequestID:      strings.TrimSpace(event.RequestID),
		OperationID:    strings.TrimSpace(event.OperationID),
		ConversationID: strings.TrimSpace(event.ConversationID),
		Promotion:      s.buildSoulAgentPromotionView(promotionFromLifecycleEvent(event)),
	}
}

func (s *Server) listSoulAgentPromotionLifecycleEvents(ctx *apptheory.Context, agentIDHex string) (*apptheory.Response, error) {
	if _, appErr := s.loadSoulAgentPromotionForAccess(ctx, agentIDHex); appErr != nil {
		return nil, appErr
	}

	cursor, limit := soulPublicCursorAndLimit(ctx)
	var events []*models.SoulAgentPromotionLifecycleEvent
	qb := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulAgentPromotionLifecycleEvent{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentIDHex)).
		Where("SK", "BEGINS_WITH", "EVENT#").
		OrderBy("SK", "DESC").
		Limit(limit)
	if cursor != "" {
		qb = qb.Cursor(cursor)
	}

	paged, err := qb.AllPaginated(&events)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list promotion lifecycle events"}
	}

	out := make([]soulAgentPromotionLifecycleEventView, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		out = append(out, s.buildSoulAgentPromotionLifecycleEventView(event))
	}

	nextCursor, hasMore := soulPaginatedResultMeta(paged)
	return apptheory.JSON(http.StatusOK, soulAgentPromotionLifecycleEventListResponse{
		Version:    "1",
		Events:     out,
		Count:      len(out),
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
}

func listSoulRequesterScopedItems[T any, V any](
	s *Server,
	ctx *apptheory.Context,
	model any,
	indexName string,
	pkField string,
	pkPrefix string,
	sortField string,
	failureMessage string,
	build func(*T) V,
) (items []V, nextCursor string, hasMore bool, appErr *apptheory.AppError) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, "", false, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, "", false, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, "", false, appErr
	}

	requestedBy := strings.TrimSpace(ctx.AuthIdentity)
	if requestedBy == "" {
		return nil, "", false, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	cursor, limit := soulPublicCursorAndLimit(ctx)
	var rawItems []*T
	qb := s.store.DB.WithContext(ctx.Context()).
		Model(model).
		Index(indexName).
		Where(pkField, "=", pkPrefix+requestedBy).
		OrderBy(sortField, "DESC").
		Limit(limit)
	if cursor != "" {
		qb = qb.Cursor(cursor)
	}

	paged, err := qb.AllPaginated(&rawItems)
	if err != nil {
		return nil, "", false, &apptheory.AppError{Code: "app.internal", Message: failureMessage}
	}

	items = make([]V, 0, len(rawItems))
	for _, raw := range rawItems {
		if raw == nil {
			continue
		}
		items = append(items, build(raw))
	}

	nextCursor, hasMore = soulPaginatedResultMeta(paged)
	return items, nextCursor, hasMore, nil
}

func (s *Server) handleSoulAgentListPromotionLifecycleEvents(ctx *apptheory.Context) (*apptheory.Response, error) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return nil, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return nil, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return nil, appErr
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return nil, appErr
	}
	return s.listSoulAgentPromotionLifecycleEvents(ctx, agentIDHex)
}

func (s *Server) handleSoulListMyPromotionLifecycleEvents(ctx *apptheory.Context) (*apptheory.Response, error) {
	out, nextCursor, hasMore, appErr := listSoulRequesterScopedItems[models.SoulAgentPromotionLifecycleEvent, soulAgentPromotionLifecycleEventView](
		s,
		ctx,
		&models.SoulAgentPromotionLifecycleEvent{},
		"gsi1",
		"GSI1PK",
		"SOUL_PROMOTION_EVENT_REQUESTER#",
		"GSI1SK",
		"failed to list promotion lifecycle events",
		s.buildSoulAgentPromotionLifecycleEventView,
	)
	if appErr != nil {
		return nil, appErr
	}
	return apptheory.JSON(http.StatusOK, soulAgentPromotionLifecycleEventListResponse{
		Version:    "1",
		Events:     out,
		Count:      len(out),
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
}
