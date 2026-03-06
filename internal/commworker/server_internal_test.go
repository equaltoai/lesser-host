package commworker

import (
	"context"
	"testing"
	"time"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const (
	testAgentBobEmail  = "agent-bob@lessersoul.ai"
	testInstanceAPIKey = "lhk_test"
)

type fakeStore struct {
	emailIndex map[string]string
	phoneIndex map[string]string

	identities map[string]*models.SoulAgentIdentity
	channels   map[string]*models.SoulAgentChannel
	prefs      map[string]*models.SoulAgentContactPreferences

	domains    map[string]*models.Domain
	instances  map[string]*models.Instance

	activities map[string][]*models.SoulAgentCommActivity
	queued     []*models.SoulAgentCommQueue
}

func (f *fakeStore) LookupAgentByEmail(_ context.Context, email string) (string, bool, error) {
	if f == nil {
		return "", false, nil
	}
	if f.emailIndex == nil {
		return "", false, nil
	}
	id, ok := f.emailIndex[email]
	return id, ok, nil
}

func (f *fakeStore) LookupAgentByPhone(_ context.Context, phone string) (string, bool, error) {
	if f == nil {
		return "", false, nil
	}
	if f.phoneIndex == nil {
		return "", false, nil
	}
	id, ok := f.phoneIndex[phone]
	return id, ok, nil
}

func (f *fakeStore) GetSoulAgentIdentity(_ context.Context, agentID string) (*models.SoulAgentIdentity, bool, error) {
	if f == nil || f.identities == nil {
		return nil, false, nil
	}
	it, ok := f.identities[agentID]
	return it, ok, nil
}

func (f *fakeStore) GetSoulAgentChannel(_ context.Context, agentID string, channelType string) (*models.SoulAgentChannel, bool, error) {
	if f == nil || f.channels == nil {
		return nil, false, nil
	}
	key := agentID + "#" + channelType
	it, ok := f.channels[key]
	return it, ok, nil
}

func (f *fakeStore) GetSoulAgentContactPreferences(_ context.Context, agentID string) (*models.SoulAgentContactPreferences, bool, error) {
	if f == nil || f.prefs == nil {
		return nil, false, nil
	}
	it, ok := f.prefs[agentID]
	return it, ok, nil
}

func (f *fakeStore) ListRecentCommActivities(_ context.Context, agentID string, _ int) ([]*models.SoulAgentCommActivity, error) {
	if f == nil || f.activities == nil {
		return nil, nil
	}
	return f.activities[agentID], nil
}

func (f *fakeStore) PutCommActivity(_ context.Context, item *models.SoulAgentCommActivity) error {
	if f == nil {
		return nil
	}
	if f.activities == nil {
		f.activities = map[string][]*models.SoulAgentCommActivity{}
	}
	f.activities[item.AgentID] = append(f.activities[item.AgentID], item)
	return nil
}

func (f *fakeStore) PutCommQueue(_ context.Context, item *models.SoulAgentCommQueue) error {
	if f == nil {
		return nil
	}
	f.queued = append(f.queued, item)
	return nil
}

func (f *fakeStore) GetDomain(_ context.Context, domain string) (*models.Domain, bool, error) {
	if f == nil || f.domains == nil {
		return nil, false, nil
	}
	it, ok := f.domains[domain]
	return it, ok, nil
}

func (f *fakeStore) GetInstance(_ context.Context, slug string) (*models.Instance, bool, error) {
	if f == nil || f.instances == nil {
		return nil, false, nil
	}
	it, ok := f.instances[slug]
	return it, ok, nil
}

func TestProcessInbound_QueuesOutsideAvailabilityWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC) // Monday
	agentID := commStoreTestAgentID
	to := testAgentBobEmail

	fs := &fakeStore{
		emailIndex: map[string]string{to: agentID},
		identities: map[string]*models.SoulAgentIdentity{
			agentID: {AgentID: agentID, Domain: "demo.greater.website", LifecycleStatus: models.SoulAgentStatusActive, Status: models.SoulAgentStatusActive},
		},
		channels: map[string]*models.SoulAgentChannel{
			agentID + "#email": {AgentID: agentID, ChannelType: "email", Identifier: to, Status: models.SoulChannelStatusActive, Verified: true, ProvisionedAt: now.Add(-time.Hour)},
		},
		prefs: map[string]*models.SoulAgentContactPreferences{
			agentID: {
				AgentID:              agentID,
				Preferred:            "email",
				AvailabilitySchedule: "custom",
				AvailabilityTimezone: "UTC",
				AvailabilityWindows: []models.SoulContactAvailabilityWindow{
					{Days: []string{"mon"}, StartTime: "09:00", EndTime: "17:00"},
				},
				RateLimits: map[string]any{
					"email": map[string]any{"maxInboundPerHour": 50, "maxInboundPerDay": 500},
				},
				UpdatedAt: now,
			},
		},
	}

	delivered := false
	s := NewServer(config.Config{Stage: "lab"}, fs, nil, nil)
	s.now = func() time.Time { return now }
	s.fetchInstanceKeyPlaintext = func(context.Context, *models.Instance) (string, error) {
		t.Fatalf("instance key fetch should not be called when queued")
		return "", nil
	}
	s.deliverNotification = func(context.Context, string, string, InboundNotification) error {
		delivered = true
		return nil
	}

	msg := QueueMessage{
		Kind:     QueueMessageKindInbound,
		Provider: "migadu",
		Notification: InboundNotification{
			Type:       "communication:inbound",
			Channel:    "email",
			From:       InboundParty{Address: "alice@example.com"},
			To:         &InboundParty{Address: to},
			Subject:    "Hello",
			Body:       "Test",
			ReceivedAt: now.Format(time.RFC3339Nano),
			MessageID:  "comm-msg-1",
		},
	}

	if err := s.processInbound(context.Background(), "req", msg); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if delivered {
		t.Fatalf("expected not delivered")
	}
	if len(fs.queued) != 1 {
		t.Fatalf("expected 1 queued message, got %d", len(fs.queued))
	}
	if fs.queued[0].ScheduledDeliveryTime.UTC().Format(time.RFC3339) != "2026-03-02T09:00:00Z" {
		t.Fatalf("unexpected scheduled time: %s", fs.queued[0].ScheduledDeliveryTime.UTC().Format(time.RFC3339))
	}
}

func TestProcessInbound_BouncesWhenRateLimited(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	agentID := commStoreTestAgentID
	to := testAgentBobEmail

	fs := &fakeStore{
		emailIndex: map[string]string{to: agentID},
		identities: map[string]*models.SoulAgentIdentity{
			agentID: {AgentID: agentID, Domain: "demo.greater.website", LifecycleStatus: models.SoulAgentStatusActive, Status: models.SoulAgentStatusActive},
		},
		channels: map[string]*models.SoulAgentChannel{
			agentID + "#email": {AgentID: agentID, ChannelType: "email", Identifier: to, Status: models.SoulChannelStatusActive, Verified: true, ProvisionedAt: now.Add(-time.Hour), SecretRef: "/lesser-host/soul/lab/agents/0xagent/channels/email/migadu_password"},
		},
		prefs: map[string]*models.SoulAgentContactPreferences{
			agentID: {
				AgentID:              agentID,
				Preferred:            "email",
				AvailabilitySchedule: "always",
				RateLimits: map[string]any{
					"email": map[string]any{"maxInboundPerHour": 1, "maxInboundPerDay": 10},
				},
				UpdatedAt: now,
			},
		},
		activities: map[string][]*models.SoulAgentCommActivity{
			agentID: {
				{AgentID: agentID, ChannelType: "email", Direction: models.SoulCommDirectionInbound, Action: "receive", Timestamp: now.Add(-10 * time.Minute)},
			},
		},
	}

	bounced := false
	s := NewServer(config.Config{Stage: "lab"}, fs, nil, nil)
	s.now = func() time.Time { return now }
	s.ssmGetParameter = func(context.Context, string) (string, error) { return "pw", nil }
	s.migaduSendSMTP = func(context.Context, string, string, string, []string, []byte) error {
		bounced = true
		return nil
	}
	s.deliverNotification = func(context.Context, string, string, InboundNotification) error {
		t.Fatalf("deliver should not be called when rate-limited")
		return nil
	}

	msg := QueueMessage{
		Kind:     QueueMessageKindInbound,
		Provider: "migadu",
		Notification: InboundNotification{
			Type:       "communication:inbound",
			Channel:    "email",
			From:       InboundParty{Address: "alice@example.com"},
			To:         &InboundParty{Address: to},
			Subject:    "Hello",
			Body:       "Test",
			ReceivedAt: now.Format(time.RFC3339Nano),
			MessageID:  "comm-msg-2",
		},
	}

	if err := s.processInbound(context.Background(), "req", msg); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !bounced {
		t.Fatalf("expected bounce email")
	}
	if len(fs.queued) != 0 {
		t.Fatalf("expected no queued messages, got %d", len(fs.queued))
	}
}

func TestProcessInbound_DeliversToInstance(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	agentID := commStoreTestAgentID
	to := testAgentBobEmail

	fs := &fakeStore{
		emailIndex: map[string]string{to: agentID},
		identities: map[string]*models.SoulAgentIdentity{
			agentID: {AgentID: agentID, Domain: "demo.greater.website", LifecycleStatus: models.SoulAgentStatusActive, Status: models.SoulAgentStatusActive},
		},
		channels: map[string]*models.SoulAgentChannel{
			agentID + "#email": {AgentID: agentID, ChannelType: "email", Identifier: to, Status: models.SoulChannelStatusActive, Verified: true, ProvisionedAt: now.Add(-time.Hour)},
		},
		prefs: map[string]*models.SoulAgentContactPreferences{
			agentID: {AgentID: agentID, Preferred: "email", AvailabilitySchedule: "always", UpdatedAt: now},
		},
		domains: map[string]*models.Domain{
			"demo.greater.website": {Domain: "demo.greater.website", InstanceSlug: "demo"},
		},
		instances: map[string]*models.Instance{
			"demo": {Slug: "demo", HostedBaseDomain: "demo.greater.website", HostedAccountID: "123", LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123:secret:test"},
		},
	}

	var gotURL string
	var gotKey string
	s := NewServer(config.Config{Stage: "lab"}, fs, nil, nil)
	s.now = func() time.Time { return now }
	s.fetchInstanceKeyPlaintext = func(context.Context, *models.Instance) (string, error) { return testInstanceAPIKey, nil }
	s.deliverNotification = func(_ context.Context, deliverURL string, apiKey string, _ InboundNotification) error {
		gotURL = deliverURL
		gotKey = apiKey
		return nil
	}

	msg := QueueMessage{
		Kind:     QueueMessageKindInbound,
		Provider: "migadu",
		Notification: InboundNotification{
			Type:       "communication:inbound",
			Channel:    "email",
			From:       InboundParty{Address: "alice@example.com"},
			To:         &InboundParty{Address: to},
			Subject:    "Hello",
			Body:       "Test",
			ReceivedAt: now.Format(time.RFC3339Nano),
			MessageID:  "comm-msg-3",
		},
	}

	if err := s.processInbound(context.Background(), "req", msg); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if gotKey != testInstanceAPIKey {
		t.Fatalf("expected api key, got %q", gotKey)
	}
	if gotURL != "https://api.dev.demo.greater.website/api/v1/notifications/deliver" {
		t.Fatalf("unexpected deliver url: %q", gotURL)
	}
}
