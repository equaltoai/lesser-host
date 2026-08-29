package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/v3/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const portalTrustTestInstanceSecret = "instance-secret"

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
	require.Equal(t, 24, data.Signatures.WindowHours)
	require.Equal(t, 0, data.Signatures.TotalFailures)
	require.NotNil(t, data.Signatures.BySource)
	require.Len(t, data.Signatures.BySource, 0)
	require.NotNil(t, data.Signatures.Series)
	require.Len(t, data.Signatures.Series, 0)

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
	appErr, ok := err.(*apptheory.AppTheoryError)
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
	appErr, ok := err.(*apptheory.AppTheoryError)
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
	appErr, ok := err.(*apptheory.AppTheoryError)
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
	appErr, ok := err.(*apptheory.AppTheoryError)
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
	appErr, ok := err.(*apptheory.AppTheoryError)
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
	appErr, ok := err.(*apptheory.AppTheoryError)
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
		appErr, ok := err.(*apptheory.AppTheoryError)
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

func TestHandlePortalGetTrustData_FederationFromManagedLesser(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	instances := []lesserFederationInstanceInfo{
		{Domain: "reachable.example", LastSeen: now.Add(-1 * time.Hour).Format(time.RFC3339), TrustScore: 95},
		{Domain: "silenced.example", IsSilenced: true, LastSeen: now.Add(-2 * time.Hour).Format(time.RFC3339), TrustScore: 90},
		{Domain: "suspended.example", IsSuspended: true, LastSeen: now.Add(-3 * time.Hour).Format(time.RFC3339), TrustScore: 90},
		{Domain: "degraded.example", LastSeen: now.Add(-4 * time.Hour).Format(time.RFC3339), TrustScore: 25},
	}

	statsCalled := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer "+portalTrustTestInstanceSecret, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/v1/admin/federation/statistics":
			statsCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"active_instances": 1,
				"total_users":      4,
				"total_messages":   12,
				"time_range": map[string]string{
					"start": now.Add(-24 * time.Hour).Format(time.RFC3339),
					"end":   now.Format(time.RFC3339),
				},
			})
		case "/api/v1/admin/federation/instances":
			require.Equal(t, "100", r.URL.Query().Get("limit"))
			_ = json.NewEncoder(w).Encode(map[string]any{"instances": instances})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	tdb := newPortalTrustTestDB(t)
	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")
	s := &Server{
		store:                store.New(tdb.db),
		cfg:                  config.Config{},
		portalCostHTTPClient: ts.Client(),
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			return portalTrustTestInstanceSecret, nil
		},
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) { return ts.URL, nil },
	}

	resp, err := s.handlePortalGetTrustData(&apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}})
	require.NoError(t, err)
	require.True(t, statsCalled, "statistics endpoint should be probed before peer rows")

	var data portalTrustDataResponse
	require.NoError(t, json.Unmarshal(resp.Body, &data))
	require.Equal(t, 1, data.Federation.Reachable)
	require.Equal(t, 2, data.Federation.Warning)
	require.Equal(t, 1, data.Federation.Severed)
	require.Equal(t, trustFederationSource, data.Federation.Source)
	require.Len(t, data.Federation.Peers, 4)

	byDomain := map[string]portalTrustFederationPeerRow{}
	for _, row := range data.Federation.Peers {
		byDomain[row.Domain] = row
		require.NotEmpty(t, row.LastSeen)
		require.Empty(t, row.LastFetch)
		require.Nil(t, row.FollowerCount, "active_users must not be mapped to follower_count")
	}
	require.Equal(t, trustFederationStatusReachable, byDomain["reachable.example"].Status)
	require.Equal(t, trustFederationStatusWarning, byDomain["silenced.example"].Status)
	require.Equal(t, trustFederationStatusWarning, byDomain["degraded.example"].Status)
	require.Equal(t, trustFederationStatusSevered, byDomain["suspended.example"].Status)
}

func TestTrustFederationStatusMapping(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	require.Equal(t, trustFederationStatusSevered, trustFederationStatus(lesserFederationInstanceInfo{IsSuspended: true, IsSilenced: true, TrustScore: 100}, now))
	require.Equal(t, trustFederationStatusWarning, trustFederationStatus(lesserFederationInstanceInfo{IsSilenced: true, TrustScore: 100}, now))
	require.Equal(t, trustFederationStatusWarning, trustFederationStatus(lesserFederationInstanceInfo{TrustScore: 25}, now))
	require.Equal(t, trustFederationStatusWarning, trustFederationStatus(lesserFederationInstanceInfo{TrustScore: 100, LastSeen: now.Add(-31 * 24 * time.Hour).Format(time.RFC3339)}, now))
	require.Equal(t, trustFederationStatusReachable, trustFederationStatus(lesserFederationInstanceInfo{TrustScore: 100, LastSeen: now.Add(-1 * time.Hour).Format(time.RFC3339)}, now))
}

func TestHandlePortalGetTrustData_FederationPeersBounded(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	instances := make([]lesserFederationInstanceInfo, 60)
	for i := range instances {
		instances[i] = lesserFederationInstanceInfo{
			Domain:     fmt.Sprintf("peer-%02d.example", i),
			LastSeen:   now.Format(time.RFC3339),
			TrustScore: 90,
		}
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/admin/federation/statistics":
			_ = json.NewEncoder(w).Encode(map[string]any{"active_instances": 60, "time_range": map[string]string{"start": now.Add(-24 * time.Hour).Format(time.RFC3339), "end": now.Format(time.RFC3339)}})
		case "/api/v1/admin/federation/instances":
			_ = json.NewEncoder(w).Encode(map[string]any{"instances": instances})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	tdb := newPortalTrustTestDB(t)
	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")
	s := &Server{
		store:                store.New(tdb.db),
		portalCostHTTPClient: ts.Client(),
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			return portalTrustTestInstanceSecret, nil
		},
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) { return ts.URL, nil },
	}

	resp, err := s.handlePortalGetTrustData(&apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}})
	require.NoError(t, err)
	var data portalTrustDataResponse
	require.NoError(t, json.Unmarshal(resp.Body, &data))
	require.Equal(t, 60, data.Federation.Reachable)
	require.Len(t, data.Federation.Peers, trustFederationPeerRowsMax)
	require.Equal(t, "peer-00.example", data.Federation.Peers[0].Domain)
	require.Equal(t, "peer-49.example", data.Federation.Peers[49].Domain)
}

func TestHandlePortalGetTrustData_ForbiddenDoesNotCallFederation(t *testing.T) {
	t.Parallel()

	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer ts.Close()

	tdb := newPortalTrustTestDB(t)
	stubOwnedInstance(t, tdb.qInstance, "bob-instance", "bob")
	s := &Server{
		store:                store.New(tdb.db),
		portalCostHTTPClient: ts.Client(),
		fetchInstanceKeyPlaintextFunc: func(context.Context, *models.Instance) (string, error) {
			return portalTrustTestInstanceSecret, nil
		},
		resolveInstanceMetricsBaseURLFunc: func(*models.Instance) (string, error) { return ts.URL, nil },
	}

	_, err := s.handlePortalGetTrustData(&apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "bob-instance"}})
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok)
	require.Equal(t, "app.forbidden", appErr.Code)
	require.False(t, called, "not-owned instances must not trigger upstream federation fetches")
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
	qMailbox  *ttmocks.MockQuery
	qQueue    *ttmocks.MockQuery
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
		{"*models.SoulCommMailboxMessage", "qMailbox"},
		{"*models.TrustQueueDepthSample", "qQueue"},
	}

	for _, m := range md {
		q := new(ttmocks.MockQuery)
		qs[m.key] = q
		db.On("Model", mock.AnythingOfType(m.typeName)).Return(q).Maybe()
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
	}

	// Bounded partition walks (issue #1061 part B) on the domain/agent-index
	// reads; the qFailure/qEndorse/qQueue mocks keep their specific Limit
	// expectations below instead.
	qs["qDomain"].On("Limit", mock.Anything).Return(qs["qDomain"]).Maybe()
	qs["qIdx"].On("Limit", mock.Anything).Return(qs["qIdx"]).Maybe()

	tdb := &soulTrustTestDB{
		db:        db,
		qUser:     qs["qUser"],
		qInstance: qs["qInstance"],
		qDomain:   qs["qDomain"],
		qIdx:      qs["qIdx"],
		qRep:      qs["qRep"],
		qFailure:  qs["qFailure"],
		qEndorse:  qs["qEndorse"],
		qMailbox:  qs["qMailbox"],
		qQueue:    qs["qQueue"],
	}

	// Default: user "alice" exists as customer
	tdb.qUser.On("First", mock.AnythingOfType("*models.User")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.User](t, args, 0)
		*dest = models.User{Username: "alice", Role: "customer", Approved: true}
	}).Maybe()

	// Default queue telemetry path: no queued mailbox rows and no persisted
	// samples. Tests that need populated queue data reset these expectations.
	tdb.qMailbox.On("Filter", mock.Anything, mock.Anything, mock.Anything).Return(tdb.qMailbox).Maybe()
	tdb.qMailbox.On("Count").Return(int64(0), nil).Maybe()
	tdb.qFailure.On("OrderBy", mock.Anything, mock.Anything).Return(tdb.qFailure).Maybe()
	tdb.qFailure.On("Limit", mock.Anything).Return(tdb.qFailure).Maybe()
	tdb.qEndorse.On("OrderBy", mock.Anything, mock.Anything).Return(tdb.qEndorse).Maybe()
	tdb.qEndorse.On("Limit", mock.Anything).Return(tdb.qEndorse).Maybe()
	tdb.qEndorse.On("Count").Return(int64(0), nil).Maybe()
	tdb.qQueue.On("OrderBy", mock.Anything, mock.Anything).Return(tdb.qQueue).Maybe()
	tdb.qQueue.On("Limit", mock.Anything).Return(tdb.qQueue).Maybe()
	tdb.qQueue.On("First", mock.AnythingOfType("*models.TrustQueueDepthSample")).Return(theoryErrors.ErrItemNotFound).Maybe()
	tdb.qQueue.On("IfNotExists").Return(tdb.qQueue).Maybe()
	tdb.qQueue.On("Create").Return(nil).Maybe()
	tdb.qQueue.On("All", mock.Anything).Return(theoryErrors.ErrItemNotFound).Maybe()

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
	tdb.qDomain.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()

	// 3. SoulDomainAgentIndex returns one agent
	tdb.qIdx.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
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

	tdb.qDomain.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()

	tdb.qIdx.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
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
	sameHour := now.Truncate(time.Hour).Add(-2 * time.Hour).Add(5 * time.Minute)

	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")

	// Two verified domains
	tdb.qDomain.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{
			{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified},
			{Domain: "other.com", InstanceSlug: "demo", Status: models.DomainStatusVerified},
		}
	}).Once()

	// Two domains → two SoulDomainAgentIndex queries; return agents for both.
	tdb.qIdx.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{
			{Domain: "example.com", LocalID: "agent-a", AgentID: agentA},
			{Domain: "other.com", LocalID: "agent-b", AgentID: agentB},
		}
	}).Twice()

	// No reputation for either agent
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(theoryErrors.ErrItemNotFound).Twice()

	// Agent A: 4 signature failures within the 24h window, 3 outside window/future, 1 non-signature
	tdb.qFailure.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{
			{FailureType: "signature_failure", Timestamp: now.Add(-1 * time.Hour)},
			{FailureType: "SIGNATURE_FAILURE", Timestamp: sameHour},
			{FailureType: "signature_failure", Timestamp: sameHour.Add(10 * time.Minute)},
			{FailureType: "signature_failure", Timestamp: now.Add(-3 * time.Hour)},
			{FailureType: "signature_failure", Timestamp: now.Add(-25 * time.Hour)},  // outside 24h window
			{FailureType: "signature_failure", Timestamp: now.Add(-200 * time.Hour)}, // outside window
			{FailureType: "signature_failure", Timestamp: now.Add(1 * time.Hour)},    // future/outside window
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

	// Total: 4 (agent A) + 1 (agent B) = 5. Older, future, and
	// non-signature failures are excluded before both aggregate and series
	// counts are computed.
	require.Equal(t, 5, data.Signatures.TotalFailures)
	require.Equal(t, 24, data.Signatures.WindowHours)

	// By-source: 2 entries (one per agent)
	require.Len(t, data.Signatures.BySource, 2)

	// Build a map for easy assertion
	srcMap := make(map[string]int)
	for _, row := range data.Signatures.BySource {
		srcMap[row.Source] = row.Failures
	}
	require.Equal(t, 4, srcMap[agentA])
	require.Equal(t, 1, srcMap[agentB])

	// Series: hourly points are derived from real failure timestamps only.
	// The shape must remain redacted: timestamp + failures, no source/agent ID
	// and no raw failure metadata.
	require.NotEmpty(t, data.Signatures.Series)
	seriesSum := 0
	var previous time.Time
	hasBucketWithMultipleFailures := false
	for i, point := range data.Signatures.Series {
		ts, parseErr := time.Parse(time.RFC3339, point.Timestamp)
		require.NoError(t, parseErr)
		require.Equal(t, ts.Truncate(time.Hour), ts, "signature bucket timestamps must be UTC hour buckets")
		require.False(t, ts.After(now.UTC()))
		require.Greater(t, point.Failures, 0)
		seriesSum += point.Failures
		if point.Failures > 1 {
			hasBucketWithMultipleFailures = true
		}
		if i > 0 {
			require.True(t, previous.Before(ts), "signature series should be sorted ascending")
		}
		previous = ts
	}
	require.Equal(t, data.Signatures.TotalFailures, seriesSum)
	require.True(t, hasBucketWithMultipleFailures, "same-hour signature failures should be bucketed together")

	// Verify no raw failure data leaks
	body := string(resp.Body)
	require.NotContains(t, body, `"failure_id"`)
	require.NotContains(t, body, `"FailureID"`)
	require.NotContains(t, body, `"description"`)
	require.NotContains(t, body, `"value"`, "signature series must not use the queue-depth/shared value shape")

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(resp.Body, &raw))
	var rawSignatures struct {
		Series []map[string]any `json:"series"`
	}
	require.NoError(t, json.Unmarshal(raw["signatures"], &rawSignatures))
	for _, point := range rawSignatures.Series {
		require.Contains(t, point, "timestamp")
		require.Contains(t, point, "failures")
		require.NotContains(t, point, "source")
		require.NotContains(t, point, "agent_id")
		require.NotContains(t, point, "failure_id")
		require.NotContains(t, point, "description")
		require.NotContains(t, point, "PK")
		require.NotContains(t, point, "SK")
	}
}

func TestLoadTrustSignaturesUsesWindowRangeAndLimit(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	qFailure := new(ttmocks.MockQuery)
	db.On("Model", mock.AnythingOfType("*models.SoulAgentFailure")).Return(qFailure).Maybe()

	agentID := "0x0000000000000000000000000000000000000000000000000000000000000aaa"
	pk := fmt.Sprintf("SOUL#AGENT#%s", agentID)
	now := time.Now().UTC()
	seeded := make([]*models.SoulAgentFailure, 0, 2504)
	for i := 0; i < 2500; i++ {
		seeded = append(seeded, portalTrustFailureFixture(t, agentID, fmt.Sprintf("legacy-%04d", i), "signature_failure", now.Add(-72*time.Hour-time.Duration(i)*time.Second)))
	}
	seeded = append(seeded,
		portalTrustFailureFixture(t, agentID, "in-window-1", "signature_failure", now.Add(-1*time.Hour)),
		portalTrustFailureFixture(t, agentID, "in-window-2", "SIGNATURE_FAILURE", now.Add(-2*time.Hour)),
		portalTrustFailureFixture(t, agentID, "in-window-other-type", "crash_loop", now.Add(-3*time.Hour)),
		portalTrustFailureFixture(t, agentID, "future", "signature_failure", now.Add(1*time.Hour)),
	)

	var (
		lowerBound string
		upperBound string
		rowsRead   int
	)
	qFailure.On("Where", "PK", "=", pk).Return(qFailure).Once()
	qFailure.On("Where", "SK", "BETWEEN", mock.MatchedBy(func(value any) bool {
		bounds, ok := value.([]any)
		if !ok || len(bounds) != 2 {
			return false
		}
		lower, okLower := bounds[0].(string)
		upper, okUpper := bounds[1].(string)
		if !okLower || !okUpper {
			return false
		}
		lowerBound = lower
		upperBound = upper
		return strings.HasPrefix(lower, "FAILURE#") &&
			strings.HasPrefix(upper, "FAILURE#") &&
			strings.HasSuffix(upper, "~") &&
			lower < upper
	})).Return(qFailure).Once()
	qFailure.On("OrderBy", "SK", "ASC").Return(qFailure).Once()
	qFailure.On("Limit", trustSignaturesMaxRowsPerAgent).Return(qFailure).Once()
	qFailure.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		require.NotEmpty(t, lowerBound)
		require.NotEmpty(t, upperBound)
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		bounded := make([]*models.SoulAgentFailure, 0)
		for _, f := range seeded {
			if f.SK >= lowerBound && f.SK <= upperBound {
				bounded = append(bounded, f)
			}
		}
		sort.Slice(bounded, func(i, j int) bool { return bounded[i].SK < bounded[j].SK })
		if len(bounded) > trustSignaturesMaxRowsPerAgent {
			bounded = bounded[:trustSignaturesMaxRowsPerAgent]
		}
		rowsRead = len(bounded)
		*dest = bounded
	}).Once()

	resp := (&Server{store: store.New(db)}).loadTrustSignatures(context.Background(), []string{agentID})

	require.Equal(t, trustSignaturesWindowHours, resp.WindowHours)
	require.Equal(t, 2, resp.TotalFailures)
	require.False(t, resp.Truncated)
	require.Len(t, resp.BySource, 1)
	require.Equal(t, agentID, resp.BySource[0].Source)
	require.Equal(t, 2, resp.BySource[0].Failures)
	require.NotEmpty(t, resp.Series)
	require.Less(t, rowsRead, len(seeded), "storage read should be bounded by the SK range, not total legacy rows")
	require.LessOrEqual(t, rowsRead, trustSignaturesMaxRowsPerAgent)

	qFailure.AssertExpectations(t)
}

func TestLoadTrustVouchesUsesDescendingLimitAndCount(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	qEndorse := new(ttmocks.MockQuery)
	db.On("Model", mock.AnythingOfType("*models.SoulAgentPeerEndorsement")).Return(qEndorse).Maybe()

	agentID := "0x0000000000000000000000000000000000000000000000000000000000000aaa"
	pk := fmt.Sprintf("SOUL#AGENT#%s", agentID)
	now := time.Now().UTC().Truncate(time.Second)
	seeded := make([]*models.SoulAgentPeerEndorsement, 0, 200)
	for i := 0; i < 200; i++ {
		seeded = append(seeded, portalTrustEndorsementFixture(t, agentID, fmt.Sprintf("0x%064x", i+1), now.Add(time.Duration(i)*time.Second)))
	}

	rowsRead := 0
	qEndorse.On("Where", "PK", "=", pk).Return(qEndorse).Twice()
	qEndorse.On("Where", "SK", "begins_with", "ENDORSEMENT#").Return(qEndorse).Twice()
	qEndorse.On("Count").Return(int64(len(seeded)), nil).Once()
	qEndorse.On("OrderBy", "SK", "DESC").Return(qEndorse).Once()
	qEndorse.On("Limit", trustVouchesMaxRowsPerAgent).Return(qEndorse).Once()
	qEndorse.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPeerEndorsement](t, args, 0)
		bounded := append([]*models.SoulAgentPeerEndorsement(nil), seeded...)
		sort.Slice(bounded, func(i, j int) bool { return bounded[i].SK > bounded[j].SK })
		bounded = bounded[:trustVouchesMaxRowsPerAgent]
		rowsRead = len(bounded)
		*dest = bounded
	}).Once()

	resp := (&Server{store: store.New(db)}).loadTrustVouches(context.Background(), []string{agentID})

	require.Equal(t, len(seeded), resp.Count)
	require.Len(t, resp.Items, trustVouchesMaxItems)
	require.True(t, resp.Truncated)
	require.Equal(t, vouchDefaultStrength, resp.Items[0].Strength)
	require.Equal(t, "endorsement", resp.Items[0].Type)
	require.Equal(t, seeded[len(seeded)-1].EndorserAgentID, resp.Items[0].Peer)
	require.Less(t, rowsRead, len(seeded), "storage read should be bounded by Limit, not total endorsements")
	require.LessOrEqual(t, rowsRead, trustVouchesMaxRowsPerAgent)

	qEndorse.AssertExpectations(t)
}

func TestBoundedTrustAgentIDsCapsFanout(t *testing.T) {
	t.Parallel()

	agentIDs := make([]string, 0, trustDataMaxAgentFanout+10)
	for i := 0; i < trustDataMaxAgentFanout+10; i++ {
		agentIDs = append(agentIDs, fmt.Sprintf("0x%064x", i+1))
	}
	bounded, truncated := boundedTrustAgentIDs(agentIDs)
	require.True(t, truncated)
	require.Len(t, bounded, trustDataMaxAgentFanout)
	require.True(t, sort.StringsAreSorted(bounded))
}

func TestHandlePortalGetTrustData_QueueDepthSnapshotCountsScopedMailboxRows(t *testing.T) {
	t.Parallel()

	tdb := newSoulTrustTestDB(t)
	agentID := "0x0000000000000000000000000000000000000000000000000000000000000aaa"

	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")
	tdb.qDomain.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()
	tdb.qIdx.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{Domain: "example.com", LocalID: "agent", AgentID: agentID}}
	}).Once()
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qFailure.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{}
	}).Once()
	tdb.qEndorse.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPeerEndorsement](t, args, 0)
		*dest = []*models.SoulAgentPeerEndorsement{}
	}).Once()

	// Override the default zero-count queue mock. The production query is scoped
	// to COMM#MAILBOX#INSTANCE#demo#AGENT#{agentID}; no global queue is read.
	tdb.qMailbox.ExpectedCalls = nil
	tdb.qMailbox.On("Where", "PK", "=", models.SoulCommMailboxAgentPK("demo", agentID)).Return(tdb.qMailbox).Once()
	tdb.qMailbox.On("Filter", "direction", "=", models.SoulCommDirectionInbound).Return(tdb.qMailbox).Once()
	tdb.qMailbox.On("Filter", "status", "=", models.SoulCommMailboxStatusQueued).Return(tdb.qMailbox).Once()
	tdb.qMailbox.On("Filter", "deleted", "=", false).Return(tdb.qMailbox).Once()
	tdb.qMailbox.On("Count").Return(int64(7), nil).Once()

	s := &Server{store: store.New(tdb.db), cfg: soulEnabledConfig()}
	resp, err := s.handlePortalGetTrustData(&apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "demo"}})
	require.NoError(t, err)

	var data portalTrustDataResponse
	require.NoError(t, json.Unmarshal(resp.Body, &data))
	require.Equal(t, trustQueueDepthWindowHours, data.QueueDepth.WindowHours)
	require.Equal(t, trustQueueDepthSource, data.QueueDepth.Source)
	require.Len(t, data.QueueDepth.Series, 1)
	require.Equal(t, 7, data.QueueDepth.Series[0].Depth)
}

func TestRecordTrustQueueDepthSnapshot_CoalescesBucketBeforeMailboxCounts(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qQueue := new(ttmocks.MockQuery)
	qMailbox := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.TrustQueueDepthSample")).Return(qQueue).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMailboxMessage")).Return(qMailbox).Maybe()

	agentID := "0x0000000000000000000000000000000000000000000000000000000000000aaa"
	now := time.Date(2026, 5, 30, 12, 5, 0, 0, time.UTC)
	laterSameBucket := now.Add(35 * time.Minute)
	existing := models.TrustQueueDepthSample{
		InstanceSlug: "demo",
		Timestamp:    now,
		Depth:        7,
		Source:       trustQueueDepthSource,
	}

	qQueue.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qQueue).Maybe()
	qQueue.On("OrderBy", "SK", "DESC").Return(qQueue).Maybe()
	qQueue.On("Limit", trustQueueDepthMaxSeriesPoints).Return(qQueue).Maybe()
	qQueue.On("All", mock.Anything).Return(theoryErrors.ErrItemNotFound).Maybe()
	qQueue.On("First", mock.AnythingOfType("*models.TrustQueueDepthSample")).Return(theoryErrors.ErrItemNotFound).Once()
	qQueue.On("First", mock.AnythingOfType("*models.TrustQueueDepthSample")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.TrustQueueDepthSample](t, args, 0)
		*dest = existing
	}).Once()
	qQueue.On("IfNotExists").Return(qQueue).Once()
	qQueue.On("Create").Return(nil).Once()

	qMailbox.On("Where", "PK", "=", models.SoulCommMailboxAgentPK("demo", agentID)).Return(qMailbox).Once()
	qMailbox.On("Filter", "direction", "=", models.SoulCommDirectionInbound).Return(qMailbox).Once()
	qMailbox.On("Filter", "status", "=", models.SoulCommMailboxStatusQueued).Return(qMailbox).Once()
	qMailbox.On("Filter", "deleted", "=", false).Return(qMailbox).Once()
	qMailbox.On("Count").Return(int64(7), nil).Once()

	s := &Server{store: store.New(db)}
	inst := &models.Instance{Slug: "demo"}

	first, err := s.recordTrustQueueDepthSnapshot(context.Background(), inst.Slug, []string{agentID}, now)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, 7, first.Depth)

	second, err := s.recordTrustQueueDepthSnapshot(context.Background(), inst.Slug, []string{agentID}, laterSameBucket)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, 7, second.Depth)

	qMailbox.AssertNumberOfCalls(t, "Count", 1)
	qQueue.AssertNumberOfCalls(t, "IfNotExists", 1)
	qQueue.AssertNumberOfCalls(t, "Create", 1)
}

func TestRecordTrustQueueDepthSnapshot_ConditionFailedSuppressesDuplicate(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qQueue := new(ttmocks.MockQuery)
	qMailbox := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.TrustQueueDepthSample")).Return(qQueue).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMailboxMessage")).Return(qMailbox).Maybe()

	agentID := "0x0000000000000000000000000000000000000000000000000000000000000aaa"
	qQueue.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qQueue).Maybe()
	qQueue.On("First", mock.AnythingOfType("*models.TrustQueueDepthSample")).Return(theoryErrors.ErrItemNotFound).Once()
	qQueue.On("IfNotExists").Return(qQueue).Once()
	qQueue.On("Create").Return(theoryErrors.ErrConditionFailed).Once()

	qMailbox.On("Where", "PK", "=", models.SoulCommMailboxAgentPK("demo", agentID)).Return(qMailbox).Once()
	qMailbox.On("Filter", "direction", "=", models.SoulCommDirectionInbound).Return(qMailbox).Once()
	qMailbox.On("Filter", "status", "=", models.SoulCommMailboxStatusQueued).Return(qMailbox).Once()
	qMailbox.On("Filter", "deleted", "=", false).Return(qMailbox).Once()
	qMailbox.On("Count").Return(int64(3), nil).Once()

	s := &Server{store: store.New(db)}
	sample, err := s.recordTrustQueueDepthSnapshot(context.Background(), "demo", []string{agentID}, time.Now().UTC())
	require.NoError(t, err)
	require.Nil(t, sample)
	qMailbox.AssertNumberOfCalls(t, "Count", 1)
	qQueue.AssertNumberOfCalls(t, "Create", 1)
}

func TestHandlePortalGetTrustData_QueueDepthSeriesBoundedAndScoped(t *testing.T) {
	t.Parallel()

	tdb := newSoulTrustTestDB(t)
	agentID := "0x0000000000000000000000000000000000000000000000000000000000000aaa"
	now := time.Now().UTC().Truncate(time.Second)

	stubOwnedInstance(t, tdb.qInstance, "alice-inst", "alice")
	tdb.qDomain.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "alice.example", InstanceSlug: "alice-inst", Status: models.DomainStatusVerified}}
	}).Once()
	tdb.qIdx.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulDomainAgentIndex](t, args, 0)
		*dest = []*models.SoulDomainAgentIndex{{Domain: "alice.example", LocalID: "agent", AgentID: agentID}}
	}).Once()
	tdb.qRep.On("First", mock.AnythingOfType("*models.SoulAgentReputation")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qFailure.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentFailure](t, args, 0)
		*dest = []*models.SoulAgentFailure{}
	}).Once()
	tdb.qEndorse.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.SoulAgentPeerEndorsement](t, args, 0)
		*dest = []*models.SoulAgentPeerEndorsement{}
	}).Once()

	// Return more rows than the DTO cap and one cross-tenant row; the handler
	// must bound and filter the response even if storage returns unexpected rows.
	tdb.qQueue.ExpectedCalls = nil
	tdb.qQueue.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(tdb.qQueue).Maybe()
	tdb.qQueue.On("OrderBy", "SK", "DESC").Return(tdb.qQueue).Once()
	tdb.qQueue.On("Limit", trustQueueDepthMaxSeriesPoints).Return(tdb.qQueue).Once()
	tdb.qQueue.On("First", mock.AnythingOfType("*models.TrustQueueDepthSample")).Return(theoryErrors.ErrItemNotFound).Once()
	tdb.qQueue.On("IfNotExists").Return(tdb.qQueue).Maybe()
	tdb.qQueue.On("Create").Return(nil).Maybe()
	samples := make([]*models.TrustQueueDepthSample, 0, 61)
	for i := 0; i < 60; i++ {
		samples = append(samples, &models.TrustQueueDepthSample{InstanceSlug: "alice-inst", Timestamp: now.Add(-time.Duration(i) * time.Minute), Depth: i + 1, Source: trustQueueDepthSource})
	}
	samples = append(samples, &models.TrustQueueDepthSample{InstanceSlug: "bob-inst", Timestamp: now, Depth: 999, Source: trustQueueDepthSource})
	tdb.qQueue.On("All", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.TrustQueueDepthSample](t, args, 0)
		*dest = samples
	}).Once()

	s := &Server{store: store.New(tdb.db), cfg: soulEnabledConfig()}
	resp, err := s.handlePortalGetTrustData(&apptheory.Context{AuthIdentity: "alice", Params: map[string]string{"slug": "alice-inst"}})
	require.NoError(t, err)

	var data portalTrustDataResponse
	require.NoError(t, json.Unmarshal(resp.Body, &data))
	require.Len(t, data.QueueDepth.Series, trustQueueDepthMaxSeriesPoints)
	for _, point := range data.QueueDepth.Series {
		require.NotEqual(t, 999, point.Depth)
	}
	body := string(resp.Body)
	require.NotContains(t, body, "bob-inst")
}

func TestHandlePortalGetTrustData_SoulNoAgentsBound(t *testing.T) {
	t.Parallel()

	tdb := newSoulTrustTestDB(t)
	stubOwnedInstance(t, tdb.qInstance, "demo", "alice")

	// Instance has verified domains, but no agents are bound through the index
	tdb.qDomain.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()

	// No agent index entries
	tdb.qIdx.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
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
	tdb.qDomain.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
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

	tdb.qDomain.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()

	tdb.qIdx.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
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

	tdb.qDomain.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "alice.example", InstanceSlug: "alice-inst", Status: models.DomainStatusVerified}}
	}).Once()

	tdb.qIdx.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
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

	tdb.qDomain.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()

	tdb.qIdx.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
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
	tdb.qDomain.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "demo.greater.website", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()

	// Agent bound to managed stage domain (dev.demo.greater.website) — two calls:
	// one for the verified domain, one for the managed stage domain.
	tdb.qIdx.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
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

	tdb.qDomain.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "example.com", InstanceSlug: "demo", Status: models.DomainStatusVerified}}
	}).Once()

	tdb.qIdx.On("AllPaginated", mock.Anything).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
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

func portalTrustFailureFixture(t *testing.T, agentID string, failureID string, failureType string, ts time.Time) *models.SoulAgentFailure {
	t.Helper()
	failure := &models.SoulAgentFailure{
		AgentID:     agentID,
		FailureID:   failureID,
		FailureType: failureType,
		Timestamp:   ts.UTC(),
	}
	require.NoError(t, failure.UpdateKeys())
	return failure
}

func portalTrustEndorsementFixture(t *testing.T, agentID string, endorserAgentID string, createdAt time.Time) *models.SoulAgentPeerEndorsement {
	t.Helper()
	endorsement := &models.SoulAgentPeerEndorsement{
		AgentID:         agentID,
		EndorserAgentID: endorserAgentID,
		Signature:       "0xsig",
		CreatedAt:       createdAt.UTC(),
	}
	require.NoError(t, endorsement.UpdateKeys())
	return endorsement
}
