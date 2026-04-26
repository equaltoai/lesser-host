package controlplane

import (
	"net/http"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type soulAgentCommActivityResponse struct {
	Version    string                      `json:"version"`
	Activities []soulAgentCommActivityItem `json:"activities"`
	Count      int                         `json:"count"`
}

type soulAgentCommQueueResponse struct {
	Version string                   `json:"version"`
	Items   []soulAgentCommQueueItem `json:"items"`
	Count   int                      `json:"count"`
}

type soulAgentCommActivityItem struct {
	AgentID             string                            `json:"agent_id"`
	ActivityID          string                            `json:"activity_id"`
	DeliveryID          string                            `json:"delivery_id,omitempty"`
	ThreadID            string                            `json:"thread_id,omitempty"`
	ChannelType         string                            `json:"channel_type"`
	Direction           string                            `json:"direction"`
	Counterparty        string                            `json:"counterparty,omitempty"`
	Action              string                            `json:"action,omitempty"`
	MessageID           string                            `json:"message_id,omitempty"`
	Status              string                            `json:"status,omitempty"`
	Subject             string                            `json:"subject,omitempty"`
	Preview             string                            `json:"preview,omitempty"`
	Content             soulAgentCommPortalContentSummary `json:"content"`
	Read                bool                              `json:"read"`
	Archived            bool                              `json:"archived"`
	Deleted             bool                              `json:"deleted"`
	BoundaryCheck       string                            `json:"boundary_check,omitempty"`
	PreferenceRespected *bool                             `json:"preference_respected,omitempty"`
	Timestamp           string                            `json:"timestamp"`
}

type soulAgentCommQueueItem struct {
	AgentID               string                            `json:"agent_id"`
	DeliveryID            string                            `json:"delivery_id,omitempty"`
	MessageID             string                            `json:"message_id"`
	ThreadID              string                            `json:"thread_id,omitempty"`
	ChannelType           string                            `json:"channel_type"`
	FromAddress           string                            `json:"from_address,omitempty"`
	FromNumber            string                            `json:"from_number,omitempty"`
	FromSoulAgentID       string                            `json:"from_soul_agent_id,omitempty"`
	FromDisplayName       string                            `json:"from_display_name,omitempty"`
	Subject               string                            `json:"subject,omitempty"`
	Preview               string                            `json:"preview,omitempty"`
	Content               soulAgentCommPortalContentSummary `json:"content"`
	ReceivedAt            string                            `json:"received_at"`
	ScheduledDeliveryTime string                            `json:"scheduled_delivery_time"`
	Status                string                            `json:"status"`
	Read                  bool                              `json:"read"`
	Archived              bool                              `json:"archived"`
	Deleted               bool                              `json:"deleted"`
}

type soulAgentCommPortalContentSummary struct {
	Available bool   `json:"available"`
	Bytes     int64  `json:"bytes,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type soulAgentCommListContext struct {
	AgentIDHex   string
	InstanceSlug string
	Limit        int
}

func (s *Server) requireSoulAgentWithDomainAccess(ctx *apptheory.Context, agentIDHex string) (*models.SoulAgentIdentity, *apptheory.AppError) {
	identity, _, _, appErr := s.requireSoulAgentWithDomainAccessDetails(ctx, agentIDHex)
	return identity, appErr
}

func (s *Server) requireSoulAgentWithDomainAccessDetails(
	ctx *apptheory.Context,
	agentIDHex string,
) (*models.SoulAgentIdentity, *models.Domain, *models.Instance, *apptheory.AppError) {
	if s == nil || ctx == nil {
		return nil, nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	identity, err := s.getSoulAgentIdentity(ctx.Context(), agentIDHex)
	if theoryErrors.IsNotFound(err) {
		return nil, nil, nil, &apptheory.AppError{Code: "app.not_found", Message: "agent not found"}
	}
	if err != nil {
		return nil, nil, nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	domain, instance, accessErr := s.requireSoulDomainAccess(ctx, strings.TrimSpace(identity.Domain))
	if accessErr != nil {
		return nil, nil, nil, accessErr
	}
	return identity, domain, instance, nil
}

func (s *Server) loadSoulAgentCommListContext(ctx *apptheory.Context) (soulAgentCommListContext, *apptheory.AppError) {
	if appErr := s.requireSoulRegistryConfigured(); appErr != nil {
		return soulAgentCommListContext{}, appErr
	}
	if appErr := s.requireSoulPortalPrereqs(ctx); appErr != nil {
		return soulAgentCommListContext{}, appErr
	}
	if appErr := requireStoreDB(s); appErr != nil {
		return soulAgentCommListContext{}, appErr
	}

	agentIDHex, _, appErr := parseSoulAgentIDHex(ctx.Param("agentId"))
	if appErr != nil {
		return soulAgentCommListContext{}, appErr
	}

	_, _, instance, appErr := s.requireSoulAgentWithDomainAccessDetails(ctx, agentIDHex)
	if appErr != nil {
		return soulAgentCommListContext{}, appErr
	}

	return soulAgentCommListContext{
		AgentIDHex:   agentIDHex,
		InstanceSlug: strings.ToLower(strings.TrimSpace(instance.Slug)),
		Limit:        parseLimit(queryFirst(ctx, "limit"), 50, 1, 200),
	}, nil
}

func (s *Server) listSoulAgentCommActivities(
	ctx *apptheory.Context,
	listCtx soulAgentCommListContext,
) ([]soulAgentCommActivityItem, *apptheory.AppError) {
	messages, appErr := s.listSoulAgentCommMailboxRows(ctx, listCtx, "DESC", listCtx.Limit)
	if appErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list communication activity"}
	}

	items := make([]soulAgentCommActivityItem, 0, len(messages))
	for _, msg := range messages {
		if msg == nil || msg.Deleted {
			continue
		}
		items = append(items, soulAgentCommActivityFromMailbox(msg))
	}
	return items, nil
}

func (s *Server) listSoulAgentCommQueueItems(
	ctx *apptheory.Context,
	listCtx soulAgentCommListContext,
) ([]soulAgentCommQueueItem, *apptheory.AppError) {
	messages, appErr := s.listSoulAgentCommMailboxRows(ctx, listCtx, "ASC", soulAgentCommPortalQueueScanLimit(listCtx.Limit))
	if appErr != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to list queued messages"}
	}

	items := make([]soulAgentCommQueueItem, 0, minInt(listCtx.Limit, len(messages)))
	for _, msg := range messages {
		if len(items) >= listCtx.Limit {
			break
		}
		if !isPortalQueuedMailboxItem(msg) {
			continue
		}
		items = append(items, soulAgentCommQueueFromMailbox(msg))
	}
	return items, nil
}

func (s *Server) listSoulAgentCommMailboxRows(
	ctx *apptheory.Context,
	listCtx soulAgentCommListContext,
	order string,
	limit int,
) ([]*models.SoulCommMailboxMessage, *apptheory.AppError) {
	var items []*models.SoulCommMailboxMessage
	order = strings.ToUpper(strings.TrimSpace(order))
	if order != "ASC" {
		order = "DESC"
	}
	if err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulCommMailboxMessage{}).
		Where("PK", "=", models.SoulCommMailboxAgentPK(listCtx.InstanceSlug, listCtx.AgentIDHex)).
		OrderBy("SK", order).
		Limit(limit).
		All(&items); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return items, nil
}

func soulAgentCommActivityFromMailbox(item *models.SoulCommMailboxMessage) soulAgentCommActivityItem {
	return soulAgentCommActivityItem{
		AgentID:      strings.ToLower(strings.TrimSpace(item.AgentID)),
		ActivityID:   mailboxPortalActivityID(item),
		DeliveryID:   strings.TrimSpace(item.DeliveryID),
		ThreadID:     strings.TrimSpace(item.ThreadID),
		ChannelType:  strings.TrimSpace(item.ChannelType),
		Direction:    strings.TrimSpace(item.Direction),
		Counterparty: mailboxPortalCounterparty(item),
		Action:       mailboxPortalAction(item),
		MessageID:    strings.TrimSpace(item.MessageID),
		Status:       strings.TrimSpace(item.Status),
		Subject:      strings.TrimSpace(item.Subject),
		Preview:      strings.TrimSpace(item.Preview),
		Content:      mailboxPortalContentSummary(item),
		Read:         item.Read,
		Archived:     item.Archived,
		Deleted:      item.Deleted,
		Timestamp:    formatMailboxTime(mailboxPortalTime(item.CreatedAt, item.UpdatedAt)),
	}
}

func soulAgentCommQueueFromMailbox(item *models.SoulCommMailboxMessage) soulAgentCommQueueItem {
	return soulAgentCommQueueItem{
		AgentID:               strings.ToLower(strings.TrimSpace(item.AgentID)),
		DeliveryID:            strings.TrimSpace(item.DeliveryID),
		MessageID:             strings.TrimSpace(item.MessageID),
		ThreadID:              strings.TrimSpace(item.ThreadID),
		ChannelType:           strings.TrimSpace(item.ChannelType),
		FromAddress:           strings.TrimSpace(item.FromAddress),
		FromNumber:            strings.TrimSpace(item.FromNumber),
		FromSoulAgentID:       strings.ToLower(strings.TrimSpace(item.FromSoulAgentID)),
		FromDisplayName:       strings.TrimSpace(item.FromDisplayName),
		Subject:               strings.TrimSpace(item.Subject),
		Preview:               strings.TrimSpace(item.Preview),
		Content:               mailboxPortalContentSummary(item),
		ReceivedAt:            formatMailboxTime(mailboxPortalTime(item.CreatedAt, item.UpdatedAt)),
		ScheduledDeliveryTime: formatMailboxTime(mailboxPortalTime(item.UpdatedAt, item.CreatedAt)),
		Status:                strings.TrimSpace(item.Status),
		Read:                  item.Read,
		Archived:              item.Archived,
		Deleted:               item.Deleted,
	}
}

func mailboxPortalContentSummary(item *models.SoulCommMailboxMessage) soulAgentCommPortalContentSummary {
	if item == nil {
		return soulAgentCommPortalContentSummary{}
	}
	return soulAgentCommPortalContentSummary{
		Available: item.HasContent && !item.Deleted,
		Bytes:     item.ContentBytes,
		MimeType:  strings.TrimSpace(item.ContentMimeType),
		SHA256:    strings.TrimSpace(item.ContentSHA256),
	}
}

func mailboxPortalActivityID(item *models.SoulCommMailboxMessage) string {
	for _, candidate := range []string{item.DeliveryID, item.MessageID, item.ThreadID} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return "mailbox"
}

func mailboxPortalCounterparty(item *models.SoulCommMailboxMessage) string {
	if strings.EqualFold(strings.TrimSpace(item.Direction), models.SoulCommDirectionOutbound) {
		return firstNonEmptySoulCommPortal(item.ToSoulAgentID, item.ToAddress, item.ToNumber, item.ToDisplayName)
	}
	return firstNonEmptySoulCommPortal(item.FromSoulAgentID, item.FromAddress, item.FromNumber, item.FromDisplayName)
}

func mailboxPortalAction(item *models.SoulCommMailboxMessage) string {
	status := strings.ToLower(strings.TrimSpace(item.Status))
	if status == models.SoulCommMailboxStatusFailed ||
		status == models.SoulCommMailboxStatusBounced ||
		status == models.SoulCommMailboxStatusDropped {
		return status
	}
	if strings.EqualFold(strings.TrimSpace(item.Direction), models.SoulCommDirectionOutbound) {
		return "send"
	}
	return "receive"
}

func mailboxPortalTime(primary time.Time, fallback time.Time) time.Time {
	if !primary.IsZero() {
		return primary
	}
	return fallback
}

func isPortalQueuedMailboxItem(item *models.SoulCommMailboxMessage) bool {
	if item == nil || item.Deleted {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(item.Direction), models.SoulCommDirectionInbound) &&
		strings.EqualFold(strings.TrimSpace(item.Status), models.SoulCommMailboxStatusQueued)
}

func soulAgentCommPortalQueueScanLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	return minInt(limit*4, 200)
}

func firstNonEmptySoulCommPortal(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *Server) handleSoulAgentCommActivity(ctx *apptheory.Context) (*apptheory.Response, error) {
	listCtx, appErr := s.loadSoulAgentCommListContext(ctx)
	if appErr != nil {
		return nil, appErr
	}

	items, appErr := s.listSoulAgentCommActivities(ctx, listCtx)
	if appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, soulAgentCommActivityResponse{
		Version:    "1",
		Activities: items,
		Count:      len(items),
	})
}

func (s *Server) handleSoulAgentCommQueue(ctx *apptheory.Context) (*apptheory.Response, error) {
	listCtx, appErr := s.loadSoulAgentCommListContext(ctx)
	if appErr != nil {
		return nil, appErr
	}

	items, appErr := s.listSoulAgentCommQueueItems(ctx, listCtx)
	if appErr != nil {
		return nil, appErr
	}

	return apptheory.JSON(http.StatusOK, soulAgentCommQueueResponse{
		Version: "1",
		Items:   items,
		Count:   len(items),
	})
}

func (s *Server) handleSoulAgentCommStatus(ctx *apptheory.Context) (*apptheory.Response, error) {
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
	if _, appErr := s.requireSoulAgentWithDomainAccess(ctx, agentIDHex); appErr != nil {
		return nil, appErr
	}

	messageID := strings.TrimSpace(ctx.Param("messageId"))
	if messageID == "" || len(messageID) > 128 {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "messageId is invalid"}
	}

	rec := &models.SoulCommMessageStatus{MessageID: messageID}
	_ = rec.UpdateKeys()
	var item models.SoulCommMessageStatus
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.SoulCommMessageStatus{}).
		Where("PK", "=", rec.PK).
		Where("SK", "=", rec.SK).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "message not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if strings.ToLower(strings.TrimSpace(item.AgentID)) != agentIDHex {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "message not found"}
	}

	resp, err := apptheory.JSON(http.StatusOK, soulCommStatusJSON(item))
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return resp, nil
}
