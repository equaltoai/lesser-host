package commworker

import (
	"context"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/commmailbox"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type inboundMailboxCapture struct {
	agentID      string
	channel      string
	provider     string
	notif        InboundNotification
	inst         *models.Instance
	status       string
	storeContent bool
	actor        string
	detailsJSON  string
}

func (s *Server) mailboxCaptureEnabled() bool {
	return s != nil && s.store != nil && s.mailboxContentStore != nil
}

func (s *Server) captureInboundMailbox(ctx context.Context, in inboundMailboxCapture) error {
	if !s.mailboxCaptureEnabled() || in.inst == nil {
		return nil
	}

	receivedAt := parseRFC3339Time(in.notif.ReceivedAt, s.now())
	messageID := strings.TrimSpace(in.notif.MessageID)
	instanceSlug := strings.ToLower(strings.TrimSpace(in.inst.Slug))
	agentID := strings.ToLower(strings.TrimSpace(in.agentID))
	channel := strings.ToLower(strings.TrimSpace(in.channel))
	status := strings.ToLower(strings.TrimSpace(in.status))
	if status == "" {
		status = models.SoulCommMailboxStatusAccepted
	}
	threadRoot := messageID
	inReplyTo := ""
	if in.notif.InReplyTo != nil {
		inReplyTo = strings.TrimSpace(*in.notif.InReplyTo)
		if inReplyTo != "" {
			threadRoot = inReplyTo
		}
	}
	deliveryID := models.SoulCommMailboxDeliveryID(instanceSlug, agentID, models.SoulCommDirectionInbound, messageID)
	threadID := models.SoulCommMailboxThreadID(instanceSlug, agentID, channel, threadRoot)

	var ptr commmailbox.ContentPointer
	contentStoredAt := time.Time{}
	if in.storeContent {
		var err error
		ptr, err = s.mailboxContentStore.PutContent(ctx, commmailbox.ContentInput{
			DeliveryID:      deliveryID,
			InstanceSlug:    instanceSlug,
			AgentID:         agentID,
			MessageID:       messageID,
			Direction:       models.SoulCommDirectionInbound,
			ChannelType:     channel,
			Body:            in.notif.Body,
			ContentMimeType: in.notif.BodyMimeType,
		})
		if err != nil {
			return err
		}
		contentStoredAt = s.now()
	}

	msg := &models.SoulCommMailboxMessage{
		DeliveryID:        deliveryID,
		MessageID:         messageID,
		ThreadID:          threadID,
		InstanceSlug:      instanceSlug,
		AgentID:           agentID,
		Direction:         models.SoulCommDirectionInbound,
		ChannelType:       channel,
		Provider:          in.provider,
		ProviderMessageID: messageID,
		Status:            status,
		FromAddress:       strings.TrimSpace(in.notif.From.Address),
		FromNumber:        strings.TrimSpace(in.notif.From.Number),
		FromDisplayName:   strings.TrimSpace(in.notif.From.DisplayName),
		ToAddress:         inboundPartyAddress(in.notif.To),
		ToNumber:          inboundPartyNumber(in.notif.To),
		ToSoulAgentID:     agentID,
		Subject:           strings.TrimSpace(in.notif.Subject),
		Preview:           models.SoulCommMailboxPreview(in.notif.Body),
		HasContent:        in.storeContent,
		Read:              false,
		CreatedAt:         receivedAt,
		UpdatedAt:         receivedAt,
	}
	if in.notif.From.SoulAgentID != nil {
		msg.FromSoulAgentID = strings.TrimSpace(*in.notif.From.SoulAgentID)
	}
	if in.notif.To != nil {
		msg.ToDisplayName = strings.TrimSpace(in.notif.To.DisplayName)
	}
	if ptr.Storage != "" {
		msg.ContentStorage = ptr.Storage
		msg.ContentBucket = ptr.Bucket
		msg.ContentKey = ptr.Key
		msg.ContentSHA256 = ptr.SHA256
		msg.ContentBytes = ptr.Bytes
		msg.ContentMimeType = ptr.ContentType
		msg.ContentStoredAt = contentStoredAt
	}
	if err := msg.BeforeCreate(); err != nil {
		return err
	}
	if err := s.store.PutMailboxMessage(ctx, msg); err != nil {
		return err
	}

	eventTime := receivedAt
	if !contentStoredAt.IsZero() && contentStoredAt.After(eventTime) {
		eventTime = contentStoredAt
	}
	return s.store.PutMailboxEvent(ctx, &models.SoulCommMailboxEvent{
		DeliveryID:   deliveryID,
		MessageID:    messageID,
		ThreadID:     threadID,
		InstanceSlug: instanceSlug,
		AgentID:      agentID,
		Direction:    models.SoulCommDirectionInbound,
		ChannelType:  channel,
		EventType:    models.SoulCommMailboxEventCreated,
		Status:       status,
		Actor:        strings.TrimSpace(in.actor),
		DetailsJSON:  strings.TrimSpace(in.detailsJSON),
		CreatedAt:    eventTime,
	})
}

func (s *Server) updateInboundMailboxStatus(ctx context.Context, agentID string, channel string, notif InboundNotification, inst *models.Instance, status string, actor string, detailsJSON string) error {
	if !s.mailboxCaptureEnabled() || inst == nil {
		return nil
	}

	receivedAt := parseRFC3339Time(notif.ReceivedAt, s.now())
	messageID := strings.TrimSpace(notif.MessageID)
	instanceSlug := strings.ToLower(strings.TrimSpace(inst.Slug))
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	channel = strings.ToLower(strings.TrimSpace(channel))
	threadRoot := messageID
	if notif.InReplyTo != nil && strings.TrimSpace(*notif.InReplyTo) != "" {
		threadRoot = strings.TrimSpace(*notif.InReplyTo)
	}
	deliveryID := models.SoulCommMailboxDeliveryID(instanceSlug, agentID, models.SoulCommDirectionInbound, messageID)
	threadID := models.SoulCommMailboxThreadID(instanceSlug, agentID, channel, threadRoot)

	msg := &models.SoulCommMailboxMessage{
		DeliveryID:   deliveryID,
		MessageID:    messageID,
		ThreadID:     threadID,
		InstanceSlug: instanceSlug,
		AgentID:      agentID,
		Direction:    models.SoulCommDirectionInbound,
		ChannelType:  channel,
		Status:       status,
		CreatedAt:    receivedAt,
		UpdatedAt:    s.now(),
	}
	if err := msg.UpdateKeys(); err != nil {
		return err
	}
	if err := s.store.UpdateMailboxMessageStatus(ctx, msg); err != nil {
		return err
	}

	return s.store.PutMailboxEvent(ctx, &models.SoulCommMailboxEvent{
		DeliveryID:   deliveryID,
		MessageID:    messageID,
		ThreadID:     threadID,
		InstanceSlug: instanceSlug,
		AgentID:      agentID,
		Direction:    models.SoulCommDirectionInbound,
		ChannelType:  channel,
		EventType:    models.SoulCommMailboxEventStateChanged,
		Status:       strings.ToLower(strings.TrimSpace(status)),
		Actor:        strings.TrimSpace(actor),
		DetailsJSON:  strings.TrimSpace(detailsJSON),
		CreatedAt:    s.now(),
	})
}

func inboundPartyAddress(p *InboundParty) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.Address)
}

func inboundPartyNumber(p *InboundParty) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.Number)
}
