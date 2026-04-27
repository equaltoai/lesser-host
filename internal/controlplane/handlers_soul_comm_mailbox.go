package controlplane

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	theoryquery "github.com/theory-cloud/tabletheory/pkg/query"

	"github.com/equaltoai/lesser-host/internal/commmailbox"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const mailboxContentMaxBytes int64 = 1024 * 1024
const mailboxListQueryMaxLength = 128

type soulCommMailboxListResponse struct {
	InstanceSlug string                   `json:"instanceSlug"`
	AgentID      string                   `json:"agentId"`
	Messages     []soulCommMailboxMessage `json:"messages"`
	Count        int                      `json:"count"`
	HasMore      bool                     `json:"hasMore"`
	NextCursor   string                   `json:"nextCursor,omitempty"`
}

type soulCommMailboxGetResponse struct {
	Message soulCommMailboxMessage `json:"message"`
}

type soulCommMailboxContentResponse struct {
	InstanceSlug string `json:"instanceSlug"`
	AgentID      string `json:"agentId"`
	MessageRef   string `json:"messageRef"`
	DeliveryID   string `json:"deliveryId"`
	MessageID    string `json:"messageId"`
	ContentType  string `json:"contentType"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
	Body         string `json:"body"`
}

type soulCommMailboxMessage struct {
	MessageRef        string                 `json:"messageRef"`
	DeliveryID        string                 `json:"deliveryId"`
	MessageID         string                 `json:"messageId"`
	ThreadID          string                 `json:"threadId"`
	Direction         string                 `json:"direction"`
	ChannelType       string                 `json:"channelType"`
	Provider          string                 `json:"provider,omitempty"`
	ProviderMessageID string                 `json:"providerMessageId,omitempty"`
	Status            string                 `json:"status"`
	From              soulCommMailboxParty   `json:"from,omitempty"`
	To                soulCommMailboxParty   `json:"to,omitempty"`
	Subject           string                 `json:"subject,omitempty"`
	Preview           string                 `json:"preview,omitempty"`
	Content           soulCommMailboxContent `json:"content"`
	State             soulCommMailboxState   `json:"state"`
	CreatedAt         string                 `json:"createdAt"`
	UpdatedAt         string                 `json:"updatedAt,omitempty"`
}

type soulCommMailboxParty struct {
	Address     string `json:"address,omitempty"`
	Number      string `json:"number,omitempty"`
	SoulAgentID string `json:"soulAgentId,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

type soulCommMailboxContent struct {
	Available   bool   `json:"available"`
	Bytes       int64  `json:"bytes,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	ContentHref string `json:"contentHref,omitempty"`
}

type soulCommMailboxState struct {
	Read     bool `json:"read"`
	Archived bool `json:"archived"`
	Deleted  bool `json:"deleted"`
}

type mailboxRequestContext struct {
	key      *models.InstanceKey
	agentID  string
	identity *models.SoulAgentIdentity
}

type mailboxListFilters struct {
	limit           int
	cursor          string
	channelType     string
	direction       string
	read            *bool
	archived        *bool
	deleted         *bool
	includeArchived *bool
	includeDeleted  bool
	threadID        string
	query           string
}

type mailboxStateAction struct {
	name        string
	eventDetail string
	apply       func(*models.SoulCommMailboxMessage) bool
}

func (s *Server) handleSoulCommMailboxList(ctx *apptheory.Context) (*apptheory.Response, error) {
	reqCtx, appErr := s.requireMailboxRequestContext(ctx)
	if appErr != nil {
		return nil, appErr
	}

	filters, appErr := parseMailboxListFilters(ctx)
	if appErr != nil {
		return nil, appErr
	}
	items, hasMore, nextCursor, listErr := s.listMailboxMessages(ctx.Context(), reqCtx.key.InstanceSlug, reqCtx.agentID, filters)
	if listErr != nil {
		return nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}

	messages := make([]soulCommMailboxMessage, 0, len(items))
	for _, item := range items {
		if item == nil || !filters.matches(item) {
			continue
		}
		messages = append(messages, mailboxMessageJSON(item))
		if len(messages) >= filters.limit {
			break
		}
	}

	return apptheory.JSON(http.StatusOK, soulCommMailboxListResponse{
		InstanceSlug: strings.ToLower(strings.TrimSpace(reqCtx.key.InstanceSlug)),
		AgentID:      reqCtx.agentID,
		Messages:     messages,
		Count:        len(messages),
		HasMore:      hasMore,
		NextCursor:   nextCursor,
	})
}

func (s *Server) handleSoulCommMailboxGet(ctx *apptheory.Context) (*apptheory.Response, error) {
	reqCtx, appErr := s.requireMailboxRequestContext(ctx)
	if appErr != nil {
		return nil, appErr
	}
	item, appErr := s.loadMailboxMessageByRef(ctx.Context(), reqCtx.key.InstanceSlug, reqCtx.agentID, mailboxMessageRefParam(ctx))
	if appErr != nil {
		return nil, appErr
	}
	if item.Deleted {
		return nil, apptheory.NewAppTheoryError("comm.not_found", "message not found").WithStatusCode(http.StatusNotFound)
	}
	return apptheory.JSON(http.StatusOK, soulCommMailboxGetResponse{Message: mailboxMessageJSON(item)})
}

func (s *Server) handleSoulCommMailboxContent(ctx *apptheory.Context) (*apptheory.Response, error) {
	reqCtx, appErr := s.requireMailboxRequestContext(ctx)
	if appErr != nil {
		return nil, appErr
	}
	item, appErr := s.loadMailboxMessageByRef(ctx.Context(), reqCtx.key.InstanceSlug, reqCtx.agentID, mailboxMessageRefParam(ctx))
	if appErr != nil {
		return nil, appErr
	}
	if item.Deleted || !item.HasContent || strings.TrimSpace(item.ContentKey) == "" {
		return nil, apptheory.NewAppTheoryError("comm.not_found", "content not found").WithStatusCode(http.StatusNotFound)
	}
	if s == nil || s.mailboxContentStore == nil {
		return nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}

	content, err := s.mailboxContentStore.GetContent(ctx.Context(), mailboxContentPointer(item), mailboxContentMaxBytes)
	if err != nil {
		return nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	s.tryWriteAuditLog(ctx, &models.AuditLogEntry{
		Actor:     "instance:" + strings.ToLower(strings.TrimSpace(reqCtx.key.InstanceSlug)),
		Action:    "soul_comm_mailbox.content_read",
		Target:    fmt.Sprintf("mailbox_delivery:%s", strings.TrimSpace(item.DeliveryID)),
		CreatedAt: time.Now().UTC(),
	})

	return apptheory.JSON(http.StatusOK, soulCommMailboxContentResponse{
		InstanceSlug: strings.ToLower(strings.TrimSpace(item.InstanceSlug)),
		AgentID:      strings.ToLower(strings.TrimSpace(item.AgentID)),
		MessageRef:   mailboxMessageRef(item),
		DeliveryID:   strings.TrimSpace(item.DeliveryID),
		MessageID:    strings.TrimSpace(item.MessageID),
		ContentType:  strings.TrimSpace(content.ContentType),
		SHA256:       strings.TrimSpace(content.SHA256),
		Bytes:        content.Bytes,
		Body:         string(content.Body),
	})
}

func (s *Server) handleSoulCommMailboxMarkRead(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleSoulCommMailboxState(ctx, mailboxStateAction{
		name:        "read",
		eventDetail: `{"state":"read"}`,
		apply: func(msg *models.SoulCommMailboxMessage) bool {
			changed := !msg.Read
			msg.Read = true
			return changed
		},
	})
}

func (s *Server) handleSoulCommMailboxMarkUnread(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleSoulCommMailboxState(ctx, mailboxStateAction{
		name:        "unread",
		eventDetail: `{"state":"unread"}`,
		apply: func(msg *models.SoulCommMailboxMessage) bool {
			changed := msg.Read
			msg.Read = false
			return changed
		},
	})
}

func (s *Server) handleSoulCommMailboxArchive(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleSoulCommMailboxState(ctx, mailboxStateAction{
		name:        "archive",
		eventDetail: `{"state":"archived"}`,
		apply: func(msg *models.SoulCommMailboxMessage) bool {
			changed := !msg.Archived
			msg.Archived = true
			return changed
		},
	})
}

func (s *Server) handleSoulCommMailboxUnarchive(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleSoulCommMailboxState(ctx, mailboxStateAction{
		name:        "unarchive",
		eventDetail: `{"state":"unarchived"}`,
		apply: func(msg *models.SoulCommMailboxMessage) bool {
			changed := msg.Archived
			msg.Archived = false
			return changed
		},
	})
}

func (s *Server) handleSoulCommMailboxDelete(ctx *apptheory.Context) (*apptheory.Response, error) {
	return s.handleSoulCommMailboxState(ctx, mailboxStateAction{
		name:        "delete",
		eventDetail: `{"state":"deleted"}`,
		apply: func(msg *models.SoulCommMailboxMessage) bool {
			changed := !msg.Deleted || !msg.Archived
			msg.Deleted = true
			msg.Archived = true
			return changed
		},
	})
}

func (s *Server) handleSoulCommMailboxState(ctx *apptheory.Context, action mailboxStateAction) (*apptheory.Response, error) {
	reqCtx, appErr := s.requireMailboxRequestContext(ctx)
	if appErr != nil {
		return nil, appErr
	}
	item, appErr := s.loadMailboxMessageByRef(ctx.Context(), reqCtx.key.InstanceSlug, reqCtx.agentID, mailboxMessageRefParam(ctx))
	if appErr != nil {
		return nil, appErr
	}
	if action.apply == nil {
		return nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if changed := action.apply(item); changed {
		now := time.Now().UTC()
		item.UpdatedAt = now
		if err := item.BeforeUpdate(); err != nil {
			return nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
		}
		evt := mailboxStateEvent(item, action, reqCtx.key.InstanceSlug, now)
		if err := s.persistMailboxStateChange(ctx.Context(), item, evt); err != nil {
			return nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
		}
	}
	return apptheory.JSON(http.StatusOK, soulCommMailboxGetResponse{Message: mailboxMessageJSON(item)})
}

func (s *Server) requireMailboxRequestContext(ctx *apptheory.Context) (mailboxRequestContext, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil {
		return mailboxRequestContext{}, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if !s.cfg.SoulEnabled {
		return mailboxRequestContext{}, apptheory.NewAppTheoryError(commCodeUnauthorized, "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	key, authErr := s.requireMailboxInstanceKey(ctx)
	if authErr != nil {
		return mailboxRequestContext{}, authErr
	}
	agentID, _, parseErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if parseErr != nil {
		return mailboxRequestContext{}, apptheory.NewAppTheoryError(parseErr.Code, parseErr.Message).WithStatusCode(http.StatusBadRequest)
	}
	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentID)
	if theoryErrors.IsNotFound(err) {
		return mailboxRequestContext{}, apptheory.NewAppTheoryError(commCodeUnauthorized, "unauthorized").WithStatusCode(http.StatusUnauthorized)
	}
	if err != nil {
		return mailboxRequestContext{}, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if accessErr := s.requireCommAgentInstanceAccess(ctx.Context(), key, identity); accessErr != nil {
		return mailboxRequestContext{}, accessErr
	}
	return mailboxRequestContext{key: key, agentID: agentID, identity: identity}, nil
}

func parseMailboxListFilters(ctx *apptheory.Context) (mailboxListFilters, *apptheory.AppTheoryError) {
	filters := mailboxListFilters{
		limit:          parseLimit(queryFirst(ctx, "limit"), 50, 1, 100),
		cursor:         strings.TrimSpace(queryFirst(ctx, "cursor")),
		channelType:    strings.ToLower(strings.TrimSpace(queryFirst(ctx, "channelType"))),
		direction:      strings.ToLower(strings.TrimSpace(queryFirst(ctx, "direction"))),
		threadID:       strings.TrimSpace(queryFirst(ctx, "threadId")),
		query:          strings.ToLower(strings.TrimSpace(queryFirst(ctx, "query"))),
		includeDeleted: queryBool(ctx, "includeDeleted"),
	}
	if appErr := validateMailboxListFilters(filters); appErr != nil {
		return mailboxListFilters{}, appErr
	}
	if queryPresent(ctx, "read") {
		value := queryBool(ctx, "read")
		filters.read = &value
	}
	if queryBool(ctx, "unreadOnly") {
		value := false
		filters.read = &value
	}
	if queryPresent(ctx, "archived") {
		value := queryBool(ctx, "archived")
		filters.archived = &value
	}
	if queryPresent(ctx, "includeArchived") {
		value := queryBool(ctx, "includeArchived")
		filters.includeArchived = &value
	}
	if queryPresent(ctx, "deleted") {
		value := queryBool(ctx, "deleted")
		filters.deleted = &value
		if value {
			filters.includeDeleted = true
		}
	}
	return filters, nil
}

func validateMailboxListFilters(filters mailboxListFilters) *apptheory.AppTheoryError {
	if filters.channelType != "" && filters.channelType != commChannelEmail && filters.channelType != commChannelSMS && filters.channelType != commChannelVoice {
		return apptheory.NewAppTheoryError(commCodeInvalidRequest, "channelType is invalid").WithStatusCode(http.StatusBadRequest)
	}
	if filters.direction != "" && filters.direction != models.SoulCommDirectionInbound && filters.direction != models.SoulCommDirectionOutbound {
		return apptheory.NewAppTheoryError(commCodeInvalidRequest, "direction is invalid").WithStatusCode(http.StatusBadRequest)
	}
	if len(filters.query) > mailboxListQueryMaxLength {
		return apptheory.NewAppTheoryError(commCodeInvalidRequest, "query is too long").WithStatusCode(http.StatusBadRequest)
	}
	if appErr := validateMailboxListCursor(filters.cursor); appErr != nil {
		return appErr
	}
	return nil
}

func validateMailboxListCursor(cursor string) *apptheory.AppTheoryError {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return nil
	}
	decoded, err := theoryquery.DecodeCursor(cursor)
	if err != nil || decoded == nil || len(decoded.LastEvaluatedKey) == 0 {
		return apptheory.NewAppTheoryError(commCodeInvalidRequest, "cursor is invalid").WithStatusCode(http.StatusBadRequest)
	}
	if _, err := decoded.ToAttributeValues(); err != nil {
		return apptheory.NewAppTheoryError(commCodeInvalidRequest, "cursor is invalid").WithStatusCode(http.StatusBadRequest)
	}
	return nil
}

func (f mailboxListFilters) queryLimit() int {
	if f.hasPostQueryFilters() {
		scanLimit := f.limit * 4
		if scanLimit < 25 {
			scanLimit = 25
		}
		if scanLimit > 200 {
			scanLimit = 200
		}
		return scanLimit
	}
	return f.limit
}

func (f mailboxListFilters) hasPostQueryFilters() bool {
	return f.channelType != "" ||
		f.direction != "" ||
		f.read != nil ||
		f.archived != nil ||
		f.deleted != nil ||
		f.includeArchived != nil ||
		f.includeDeleted ||
		f.query != ""
}

func (f mailboxListFilters) matches(item *models.SoulCommMailboxMessage) bool {
	if item == nil {
		return false
	}
	return f.matchesChannelAndDirection(item) && f.matchesReadArchiveState(item) && f.matchesDeletedState(item) && f.matchesQuery(item)
}

func (f mailboxListFilters) matchesChannelAndDirection(item *models.SoulCommMailboxMessage) bool {
	if f.channelType != "" && !strings.EqualFold(strings.TrimSpace(item.ChannelType), f.channelType) {
		return false
	}
	if f.direction != "" && !strings.EqualFold(strings.TrimSpace(item.Direction), f.direction) {
		return false
	}
	return true
}

func (f mailboxListFilters) matchesReadArchiveState(item *models.SoulCommMailboxMessage) bool {
	if f.read != nil && item.Read != *f.read {
		return false
	}
	if f.archived != nil && item.Archived != *f.archived {
		return false
	}
	if f.includeArchived != nil && !*f.includeArchived && item.Archived {
		return false
	}
	return true
}

func (f mailboxListFilters) matchesDeletedState(item *models.SoulCommMailboxMessage) bool {
	if f.deleted != nil {
		if item.Deleted != *f.deleted {
			return false
		}
	} else if item.Deleted && !f.includeDeleted {
		return false
	}
	return true
}

func (f mailboxListFilters) matchesQuery(item *models.SoulCommMailboxMessage) bool {
	return f.query == "" || mailboxMetadataMatchesQuery(item, f.query)
}

func mailboxMetadataMatchesQuery(item *models.SoulCommMailboxMessage, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if item == nil || query == "" {
		return true
	}
	fields := []string{
		item.DeliveryID,
		item.MessageID,
		item.ThreadID,
		item.ProviderMessageID,
		item.Subject,
		item.Preview,
		item.FromAddress,
		item.FromNumber,
		item.FromSoulAgentID,
		item.FromDisplayName,
		item.ToAddress,
		item.ToNumber,
		item.ToSoulAgentID,
		item.ToDisplayName,
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(strings.TrimSpace(field)), query) {
			return true
		}
	}
	return false
}

func (s *Server) listMailboxMessages(ctx context.Context, instanceSlug string, agentID string, filters mailboxListFilters) ([]*models.SoulCommMailboxMessage, bool, string, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, false, "", fmt.Errorf("store not configured")
	}
	items := []*models.SoulCommMailboxMessage{}
	qb := s.store.DB.WithContext(ctx).
		Model(&models.SoulCommMailboxMessage{})
	if strings.TrimSpace(filters.threadID) != "" {
		qb = qb.Index("gsi2").
			Where("gsi2PK", "=", models.SoulCommMailboxThreadPK(instanceSlug, agentID, filters.threadID)).
			OrderBy("gsi2SK", "DESC")
	} else {
		qb = qb.Where("PK", "=", models.SoulCommMailboxAgentPK(instanceSlug, agentID)).
			OrderBy("SK", "DESC")
	}
	qb = qb.Limit(filters.queryLimit())
	if strings.TrimSpace(filters.cursor) != "" {
		qb = qb.Cursor(strings.TrimSpace(filters.cursor))
	}
	paged, err := qb.AllPaginated(&items)
	if err != nil {
		return nil, false, "", err
	}
	if paged == nil {
		return items, false, "", nil
	}
	return items, paged.HasMore, strings.TrimSpace(paged.NextCursor), nil
}

func (s *Server) loadMailboxMessage(ctx context.Context, instanceSlug string, agentID string, deliveryID string) (*models.SoulCommMailboxMessage, *apptheory.AppTheoryError) {
	return s.loadMailboxMessageByDeliveryID(ctx, instanceSlug, agentID, deliveryID)
}

func (s *Server) loadMailboxMessageByRef(ctx context.Context, instanceSlug string, agentID string, messageRef string) (*models.SoulCommMailboxMessage, *apptheory.AppTheoryError) {
	messageRef = strings.TrimSpace(messageRef)
	if messageRef == "" {
		return nil, apptheory.NewAppTheoryError(commCodeInvalidRequest, "messageRef is required").WithStatusCode(http.StatusBadRequest)
	}

	if item, appErr := s.loadMailboxMessageByDeliveryID(ctx, instanceSlug, agentID, messageRef); appErr == nil {
		return item, nil
	} else if appErr.StatusCode != http.StatusNotFound {
		return nil, appErr
	} else if strings.HasPrefix(messageRef, "comm-delivery-") {
		return nil, appErr
	}

	candidates := []string{
		models.SoulCommMailboxDeliveryID(instanceSlug, agentID, models.SoulCommDirectionInbound, messageRef),
		models.SoulCommMailboxDeliveryID(instanceSlug, agentID, models.SoulCommDirectionOutbound, messageRef),
	}
	var matched *models.SoulCommMailboxMessage
	for _, deliveryID := range candidates {
		if deliveryID == messageRef {
			continue
		}
		item, appErr := s.loadMailboxMessageByDeliveryID(ctx, instanceSlug, agentID, deliveryID)
		if appErr != nil {
			if appErr.StatusCode == http.StatusNotFound {
				continue
			}
			return nil, appErr
		}
		if strings.TrimSpace(item.MessageID) != messageRef {
			continue
		}
		if matched != nil && !strings.EqualFold(strings.TrimSpace(matched.DeliveryID), strings.TrimSpace(item.DeliveryID)) {
			return nil, apptheory.NewAppTheoryError("comm.ambiguous_message_ref", "messageRef is ambiguous; use deliveryId").WithStatusCode(http.StatusConflict)
		}
		matched = item
	}
	if matched != nil {
		return matched, nil
	}
	return nil, apptheory.NewAppTheoryError("comm.not_found", "message not found").WithStatusCode(http.StatusNotFound)
}

func (s *Server) loadMailboxMessageByDeliveryID(ctx context.Context, instanceSlug string, agentID string, deliveryID string) (*models.SoulCommMailboxMessage, *apptheory.AppTheoryError) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	deliveryID = strings.TrimSpace(deliveryID)
	if deliveryID == "" {
		return nil, apptheory.NewAppTheoryError(commCodeInvalidRequest, "deliveryId is required").WithStatusCode(http.StatusBadRequest)
	}

	var item models.SoulCommMailboxMessage
	err := s.store.DB.WithContext(ctx).
		Model(&models.SoulCommMailboxMessage{}).
		Index("gsi1").
		Where("gsi1PK", "=", models.SoulCommMailboxDeliveryPK(deliveryID)).
		Where("gsi1SK", "=", "CURRENT").
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, apptheory.NewAppTheoryError("comm.not_found", "message not found").WithStatusCode(http.StatusNotFound)
	}
	if err != nil {
		return nil, apptheory.NewAppTheoryError(commCodeInternal, "internal error").WithStatusCode(http.StatusInternalServerError)
	}
	if !strings.EqualFold(strings.TrimSpace(item.InstanceSlug), strings.TrimSpace(instanceSlug)) || !strings.EqualFold(strings.TrimSpace(item.AgentID), strings.TrimSpace(agentID)) {
		return nil, apptheory.NewAppTheoryError("comm.not_found", "message not found").WithStatusCode(http.StatusNotFound)
	}
	return &item, nil
}

func mailboxStateEvent(item *models.SoulCommMailboxMessage, action mailboxStateAction, instanceSlug string, now time.Time) *models.SoulCommMailboxEvent {
	return &models.SoulCommMailboxEvent{
		DeliveryID:   strings.TrimSpace(item.DeliveryID),
		MessageID:    strings.TrimSpace(item.MessageID),
		ThreadID:     strings.TrimSpace(item.ThreadID),
		InstanceSlug: strings.ToLower(strings.TrimSpace(item.InstanceSlug)),
		AgentID:      strings.ToLower(strings.TrimSpace(item.AgentID)),
		Direction:    strings.TrimSpace(item.Direction),
		ChannelType:  strings.TrimSpace(item.ChannelType),
		EventType:    models.SoulCommMailboxEventStateChanged,
		Status:       strings.TrimSpace(item.Status),
		Actor:        "instance:" + strings.ToLower(strings.TrimSpace(instanceSlug)),
		DetailsJSON:  strings.TrimSpace(action.eventDetail),
		CreatedAt:    now,
	}
}

func (s *Server) persistMailboxStateChange(ctx context.Context, item *models.SoulCommMailboxMessage, evt *models.SoulCommMailboxEvent) error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not configured")
	}
	if evt != nil {
		if err := evt.BeforeCreate(); err != nil {
			return err
		}
	}
	return s.store.DB.TransactWrite(ctx, func(tx core.TransactionBuilder) error {
		tx.Update(item, []string{"Read", "Archived", "Deleted", "UpdatedAt"}, tabletheory.IfExists())
		if evt != nil {
			tx.Create(evt)
		}
		return nil
	})
}

func mailboxMessageJSON(item *models.SoulCommMailboxMessage) soulCommMailboxMessage {
	if item == nil {
		return soulCommMailboxMessage{}
	}
	return soulCommMailboxMessage{
		MessageRef:        mailboxMessageRef(item),
		DeliveryID:        strings.TrimSpace(item.DeliveryID),
		MessageID:         strings.TrimSpace(item.MessageID),
		ThreadID:          strings.TrimSpace(item.ThreadID),
		Direction:         strings.TrimSpace(item.Direction),
		ChannelType:       strings.TrimSpace(item.ChannelType),
		Provider:          strings.TrimSpace(item.Provider),
		ProviderMessageID: strings.TrimSpace(item.ProviderMessageID),
		Status:            strings.TrimSpace(item.Status),
		From: soulCommMailboxParty{
			Address:     strings.TrimSpace(item.FromAddress),
			Number:      strings.TrimSpace(item.FromNumber),
			SoulAgentID: strings.TrimSpace(item.FromSoulAgentID),
			DisplayName: strings.TrimSpace(item.FromDisplayName),
		},
		To: soulCommMailboxParty{
			Address:     strings.TrimSpace(item.ToAddress),
			Number:      strings.TrimSpace(item.ToNumber),
			SoulAgentID: strings.TrimSpace(item.ToSoulAgentID),
			DisplayName: strings.TrimSpace(item.ToDisplayName),
		},
		Subject: strings.TrimSpace(item.Subject),
		Preview: strings.TrimSpace(item.Preview),
		Content: soulCommMailboxContent{
			Available:   item.HasContent,
			Bytes:       item.ContentBytes,
			MimeType:    strings.TrimSpace(item.ContentMimeType),
			SHA256:      strings.TrimSpace(item.ContentSHA256),
			ContentHref: mailboxContentHref(item),
		},
		State: soulCommMailboxState{
			Read:     item.Read,
			Archived: item.Archived,
			Deleted:  item.Deleted,
		},
		CreatedAt: formatMailboxTime(item.CreatedAt),
		UpdatedAt: formatMailboxTime(item.UpdatedAt),
	}
}

func mailboxContentPointer(item *models.SoulCommMailboxMessage) commmailbox.ContentPointer {
	if item == nil {
		return commmailbox.ContentPointer{}
	}
	return commmailbox.ContentPointer{
		Storage:     strings.TrimSpace(item.ContentStorage),
		Bucket:      strings.TrimSpace(item.ContentBucket),
		Key:         strings.TrimSpace(item.ContentKey),
		SHA256:      strings.TrimSpace(item.ContentSHA256),
		Bytes:       item.ContentBytes,
		ContentType: strings.TrimSpace(item.ContentMimeType),
	}
}

func mailboxContentHref(item *models.SoulCommMailboxMessage) string {
	if item == nil || !item.HasContent || item.Deleted {
		return ""
	}
	return fmt.Sprintf("/api/v1/soul/comm/mailbox/%s/messages/%s/content", strings.TrimSpace(item.AgentID), mailboxMessageRef(item))
}

func mailboxMessageRef(item *models.SoulCommMailboxMessage) string {
	if item == nil {
		return ""
	}
	return strings.TrimSpace(item.DeliveryID)
}

func mailboxMessageRefParam(ctx *apptheory.Context) string {
	if ctx == nil {
		return ""
	}
	if ref := strings.TrimSpace(ctx.Param("messageRef")); ref != "" {
		return ref
	}
	return strings.TrimSpace(ctx.Param("deliveryId"))
}

func formatMailboxTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func queryBool(ctx *apptheory.Context, key string) bool {
	switch strings.ToLower(strings.TrimSpace(queryFirst(ctx, key))) {
	case "1", "true", "yes", "y":
		return true
	default:
		return false
	}
}

func queryPresent(ctx *apptheory.Context, key string) bool {
	if ctx == nil {
		return false
	}
	if ctx.Request.Query == nil {
		return false
	}
	_, ok := ctx.Request.Query[key]
	return ok
}
