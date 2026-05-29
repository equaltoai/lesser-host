package controlplane

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// portalTrustTestDB wraps mock DB and queries for trust-data handler tests.
type portalTrustTestDB struct {
	db        *ttmocks.MockExtendedDB
	qUser     *ttmocks.MockQuery
	qInstance *ttmocks.MockQuery
}

// newPortalTrustTestDB constructs a mock DB with queries for User and Instance models.
func newPortalTrustTestDB(t *testing.T) *portalTrustTestDB {
	t.Helper()
	db, queries := newTestDBWithModelQueries("*models.User", "*models.Instance")
	tdb := &portalTrustTestDB{
		db:        db,
		qUser:     queries[0],
		qInstance: queries[1],
	}

	// Default: user "alice" exists as customer
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{Username: "alice", Role: "customer", Approved: true}
	}).Maybe()

	return tdb
}

// stubOwnedInstance makes qInstance.First return an instance owned by owner.
func stubOwnedInstance(t *testing.T, q *ttmocks.MockQuery, slug, owner string) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: slug, Owner: owner, Status: models.InstanceStatusActive}
	}).Once()
}

// stubInstanceNotFound makes qInstance.First return ErrItemNotFound.
func stubInstanceNotFound(t *testing.T, q *ttmocks.MockQuery) {
	t.Helper()
	q.On("First", mock.AnythingOfType("*models.Instance")).Return(theoryErrors.ErrItemNotFound).Once()
}

func TestHandlePortalGetTrustData_HappyPath(t *testing.T) {
	t.Parallel()

	tdb := newPortalTrustTestDB(t)
	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")

	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	require.Equal(t, http.StatusOK, resp.Status)

	var data portalTrustDataResponse
	err = json.Unmarshal(resp.Body, &data)
	require.NoError(t, err)

	// Verify instance slug
	require.Equal(t, "demo", data.InstanceSlug)

	// Federation: zero defaults
	require.Equal(t, 0, data.Federation.Reachable)
	require.Equal(t, 0, data.Federation.Warning)
	require.Equal(t, 0, data.Federation.Severed)
	require.NotNil(t, data.Federation.Peers)
	require.Len(t, data.Federation.Peers, 0)

	// Signatures: zero defaults with correct window
	require.Equal(t, 168, data.Signatures.WindowHours)
	require.Equal(t, 0, data.Signatures.TotalFailures)
	require.NotNil(t, data.Signatures.BySource)
	require.Len(t, data.Signatures.BySource, 0)

	// Queue depth: empty series
	require.NotNil(t, data.QueueDepth.Series)
	require.Len(t, data.QueueDepth.Series, 0)

	// Trust score: documented zero placeholder (soul not enabled)
	require.Equal(t, 0.0, data.TrustScore.Score)
	require.NotEmpty(t, data.TrustScore.Formula)
	require.Contains(t, data.TrustScore.Formula, "trust_score")
	require.NotEmpty(t, data.TrustScore.Source)
	require.Contains(t, data.TrustScore.Source, "soul_agent_reputation")
	require.Equal(t, 0.0, data.TrustScore.Dimensions.Operational)
	require.Equal(t, 0.0, data.TrustScore.Dimensions.Attestation)
	require.Equal(t, 0.0, data.TrustScore.Dimensions.Social)
	require.Equal(t, 0.0, data.TrustScore.Dimensions.Economic)
	require.Equal(t, 0.0, data.TrustScore.Dimensions.Integrity)

	// Vouches: empty list
	require.NotNil(t, data.Vouches.Items)
	require.Len(t, data.Vouches.Items, 0)
	require.Equal(t, 0, data.Vouches.Count)
}

func TestHandlePortalGetTrustData_Unauthenticated(t *testing.T) {
	t.Parallel()

	tdb := newPortalTrustTestDB(t)
	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}
	ctx := &apptheory.Context{
		Params: map[string]string{"slug": "demo"},
	}

	_, err := s.handlePortalGetTrustData(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.unauthorized", appErr.Code)
}

func TestHandlePortalGetTrustData_Forbidden(t *testing.T) {
	t.Parallel()

	tdb := newPortalTrustTestDB(t)
	stubOwnedInstance(t, tdb.qInstance, "demo", "bob") // owned by bob, not alice

	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	_, err := s.handlePortalGetTrustData(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.forbidden", appErr.Code)
}

func TestHandlePortalGetTrustData_NotFound(t *testing.T) {
	t.Parallel()

	tdb := newPortalTrustTestDB(t)
	stubInstanceNotFound(t, tdb.qInstance)

	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "missing"},
	}

	_, err := s.handlePortalGetTrustData(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.not_found", appErr.Code)
}

func TestHandlePortalGetTrustData_OperatorBypass(t *testing.T) {
	t.Parallel()

	tdb := newPortalTrustTestDB(t)
	stubOwnedInstance(t, tdb.qInstance, "demo", "bob") // owned by bob

	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}
	ctx := &apptheory.Context{
		AuthIdentity: "admin",
		Params:       map[string]string{"slug": "demo"},
	}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	var data portalTrustDataResponse
	err = json.Unmarshal(resp.Body, &data)
	require.NoError(t, err)
	require.Equal(t, "demo", data.InstanceSlug)
}

func TestHandlePortalGetTrustData_EmptySlug(t *testing.T) {
	t.Parallel()

	tdb := newPortalTrustTestDB(t)
	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": ""},
	}

	_, err := s.handlePortalGetTrustData(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.bad_request", appErr.Code)
}

func TestHandlePortalGetTrustData_NilServer(t *testing.T) {
	t.Parallel()

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	_, err := (*Server)(nil).handlePortalGetTrustData(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.internal", appErr.Code)
}

func TestHandlePortalGetTrustData_NilStore(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{}}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	_, err := s.handlePortalGetTrustData(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.internal", appErr.Code)
}

// TestHandlePortalGetTrustData_RedactionProof verifies that the response DTO
// does not leak internal storage fields, raw secrets, account IDs, or
// cross-tenant identifiers.
func TestHandlePortalGetTrustData_RedactionProof(t *testing.T) {
	t.Parallel()

	tdb := newPortalTrustTestDB(t)
	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")

	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)

	body := string(resp.Body)

	// No PK/SK/internal storage fields
	require.NotContains(t, body, `"PK"`)
	require.NotContains(t, body, `"SK"`)
	require.NotContains(t, body, `"gsi1PK"`)
	require.NotContains(t, body, `"gsi1SK"`)
	require.NotContains(t, body, `"TTL"`)
	require.NotContains(t, body, `"ttl"`)

	// No account IDs
	require.NotContains(t, body, `"account_id"`)
	require.NotContains(t, body, `"accountId"`)
	require.NotContains(t, body, `"hostedAccountId"`)
	require.NotContains(t, body, `"hosted_account_id"`)

	// No raw secrets or keys
	require.NotContains(t, body, `"secret"`)
	require.NotContains(t, body, `"key_id"`)
	require.NotContains(t, body, `"raw_key"`)
	require.NotContains(t, body, `"jws"`)

	// No PII patterns (partial check)
	require.NotContains(t, body, `"PAN"`)
	require.NotContains(t, body, `"CVV"`)

	// Verify we CAN unmarshal — structure is clean
	var data portalTrustDataResponse
	err = json.Unmarshal(resp.Body, &data)
	require.NoError(t, err)
}

// TestHandlePortalGetTrustData_TenantIsolation verifies that a customer
// cannot access another customer's trust data, and that the response does
// not leak a global federation graph.
func TestHandlePortalGetTrustData_TenantIsolation(t *testing.T) {
	t.Parallel()

	t.Run("customer_cannot_access_other_instance", func(t *testing.T) {
		t.Parallel()

		tdb := newPortalTrustTestDB(t)
		// Stub bob's instance — owned by bob, not alice
		stubOwnedInstance(t, tdb.qInstance, "bob-instance", "bob")

		s := &Server{store: store.New(tdb.db), cfg: config.Config{}}

		// Alice tries to access bob's instance — should get forbidden
		ctx := &apptheory.Context{
			AuthIdentity: "alice",
			Params:       map[string]string{"slug": "bob-instance"},
		}
		_, err := s.handlePortalGetTrustData(ctx)
		require.Error(t, err)
		appErr, ok := err.(*apptheory.AppError)
		require.True(t, ok)
		require.Equal(t, "app.forbidden", appErr.Code)
	})

	t.Run("response_contains_only_own_instance_data", func(t *testing.T) {
		t.Parallel()

		tdb := newPortalTrustTestDB(t)
		stubOwnedInstance(t, tdb.qInstance, "my-instance", "alice")

		s := &Server{store: store.New(tdb.db), cfg: config.Config{}}
		ctx := &apptheory.Context{
			AuthIdentity: "alice",
			Params:       map[string]string{"slug": "my-instance"},
		}

		resp, err := s.handlePortalGetTrustData(ctx)
		require.NoError(t, err)

		var data portalTrustDataResponse
		err = json.Unmarshal(resp.Body, &data)
		require.NoError(t, err)

		// Only own instance slug in response
		require.Equal(t, "my-instance", data.InstanceSlug)

		// No other slugs in response body
		body := string(resp.Body)
		require.NotContains(t, body, "other-instance")
	})
}

// TestHandlePortalGetTrustData_ResponseShapeStability verifies the response
// JSON shape is stable — all expected top-level keys are present.
func TestHandlePortalGetTrustData_ResponseShapeStability(t *testing.T) {
	t.Parallel()

	tdb := newPortalTrustTestDB(t)
	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")

	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	err = json.Unmarshal(resp.Body, &raw)
	require.NoError(t, err)

	// Verify all expected top-level keys are present
	expectedKeys := []string{"instance_slug", "federation", "signatures", "queue_depth", "trust_score", "vouches"}
	for _, key := range expectedKeys {
		_, ok := raw[key]
		require.True(t, ok, "missing key: %s", key)
	}

	// Verify no unexpected top-level keys
	for key := range raw {
		found := false
		for _, expected := range expectedKeys {
			if key == expected {
				found = true
				break
			}
		}
		require.True(t, found, "unexpected key in response: %s", key)
	}
}

// ============================================================================
// M16 populated-data tests — soul registry enabled with bound agents
// ============================================================================

// soulTrustTestDB is the extended mock DB for tests that exercise the
// soul-backed trust-data resolution path.
type soulTrustTestDB struct {
	db        *ttmocks.MockExtendedDB
	qUser     *ttmocks.MockQuery
	qInstance *ttmocks.MockQuery
	qDomain   *ttmocks.MockQuery
	qIdx      *ttmocks.MockQuery
	qRep      *ttmocks.MockQuery
	qFailure  *ttmocks.MockQuery
	qEndorse  *ttmocks.MockQuery
}

func newSoulTrustTestDB(t *testing.T) *soulTrustTestDB {
	t.Helper()

	db := ttmocks.NewMockExtendedDB()
	db.On("WithContext", mock.Anything).Return(db).Maybe()

	qs := make(map[string]*ttmocks.MockQuery)
	md := []struct {
		typeName string
		key      string
	}{
		{"*models.User", "qUser"},
		{"*models.Instance", "qInstance"},
		{"*models.Domain", "qDomain"},
		{"*models.SoulDomainAgentIndex", "qIdx"},
		{"*models.SoulAgentReputation", "qRep"},
		{"*models.SoulAgentFailure", "qFailure"},
		{"*models.SoulAgentPeerEndorsement", "qEndorse"},
	}

	for _, m := range md {
		q := new(ttmocks.MockQuery)
		qs[m.key] = q
		db.On("Model", mock.AnythingOfType(m.typeName)).Return(q).Maybe()
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
	}

	tdb := &soulTrustTestDB{
		db:        db,
		qUser:     qs["qUser"],
		qInstance: qs["qInstance"],
		qDomain:   qs["qDomain"],
		qIdx:      qs["qIdx"],
		qRep:      qs["qRep"],
		qFailure:  qs["qFailure"],
		qEndorse:  qs["qEndorse"],
	}

	// Default: user "alice" exists as customer
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{Username: "alice", Role: "customer", Approved: true}
	}).Maybe()

	return tdb
}

// soulEnabledConfig returns a config with soul enabled but all other fields
// at safe zero/empty defaults so handlers don't panic on missing rpc/chain/etc.
func soulEnabledConfig() config.Config {
	return config.Config{
		SoulEnabled:                 true,
		SoulChainID:                 1,
		SoulRegistryContractAddress: "0x0000000000000000000000000000000000000001",
	}
}

func TestHandlePortalGetTrustData_SoulPopulatedReputation(t *testing.T) {
	t.Parallel()

	tdb := newSoulTrustTestDB(t)
	agentID := "0x0000000000000000000000000000000000000000000000000000000000000aaa"

	// 1. Instance owned by alice
	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")

	// 2. Verified domain for the instance
	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()

	// 3. SoulDomainAgentIndex returns one agent
	tdb.qIdx.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{
			{Domain: "example.com", LocalID: "agent-0", AgentID: agentID},
		}
	}).Once()

	// 4. Reputation row with Composite = 80.0 and dimension scores
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentReputation](t, args, 0)
		*dest = models.SoulAgentReputation{
			AgentID:    agentID,
			Composite:  80.0,
			Trust:      75.0,
			Validation: 82.0,
			Social:     70.0,
			Economic:   88.0,
			Integrity:  85.0,
		}
	}).Once()

	// 5. No signature failures
	tdb.qFailure.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{}
	}).Once()

	// 6. No endorsements
	tdb.qEndorse.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPeerEndorsement](t, args, 0)
		*dest = []*models.SoulAgentPeerEndorsement{}
	}).Once()

	s := &Server{store: store.New(tdb.db), cfg: soulEnabledConfig()}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.Status)

	var data portalTrustDataResponse
	err = json.Unmarshal(resp.Body, &data)
	require.NoError(t, err)

	// Trust score should be populated from the reputation row
	require.Equal(t, 80.0, data.TrustScore.Score)
	require.NotEmpty(t, data.TrustScore.Formula)
	require.Contains(t, data.TrustScore.Source, "soul_agent_reputation")
	require.Equal(t, 75.0, data.TrustScore.Dimensions.Operational) // ← Trust
	require.Equal(t, 82.0, data.TrustScore.Dimensions.Attestation) // ← Validation
	require.Equal(t, 70.0, data.TrustScore.Dimensions.Social)      // ← Social
	require.Equal(t, 88.0, data.TrustScore.Dimensions.Economic)    // ← Economic
	require.Equal(t, 85.0, data.TrustScore.Dimensions.Integrity)   // ← Integrity
}

func TestHandlePortalGetTrustData_SoulPopulatedVouches(t *testing.T) {
	t.Parallel()

	tdb := newSoulTrustTestDB(t)
	agentID := "0x0000000000000000000000000000000000000000000000000000000000000aaa"

	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")

	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()

	tdb.qIdx.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{
			{Domain: "example.com", LocalID: "agent-0", AgentID: agentID},
		}
	}).Once()

	// No reputation
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(theoryErrors.ErrItemNotFound).Once()

	// No signature failures
	tdb.qFailure.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{}
	}).Once()

	// Two endorsements from peers
	now := time.Now().UTC()
	tdb.qEndorse.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPeerEndorsement](t, args, 0)
		*dest = []*models.SoulAgentPeerEndorsement{
			{
				AgentID:         agentID,
				EndorserAgentID: "0xpeer1111111111111111111111111111111111111111111111111111111111111111",
				Signature:       "0xsig1",
				CreatedAt:       now,
			},
			{
				AgentID:         agentID,
				EndorserAgentID: "0xpeer2222222222222222222222222222222222222222222222222222222222222222",
				Signature:       "0xsig2",
				CreatedAt:       now.Add(-1 * time.Hour),
			},
		}
	}).Once()

	s := &Server{store: store.New(tdb.db), cfg: soulEnabledConfig()}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.Status)

	var data portalTrustDataResponse
	err = json.Unmarshal(resp.Body, &data)
	require.NoError(t, err)

	// Vouches should be populated
	require.Equal(t, 2, data.Vouches.Count)
	require.Len(t, data.Vouches.Items, 2)

	for _, item := range data.Vouches.Items {
		require.NotEmpty(t, item.Peer)
		require.Equal(t, 1.0, item.Strength)
		require.Equal(t, "endorsement", item.Type)
		require.NotEmpty(t, item.CreatedAt)
		// No raw signature leaked
		require.NotEqual(t, "0xsig1", item.Peer)
		require.NotEqual(t, "0xsig2", item.Peer)
	}

	// Verify no raw signature leaks into the response
	body := string(resp.Body)
	require.NotContains(t, body, `"signature"`)
	require.NotContains(t, body, `"PK"`)
	require.NotContains(t, body, `"SK"`)
}

func TestHandlePortalGetTrustData_SoulPopulatedSignatureFailures(t *testing.T) {
	t.Parallel()

	tdb := newSoulTrustTestDB(t)
	agentA := "0x0000000000000000000000000000000000000000000000000000000000000aaa"
	agentB := "0x0000000000000000000000000000000000000000000000000000000000000bbb"
	now := time.Now().UTC()

	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")

	// Two verified domains
	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{
			{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified},
			{Domain: "other.com", InstanceSlug: "demo", Status: models.DomainStatusVerified},
		}
	}).Once()

	// Two domains → two SoulDomainAgentIndex queries; return agents for both.
	tdb.qIdx.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{
			{Domain: "example.com", LocalID: "agent-a", AgentID: agentA},
			{Domain: "other.com", LocalID: "agent-b", AgentID: agentB},
		}
	}).Twice()

	// No reputation for either agent
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(theoryErrors.ErrItemNotFound).Twice()

	// Agent A: 3 signature failures within window, 1 outside window, 1 non-signature
	tdb.qFailure.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{
			{FailureType: "signature_failure", Timestamp: now.Add(-1 * time.Hour)},
			{FailureType: "signature_failure", Timestamp: now.Add(-2 * time.Hour)},
			{FailureType: "signature_failure", Timestamp: now.Add(-3 * time.Hour)},
			{FailureType: "signature_failure", Timestamp: now.Add(-200 * time.Hour)}, // outside window
			{FailureType: "crash_loop", Timestamp: now.Add(-1 * time.Hour)},          // not signature
		}
	}).Once()

	// Agent B: 1 signature failure within window
	tdb.qFailure.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{
			{FailureType: "signature_failure", Timestamp: now.Add(-5 * time.Hour)},
		}
	}).Once()

	// No endorsements
	tdb.qEndorse.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPeerEndorsement](t, args, 0)
		*dest = []*models.SoulAgentPeerEndorsement{}
	}).Twice()

	s := &Server{store: store.New(tdb.db), cfg: soulEnabledConfig()}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.Status)

	var data portalTrustDataResponse
	err = json.Unmarshal(resp.Body, &data)
	require.NoError(t, err)

	// Total: 3 (agent A) + 1 (agent B) = 4
	require.Equal(t, 4, data.Signatures.TotalFailures)
	require.Equal(t, 168, data.Signatures.WindowHours)

	// By-source: 2 entries (one per agent)
	require.Len(t, data.Signatures.BySource, 2)

	// Build a map for easy assertion
	srcMap := make(map[string]int)
	for _, row := range data.Signatures.BySource {
		srcMap[row.Source] = row.Failures
	}
	require.Equal(t, 3, srcMap[agentA])
	require.Equal(t, 1, srcMap[agentB])

	// Verify no raw failure data leaks
	body := string(resp.Body)
	require.NotContains(t, body, `"failure_id"`)
	require.NotContains(t, body, `"FailureID"`)
	require.NotContains(t, body, `"description"`)
}

func TestHandlePortalGetTrustData_SoulNoAgentsBound(t *testing.T) {
	t.Parallel()

	tdb := newSoulTrustTestDB(t)
	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")

	// Instance has verified domains, but no agents are bound through the index
	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()

	// No agent index entries
	tdb.qIdx.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{}
	}).Once()

	s := &Server{store: store.New(tdb.db), cfg: soulEnabledConfig()}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)

	var data portalTrustDataResponse
	err = json.Unmarshal(resp.Body, &data)
	require.NoError(t, err)

	// All soul-backed fields remain zero/empty since no agents found
	require.Equal(t, 0.0, data.TrustScore.Score)
	require.Equal(t, 0, data.Signatures.TotalFailures)
	require.Equal(t, 0, data.Vouches.Count)
	require.Len(t, data.Vouches.Items, 0)
}

func TestHandlePortalGetTrustData_SoulNoVerifiedDomains(t *testing.T) {
	t.Parallel()

	tdb := newSoulTrustTestDB(t)
	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")

	// Only pending (unverified) domains — should be filtered out
	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "pending.example", InstanceSlug: "demo", Status: models.DomainStatusPending}}
	}).Once()

	s := &Server{store: store.New(tdb.db), cfg: soulEnabledConfig()}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)

	var data portalTrustDataResponse
	err = json.Unmarshal(resp.Body, &data)
	require.NoError(t, err)

	require.Equal(t, 0.0, data.TrustScore.Score)
	require.Equal(t, 0, data.Signatures.TotalFailures)
	require.Equal(t, 0, data.Vouches.Count)
}

func TestHandlePortalGetTrustData_SoulRedactionProofWithPopulatedData(t *testing.T) {
	t.Parallel()

	tdb := newSoulTrustTestDB(t)
	agentID := "0x0000000000000000000000000000000000000000000000000000000000000aaa"
	now := time.Now().UTC()

	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")

	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()

	tdb.qIdx.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{
			{Domain: "example.com", LocalID: "agent-0", AgentID: agentID},
		}
	}).Once()

	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentReputation](t, args, 0)
		*dest = models.SoulAgentReputation{AgentID: agentID, Composite: 50.0, Trust: 50.0}
	}).Once()

	tdb.qFailure.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{
			{
				FailureType: "signature_failure",
				FailureID:   "fail-001",
				Description: "sensitive description should not leak",
				Timestamp:   now,
			},
		}
	}).Once()

	tdb.qEndorse.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPeerEndorsement](t, args, 0)
		*dest = []*models.SoulAgentPeerEndorsement{
			{
				AgentID:         agentID,
				EndorserAgentID: "0xpeer1111111111111111111111111111111111111111111111111111111111111111",
				Signature:       "0xsensitive_raw_signature",
				Message:         "secret message",
				CreatedAt:       now,
			},
		}
	}).Once()

	s := &Server{store: store.New(tdb.db), cfg: soulEnabledConfig()}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)

	body := string(resp.Body)

	// Standard redaction checks
	require.NotContains(t, body, `"PK"`)
	require.NotContains(t, body, `"SK"`)
	require.NotContains(t, body, `"TTL"`)
	require.NotContains(t, body, `"account_id"`)

	// Soul-specific: no raw failure data leaked
	require.NotContains(t, body, `"failure_id"`)
	require.NotContains(t, body, `"fail-001"`)
	require.NotContains(t, body, `"sensitive description should not leak"`)

	// No raw endorsement data leaked
	require.NotContains(t, body, `"signature"`)
	require.NotContains(t, body, `"0xsensitive_raw_signature"`)
	require.NotContains(t, body, `"secret message"`)

	// No internal model keys leaked
	require.NotContains(t, body, `"endorser_agent_id"`)
	require.NotContains(t, body, `"message"`)
}

func TestHandlePortalGetTrustData_SoulTenantIsolationWithPopulatedData(t *testing.T) {
	t.Parallel()

	tdb := newSoulTrustTestDB(t)
	agentA := "0x0000000000000000000000000000000000000000000000000000000000000aaa"

	// Alice's instance "alice-inst" has its own agents
	stubOwnedInstance(t, tdb.qInstance, "alice-inst", "alice")

	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "alice.example", InstanceSlug: "alice-inst", Status: models.DomainStatusVerified}}
	}).Once()

	tdb.qIdx.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{
			{Domain: "alice.example", LocalID: "alice-agent", AgentID: agentA},
		}
	}).Once()

	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentReputation](t, args, 0)
		*dest = models.SoulAgentReputation{AgentID: agentA, Composite: 99.0}
	}).Once()

	tdb.qFailure.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{}
	}).Once()

	tdb.qEndorse.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPeerEndorsement](t, args, 0)
		*dest = []*models.SoulAgentPeerEndorsement{}
	}).Once()

	s := &Server{store: store.New(tdb.db), cfg: soulEnabledConfig()}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "alice-inst"},
	}

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)

	var data portalTrustDataResponse
	err = json.Unmarshal(resp.Body, &data)
	require.NoError(t, err)

	require.Equal(t, "alice-inst", data.InstanceSlug)
	require.Equal(t, 99.0, data.TrustScore.Score)

	// Verify no cross-tenant data leaked — response should not mention other slugs
	body := string(resp.Body)
	require.NotContains(t, body, "bob-inst")
}

func TestHandlePortalGetTrustData_SoulNotEnabledReturnsZeroDefaults(t *testing.T) {
	t.Parallel()

	tdb := newPortalTrustTestDB(t)
	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")

	// SoulEnabled = false (cfg is empty)
	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)

	var data portalTrustDataResponse
	err = json.Unmarshal(resp.Body, &data)
	require.NoError(t, err)

	// All zero defaults — soul path not taken
	require.Equal(t, 0.0, data.TrustScore.Score)
	require.Equal(t, 0, data.Signatures.TotalFailures)
	require.Equal(t, 0, data.Vouches.Count)
}

func TestHandlePortalGetTrustData_SoulMultiAgentAverageScore(t *testing.T) {
	t.Parallel()

	tdb := newSoulTrustTestDB(t)
	agentA := "0x0000000000000000000000000000000000000000000000000000000000000aaa"
	agentB := "0x0000000000000000000000000000000000000000000000000000000000000bbb"

	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")

	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()

	tdb.qIdx.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{
			{Domain: "example.com", LocalID: "a", AgentID: agentA},
			{Domain: "example.com", LocalID: "b", AgentID: agentB},
		}
	}).Once()

	// Agent A: composite 60, dimensions around 60
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentReputation](t, args, 0)
		*dest = models.SoulAgentReputation{
			AgentID:    agentA,
			Composite:  60.0,
			Trust:      55.0,
			Validation: 65.0,
			Social:     50.0,
			Economic:   70.0,
			Integrity:  60.0,
		}
	}).Once()

	// Agent B: composite 40, dimensions around 40
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentReputation](t, args, 0)
		*dest = models.SoulAgentReputation{
			AgentID:    agentB,
			Composite:  40.0,
			Trust:      45.0,
			Validation: 35.0,
			Social:     50.0,
			Economic:   30.0,
			Integrity:  40.0,
		}
	}).Once()

	tdb.qFailure.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{}
	}).Twice()

	tdb.qEndorse.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPeerEndorsement](t, args, 0)
		*dest = []*models.SoulAgentPeerEndorsement{}
	}).Twice()

	s := &Server{store: store.New(tdb.db), cfg: soulEnabledConfig()}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)

	var data portalTrustDataResponse
	err = json.Unmarshal(resp.Body, &data)
	require.NoError(t, err)

	// Average composite: (60 + 40) / 2 = 50
	require.Equal(t, 50.0, data.TrustScore.Score)

	// Average dimensions
	require.Equal(t, 50.0, data.TrustScore.Dimensions.Operational) // (55+45)/2
	require.Equal(t, 50.0, data.TrustScore.Dimensions.Attestation) // (65+35)/2
	require.Equal(t, 50.0, data.TrustScore.Dimensions.Social)      // (50+50)/2
	require.Equal(t, 50.0, data.TrustScore.Dimensions.Economic)    // (70+30)/2
	require.Equal(t, 50.0, data.TrustScore.Dimensions.Integrity)   // (60+40)/2
}

func TestHandlePortalGetTrustData_SoulIncludesManagedStageDomain(t *testing.T) {
	t.Parallel()

	tdb := newSoulTrustTestDB(t)
	agentID := "0x0000000000000000000000000000000000000000000000000000000000000aaa"

	// Instance with HostedBaseDomain so managed stage domain is computed.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice", Status: models.InstanceStatusActive, HostedBaseDomain: "demo.greater.website"}
	}).Once()

	// Verified domain
	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "demo.greater.website", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()

	// Agent bound to managed stage domain (dev.demo.greater.website) — two calls:
	// one for the verified domain, one for the managed stage domain.
	tdb.qIdx.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{
			{Domain: "dev.demo.greater.website", LocalID: "agent-0", AgentID: agentID},
		}
	}).Maybe()

	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulAgentReputation](t, args, 0)
		*dest = models.SoulAgentReputation{AgentID: agentID, Composite: 75.0, Trust: 70.0}
	}).Once()

	tdb.qFailure.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{}
	}).Once()

	tdb.qEndorse.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPeerEndorsement](t, args, 0)
		*dest = []*models.SoulAgentPeerEndorsement{}
	}).Once()

	s := &Server{store: store.New(tdb.db), cfg: soulEnabledConfig()}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)

	var data portalTrustDataResponse
	err = json.Unmarshal(resp.Body, &data)
	require.NoError(t, err)

	require.Equal(t, 75.0, data.TrustScore.Score)
}

func TestHandlePortalGetTrustData_SoulVouchesBoundedTo50(t *testing.T) {
	t.Parallel()

	tdb := newSoulTrustTestDB(t)
	agentID := "0x0000000000000000000000000000000000000000000000000000000000000aaa"
	now := time.Now().UTC()

	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")

	tdb.qDomain.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()

	tdb.qIdx.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{
			{Domain: "example.com", LocalID: "agent-0", AgentID: agentID},
		}
	}).Once()

	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qFailure.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{}
	}).Once()

	// 60 endorsements — items should be capped at 50 but count = 60
	endorsements := make([]*models.SoulAgentPeerEndorsement, 60)
	for i := 0; i < 60; i++ {
		endorsements[i] = &models.SoulAgentPeerEndorsement{
			AgentID:         agentID,
			EndorserAgentID: "0xpeer" + strings.Repeat("x", 58) + fmt.Sprintf("%02d", i),
			Signature:       "0xsig",
			CreatedAt:       now.Add(-time.Duration(i) * time.Hour),
		}
	}
	tdb.qEndorse.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPeerEndorsement](t, args, 0)
		*dest = endorsements
	}).Once()

	s := &Server{store: store.New(tdb.db), cfg: soulEnabledConfig()}
	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		Params:       map[string]string{"slug": "demo"},
	}

	resp, err := s.handlePortalGetTrustData(ctx)
	require.NoError(t, err)

	var data portalTrustDataResponse
	err = json.Unmarshal(resp.Body, &data)
	require.NoError(t, err)

	require.Equal(t, 60, data.Vouches.Count) // total count
	require.Len(t, data.Vouches.Items, 50)   // bounded to 50
	for _, item := range data.Vouches.Items {
		require.Equal(t, 1.0, item.Strength)
		require.Equal(t, "endorsement", item.Type)
	}
}
