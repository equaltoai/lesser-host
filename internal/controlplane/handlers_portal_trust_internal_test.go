package controlplane

import (
	"encoding/json"
	"net/http"
	"testing"

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

	// Trust score: documented placeholder
	require.Equal(t, 0.0, data.TrustScore.Score)
	require.NotEmpty(t, data.TrustScore.Formula)
	require.Contains(t, data.TrustScore.Formula, "trust_score")
	require.NotEmpty(t, data.TrustScore.Source)
	require.Contains(t, data.TrustScore.Source, "preliminary")
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
