package controlplane

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestHandleSoulArchiveAgentBegin_MorePaths(t *testing.T) {
	t.Parallel()

	agentIDHex := soulLifecycleTestAgentIDHex

	t.Run("success", func(t *testing.T) {
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
			AuthIdentity: "alice",
			Params:       map[string]string{"agentId": agentIDHex},
		}
		ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

		resp, err := s.handleSoulArchiveAgentBegin(ctx)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.Status)
		}

		var out soulArchiveBeginResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Version != "1" || out.Entry.AgentID != agentIDHex || out.Entry.Type != models.SoulContinuityEntryTypeArchived {
			t.Fatalf("unexpected response: %#v", out)
		}
		if len(out.Entry.References) != 1 || out.Entry.References[0] != "agent:"+agentIDHex || !strings.HasPrefix(out.Entry.DigestHex, "0x") {
			t.Fatalf("unexpected continuity entry: %#v", out.Entry)
		}
	})

	t.Run("rejects_invalid_status", func(t *testing.T) {
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
				Status:  models.SoulAgentStatusArchived,
			}
		}).Once()

		ctx := &apptheory.Context{
			AuthIdentity: "alice",
			Params:       map[string]string{"agentId": agentIDHex},
		}
		ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

		if _, err := s.handleSoulArchiveAgentBegin(ctx); err == nil {
			t.Fatalf("expected conflict error")
		}
	})
}

func TestHandleSoulDesignateSuccessorBegin_MorePaths(t *testing.T) {
	t.Parallel()

	agentIDHex := soulLifecycleTestAgentIDHex
	successorIDHex := "0x" + strings.Repeat("22", 32)

	t.Run("success", func(t *testing.T) {
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
				AgentID: successorIDHex,
				Domain:  "example.com",
				Status:  models.SoulAgentStatusActive,
			}
		}).Once()
		tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID: agentIDHex,
				Domain:  "example.com",
				Status:  models.SoulAgentStatusActive,
			}
		}).Once()

		ctx := &apptheory.Context{
			AuthIdentity: "alice",
			Params:       map[string]string{"agentId": agentIDHex},
			Request:      apptheory.Request{Body: []byte(`{"successor_agent_id":"` + successorIDHex + `"}`)},
		}
		ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

		resp, err := s.handleSoulDesignateSuccessorBegin(ctx)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.Status != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.Status)
		}

		var out soulDesignateSuccessorBeginResponse
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out.Version != "1" || out.PredecessorEntry.AgentID != agentIDHex || out.SuccessorEntry.AgentID != successorIDHex {
			t.Fatalf("unexpected response: %#v", out)
		}
		if out.PredecessorEntry.Type != models.SoulContinuityEntryTypeSuccessionDeclared || out.SuccessorEntry.Type != models.SoulContinuityEntryTypeSuccessionReceived {
			t.Fatalf("unexpected continuity types: %#v", out)
		}
	})

	t.Run("rejects_invalid_successor_inputs", func(t *testing.T) {
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
				Status:  models.SoulAgentStatusActive,
			}
		}).Once()

		ctx := &apptheory.Context{
			AuthIdentity: "alice",
			Params:       map[string]string{"agentId": agentIDHex},
			Request:      apptheory.Request{Body: []byte(`{"successor_agent_id":"` + agentIDHex + `"}`)},
		}
		ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

		if _, err := s.handleSoulDesignateSuccessorBegin(ctx); err == nil {
			t.Fatalf("expected self-successor error")
		}
	})

	t.Run("rejects_inactive_successor", func(t *testing.T) {
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
				Status:  models.SoulAgentStatusActive,
			}
		}).Once()
		tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			*dest = models.SoulAgentIdentity{
				AgentID: successorIDHex,
				Domain:  "example.com",
				Status:  models.SoulAgentStatusArchived,
			}
		}).Once()

		ctx := &apptheory.Context{
			AuthIdentity: "alice",
			Params:       map[string]string{"agentId": agentIDHex},
			Request:      apptheory.Request{Body: []byte(`{"successor_agent_id":"` + successorIDHex + `"}`)},
		}
		ctx.Set(ctxKeyOperatorRole, models.RoleOperator)

		if _, err := s.handleSoulDesignateSuccessorBegin(ctx); err == nil {
			t.Fatalf("expected inactive-successor error")
		}
	})
}
