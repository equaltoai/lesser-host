package controlplane

import (
	"context"
	"strings"
	"time"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/commmailbox"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) outboundMailboxCaptureEnabled() bool {
	return s != nil && s.store != nil && s.store.DB != nil && s.mailboxContentStore != nil
}

func (s *Server) captureOutboundMailbox(ctx context.Context, key *models.InstanceKey, req validatedSoulCommSendRequest, messageID string, delivery soulCommSendDelivery, status string, now time.Time) error {
	if !s.outboundMailboxCaptureEnabled() {
		return nil
	}
	if key == nil {
		return nil
	}
	instanceSlug := strings.ToLower(strings.TrimSpace(key.InstanceSlug))
	agentID := strings.ToLower(strings.TrimSpace(req.agentIDHex))
	channel := strings.ToLower(strings.TrimSpace(req.channel))
	messageID = strings.TrimSpace(messageID)
	threadRoot := strings.TrimSpace(req.inReplyTo)
	if threadRoot == "" {
		threadRoot = messageID
	}
	deliveryID := models.SoulCommMailboxDeliveryID(instanceSlug, agentID, models.SoulCommDirectionOutbound, messageID)
	threadID := models.SoulCommMailboxThreadID(instanceSlug, agentID, channel, threadRoot)
	if strings.TrimSpace(req.threadID) != "" {
		threadID = strings.TrimSpace(req.threadID)
	}

	ptr, err := s.mailboxContentStore.PutContent(ctx, commmailbox.ContentInput{
		DeliveryID:      deliveryID,
		InstanceSlug:    instanceSlug,
		AgentID:         agentID,
		MessageID:       messageID,
		Direction:       models.SoulCommDirectionOutbound,
		ChannelType:     channel,
		Body:            req.body,
		ContentMimeType: commmailbox.DefaultContentType(channel),
	})
	if err != nil {
		return err
	}

	msg := &models.SoulCommMailboxMessage{
		DeliveryID:        deliveryID,
		MessageID:         messageID,
		ThreadID:          threadID,
		InstanceSlug:      instanceSlug,
		AgentID:           agentID,
		Direction:         models.SoulCommDirectionOutbound,
		ChannelType:       channel,
		Provider:          delivery.provider,
		ProviderMessageID: delivery.providerMessageID,
		Status:            soulCommSendResultStatus(status),
		FromSoulAgentID:   agentID,
		Subject:           req.subject,
		Preview:           models.SoulCommMailboxPreview(req.body),
		ContentStorage:    ptr.Storage,
		ContentBucket:     ptr.Bucket,
		ContentKey:        ptr.Key,
		ContentSHA256:     ptr.SHA256,
		ContentBytes:      ptr.Bytes,
		ContentMimeType:   ptr.ContentType,
		ContentStoredAt:   now,
		HasContent:        true,
		Read:              true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if channel == commChannelEmail {
		msg.ToAddress = req.to
	} else {
		msg.ToNumber = req.to
	}
	if err := msg.BeforeCreate(); err != nil {
		return err
	}
	if err := s.store.DB.WithContext(ctx).Model(msg).IfNotExists().Create(); err != nil && !theoryErrors.IsConditionFailed(err) {
		return err
	}

	evt := &models.SoulCommMailboxEvent{
		DeliveryID:   deliveryID,
		MessageID:    messageID,
		ThreadID:     threadID,
		InstanceSlug: instanceSlug,
		AgentID:      agentID,
		Direction:    models.SoulCommDirectionOutbound,
		ChannelType:  channel,
		EventType:    models.SoulCommMailboxEventCreated,
		Status:       msg.Status,
		Actor:        "instance:" + instanceSlug,
		DetailsJSON:  `{"source":"soul_comm_send"}`,
		CreatedAt:    now,
	}
	if err := evt.BeforeCreate(); err != nil {
		return err
	}
	if err := s.store.DB.WithContext(ctx).Model(evt).IfNotExists().Create(); err != nil && !theoryErrors.IsConditionFailed(err) {
		return err
	}
	return nil
}
