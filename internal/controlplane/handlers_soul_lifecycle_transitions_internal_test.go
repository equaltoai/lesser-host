package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandleSoulArchiveAgent_ArchivesAndCreatesFinalContinuityEntry(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()

	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: agentIDHex,
			Domain:  "example.com",
			LocalID: "agent-alice",
			Wallet:  "0x000000000000000000000000000000000000beef",
			Status:  models.SoulAgentStatusActive,
		}
	}).Once()

	tb.On("Update", mock.Anything, mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		ident := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		if ident.Status != models.SoulAgentStatusArchived {
			t.Fatalf("expected status archived, got %q", ident.Status)
		}
		if ident.LifecycleStatus != models.SoulAgentStatusArchived {
			t.Fatalf("expected lifecycle status archived, got %q", ident.LifecycleStatus)
		}
		if strings.TrimSpace(ident.LifecycleReason) != "done" {
			t.Fatalf("expected lifecycle reason, got %q", ident.LifecycleReason)
		}
	}).Once()
	tb.On("Create", mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		entry := testutil.RequireMockArg[*models.SoulAgentContinuity](t, args, 0)
		if entry.AgentID != agentIDHex {
			t.Fatalf("unexpected continuity agent id: %q", entry.AgentID)
		}
		if entry.Type != models.SoulContinuityEntryTypeArchived {
			t.Fatalf("expected archived entry type, got %q", entry.Type)
		}
		if !strings.Contains(entry.Summary, "done") {
			t.Fatalf("expected summary to include reason, got %q", entry.Summary)
		}
	}).Once()
	tb.On("Put", mock.Anything, mock.Anything).Return(tb).Once()

	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
		Request: apptheory.Request{
			Body: []byte(`{"reason":"done"}`),
		},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	resp, err := s.handleSoulArchiveAgent(ctx)
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
	if out.Status != models.SoulAgentStatusArchived {
		t.Fatalf("expected archived status, got %q", out.Status)
	}
}

func TestHandleSoulDesignateSuccessor_SucceedsAndCreatesEntries(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()

	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tdb.db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex
	successorIDHex := "0x" + strings.Repeat("22", 32)

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()

	// First identity read: the current agent.
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: agentIDHex,
			Domain:  "example.com",
			LocalID: "agent-alice",
			Wallet:  "0x000000000000000000000000000000000000beef",
			Status:  models.SoulAgentStatusActive,
		}
	}).Once()
	// Second identity read: the successor.
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: successorIDHex,
			Domain:  "example.com",
			LocalID: "agent-bob",
			Wallet:  "0x000000000000000000000000000000000000cafe",
			Status:  models.SoulAgentStatusActive,
		}
	}).Once()

	tb.On("Update", mock.Anything, mock.Anything, mock.Anything).Return(tb).Once().Run(func(args mock.Arguments) {
		ident := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		if ident.Status != models.SoulAgentStatusSucceeded {
			t.Fatalf("expected status succeeded, got %q", ident.Status)
		}
		if ident.SuccessorAgentId != successorIDHex {
			t.Fatalf("expected successor agent id %q, got %q", successorIDHex, ident.SuccessorAgentId)
		}
	}).Once()

	created := map[string]*models.SoulAgentContinuity{}
	tb.On("Create", mock.Anything, mock.Anything).Return(tb).Twice().Run(func(args mock.Arguments) {
		entry := testutil.RequireMockArg[*models.SoulAgentContinuity](t, args, 0)
		created[entry.AgentID+"|"+entry.Type] = entry
	})
	tb.On("Put", mock.Anything, mock.Anything).Return(tb).Once()

	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
		Request: apptheory.Request{
			Body: []byte(`{"successor_agent_id":"` + successorIDHex + `","reason":"upgrade"}`),
		},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	resp, err := s.handleSoulDesignateSuccessor(ctx)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	if created[agentIDHex+"|"+models.SoulContinuityEntryTypeSuccessionDeclared] == nil {
		t.Fatalf("expected succession_declared entry for primary agent")
	}
	if created[successorIDHex+"|"+models.SoulContinuityEntryTypeSuccessionReceived] == nil {
		t.Fatalf("expected succession_received entry for successor agent")
	}
}

func TestHandleSoulArchiveAgent_RejectsInvalidTransition(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: agentIDHex,
			Domain:  "example.com",
			LocalID: "agent-alice",
			Status:  models.SoulAgentStatusArchived,
		}
	}).Once()

	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	_, err := s.handleSoulArchiveAgent(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "app.conflict" {
		t.Fatalf("expected app.conflict, got %s", appErr.Code)
	}
}

func TestHandleSoulAgentUpdateRegistration_ArchivedAgentRejected(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	s := &Server{
		store:     store.New(tdb.db),
		soulPacks: &fakeSoulPackStore{},
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRPCURL:                  "http://rpc.local",
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: agentIDHex,
			Domain:  "example.com",
			LocalID: "agent-alice",
			Wallet:  "0x000000000000000000000000000000000000beef",
			Status:  models.SoulAgentStatusArchived,
		}
	}).Once()

	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
		Request: apptheory.Request{
			Body: []byte(`{"version":"2"}`),
		},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	_, err := s.handleSoulAgentUpdateRegistration(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "app.conflict" {
		t.Fatalf("expected app.conflict, got %s", appErr.Code)
	}
}

func TestHandleSoulArchiveAgent_TransactionFailureReturnsInternalError(t *testing.T) {
	t.Parallel()

	tdb := newSoulLifecycleTestDB()
	tb := new(ttmocks.MockTransactionBuilder)
	tdb.db.TransactWriteBuilder = tb
	tb.On("Execute").Return(errors.New("boom")).Once()

	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentIDHex := soulLifecycleTestAgentIDHex

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
		*dest = models.Domain{Domain: "example.com", InstanceSlug: "inst1", Status: models.DomainStatusVerified}
	}).Once()
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "inst1", Owner: "alice"}
	}).Once()

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID: agentIDHex,
			Domain:  "example.com",
			LocalID: "agent-alice",
			Status:  models.SoulAgentStatusActive,
		}
	}).Once()

	ctx := &apptheory.Context{
		RequestID:    "r1",
		AuthIdentity: "alice",
		Params:       map[string]string{"agentId": agentIDHex},
		Request: apptheory.Request{
			Body: []byte(`{"reason":"done"}`),
		},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

	_, err := s.handleSoulArchiveAgent(ctx)
	if err == nil {
		t.Fatalf("expected error")
	}
	appErr, ok := err.(*apptheory.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "app.internal" {
		t.Fatalf("expected app.internal, got %s", appErr.Code)
	}
}
