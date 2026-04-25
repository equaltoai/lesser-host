package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/commmailbox"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type soulCommPortalTestDB struct {
	base     soulLifecycleTestDB
	qAct     *ttmocks.MockQuery
	qQueue   *ttmocks.MockQuery
	qMailbox *ttmocks.MockQuery
	qStatus  *ttmocks.MockQuery
	qFailure *ttmocks.MockQuery
}

func newSoulCommPortalTestDB() soulCommPortalTestDB {
	base := newSoulLifecycleTestDB()
	qAct := new(ttmocks.MockQuery)
	qQueue := new(ttmocks.MockQuery)
	qMailbox := new(ttmocks.MockQuery)
	qStatus := new(ttmocks.MockQuery)
	qFailure := new(ttmocks.MockQuery)

	base.db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qAct).Maybe()
	base.db.On("Model", mock.AnythingOfType("*models.SoulAgentCommQueue")).Return(qQueue).Maybe()
	base.db.On("Model", mock.AnythingOfType("*models.SoulCommMailboxMessage")).Return(qMailbox).Maybe()
	base.db.On("Model", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(qStatus).Maybe()
	base.db.On("Model", mock.AnythingOfType("*models.SoulAgentFailure")).Return(qFailure).Maybe()

	for _, q := range []*ttmocks.MockQuery{qAct, qQueue, qMailbox, qStatus, qFailure} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("Cursor", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
	}

	return soulCommPortalTestDB{
		base:     base,
		qAct:     qAct,
		qQueue:   qQueue,
		qMailbox: qMailbox,
		qStatus:  qStatus,
		qFailure: qFailure,
	}
}

func seedSoulAgentPortalAccess(t *testing.T, tdb soulCommPortalTestDB, agentID string, status string) {
	t.Helper()

	if status == "" {
		status = models.SoulAgentStatusActive
	}

	tdb.base.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:         agentID,
			Domain:          "example.com",
			LocalID:         "agent-bot",
			Status:          status,
			LifecycleStatus: status,
			UpdatedAt:       time.Now().UTC(),
		}
	}).Maybe()

	tdb.base.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Maybe()

	tdb.base.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Maybe()
}

func newSoulPortalServer(tdb soulCommPortalTestDB) *Server {
	return &Server{
		store: store.New(tdb.base.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
			SoulRPCURL:                  "https://rpc.example.com",
		},
	}
}

func portalMailboxMessage(agentID string, status string) *models.SoulCommMailboxMessage {
	now := time.Date(2026, 4, 25, 18, 0, 0, 0, time.UTC)
	return &models.SoulCommMailboxMessage{
		DeliveryID:      "delivery-1",
		MessageID:       "message-1",
		ThreadID:        "thread-1",
		InstanceSlug:    "inst1",
		AgentID:         agentID,
		Direction:       models.SoulCommDirectionInbound,
		ChannelType:     commChannelEmail,
		Provider:        commDeliveryProviderMigadu,
		Status:          status,
		FromAddress:     "sender@example.com",
		ToAddress:       "agent@example.com",
		Subject:         "Hello",
		Preview:         "redacted preview",
		ContentStorage:  commmailbox.ContentStorageS3,
		ContentBucket:   "mailbox-bucket",
		ContentKey:      "mailbox/v1/secret/content",
		ContentSHA256:   "sha256-body",
		ContentBytes:    42,
		ContentMimeType: "text/plain",
		HasContent:      true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func TestRequireSoulAgentWithDomainAccess(t *testing.T) {
	t.Parallel()

	s := &Server{}
	if _, appErr := s.requireSoulAgentWithDomainAccess(nil, ""); appErr == nil || appErr.Message != provisionPhoneInternalError {
		t.Fatalf("expected internal error, got %#v", appErr)
	}

	tdb := newSoulCommPortalTestDB()
	s = newSoulPortalServer(tdb)
	ctx := adminCtx()
	tdb.base.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, appErr := s.requireSoulAgentWithDomainAccess(ctx, soulLifecycleTestAgentIDHex); appErr == nil || appErr.Code != appErrCodeNotFound {
		t.Fatalf("expected not found, got %#v", appErr)
	}

	tdb2 := newSoulCommPortalTestDB()
	s = newSoulPortalServer(tdb2)
	tdb2.base.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(errors.New("boom")).Once()
	if _, appErr := s.requireSoulAgentWithDomainAccess(ctx, soulLifecycleTestAgentIDHex); appErr == nil || appErr.Message != provisionPhoneInternalError {
		t.Fatalf("expected internal error, got %#v", appErr)
	}

	tdb3 := newSoulCommPortalTestDB()
	s = newSoulPortalServer(tdb3)
	seedSoulAgentPortalAccess(t, tdb3, soulLifecycleTestAgentIDHex, models.SoulAgentStatusActive)
	identity, appErr := s.requireSoulAgentWithDomainAccess(ctx, soulLifecycleTestAgentIDHex)
	if appErr != nil || identity == nil || identity.AgentID != soulLifecycleTestAgentIDHex {
		t.Fatalf("unexpected identity/appErr: %#v %#v", identity, appErr)
	}
}

func TestHandleSoulAgentCommActivityReadsMailbox(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex
	ctx := soulAgentCommPortalCtx(agentID)

	tdb := newSoulCommPortalTestDB()
	seedSoulAgentPortalAccess(t, tdb, agentID, models.SoulAgentStatusActive)
	tdb.qMailbox.On("All", mock.Anything).Return(errors.New("boom")).Once()
	s := newSoulPortalServer(tdb)
	if _, err := s.handleSoulAgentCommActivity(ctx); err == nil {
		t.Fatalf("expected list error")
	}

	tdb2 := newSoulCommPortalTestDB()
	seedSoulAgentPortalAccess(t, tdb2, agentID, models.SoulAgentStatusActive)
	expectPortalMailboxRows(t, tdb2.qMailbox, []*models.SoulCommMailboxMessage{
		portalMailboxMessage(agentID, models.SoulCommMailboxStatusDelivered),
	})
	s = newSoulPortalServer(tdb2)
	resp, err := s.handleSoulAgentCommActivity(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected response: %#v %v", resp, err)
	}
	assertPortalResponseRedacted(t, "activity", resp.Body)

	var out soulAgentCommActivityResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal activity: %v", err)
	}
	if out.Count != 1 || out.Activities[0].DeliveryID != "delivery-1" || out.Activities[0].Content.SHA256 != "sha256-body" {
		t.Fatalf("unexpected canonical activity response: %#v", out)
	}
}

func TestHandleSoulAgentCommQueueReadsMailbox(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex
	tdb := newSoulCommPortalTestDB()
	seedSoulAgentPortalAccess(t, tdb, agentID, models.SoulAgentStatusActive)
	expectPortalMailboxRows(t, tdb.qMailbox, portalQueueMailboxRows(agentID))

	s := newSoulPortalServer(tdb)
	resp, err := s.handleSoulAgentCommQueue(soulAgentCommPortalCtx(agentID))
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected response: %#v %v", resp, err)
	}
	assertPortalResponseRedacted(t, "queue", resp.Body)

	var out soulAgentCommQueueResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal queue: %v", err)
	}
	if out.Count != 1 || out.Items[0].DeliveryID != "delivery-1" || out.Items[0].Preview != "redacted preview" {
		t.Fatalf("unexpected canonical queue response: %#v", out)
	}
}

func TestHandleSoulAgentCommStatusBranches(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex
	tdb := newSoulCommPortalTestDB()
	seedSoulAgentPortalAccess(t, tdb, agentID, models.SoulAgentStatusActive)
	s := newSoulPortalServer(tdb)

	badCtx := adminCtx()
	badCtx.Params = map[string]string{"agentId": agentID, "messageId": ""}
	if _, err := s.handleSoulAgentCommStatus(badCtx); err == nil {
		t.Fatalf("expected invalid message id error")
	}

	statusCtx := soulAgentCommStatusCtx(agentID, "msg-1")
	tdb.qStatus.On("First", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(theoryErrors.ErrItemNotFound).Once()
	if _, err := s.handleSoulAgentCommStatus(statusCtx); err == nil {
		t.Fatalf("expected not found error")
	}

	expectSoulCommStatusMismatch(t, agentID, statusCtx)
	expectSoulCommStatusSuccess(t, agentID, statusCtx)
}

func soulAgentCommPortalCtx(agentID string) *apptheory.Context {
	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": agentID}
	return ctx
}

func soulAgentCommStatusCtx(agentID string, messageID string) *apptheory.Context {
	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": agentID, "messageId": messageID}
	return ctx
}

func expectPortalMailboxRows(t *testing.T, q *ttmocks.MockQuery, rows []*models.SoulCommMailboxMessage) {
	t.Helper()
	q.On("All", mock.AnythingOfType("*[]*models.SoulCommMailboxMessage")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulCommMailboxMessage](t, args, 0)
		*dest = rows
	}).Once()
}

func portalQueueMailboxRows(agentID string) []*models.SoulCommMailboxMessage {
	delivered := portalMailboxMessage(agentID, models.SoulCommMailboxStatusDelivered)
	delivered.DeliveryID = "delivery-delivered"
	outbound := portalMailboxMessage(agentID, models.SoulCommMailboxStatusQueued)
	outbound.DeliveryID = "delivery-outbound"
	outbound.Direction = models.SoulCommDirectionOutbound
	deleted := portalMailboxMessage(agentID, models.SoulCommMailboxStatusQueued)
	deleted.DeliveryID = "delivery-deleted"
	deleted.Deleted = true
	return []*models.SoulCommMailboxMessage{
		portalMailboxMessage(agentID, models.SoulCommMailboxStatusQueued),
		delivered,
		outbound,
		deleted,
	}
}

func assertPortalResponseRedacted(t *testing.T, label string, body []byte) {
	t.Helper()
	text := string(body)
	if strings.Contains(text, `"body"`) || strings.Contains(text, "mailbox-bucket") || strings.Contains(text, "mailbox/v1/secret") {
		t.Fatalf("%s leaked full body or content storage pointer: %s", label, text)
	}
}

func expectSoulCommStatusMismatch(t *testing.T, agentID string, statusCtx *apptheory.Context) {
	t.Helper()
	tdb := newSoulCommPortalTestDB()
	seedSoulAgentPortalAccess(t, tdb, agentID, models.SoulAgentStatusActive)
	tdb.qStatus.On("First", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCommMessageStatus](t, args, 0)
		*dest = models.SoulCommMessageStatus{MessageID: "msg-1", AgentID: "0xother"}
	}).Once()
	s := newSoulPortalServer(tdb)
	if _, err := s.handleSoulAgentCommStatus(statusCtx); err == nil {
		t.Fatalf("expected mismatched agent error")
	}
}

func expectSoulCommStatusSuccess(t *testing.T, agentID string, statusCtx *apptheory.Context) {
	t.Helper()
	tdb := newSoulCommPortalTestDB()
	seedSoulAgentPortalAccess(t, tdb, agentID, models.SoulAgentStatusActive)
	tdb.qStatus.On("First", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCommMessageStatus](t, args, 0)
		*dest = models.SoulCommMessageStatus{
			MessageID:         "msg-1",
			AgentID:           agentID,
			Status:            "sent",
			ChannelType:       "email",
			Provider:          commDeliveryProviderMigadu,
			ProviderMessageID: "provider-1",
			CreatedAt:         time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC),
			UpdatedAt:         time.Date(2026, 3, 5, 12, 5, 0, 0, time.UTC),
		}
	}).Once()
	s := newSoulPortalServer(tdb)
	resp, err := s.handleSoulAgentCommStatus(statusCtx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected response: %#v %v", resp, err)
	}
}

func TestHandleSoulFailures_RecordBranches(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex
	tdb := newSoulCommPortalTestDB()
	seedSoulAgentPortalAccess(t, tdb, agentID, models.SoulAgentStatusActive)
	s := newSoulPortalServer(tdb)

	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": agentID}
	ctx.Request.Body = []byte(`{"failure_type":"boundary","description":"desc"}`)
	if _, err := s.handleSoulRecordFailure(ctx); err == nil {
		t.Fatalf("expected missing id error")
	}

	ctx.Request.Body = []byte(`{"failure_id":"f1","failure_type":"boundary","description":"desc"}`)
	tdb.qFailure.On("Create").Return(nil).Once()
	resp, err := s.handleSoulRecordFailure(ctx)
	if err != nil || resp.Status != http.StatusCreated {
		t.Fatalf("unexpected response: %#v %v", resp, err)
	}

	tdb2 := newSoulCommPortalTestDB()
	seedSoulAgentPortalAccess(t, tdb2, agentID, models.SoulAgentStatusActive)
	tdb2.qFailure.On("Create").Return(errors.New("duplicate")).Once()
	s = newSoulPortalServer(tdb2)
	if _, err := s.handleSoulRecordFailure(ctx); err == nil {
		t.Fatalf("expected conflict")
	}
}

func TestHandleSoulFailures_RecoveryBranches(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex
	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": agentID}
	ctx.Request.Body = []byte(`{"failure_id":"f1"}`)

	tdb := newSoulCommPortalTestDB()
	seedSoulAgentPortalAccess(t, tdb, agentID, models.SoulAgentStatusActive)
	s := newSoulPortalServer(tdb)
	tdb.qFailure.On("All", mock.AnythingOfType("*[]*models.SoulAgentFailure")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{}
	}).Once()
	if _, err := s.handleSoulRecordRecovery(ctx); err == nil {
		t.Fatalf("expected not found")
	}

	tdb2 := newSoulCommPortalTestDB()
	seedSoulAgentPortalAccess(t, tdb2, agentID, models.SoulAgentStatusActive)
	tdb2.qFailure.On("All", mock.AnythingOfType("*[]*models.SoulAgentFailure")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{{AgentID: agentID, FailureID: "f1", Status: "recovered"}}
	}).Once()
	s = newSoulPortalServer(tdb2)
	if _, err := s.handleSoulRecordRecovery(ctx); err == nil {
		t.Fatalf("expected already recovered conflict")
	}

	tdb3 := newSoulCommPortalTestDB()
	seedSoulAgentPortalAccess(t, tdb3, agentID, models.SoulAgentStatusActive)
	tdb3.qFailure.On("All", mock.AnythingOfType("*[]*models.SoulAgentFailure")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{{AgentID: agentID, FailureID: "f1", Status: "open"}}
	}).Once()
	tdb3.qFailure.On("Update", mock.Anything).Return(nil).Once()
	s = newSoulPortalServer(tdb3)
	resp, err := s.handleSoulRecordRecovery(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected response: %#v %v", resp, err)
	}
}

func TestHandleSoulFailures_PublicList(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex
	ctx := &apptheory.Context{
		Params: map[string]string{"agentId": agentID},
		Request: apptheory.Request{Query: map[string][]string{
			"cursor": {" c1 "},
			"limit":  {"300"},
			"origin": {"https://portal.example.com"},
		}},
	}

	tdb := newSoulCommPortalTestDB()
	s := newSoulPortalServer(tdb)
	tdb.qFailure.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentFailure")).Return((*core.PaginatedResult)(nil), errors.New("boom")).Once()
	if _, err := s.handleSoulPublicGetFailures(ctx); err == nil {
		t.Fatalf("expected list error")
	}

	tdb2 := newSoulCommPortalTestDB()
	s = newSoulPortalServer(tdb2)
	tdb2.qFailure.On("AllPaginated", mock.AnythingOfType("*[]*models.SoulAgentFailure")).Return(&core.PaginatedResult{HasMore: true, NextCursor: " next "}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{{AgentID: agentID, FailureID: "f1", Status: "open"}, nil}
	}).Once()
	resp, err := s.handleSoulPublicGetFailures(ctx)
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("unexpected response: %#v %v", resp, err)
	}

	var out soulListFailuresResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Count != 1 || !out.HasMore || out.NextCursor != testSoulPaginationNextCursor {
		t.Fatalf("unexpected failures response: %#v", out)
	}
}
