package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func newPortalSoulRosterServer(t *testing.T, handler http.HandlerFunc) (*Server, *httptest.Server, soulMineTestDB) {
	t.Helper()

	tdb := newSoulMineTestDB()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	s := &Server{
		store: store.New(tdb.db),
		cfg: config.Config{
			Stage:                       "lab",
			SoulEnabled:                 true,
			SoulChainID:                 1,
			SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
		},
		portalCostHTTPClient: ts.Client(),
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) {
			return ts.URL, nil
		},
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			t.Fatal("portal soul roster should not expose or require raw instance keys for public agent metadata")
			return "", nil
		},
	}
	return s, ts, tdb
}

func seedPortalSoulRosterQueries(t *testing.T, tdb soulMineTestDB, agentID string) {
	t.Helper()

	tdb.qInst.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{{
			Slug:             "simulacrum",
			Owner:            "alice",
			HostedBaseDomain: "simulacrum.greater.website",
		}}
	}).Once()

	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "simulacrum.greater.website", InstanceSlug: "simulacrum", Status: models.DomainStatusVerified}}
	}).Once()

	idxCalls := 0
	tdb.qIdx.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		if idxCalls == 0 {
			*dest = []*models.SoulDomainAgentIndex{{
				Domain:  "dev.simulacrum.greater.website",
				LocalID: "agent-a",
				AgentID: agentID,
			}}
		} else {
			*dest = []*models.SoulDomainAgentIndex{}
		}
		idxCalls++
	}).Twice()

	tdb.qIdentity.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
		*dest = models.SoulAgentIdentity{
			AgentID:   agentID,
			Domain:    "dev.simulacrum.greater.website",
			LocalID:   "agent-a",
			Wallet:    "0x000000000000000000000000000000000000beef",
			Status:    models.SoulAgentStatusActive,
			UpdatedAt: time.Now().UTC(),
		}
	}).Once()

	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentReputation](t, args, 0)
		*dest = models.SoulAgentReputation{AgentID: agentID, TipsReceived: 7, UpdatedAt: time.Now().UTC()}
	}).Once()
}

func TestHandlePortalSoulRoster_EnrichesRowsFromManagedLesser(t *testing.T) {
	t.Parallel()

	agentID := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	var gotPath string
	var gotAuth string
	s, _, tdb := newPortalSoulRosterServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"username": "agent-a",
			"display_name": "Agent A",
			"agent_type": "assistant",
			"agent_version": "gpt-5-mini"
		}`)
	})
	seedPortalSoulRosterQueries(t, tdb, agentID)

	ctx := &apptheory.Context{RequestID: "r1", AuthIdentity: "alice"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handlePortalSoulRoster(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.Status)
	require.Equal(t, "/api/v1/agents/agent-a", gotPath)
	require.Empty(t, gotAuth)

	var out portalSoulRosterResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Equal(t, 1, out.Count)
	require.Len(t, out.Souls, 1)
	require.Equal(t, "simulacrum", out.Souls[0].Instance.Slug)
	require.Equal(t, "dev.simulacrum.greater.website", out.Souls[0].Instance.Domain)
	require.NotNil(t, out.Souls[0].LesserAgent)
	require.Equal(t, "loaded", out.Souls[0].LesserAgent.Status)
	require.Equal(t, "gpt-5-mini", out.Souls[0].LesserAgent.AgentVersion)
	require.Equal(t, int64(7), out.Souls[0].Tips.Received)
	require.Equal(t, "all_time", out.Souls[0].Tips.Period)

	require.NotNil(t, out.Souls[0].AnchorAssurance)
	require.NotEmpty(t, out.Souls[0].AnchorAssurance.State)
	require.True(t, out.Souls[0].AnchorAssurance.State == models.SoulAnchorStateImmutableOnchain ||
		out.Souls[0].AnchorAssurance.State == models.SoulAnchorStateHostedOffchain,
		"unexpected anchor assurance state: %s", out.Souls[0].AnchorAssurance.State)
}

func TestHandlePortalSoulRoster_KeepsRowsWhenLesserMetadataUnavailable(t *testing.T) {
	t.Parallel()

	agentID := "0x00000000000000000000000000000000000000000000000000000000000000aa"
	s, _, tdb := newPortalSoulRosterServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"temporarily unavailable"}`))
	})
	seedPortalSoulRosterQueries(t, tdb, agentID)

	ctx := &apptheory.Context{RequestID: "r1", AuthIdentity: "alice"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handlePortalSoulRoster(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.Status)

	var out portalSoulRosterResponse
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	require.Equal(t, 1, out.Count)
	require.NotNil(t, out.Souls[0].LesserAgent)
	require.Equal(t, "unavailable", out.Souls[0].LesserAgent.Status)
	require.Empty(t, out.Souls[0].LesserAgent.AgentVersion)
	require.NotNil(t, out.Souls[0].AnchorAssurance)
	require.NotEmpty(t, out.Souls[0].AnchorAssurance.State)
}

func TestPortalSoulRosterTipsFromReputationDefaultsToZeroAllTime(t *testing.T) {
	t.Parallel()

	got := portalSoulRosterTipsFromReputation(nil)
	require.Equal(t, int64(0), got.Received)
	require.Equal(t, "all_time", got.Period)
	require.Equal(t, "Tip events · all time", got.Label)
}

func TestListSoulRosterCandidatesForDomainsDedupesAgents(t *testing.T) {
	t.Parallel()

	tdb := newSoulMineTestDB()
	s := &Server{store: store.New(tdb.db)}
	agentID := "0x00000000000000000000000000000000000000000000000000000000000000aa"

	tdb.qIdx.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{Domain: "one.example", LocalID: "agent-a", AgentID: agentID}}
	}).Once()
	tdb.qIdx.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{Domain: "two.example", LocalID: "agent-a", AgentID: agentID}}
	}).Once()

	candidates, appErr := s.listSoulRosterCandidatesForDomains(&apptheory.Context{}, map[string]*models.Instance{
		"one.example": {Slug: "one"},
		"two.example": {Slug: "two"},
	})
	require.Nil(t, appErr)
	require.Len(t, candidates, 1)
	require.Equal(t, agentID, candidates[0].agentID)
}

func TestListSoulRosterCandidatesForDomainsIgnoresNotFound(t *testing.T) {
	t.Parallel()

	tdb := newSoulMineTestDB()
	s := &Server{store: store.New(tdb.db)}
	tdb.qIdx.On("All", mock.Anything).Return(theoryErrors.ErrItemNotFound).Once()

	candidates, appErr := s.listSoulRosterCandidatesForDomains(&apptheory.Context{}, map[string]*models.Instance{
		"empty.example": {Slug: "empty"},
	})
	require.Nil(t, appErr)
	require.Empty(t, candidates)
}

// TestListSoulRosterDomainOwners_RespectsInstanceOwnershipBoundary verifies
// that listSoulRosterDomainOwners only builds domain entries for the
// instances it is given and never expands to other instances' domains.
// This is a tenant-isolation regression test: the roster must not leak
// souls under domains the caller does not own.
func TestListSoulRosterDomainOwners_RespectsInstanceOwnershipBoundary(t *testing.T) {
	t.Parallel()

	tdb := newSoulMineTestDB()
	s := &Server{
		store: store.New(tdb.db),
		cfg:   config.Config{Stage: "lab"},
	}

	// Alice owns one instance with one verified domain.
	// qDomain.All is set up for exactly one call (alice's instance).
	// If listSoulRosterDomainOwners were to query for any other
	// instance's domains, testify/mock would fail because the call
	// count would exceed the expected Once().
	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{
			{Domain: "alice.example", InstanceSlug: "alice-inst", Status: models.DomainStatusVerified},
		}
	}).Once()

	domainOwners, appErr := s.listSoulRosterDomainOwners(context.Background(), []*models.Instance{
		{Slug: "alice-inst", Owner: "alice", HostedBaseDomain: "alice-inst.greater.website"},
	})
	require.Nil(t, appErr)

	// Must contain alice's verified domain.
	require.Contains(t, domainOwners, "alice.example")
	require.NotNil(t, domainOwners["alice.example"])
	require.Equal(t, "alice-inst", domainOwners["alice.example"].Slug)

	// Must contain the managed staged domain.
	managedDomain := managedInstanceStageDomain("lab", "alice-inst.greater.website")
	require.Contains(t, domainOwners, managedDomain)

	// Must NOT contain any domain from an unowned instance.
	// If a future change added a "query all domains" fallback here,
	// this assertion would fail — and testify/mock would have already
	// panicked above because qDomain.All was only set up for one call.
	require.NotContains(t, domainOwners, "bob.example")
}
