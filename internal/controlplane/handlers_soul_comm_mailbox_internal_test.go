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
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

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

func newMailboxAPITestDB() mailboxAPITestDB {
	db := ttmocks.NewMockExtendedDB()
	fixture := mailboxAPITestDB{
		db:        db,
		qKey:      new(ttmocks.MockQuery),
		qIdentity: new(ttmocks.MockQuery),
		qDomain:   new(ttmocks.MockQuery),
		qMsg:      new(ttmocks.MockQuery),
		qAudit:    new(ttmocks.MockQuery),
	}
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(fixture.qKey).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(fixture.qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(fixture.qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMailboxMessage")).Return(fixture.qMsg).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(fixture.qAudit).Maybe()
	for _, q := range []*ttmocks.MockQuery{fixture.qKey, fixture.qIdentity, fixture.qDomain, fixture.qMsg, fixture.qAudit} {
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
