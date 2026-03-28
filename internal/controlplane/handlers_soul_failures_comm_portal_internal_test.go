package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type soulCommPortalTestDB struct {
	base     soulLifecycleTestDB
	qAct     *ttmocks.MockQuery
	qQueue   *ttmocks.MockQuery
	qStatus  *ttmocks.MockQuery
	qFailure *ttmocks.MockQuery
}

func newSoulCommPortalTestDB() soulCommPortalTestDB {
	base := newSoulLifecycleTestDB()
	qAct := new(ttmocks.MockQuery)
	qQueue := new(ttmocks.MockQuery)
	qStatus := new(ttmocks.MockQuery)
	qFailure := new(ttmocks.MockQuery)

	base.db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qAct).Maybe()
	base.db.On("Model", mock.AnythingOfType("*models.SoulAgentCommQueue")).Return(qQueue).Maybe()
	base.db.On("Model", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(qStatus).Maybe()
	base.db.On("Model", mock.AnythingOfType("*models.SoulAgentFailure")).Return(qFailure).Maybe()

	for _, q := range []*ttmocks.MockQuery{qAct, qQueue, qStatus, qFailure} {
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

func TestHandleSoulAgentCommPortalHandlers(t *testing.T) {
	t.Parallel()

	agentID := soulLifecycleTestAgentIDHex
	ctx := adminCtx()
	ctx.Params = map[string]string{"agentId": agentID}

	t.Run("activity error and success", func(t *testing.T) {
		tdb := newSoulCommPortalTestDB()
		seedSoulAgentPortalAccess(t, tdb, agentID, models.SoulAgentStatusActive)
		tdb.qAct.On("All", mock.Anything).Return(errors.New("boom")).Once()
		s := newSoulPortalServer(tdb)
		if _, err := s.handleSoulAgentCommActivity(ctx); err == nil {
			t.Fatalf("expected list error")
		}

		tdb2 := newSoulCommPortalTestDB()
		seedSoulAgentPortalAccess(t, tdb2, agentID, models.SoulAgentStatusActive)
		tdb2.qAct.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
			*dest = []*models.SoulAgentCommActivity{{AgentID: agentID, MessageID: "m1"}}
		}).Once()
		s = newSoulPortalServer(tdb2)
		resp, err := s.handleSoulAgentCommActivity(ctx)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("unexpected response: %#v %v", resp, err)
		}
	})

	t.Run("queue success", func(t *testing.T) {
		tdb := newSoulCommPortalTestDB()
		seedSoulAgentPortalAccess(t, tdb, agentID, models.SoulAgentStatusActive)
		tdb.qQueue.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommQueue")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentCommQueue](t, args, 0)
			*dest = []*models.SoulAgentCommQueue{{AgentID: agentID, MessageID: "m1"}}
		}).Once()
		s := newSoulPortalServer(tdb)
		resp, err := s.handleSoulAgentCommQueue(ctx)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("unexpected response: %#v %v", resp, err)
		}
	})

	t.Run("status branches", func(t *testing.T) {
		tdb := newSoulCommPortalTestDB()
		seedSoulAgentPortalAccess(t, tdb, agentID, models.SoulAgentStatusActive)
		s := newSoulPortalServer(tdb)

		badCtx := adminCtx()
		badCtx.Params = map[string]string{"agentId": agentID, "messageId": ""}
		if _, err := s.handleSoulAgentCommStatus(badCtx); err == nil {
			t.Fatalf("expected invalid message id error")
		}

		statusCtx := adminCtx()
		statusCtx.Params = map[string]string{"agentId": agentID, "messageId": "msg-1"}
		tdb.qStatus.On("First", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(theoryErrors.ErrItemNotFound).Once()
		if _, err := s.handleSoulAgentCommStatus(statusCtx); err == nil {
			t.Fatalf("expected not found error")
		}

		tdb2 := newSoulCommPortalTestDB()
		seedSoulAgentPortalAccess(t, tdb2, agentID, models.SoulAgentStatusActive)
		tdb2.qStatus.On("First", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulCommMessageStatus](t, args, 0)
			*dest = models.SoulCommMessageStatus{MessageID: "msg-1", AgentID: "0xother"}
		}).Once()
		s = newSoulPortalServer(tdb2)
		if _, err := s.handleSoulAgentCommStatus(statusCtx); err == nil {
			t.Fatalf("expected mismatched agent error")
		}

		tdb3 := newSoulCommPortalTestDB()
		seedSoulAgentPortalAccess(t, tdb3, agentID, models.SoulAgentStatusActive)
		tdb3.qStatus.On("First", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
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
		s = newSoulPortalServer(tdb3)
		resp, err := s.handleSoulAgentCommStatus(statusCtx)
		if err != nil || resp.Status != http.StatusOK {
			t.Fatalf("unexpected response: %#v %v", resp, err)
		}
	})
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
