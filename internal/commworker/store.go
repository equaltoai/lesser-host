package commworker

import (
	"context"
	"fmt"
	"strings"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type commStore interface {
	LookupAgentByEmail(ctx context.Context, email string) (string, bool, error)
	LookupAgentByPhone(ctx context.Context, phone string) (string, bool, error)

	GetSoulAgentIdentity(ctx context.Context, agentID string) (*models.SoulAgentIdentity, bool, error)
	GetSoulAgentChannel(ctx context.Context, agentID string, channelType string) (*models.SoulAgentChannel, bool, error)
	GetSoulAgentContactPreferences(ctx context.Context, agentID string) (*models.SoulAgentContactPreferences, bool, error)

	ListRecentCommActivities(ctx context.Context, agentID string, limit int) ([]*models.SoulAgentCommActivity, error)
	PutCommActivity(ctx context.Context, item *models.SoulAgentCommActivity) error
	PutCommQueue(ctx context.Context, item *models.SoulAgentCommQueue) error

	PutMailboxMessage(ctx context.Context, item *models.SoulCommMailboxMessage) error
	PutMailboxEvent(ctx context.Context, item *models.SoulCommMailboxEvent) error
	UpdateMailboxMessageStatus(ctx context.Context, item *models.SoulCommMailboxMessage) error

	GetDomain(ctx context.Context, domain string) (*models.Domain, bool, error)
	GetInstance(ctx context.Context, slug string) (*models.Instance, bool, error)
}

type dynamoStore struct {
	db store.DB
}

func newDynamoStore(st *store.Store) *dynamoStore {
	if st == nil {
		return &dynamoStore{}
	}
	return &dynamoStore{db: st.DB}
}

func (s *dynamoStore) LookupAgentByEmail(ctx context.Context, email string) (string, bool, error) {
	if s == nil || s.db == nil {
		return "", false, fmt.Errorf("store not initialized")
	}
	idx := &models.SoulEmailAgentIndex{Email: strings.TrimSpace(email)}
	_ = idx.UpdateKeys()

	var item models.SoulEmailAgentIndex
	return s.lookupAgentIndex(ctx, &models.SoulEmailAgentIndex{}, idx.GetPK(), idx.GetSK(), &item, func() string {
		return item.AgentID
	})
}

func (s *dynamoStore) LookupAgentByPhone(ctx context.Context, phone string) (string, bool, error) {
	if s == nil || s.db == nil {
		return "", false, fmt.Errorf("store not initialized")
	}
	idx := &models.SoulPhoneAgentIndex{Phone: strings.TrimSpace(phone)}
	_ = idx.UpdateKeys()

	var item models.SoulPhoneAgentIndex
	return s.lookupAgentIndex(ctx, &models.SoulPhoneAgentIndex{}, idx.GetPK(), idx.GetSK(), &item, func() string {
		return item.AgentID
	})
}

func (s *dynamoStore) lookupAgentIndex(ctx context.Context, model any, pk string, sk string, dest any, agentID func() string) (string, bool, error) {
	err := s.db.WithContext(ctx).
		Model(model).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		First(dest)
	if theoryErrors.IsNotFound(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	normalized := strings.ToLower(strings.TrimSpace(agentID()))
	return normalized, normalized != "", nil
}

func (s *dynamoStore) GetSoulAgentIdentity(ctx context.Context, agentID string) (*models.SoulAgentIdentity, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, fmt.Errorf("store not initialized")
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" {
		return nil, false, fmt.Errorf("agentID is required")
	}

	var item models.SoulAgentIdentity
	err := s.db.WithContext(ctx).
		Model(&models.SoulAgentIdentity{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "=", "IDENTITY").
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &item, true, nil
}

func (s *dynamoStore) GetSoulAgentChannel(ctx context.Context, agentID string, channelType string) (*models.SoulAgentChannel, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, fmt.Errorf("store not initialized")
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	channelType = strings.ToLower(strings.TrimSpace(channelType))
	if agentID == "" || channelType == "" {
		return nil, false, fmt.Errorf("agentID and channelType are required")
	}

	var item models.SoulAgentChannel
	err := s.db.WithContext(ctx).
		Model(&models.SoulAgentChannel{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "=", fmt.Sprintf("CHANNEL#%s", channelType)).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &item, true, nil
}

func (s *dynamoStore) GetSoulAgentContactPreferences(ctx context.Context, agentID string) (*models.SoulAgentContactPreferences, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, fmt.Errorf("store not initialized")
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" {
		return nil, false, fmt.Errorf("agentID is required")
	}

	var item models.SoulAgentContactPreferences
	err := s.db.WithContext(ctx).
		Model(&models.SoulAgentContactPreferences{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "=", "CONTACT_PREFERENCES").
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &item, true, nil
}

func (s *dynamoStore) ListRecentCommActivities(ctx context.Context, agentID string, limit int) ([]*models.SoulAgentCommActivity, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	if agentID == "" {
		return nil, fmt.Errorf("agentID is required")
	}
	if limit <= 0 {
		limit = 250
	}
	if limit > 1000 {
		limit = 1000
	}

	var items []*models.SoulAgentCommActivity
	err := s.db.WithContext(ctx).
		Model(&models.SoulAgentCommActivity{}).
		Where("PK", "=", fmt.Sprintf("SOUL#AGENT#%s", agentID)).
		Where("SK", "BEGINS_WITH", "COMM#").
		OrderBy("SK", "DESC").
		Limit(limit).
		All(&items)
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (s *dynamoStore) PutCommActivity(ctx context.Context, item *models.SoulAgentCommActivity) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store not initialized")
	}
	if item == nil {
		return fmt.Errorf("activity is nil")
	}
	return s.db.WithContext(ctx).Model(item).CreateOrUpdate()
}

func (s *dynamoStore) PutCommQueue(ctx context.Context, item *models.SoulAgentCommQueue) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store not initialized")
	}
	if item == nil {
		return fmt.Errorf("queue item is nil")
	}
	return s.db.WithContext(ctx).Model(item).CreateOrUpdate()
}

func (s *dynamoStore) PutMailboxMessage(ctx context.Context, item *models.SoulCommMailboxMessage) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store not initialized")
	}
	if item == nil {
		return fmt.Errorf("mailbox message is nil")
	}
	err := s.db.WithContext(ctx).Model(item).IfNotExists().Create()
	if theoryErrors.IsConditionFailed(err) {
		return nil
	}
	return err
}

func (s *dynamoStore) PutMailboxEvent(ctx context.Context, item *models.SoulCommMailboxEvent) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store not initialized")
	}
	if item == nil {
		return fmt.Errorf("mailbox event is nil")
	}
	err := s.db.WithContext(ctx).Model(item).IfNotExists().Create()
	if theoryErrors.IsConditionFailed(err) {
		return nil
	}
	return err
}

func (s *dynamoStore) UpdateMailboxMessageStatus(ctx context.Context, item *models.SoulCommMailboxMessage) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store not initialized")
	}
	if item == nil {
		return fmt.Errorf("mailbox message is nil")
	}
	return s.db.WithContext(ctx).Model(item).IfExists().Update("Status", "UpdatedAt")
}

func (s *dynamoStore) GetDomain(ctx context.Context, domain string) (*models.Domain, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, fmt.Errorf("store not initialized")
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return nil, false, fmt.Errorf("domain is required")
	}
	var item models.Domain
	err := s.db.WithContext(ctx).
		Model(&models.Domain{}).
		Where("PK", "=", fmt.Sprintf("DOMAIN#%s", domain)).
		Where("SK", "=", models.SKMetadata).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &item, true, nil
}

func (s *dynamoStore) GetInstance(ctx context.Context, slug string) (*models.Instance, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, fmt.Errorf("store not initialized")
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return nil, false, fmt.Errorf("slug is required")
	}
	var item models.Instance
	err := s.db.WithContext(ctx).
		Model(&models.Instance{}).
		Where("PK", "=", fmt.Sprintf("INSTANCE#%s", slug)).
		Where("SK", "=", models.SKMetadata).
		First(&item)
	if theoryErrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &item, true, nil
}
