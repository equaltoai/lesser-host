package controlplane

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type soulSuspensionTestDB struct {
	db     *ttmocks.MockExtendedDB
	qID    *ttmocks.MockQuery
	qAudit *ttmocks.MockQuery
}

func newSoulSuspensionTestDB() soulSuspensionTestDB {
	db := ttmocks.NewMockExtendedDB()
	qID := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qID).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()

	for _, q := range []*ttmocks.MockQuery{qID, qAudit} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
		q.On("Create").Return(nil).Maybe()
	}

	return soulSuspensionTestDB{db: db, qID: qID, qAudit: qAudit}
}

func TestSoulSuspensionHandlers_SuspendAndReinstate(t *testing.T) {
	t.Parallel()

	tdb := newSoulSuspensionTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentID := "0x" + strings.Repeat("11", 32)

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusActive, UpdatedAt: time.Now().Add(-time.Minute).UTC()}
	}).Once()

	body, _ := json.Marshal(soulSuspendAgentRequest{Reason: "because"})
	suspendCtx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "op",
		Params:       map[string]string{"agentId": agentID},
		Request:      apptheory.Request{Body: body},
	}
	suspendCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	suspendResp, err := s.handleSuspendSoulAgent(suspendCtx)
	if err != nil || suspendResp.Status != 200 {
		t.Fatalf("suspend: resp=%#v err=%v", suspendResp, err)
	}

	tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{AgentID: agentID, Status: models.SoulAgentStatusSuspended, UpdatedAt: time.Now().Add(-time.Minute).UTC()}
	}).Once()

	reinstateCtx := &apptheory.Context{
		RequestID:    "r2",
		AuthIdentity: "op",
		Params:       map[string]string{"agentId": agentID},
	}
	reinstateCtx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	reinstateResp, err := s.handleReinstateSoulAgent(reinstateCtx)
	if err != nil || reinstateResp.Status != 200 {
		t.Fatalf("reinstate: resp=%#v err=%v", reinstateResp, err)
	}
}
