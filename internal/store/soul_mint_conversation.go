package store

import (
	"context"
	"fmt"
	"strings"

	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

// GetSoulAgentRegistration loads a soul agent registration by id.
func (s *Store) GetSoulAgentRegistration(ctx context.Context, id string) (*models.SoulAgentRegistration, error) {
	if s == nil || s.DB == nil {
		return nil, theoryErrors.ErrItemNotFound
	}
	probe := &models.SoulAgentRegistration{ID: strings.TrimSpace(id)}
	_ = probe.UpdateKeys()
	var item models.SoulAgentRegistration
	if err := s.DB.WithContext(ctx).
		Model(&models.SoulAgentRegistration{}).
		Where("PK", "=", probe.PK).
		Where("SK", "=", probe.SK).
		First(&item); err != nil {
		return nil, err
	}
	return &item, nil
}

// GetDomain loads a managed domain by normalized domain name.
func (s *Store) GetDomain(ctx context.Context, domain string) (*models.Domain, error) {
	if s == nil || s.DB == nil {
		return nil, theoryErrors.ErrItemNotFound
	}
	var item models.Domain
	if err := s.DB.WithContext(ctx).
		Model(&models.Domain{}).
		Where("PK", "=", fmt.Sprintf("DOMAIN#%s", strings.ToLower(strings.TrimSpace(domain)))).
		Where("SK", "=", models.SKMetadata).
		First(&item); err != nil {
		return nil, err
	}
	return &item, nil
}

// GetInstance loads instance metadata by slug.
func (s *Store) GetInstance(ctx context.Context, slug string) (*models.Instance, error) {
	if s == nil || s.DB == nil {
		return nil, theoryErrors.ErrItemNotFound
	}
	var item models.Instance
	if err := s.DB.WithContext(ctx).
		Model(&models.Instance{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE#%s", strings.ToLower(strings.TrimSpace(slug)))).
		Where("SK", "=", models.SKMetadata).
		First(&item); err != nil {
		return nil, err
	}
	return &item, nil
}

// GetSoulAgentMintConversation loads a soul mint conversation by agent and conversation id.
func (s *Store) GetSoulAgentMintConversation(ctx context.Context, agentID string, conversationID string) (*models.SoulAgentMintConversation, error) {
	if s == nil || s.DB == nil {
		return nil, theoryErrors.ErrItemNotFound
	}
	var item models.SoulAgentMintConversation
	if err := s.DB.WithContext(ctx).
		Model(&models.SoulAgentMintConversation{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", strings.ToLower(strings.TrimSpace(agentID)))).
		Where("SK", "=", fmt.Sprintf("MINT_CONVERSATION#%s", strings.TrimSpace(conversationID))).
		ConsistentRead().
		First(&item); err != nil {
		return nil, err
	}
	return &item, nil
}

// PutSoulAgentMintConversation stores a soul mint conversation record.
func (s *Store) PutSoulAgentMintConversation(ctx context.Context, item *models.SoulAgentMintConversation) error {
	if s == nil || s.DB == nil || item == nil {
		return theoryErrors.ErrItemNotFound
	}
	return s.DB.WithContext(ctx).Model(item).CreateOrUpdate()
}

// ListHostedGenesisSessionsByAgent queries all hosted-genesis sessions for an agent
// within a managed instance using the agent-scoped GSI2 index. Results are ordered
// by GSI2SK (createdAt) natively; callers sort by updatedAt in-memory if needed.
// The read is bounded (issue #1061 part B): page-capped GSI2 queries resumed via
// the opaque cursor, failing closed if the partition exceeds the page cap.
func (s *Store) ListHostedGenesisSessionsByAgent(ctx context.Context, instanceSlug string, agentID string) ([]*models.HostedGenesisSession, error) {
	if s == nil || s.DB == nil {
		return nil, theoryErrors.ErrItemNotFound
	}
	items, err := allPartitionItemsBounded[models.HostedGenesisSession](
		s.DB.WithContext(ctx).
			Model(&models.HostedGenesisSession{}).
			Index("gsi2").
			Where("gsi2PK", "=", models.HostedGenesisSessionAgentGSI2PK(instanceSlug, agentID)),
		storePartitionWalkPageSize,
		storePartitionWalkMaxPages,
	)
	if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, err
	}
	return items, nil
}

// GetSoulMintConversationIdempotency loads a hosted-genesis idempotency reservation.
func (s *Store) GetSoulMintConversationIdempotency(ctx context.Context, instanceSlug string, registrationID string, idempotencyKey string) (*models.SoulMintConversationIdempotency, error) {
	if s == nil || s.DB == nil {
		return nil, theoryErrors.ErrItemNotFound
	}
	var item models.SoulMintConversationIdempotency
	if err := s.DB.WithContext(ctx).
		Model(&models.SoulMintConversationIdempotency{}).
		Where("PK", "=", models.SoulMintConversationIdempotencyPK(instanceSlug, registrationID, idempotencyKey)).
		Where("SK", "=", "STATE").
		ConsistentRead().
		First(&item); err != nil {
		return nil, err
	}
	return &item, nil
}
