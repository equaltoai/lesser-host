package controlplane

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

type soulMineTestDB struct {
	db        *ttmocks.MockExtendedDB
	qInst     *ttmocks.MockQuery
	qDomain   *ttmocks.MockQuery
	qIdx      *ttmocks.MockQuery
	qIdentity *ttmocks.MockQuery
	qRep      *ttmocks.MockQuery
}

func newSoulMineTestDB() soulMineTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qIdx := new(ttmocks.MockQuery)
	qIdentity := new(ttmocks.MockQuery)
	qRep := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulDomainAgentIndex")).Return(qIdx).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdentity).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentReputation")).Return(qRep).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInst, qDomain, qIdx, qIdentity, qRep} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
	}

	return soulMineTestDB{db: db, qInst: qInst, qDomain: qDomain, qIdx: qIdx, qIdentity: qIdentity, qRep: qRep}
}

func TestHandleSoulListMyAgents_ReturnsOwnedAgents(t *testing.T) {
	t.Parallel()

	tdb := newSoulMineTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentA := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	agentB := "0x00000000000000000000000000000000000000000000000000000000000000bb"

	tdb.qInst.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{{Slug: "inst1"}}
	}).Once()

	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "example.com", InstanceSlug: "inst1"}}
	}).Once()

	tdb.qIdx.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{
			{Domain: "example.com", LocalID: "agent-a", AgentID: agentA},
			{Domain: "example.com", LocalID: "agent-b", AgentID: agentB},
		}
	}).Once()

	idCalls := 0
	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		switch idCalls {
		case 0:
			*dest = models.SoulAgentIdentity{AgentID: agentA, Domain: "example.com", LocalID: "agent-a", Wallet: "0x000000000000000000000000000000000000beef", Status: models.SoulAgentStatusActive, UpdatedAt: time.Now().UTC()}
		default:
			*dest = models.SoulAgentIdentity{AgentID: agentB, Domain: "example.com", LocalID: "agent-b", Wallet: "0x000000000000000000000000000000000000f00d", Status: models.SoulAgentStatusPending, UpdatedAt: time.Now().UTC()}
		}
		idCalls++
	}).Times(2)

	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentReputation](t, args, 0)
		*dest = models.SoulAgentReputation{AgentID: agentA, BlockRef: 10, Composite: 0.25, Economic: 0.25, UpdatedAt: time.Now().UTC()}
	}).Once()
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{RequestID: "r1", AuthIdentity: "alice"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handleSoulListMyAgents(ctx)
	if err != nil {
		t.Fatalf("handleSoulListMyAgents: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulMineAgentsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 2 || len(out.Agents) != 2 {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.Agents[0].Agent.AgentID != agentA {
		t.Fatalf("expected first agent %q, got %q", agentA, out.Agents[0].Agent.AgentID)
	}
	if out.Agents[0].Reputation == nil || out.Agents[0].Reputation.BlockRef != 10 {
		t.Fatalf("expected reputation for agentA, got %#v", out.Agents[0].Reputation)
	}
	if out.Agents[1].Agent.AgentID != agentB {
		t.Fatalf("expected second agent %q, got %q", agentB, out.Agents[1].Agent.AgentID)
	}
	if out.Agents[1].Reputation != nil {
		t.Fatalf("expected no reputation for agentB, got %#v", out.Agents[1].Reputation)
	}
}

func TestHandleSoulListMyAgents_IncludesManagedStageDomainAgents(t *testing.T) {
	t.Parallel()

	tdb := newSoulMineTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			Stage:                       "lab",
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
	}

	agentID := "0xaf87d423717e3aecbb2fc829d6224ea2acb66e7475f88c920d1a53e0789f313d"

	tdb.qInst.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{{Slug: "simulacrum", HostedBaseDomain: "simulacrum.greater.website"}}
	}).Once()

	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "simulacrum.greater.website", InstanceSlug: "simulacrum"}}
	}).Once()

	tdb.qIdx.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{
			Domain:  "dev.simulacrum.greater.website",
			LocalID: "agent-0",
			AgentID: agentID,
		}}
	}).Twice()

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:   agentID,
			Domain:    "dev.simulacrum.greater.website",
			LocalID:   "agent-0",
			Wallet:    "0xf7c8c15eefb7a907ceee47ae26f3243dd3bcf59f",
			Status:    models.SoulAgentStatusPending,
			UpdatedAt: time.Now().UTC(),
		}
	}).Once()
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(theoryErrors.ErrItemNotFound).Once()

	ctx := &apptheory.Context{RequestID: "r1", AuthIdentity: "alice"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handleSoulListMyAgents(ctx)
	if err != nil {
		t.Fatalf("handleSoulListMyAgents: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%q)", resp.Status, string(resp.Body))
	}

	var out soulMineAgentsResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count != 1 || len(out.Agents) != 1 {
		t.Fatalf("unexpected response: %#v", out)
	}
	if out.Agents[0].Agent.AgentID != agentID {
		t.Fatalf("expected agent %q, got %q", agentID, out.Agents[0].Agent.AgentID)
	}
	if out.Agents[0].Agent.Domain != "dev.simulacrum.greater.website" {
		t.Fatalf("expected managed stage domain, got %#v", out.Agents[0].Agent.Domain)
	}
}
