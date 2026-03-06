package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type soulSovereigntyActionsTestDB struct {
	db       *ttmocks.MockExtendedDB
	qDomain  *ttmocks.MockQuery
	qInst    *ttmocks.MockQuery
	qID      *ttmocks.MockQuery
	qAudit   *ttmocks.MockQuery
	qDispute *ttmocks.MockQuery
}

func newSoulSovereigntyActionsTestDB() soulSovereigntyActionsTestDB {
	db := ttmocks.NewMockExtendedDB()
	qDomain := new(ttmocks.MockQuery)
	qInst := new(ttmocks.MockQuery)
	qID := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	qDispute := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qID).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentDispute")).Return(qDispute).Maybe()

	for _, q := range []*ttmocks.MockQuery{qDomain, qInst, qID, qAudit, qDispute} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
	}
	qID.On("Update", []string{"Status", "LifecycleStatus", "LifecycleReason", "UpdatedAt"}).Return(nil).Maybe()
	qDispute.On("Update", []string{"OptInStatus", "Status", "Result", "Score", "EvaluatedAt", "UpdatedAt"}).Return(nil).Maybe()
	qDispute.On("Update", []string{"OptInStatus", "UpdatedAt"}).Return(nil).Maybe()
	qAudit.On("Create").Return(nil).Maybe()

	return soulSovereigntyActionsTestDB{
		db:       db,
		qDomain:  qDomain,
		qInst:    qInst,
		qID:      qID,
		qAudit:   qAudit,
		qDispute: qDispute,
	}
}

func newSovereigntyActionsServer(tdb soulSovereigntyActionsTestDB) *Server {
	return &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}
}

func stubSovereigntyDomainAccess(t *testing.T, tdb soulSovereigntyActionsTestDB) {
	t.Helper()
	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "admin"}
	}).Once()
}

func TestHandleSoulSelfSuspend_SuccessAndUpdateError(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("11", 32)

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulSovereigntyActionsTestDB()
		s := newSovereigntyActionsServer(tdb)

		stubSovereigntyDomainAccess(t, tdb)

		tdb.qID.ExpectedCalls = nil
		tdb.qID.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(tdb.qID).Maybe()
		tdb.qID.On("IfExists").Return(tdb.qID).Maybe()
		tdb.qID.On("Update", []string{"Status", "LifecycleStatus", "LifecycleReason", "UpdatedAt"}).Return(nil).Once()
		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID:         agentID,
				Domain:          "example.com",
				LocalID:         "agent",
				Status:          models.SoulAgentStatusActive,
				LifecycleStatus: models.SoulAgentStatusActive,
				UpdatedAt:       time.Now().Add(-time.Minute).UTC(),
			}
		}).Once()

		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": agentID}
		ctx.Request.Body = []byte(`{"reason":"because"}`)

		resp, err := s.handleSoulSelfSuspend(ctx)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != http.StatusOK {
			t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
		}

		var out models.SoulAgentIdentity
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Status != models.SoulAgentStatusSelfSuspended || out.LifecycleStatus != models.SoulAgentStatusSelfSuspended || out.LifecycleReason != "because" {
			t.Fatalf("unexpected identity state: %#v", out)
		}
	})

	t.Run("update_error", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulSovereigntyActionsTestDB()
		s := newSovereigntyActionsServer(tdb)

		stubSovereigntyDomainAccess(t, tdb)

		tdb.qID.ExpectedCalls = nil
		tdb.qID.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(tdb.qID).Maybe()
		tdb.qID.On("IfExists").Return(tdb.qID).Maybe()
		tdb.qID.On("Update", []string{"Status", "LifecycleStatus", "LifecycleReason", "UpdatedAt"}).Return(errors.New("boom")).Once()
		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID:         agentID,
				Domain:          "example.com",
				LocalID:         "agent",
				Status:          models.SoulAgentStatusActive,
				LifecycleStatus: models.SoulAgentStatusActive,
				UpdatedAt:       time.Now().Add(-time.Minute).UTC(),
			}
		}).Once()

		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": agentID}
		ctx.Request.Body = []byte(`{"reason":"x"}`)

		if _, err := s.handleSoulSelfSuspend(ctx); err == nil {
			t.Fatalf("expected error")
		}
	})
}

func TestHandleSoulSelfReinstate_ConflictNotFoundAndSuccess(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("22", 32)

	t.Run("not_found", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulSovereigntyActionsTestDB()
		s := newSovereigntyActionsServer(tdb)

		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(errors.New("boom")).Once()

		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": agentID}
		if _, err := s.handleSoulSelfReinstate(ctx); err == nil {
			t.Fatalf("expected not_found")
		}
	})

	t.Run("conflict_not_self_suspended", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulSovereigntyActionsTestDB()
		s := newSovereigntyActionsServer(tdb)

		stubSovereigntyDomainAccess(t, tdb)
		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID:         agentID,
				Domain:          "example.com",
				LocalID:         "agent",
				Status:          models.SoulAgentStatusActive,
				LifecycleStatus: models.SoulAgentStatusActive,
				UpdatedAt:       time.Now().Add(-time.Minute).UTC(),
			}
		}).Once()

		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": agentID}
		if _, err := s.handleSoulSelfReinstate(ctx); err == nil {
			t.Fatalf("expected conflict")
		}
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		tdb := newSoulSovereigntyActionsTestDB()
		s := newSovereigntyActionsServer(tdb)

		stubSovereigntyDomainAccess(t, tdb)
		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID:         agentID,
				Domain:          "example.com",
				LocalID:         "agent",
				Status:          models.SoulAgentStatusSelfSuspended,
				LifecycleStatus: models.SoulAgentStatusSelfSuspended,
				LifecycleReason: "because",
				UpdatedAt:       time.Now().Add(-time.Minute).UTC(),
			}
		}).Once()

		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": agentID}
		resp, err := s.handleSoulSelfReinstate(ctx)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != http.StatusOK {
			t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
		}
		var out models.SoulAgentIdentity
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Status != models.SoulAgentStatusActive || out.LifecycleStatus != models.SoulAgentStatusActive || out.LifecycleReason != "" {
			t.Fatalf("unexpected identity state: %#v", out)
		}
	})
}

func TestHandleSoulCreateDispute_ValidationsAndSuccess(t *testing.T) {
	t.Parallel()

	agentID := "0x" + strings.Repeat("33", 32)

	makeServer := func(t *testing.T) (*Server, soulSovereigntyActionsTestDB) {
		t.Helper()
		tdb := newSoulSovereigntyActionsTestDB()
		s := newSovereigntyActionsServer(tdb)
		stubSovereigntyDomainAccess(t, tdb)
		tdb.qID.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID:         agentID,
				Domain:          "example.com",
				LocalID:         "agent",
				Status:          models.SoulAgentStatusActive,
				LifecycleStatus: models.SoulAgentStatusActive,
				UpdatedAt:       time.Now().Add(-time.Minute).UTC(),
			}
		}).Once()
		return s, tdb
	}

	t.Run("validation_errors", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name string
			body map[string]any
		}{
			{name: "missing_dispute_id", body: map[string]any{"signal_ref": "x", "statement": "s"}},
			{name: "dispute_id_too_long", body: map[string]any{"dispute_id": strings.Repeat("a", 129), "signal_ref": "x", "statement": "s"}},
			{name: "missing_signal_ref", body: map[string]any{"dispute_id": "d1", "statement": "s"}},
			{name: "signal_ref_too_long", body: map[string]any{"dispute_id": "d1", "signal_ref": strings.Repeat("x", 1025), "statement": "s"}},
			{name: "evidence_too_long", body: map[string]any{"dispute_id": "d1", "signal_ref": "x", "evidence": strings.Repeat("e", 8193), "statement": "s"}},
			{name: "missing_statement", body: map[string]any{"dispute_id": "d1", "signal_ref": "x"}},
			{name: "statement_too_long", body: map[string]any{"dispute_id": "d1", "signal_ref": "x", "statement": strings.Repeat("s", 4097)}},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				s, _ := makeServer(t)
				body, _ := json.Marshal(tc.body)
				ctx := adminCtx()
				ctx.Params = map[string]string{"agentId": agentID}
				ctx.Request.Body = body
				if _, err := s.handleSoulCreateDispute(ctx); err == nil {
					t.Fatalf("expected error")
				}
			})
		}
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		s, tdb := makeServer(t)
		tdb.qDispute.On("Create").Return(nil).Once()

		body, _ := json.Marshal(map[string]any{
			"dispute_id": "d-1",
			"signal_ref": "sig-1",
			"evidence":   "e",
			"statement":  "s",
		})
		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": agentID}
		ctx.Request.Body = body

		resp, err := s.handleSoulCreateDispute(ctx)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != http.StatusCreated {
			t.Fatalf("expected 201, got %d (body=%q)", resp.Status, string(resp.Body))
		}
		var out models.SoulAgentDispute
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.AgentID != agentID || out.DisputeID != "d-1" || out.Status != models.SoulDisputeStatusOpen {
			t.Fatalf("unexpected dispute: %#v", out)
		}
	})

	t.Run("conflict_when_create_fails", func(t *testing.T) {
		t.Parallel()

		s, tdb := makeServer(t)
		tdb.qDispute.On("Create").Return(errors.New("boom")).Once()

		body, _ := json.Marshal(map[string]any{
			"dispute_id": "d-1",
			"signal_ref": "sig-1",
			"statement":  "s",
		})
		ctx := adminCtx()
		ctx.Params = map[string]string{"agentId": agentID}
		ctx.Request.Body = body

		if _, err := s.handleSoulCreateDispute(ctx); err == nil {
			t.Fatalf("expected conflict error")
		}
	})
}
