package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/commmailbox"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type mailboxAPITestDB struct {
	db        *ttmocks.MockExtendedDB
	qKey      *ttmocks.MockQuery
	qIdentity *ttmocks.MockQuery
	qDomain   *ttmocks.MockQuery
	qPrefs    *ttmocks.MockQuery
	qChannel  *ttmocks.MockQuery
	qMsg      *ttmocks.MockQuery
	qAudit    *ttmocks.MockQuery
}

type fakeMailboxAPIContentStore struct{}

func (f fakeMailboxAPIContentStore) PutContent(context.Context, commmailbox.ContentInput) (commmailbox.ContentPointer, error) {
	return commmailbox.ContentPointer{}, nil
}

func (f fakeMailboxAPIContentStore) GetContent(context.Context, commmailbox.ContentPointer, int64) (commmailbox.ContentOutput, error) {
	return commmailbox.ContentOutput{Body: []byte("Full body"), ContentType: "text/plain", SHA256: "sha256-body", Bytes: int64(len("Full body"))}, nil
}

func TestHandleSoulCommMailboxListRedactsContent(t *testing.T) {
	t.Parallel()

	fixture := newMailboxAPITestDB()
	expectMailboxAPIAccess(t, fixture, soulLifecycleTestAgentIDHex)
	fixture.qMsg.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulCommMailboxMessage")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "cursor-2"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulCommMailboxMessage](t, args, 0)
		*dest = []*models.SoulCommMailboxMessage{mailboxAPITestMessage(soulLifecycleTestAgentIDHex)}
	}).Once()

	s := newMailboxAPITestServer(fixture)
	resp, err := s.handleSoulCommMailboxList(newMailboxAPIContext(soulLifecycleTestAgentIDHex, "", map[string][]string{"limit": {"10"}}))
	if err != nil {
		t.Fatalf("handleSoulCommMailboxList: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
	body := string(resp.Body)
	if strings.Contains(body, "Full body") || strings.Contains(body, "contentKey") || strings.Contains(body, "mailbox-bucket") {
		t.Fatalf("list leaked content or storage pointer: %s", body)
	}
	var out soulCommMailboxListResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || !out.HasMore || out.NextCursor != "cursor-2" {
		t.Fatalf("unexpected page metadata: %#v", out)
	}
	if out.Messages[0].Preview != "redacted preview" || out.Messages[0].Content.ContentHref == "" {
		t.Fatalf("unexpected message summary: %#v", out.Messages[0])
	}
}

func TestHandleSoulCommMailboxContentFetchesExplicitBody(t *testing.T) {
	t.Parallel()

	fixture := newMailboxAPITestDB()
	expectMailboxAPIAccess(t, fixture, soulLifecycleTestAgentIDHex)
	expectMailboxMessageLoad(t, fixture.qMsg, mailboxAPITestMessage(soulLifecycleTestAgentIDHex))
	fixture.qAudit.On("Create").Return(nil).Once()

	s := newMailboxAPITestServer(fixture)
	s.mailboxContentStore = fakeMailboxAPIContentStore{}
	resp, err := s.handleSoulCommMailboxContent(newMailboxAPIContext(soulLifecycleTestAgentIDHex, "comm-delivery-1", nil))
	if err != nil {
		t.Fatalf("handleSoulCommMailboxContent: %v", err)
	}
	var out soulCommMailboxContentResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Body != "Full body" || out.DeliveryID != "comm-delivery-1" || out.SHA256 != "sha256-body" {
		t.Fatalf("unexpected content response: %#v", out)
	}
}

func TestHandleSoulCommMailboxGetRejectsCrossInstanceDelivery(t *testing.T) {
	t.Parallel()

	fixture := newMailboxAPITestDB()
	expectMailboxAPIAccess(t, fixture, soulLifecycleTestAgentIDHex)
	msg := mailboxAPITestMessage(soulLifecycleTestAgentIDHex)
	msg.InstanceSlug = "other"
	expectMailboxMessageLoad(t, fixture.qMsg, msg)

	s := newMailboxAPITestServer(fixture)
	_, err := s.handleSoulCommMailboxGet(newMailboxAPIContext(soulLifecycleTestAgentIDHex, "comm-delivery-1", nil))
	assertCommTheoryErrorCode(t, err, "comm.not_found", http.StatusNotFound)
}

func TestHandleSoulCommContactabilityReturnsBoundedAffordances(t *testing.T) {
	t.Parallel()

	fixture := newMailboxAPITestDB()
	expectMailboxAPIAccess(t, fixture, soulLifecycleTestAgentIDHex)
	minReputation := 0.35
	now := time.Date(2026, 4, 25, 16, 15, 0, 0, time.UTC)
	fixture.qPrefs.On("First", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentContactPreferences](t, args, 0)
		*dest = models.SoulAgentContactPreferences{
			AgentID:                          soulLifecycleTestAgentIDHex,
			Preferred:                        commChannelEmail,
			Fallback:                         models.SoulChannelTypePhone,
			AvailabilitySchedule:             "business-hours",
			AvailabilityTimezone:             "America/New_York",
			FirstContactRequireSoul:          true,
			FirstContactRequireReputation:    &minReputation,
			FirstContactIntroductionExpected: true,
			UpdatedAt:                        now,
		}
	}).Once()
	fixture.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:       soulLifecycleTestAgentIDHex,
			ChannelType:   models.SoulChannelTypeEmail,
			Identifier:    "agent@example.com",
			Capabilities:  []string{"receive", "send"},
			Protocols:     []string{"smtp", "imap"},
			Provider:      commDeliveryProviderMigadu,
			Verified:      true,
			Status:        models.SoulChannelStatusActive,
			SecretRef:     "/secret/not-returned",
			ProvisionedAt: now,
			UpdatedAt:     now,
		}
	}).Once()
	fixture.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:       soulLifecycleTestAgentIDHex,
			ChannelType:   models.SoulChannelTypePhone,
			Identifier:    "+15551234567",
			Capabilities:  []string{"sms-receive", "sms-send", "voice-send"},
			Provider:      commDeliveryProviderTelnyx,
			Verified:      true,
			Status:        models.SoulChannelStatusActive,
			SecretRef:     "/secret/phone-not-returned",
			ProvisionedAt: now,
			UpdatedAt:     now,
		}
	}).Once()

	s := newMailboxAPITestServer(fixture)
	resp, err := s.handleSoulCommContactability(newMailboxAPIContext(soulLifecycleTestAgentIDHex, "", nil))
	require.NoError(t, err)
	out := decodeMailboxContactabilityResponse(t, resp)
	require.True(t, out.Contactable, "expected contactable response: %#v", out)
	require.Len(t, out.Channels, 2)
	require.True(t, out.Mailbox.ListAllowed)
	require.True(t, out.Mailbox.ContentAllowed)
	require.Equal(t, "agent@example.com", out.Channels[0].Address)
	require.True(t, out.Channels[0].ReceiveAllowed)
	require.True(t, out.Channels[0].SendAllowed)
	require.Equal(t, "+15551234567", out.Channels[1].Number)
	require.True(t, out.Channels[1].ReceiveAllowed)
	require.True(t, out.Channels[1].SendAllowed)
	body := string(resp.Body)
	require.NotContains(t, body, "secret")
	require.NotContains(t, body, "SecretRef")
	require.NotNil(t, out.FirstContact.RequireReputation)
	require.Equal(t, minReputation, *out.FirstContact.RequireReputation)
}

func TestHandleSoulCommContactabilityOmitsUnprovisionedChannels(t *testing.T) {
	t.Parallel()

	fixture := newMailboxAPITestDB()
	expectMailboxAPIAccess(t, fixture, soulLifecycleTestAgentIDHex)
	fixture.qPrefs.On("First", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(theoryErrors.ErrItemNotFound).Once()
	fixture.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentChannel](t, args, 0)
		*dest = models.SoulAgentChannel{
			AgentID:      soulLifecycleTestAgentIDHex,
			ChannelType:  models.SoulChannelTypeEmail,
			Identifier:   "agent@example.com",
			Capabilities: []string{"receive", "send"},
			Verified:     true,
			Status:       models.SoulChannelStatusActive,
		}
	}).Once()
	fixture.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()

	s := newMailboxAPITestServer(fixture)
	resp, err := s.handleSoulCommContactability(newMailboxAPIContext(soulLifecycleTestAgentIDHex, "", nil))
	require.NoError(t, err)
	out := decodeMailboxContactabilityResponse(t, resp)
	require.False(t, out.Contactable)
	require.Empty(t, out.Channels)
	require.False(t, out.Mailbox.ListAllowed)
}

func TestSoulCommContactabilityHelpersCoverPolicyEdges(t *testing.T) {
	t.Parallel()

	inactive := &models.SoulAgentIdentity{Status: models.SoulAgentStatusActive, LifecycleStatus: models.SoulAgentStatusSuspended}
	require.False(t, soulCommContactabilityIdentityActive(inactive))
	require.False(t, contactabilityChannelVisible(nil))
	require.False(t, contactabilityChannelVisible(&models.SoulAgentChannel{Identifier: "agent@example.com", Verified: true, Status: models.SoulChannelStatusActive}))

	now := time.Now().UTC()
	voiceOnlyPhone := &models.SoulAgentChannel{
		ChannelType:   models.SoulChannelTypePhone,
		Identifier:    "+15551234567",
		Capabilities:  []string{"voice-receive"},
		Verified:      true,
		Status:        models.SoulChannelStatusActive,
		ProvisionedAt: now,
	}
	view, ok := contactabilityChannelView(voiceOnlyPhone)
	require.True(t, ok)
	require.True(t, view.ReceiveAllowed)
	require.False(t, view.SendAllowed)
	require.Equal(t, "always", contactabilityAvailability(nil).Schedule)
	require.Empty(t, contactabilityPreference(nil, "preferred"))
	require.False(t, contactabilityMailbox(nil).ListAllowed)
}

func TestSoulCommContactabilityBuildInactiveIdentityOmitsChannels(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	resp := buildSoulCommContactabilityResponse(mailboxRequestContext{
		key:     &models.InstanceKey{InstanceSlug: commWebhookTestInstanceSlug},
		agentID: soulLifecycleTestAgentIDHex,
		identity: &models.SoulAgentIdentity{
			Status:          models.SoulAgentStatusActive,
			LifecycleStatus: models.SoulAgentStatusSuspended,
			UpdatedAt:       now,
		},
	}, nil, &models.SoulAgentChannel{
		ChannelType:   models.SoulChannelTypeEmail,
		Identifier:    "agent@example.com",
		Capabilities:  []string{"receive", "send"},
		Verified:      true,
		Status:        models.SoulChannelStatusActive,
		ProvisionedAt: now,
		UpdatedAt:     now.Add(-time.Minute),
	}, nil)

	require.False(t, resp.Contactable)
	require.Empty(t, resp.Channels)
	require.False(t, resp.Mailbox.ListAllowed)
	require.Equal(t, now.UTC().Format(time.RFC3339Nano), resp.UpdatedAt)
}

func TestSoulCommContactabilityChannelPolicyEdges(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	require.False(t, contactabilityChannelVisible(&models.SoulAgentChannel{Identifier: "agent@example.com", Verified: false, Status: models.SoulChannelStatusActive, ProvisionedAt: now}))
	require.False(t, contactabilityChannelVisible(&models.SoulAgentChannel{Identifier: "agent@example.com", Verified: true, Status: models.SoulChannelStatusPaused, ProvisionedAt: now}))
	require.False(t, contactabilityReceiveAllowed(&models.SoulAgentChannel{ChannelType: models.SoulChannelTypeENS, Capabilities: []string{"receive"}}))
	require.False(t, contactabilitySendAllowed(&models.SoulAgentChannel{ChannelType: models.SoulChannelTypeENS, Capabilities: []string{"send"}}))

	ensView, ok := contactabilityChannelView(&models.SoulAgentChannel{
		ChannelType:   models.SoulChannelTypeENS,
		Identifier:    "agent.eth",
		Verified:      true,
		Status:        models.SoulChannelStatusActive,
		ProvisionedAt: now,
	})
	require.True(t, ok)
	require.False(t, ensView.ReceiveAllowed)
	require.False(t, ensView.SendAllowed)
	availability := contactabilityAvailability(&models.SoulAgentContactPreferences{
		AvailabilitySchedule: "custom",
		AvailabilityTimezone: "UTC",
		AvailabilityWindows: []models.SoulContactAvailabilityWindow{{
			Days:      []string{"mon"},
			StartTime: "09:00",
			EndTime:   "17:00",
		}},
	})
	require.Equal(t, "custom", availability.Schedule)
	require.Len(t, availability.Windows, 1)
}

func TestSoulCommContactabilityOptionalItemStoreError(t *testing.T) {
	t.Parallel()

	item, appErr := loadOptionalSoulCommContactabilityItem[models.SoulAgentChannel](nil, context.Background(), soulLifecycleTestAgentIDHex, "CHANNEL#email")
	require.Nil(t, item)
	assertCommTheoryErrorCode(t, appErr, commCodeInternal, http.StatusInternalServerError)
}

func decodeMailboxContactabilityResponse(t *testing.T, resp *apptheory.Response) soulCommContactabilityResponse {
	t.Helper()
	require.NotNil(t, resp)
	var out soulCommContactabilityResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	return out
}

func newMailboxAPITestDB() mailboxAPITestDB {
	db := ttmocks.NewMockExtendedDB()
	fixture := mailboxAPITestDB{
		db:        db,
		qKey:      new(ttmocks.MockQuery),
		qIdentity: new(ttmocks.MockQuery),
		qDomain:   new(ttmocks.MockQuery),
		qPrefs:    new(ttmocks.MockQuery),
		qChannel:  new(ttmocks.MockQuery),
		qMsg:      new(ttmocks.MockQuery),
		qAudit:    new(ttmocks.MockQuery),
	}
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(fixture.qKey).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(fixture.qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(fixture.qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(fixture.qPrefs).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(fixture.qChannel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMailboxMessage")).Return(fixture.qMsg).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(fixture.qAudit).Maybe()
	for _, q := range []*ttmocks.MockQuery{fixture.qKey, fixture.qIdentity, fixture.qDomain, fixture.qPrefs, fixture.qChannel, fixture.qMsg, fixture.qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("Cursor", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}
	return fixture
}

func newMailboxAPITestServer(fixture mailboxAPITestDB) *Server {
	return &Server{store: store.New(fixture.db), cfg: config.Config{SoulEnabled: true}}
}

func newMailboxAPIContext(agentID string, deliveryID string, query map[string][]string) *apptheory.Context {
	params := map[string]string{"agentId": agentID}
	if deliveryID != "" {
		params["deliveryId"] = deliveryID
	}
	return &apptheory.Context{
		RequestID: "req-mailbox",
		Params:    params,
		Request: apptheory.Request{
			Headers: map[string][]string{"authorization": {"Bearer raw-key"}},
			Query:   query,
		},
	}
}

func expectMailboxAPIAccess(t *testing.T, fixture mailboxAPITestDB, agentID string) {
	t.Helper()
	fixture.qKey.On("First", mock.AnythingOfType("*models.InstanceKey")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.InstanceKey](t, args, 0)
		*dest = models.InstanceKey{ID: sha256HexTrimmed("raw-key"), InstanceSlug: "inst1", CreatedAt: time.Now().Add(-time.Hour)}
	}).Once()
	fixture.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Domain: "example.com", Status: models.SoulAgentStatusActive, LifecycleStatus: models.SoulAgentStatusActive}
	}).Once()
	fixture.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
}

func expectMailboxMessageLoad(t *testing.T, q *ttmocks.MockQuery, msg *models.SoulCommMailboxMessage) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.SoulCommMailboxMessage")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCommMailboxMessage](t, args, 0)
		*dest = *msg
	}).Once()
}

func mailboxAPITestMessage(agentID string) *models.SoulCommMailboxMessage {
	now := time.Date(2026, 4, 25, 15, 45, 0, 0, time.UTC)
	return &models.SoulCommMailboxMessage{
		DeliveryID:      "comm-delivery-1",
		MessageID:       "comm-msg-1",
		ThreadID:        "comm-thread-1",
		InstanceSlug:    "inst1",
		AgentID:         agentID,
		Direction:       models.SoulCommDirectionInbound,
		ChannelType:     commChannelEmail,
		Provider:        commDeliveryProviderMigadu,
		Status:          models.SoulCommMailboxStatusDelivered,
		FromAddress:     "sender@example.com",
		ToAddress:       "agent@example.com",
		Subject:         "Hello",
		Preview:         "redacted preview",
		ContentStorage:  commmailbox.ContentStorageS3,
		ContentBucket:   "mailbox-bucket",
		ContentKey:      "mailbox/v1/secret/content",
		ContentSHA256:   "sha256-body",
		ContentBytes:    9,
		ContentMimeType: "text/plain",
		HasContent:      true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func TestHandleSoulCommMailboxStateMutationsAreIdempotent(t *testing.T) {
	t.Parallel()

	t.Run("mark read", func(t *testing.T) {
		t.Parallel()
		fixture := newMailboxAPITestDB()
		expectMailboxAPIAccess(t, fixture, soulLifecycleTestAgentIDHex)
		msg := mailboxAPITestMessage(soulLifecycleTestAgentIDHex)
		msg.Read = false
		expectMailboxMessageLoad(t, fixture.qMsg, msg)

		s := newMailboxAPITestServer(fixture)
		resp, err := s.handleSoulCommMailboxMarkRead(newMailboxAPIContext(soulLifecycleTestAgentIDHex, "comm-delivery-1", nil))
		if err != nil {
			t.Fatalf("mark read: %v", err)
		}
		var out soulCommMailboxGetResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !out.Message.State.Read || out.Message.State.Deleted {
			t.Fatalf("unexpected state: %#v", out.Message.State)
		}
	})

	t.Run("delete is soft state and hides content href", func(t *testing.T) {
		t.Parallel()
		fixture := newMailboxAPITestDB()
		expectMailboxAPIAccess(t, fixture, soulLifecycleTestAgentIDHex)
		expectMailboxMessageLoad(t, fixture.qMsg, mailboxAPITestMessage(soulLifecycleTestAgentIDHex))

		s := newMailboxAPITestServer(fixture)
		resp, err := s.handleSoulCommMailboxDelete(newMailboxAPIContext(soulLifecycleTestAgentIDHex, "comm-delivery-1", nil))
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		var out soulCommMailboxGetResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !out.Message.State.Deleted || !out.Message.State.Archived || out.Message.Content.ContentHref != "" {
			t.Fatalf("unexpected delete state/content: %#v", out.Message)
		}
	})

	t.Run("repeated archive remains successful", func(t *testing.T) {
		t.Parallel()
		fixture := newMailboxAPITestDB()
		expectMailboxAPIAccess(t, fixture, soulLifecycleTestAgentIDHex)
		msg := mailboxAPITestMessage(soulLifecycleTestAgentIDHex)
		msg.Archived = true
		expectMailboxMessageLoad(t, fixture.qMsg, msg)

		s := newMailboxAPITestServer(fixture)
		resp, err := s.handleSoulCommMailboxArchive(newMailboxAPIContext(soulLifecycleTestAgentIDHex, "comm-delivery-1", nil))
		if err != nil {
			t.Fatalf("archive: %v", err)
		}
		var out soulCommMailboxGetResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !out.Message.State.Archived {
			t.Fatalf("expected archived state: %#v", out.Message.State)
		}
	})
}

func TestHandleSoulCommMailboxStateAdditionalMutations(t *testing.T) {
	t.Parallel()

	t.Run("mark unread", func(t *testing.T) {
		t.Parallel()
		fixture := newMailboxAPITestDB()
		expectMailboxAPIAccess(t, fixture, soulLifecycleTestAgentIDHex)
		msg := mailboxAPITestMessage(soulLifecycleTestAgentIDHex)
		msg.Read = true
		expectMailboxMessageLoad(t, fixture.qMsg, msg)

		resp, err := newMailboxAPITestServer(fixture).handleSoulCommMailboxMarkUnread(newMailboxAPIContext(soulLifecycleTestAgentIDHex, "comm-delivery-1", nil))
		require.NoError(t, err)
		require.False(t, decodeMailboxGetResponse(t, resp).Message.State.Read)
	})

	t.Run("unarchive", func(t *testing.T) {
		t.Parallel()
		fixture := newMailboxAPITestDB()
		expectMailboxAPIAccess(t, fixture, soulLifecycleTestAgentIDHex)
		msg := mailboxAPITestMessage(soulLifecycleTestAgentIDHex)
		msg.Archived = true
		expectMailboxMessageLoad(t, fixture.qMsg, msg)

		resp, err := newMailboxAPITestServer(fixture).handleSoulCommMailboxUnarchive(newMailboxAPIContext(soulLifecycleTestAgentIDHex, "comm-delivery-1", nil))
		require.NoError(t, err)
		require.False(t, decodeMailboxGetResponse(t, resp).Message.State.Archived)
	})
}

func TestHandleSoulCommMailboxContentRejectsDeletedOrMissingContent(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*models.SoulCommMailboxMessage){
		"deleted": func(msg *models.SoulCommMailboxMessage) {
			msg.Deleted = true
		},
		"missing content": func(msg *models.SoulCommMailboxMessage) {
			msg.HasContent = false
			msg.ContentKey = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := newMailboxAPITestDB()
			expectMailboxAPIAccess(t, fixture, soulLifecycleTestAgentIDHex)
			msg := mailboxAPITestMessage(soulLifecycleTestAgentIDHex)
			mutate(msg)
			expectMailboxMessageLoad(t, fixture.qMsg, msg)

			_, err := newMailboxAPITestServer(fixture).handleSoulCommMailboxContent(newMailboxAPIContext(soulLifecycleTestAgentIDHex, "comm-delivery-1", nil))
			assertCommTheoryErrorCode(t, err, "comm.not_found", http.StatusNotFound)
		})
	}
}

func TestHandleSoulCommMailboxGetRejectsDeletedMessage(t *testing.T) {
	t.Parallel()

	fixture := newMailboxAPITestDB()
	expectMailboxAPIAccess(t, fixture, soulLifecycleTestAgentIDHex)
	msg := mailboxAPITestMessage(soulLifecycleTestAgentIDHex)
	msg.Deleted = true
	expectMailboxMessageLoad(t, fixture.qMsg, msg)

	_, err := newMailboxAPITestServer(fixture).handleSoulCommMailboxGet(newMailboxAPIContext(soulLifecycleTestAgentIDHex, "comm-delivery-1", nil))
	assertCommTheoryErrorCode(t, err, "comm.not_found", http.StatusNotFound)
}

func TestSoulCommMailboxHelpersCoverEdges(t *testing.T) {
	t.Parallel()

	require.Equal(t, soulCommMailboxMessage{}, mailboxMessageJSON(nil))
	require.Equal(t, commmailbox.ContentPointer{}, mailboxContentPointer(nil))
	require.Empty(t, formatMailboxTime(time.Time{}))
	require.True(t, queryBool(&apptheory.Context{Request: apptheory.Request{Query: map[string][]string{"includeDeleted": {"yes"}}}}, "includeDeleted"))

	_, appErr := newMailboxAPITestServer(newMailboxAPITestDB()).loadMailboxMessage(context.Background(), "inst1", soulLifecycleTestAgentIDHex, "")
	assertCommTheoryErrorCode(t, appErr, commCodeInvalidRequest, http.StatusBadRequest)
}

func decodeMailboxGetResponse(t *testing.T, resp *apptheory.Response) soulCommMailboxGetResponse {
	t.Helper()
	require.NotNil(t, resp)
	var out soulCommMailboxGetResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	return out
}
