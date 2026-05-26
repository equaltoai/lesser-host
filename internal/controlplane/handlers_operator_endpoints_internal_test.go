package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

// --- Helper types for operator test DB mocking ---

type operatorEndpointTestDB struct {
	db        *ttmocks.MockExtendedDB
	qInstance *ttmocks.MockQuery
	qJob      *ttmocks.MockQuery
	qAudit    *ttmocks.MockQuery
	qUpdate   *ttmocks.MockQuery
}

func newOperatorEndpointTestDB() operatorEndpointTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInstance := new(ttmocks.MockQuery)
	qJob := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	qUpdate := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInstance, qJob, qAudit, qUpdate} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Maybe()
		q.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Maybe()
		addStandardMockQueryStubs(q)
	}

	return operatorEndpointTestDB{db: db, qInstance: qInstance, qJob: qJob, qAudit: qAudit, qUpdate: qUpdate}
}

func operatorAuthenticatedCtx() *apptheory.Context {
	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	return ctx
}

func portalUserCtx() *apptheory.Context {
	return &apptheory.Context{AuthIdentity: "bob", RequestID: "rid"}
}

func unauthenticatedCtx() *apptheory.Context {
	return &apptheory.Context{RequestID: "rid"}
}

func makeInstance(slug, lesserVer, bodyVer string, bodyUpdateAt, mcpWiredAt time.Time) *models.Instance {
	return &models.Instance{
		Slug:               slug,
		Owner:              "alice",
		Status:             models.InstanceStatusActive,
		LesserVersion:      lesserVer,
		LesserBodyVersion:  bodyVer,
		LesserUpdateAt:     bodyUpdateAt,
		LesserBodyUpdateAt: bodyUpdateAt,
		McpWiredAt:         mcpWiredAt,
		UpdatedAt:          bodyUpdateAt,
	}
}

// --- #434: Operator releases endpoint ---

func TestHandleOperatorReleases_HappyPath(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserDefaultVersion:     "v1.4.2",
		ManagedLesserBodyDefaultVersion: "v0.3.0",
	}}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			makeInstance("alpha", "v1.4.2", "v0.3.0", now, now),
			makeInstance("beta", "v1.4.1", "v0.2.9", now.Add(-time.Hour), now.Add(-time.Hour)),
			makeInstance("gamma", "v1.4.2", "v0.3.0", now, now),
			makeInstance("delta", "v1.4.2", "v0.2.9", now, now.Add(-time.Hour)),
		}
	}).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorReleases(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body operatorReleasesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, 4, body.FleetTotal)
	require.Len(t, body.Channels, 2)

	lesserCh := findChannel(body.Channels, "lesser")
	require.NotNil(t, lesserCh)
	require.Len(t, lesserCh.Versions, 2)
	// v1.4.2 should be first (newest) and is_latest.
	require.Equal(t, "v1.4.2", lesserCh.Versions[0].Version)
	require.True(t, lesserCh.Versions[0].IsLatest)
	require.Equal(t, 3, lesserCh.Versions[0].Adoption.Instances)
	require.Equal(t, 75, lesserCh.Versions[0].Adoption.Percent)
	require.Equal(t, "v1.4.1", lesserCh.Versions[1].Version)
	require.False(t, lesserCh.Versions[1].IsLatest)
	require.Equal(t, 1, lesserCh.Versions[1].Adoption.Instances)

	bodyCh := findChannel(body.Channels, "lesser-body")
	require.NotNil(t, bodyCh)
	require.Len(t, bodyCh.Versions, 2)
	require.Equal(t, "v0.3.0", bodyCh.Versions[0].Version)
	require.True(t, bodyCh.Versions[0].IsLatest)
	require.Equal(t, 2, bodyCh.Versions[0].Adoption.Instances)
}

func TestHandleOperatorReleases_NoInstances(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserDefaultVersion:     "v1.4.2",
		ManagedLesserBodyDefaultVersion: "v0.3.0",
	}}

	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{}
	}).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorReleases(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body operatorReleasesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, 0, body.FleetTotal)
	require.Len(t, body.Channels, 2)
	for _, ch := range body.Channels {
		require.Empty(t, ch.Versions)
	}
}

func TestHandleOperatorReleases_Unauthorized(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db)}

	// Portal user (no operator role).
	ctx := portalUserCtx()
	_, err := s.handleOperatorReleases(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok, "expected AppError, got %T: %v", err, err)
	require.Equal(t, "app.forbidden", appErr.Code)

	// Unauthenticated.
	ctx2 := unauthenticatedCtx()
	_, err = s.handleOperatorReleases(ctx2)
	require.Error(t, err)
}

func TestHandleOperatorReleases_FiltersInactive(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserDefaultVersion: "v1.4.2",
	}}

	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			makeInstance("active-a", "v1.4.2", "", time.Time{}, time.Time{}),
			{Slug: "disabled-b", Status: models.InstanceStatusDisabled, LesserVersion: "v1.4.1"},
			{Slug: "", Status: models.InstanceStatusActive}, // empty slug, filtered
		}
	}).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorReleases(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body operatorReleasesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, 1, body.FleetTotal)
}

func findChannel(channels []operatorReleaseChannel, id string) *operatorReleaseChannel {
	for i := range channels {
		if channels[i].ID == id {
			return &channels[i]
		}
	}
	return nil
}

// --- #435: Operator drift endpoint ---

func TestHandleOperatorInstancesDrift_AllOK(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserDefaultVersion:     "v1.4.2",
		ManagedLesserBodyDefaultVersion: "v0.3.0",
	}}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			makeInstance("ok1", "v1.4.2", "v0.3.0", now, now),
			makeInstance("ok2", "v1.4.2", "v0.3.0", now, now),
		}
	}).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorInstancesDrift(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body fleetDriftResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, 2, body.Summary.Total)
	require.Equal(t, 0, body.Summary.LesserStale)
	require.Equal(t, 0, body.Summary.BodyStale)
	require.Equal(t, 0, body.Summary.MCPWireStale)
}

func TestHandleOperatorInstancesDrift_LesserStale(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserDefaultVersion: "v1.4.2",
	}}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			makeInstance("old1", "v1.4.0", "", now, time.Time{}),
			makeInstance("ok1", "v1.4.2", "", now, time.Time{}),
		}
	}).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorInstancesDrift(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body fleetDriftResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, 2, body.Summary.Total)
	require.Equal(t, 1, body.Summary.LesserStale)
	require.Equal(t, 0, body.Summary.MCPWireStale)
}

func TestHandleOperatorInstancesDrift_BodyStale(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserBodyDefaultVersion: "v0.3.0",
	}}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			makeInstance("oldbody", "", "v0.2.5", now, time.Time{}),
		}
	}).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorInstancesDrift(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body fleetDriftResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, 1, body.Summary.BodyStale)
}

func TestHandleOperatorInstancesDrift_MCPWireStale(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserBodyDefaultVersion: "v0.3.0",
	}}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	// MCP wired before body update → wire-stale.
	mcpTime := now.Add(-24 * time.Hour)
	bodyTime := now

	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			{
				Slug:               "wired-stale",
				Owner:              "alice",
				Status:             models.InstanceStatusActive,
				LesserBodyVersion:  "v0.3.0",
				LesserBodyUpdateAt: bodyTime,
				McpWiredAt:         mcpTime,
				UpdatedAt:          bodyTime,
			},
		}
	}).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorInstancesDrift(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body fleetDriftResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, 1, body.Summary.MCPWireStale)
}

func TestHandleOperatorInstancesDrift_UnknownTargetVersion(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	// No config defaults → all drift is "unknown".
	s := &Server{store: store.New(tdb.db), cfg: config.Config{}}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			makeInstance("x", "v1.4.2", "v0.3.0", now, now),
		}
	}).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorInstancesDrift(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body fleetDriftResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, stackDriftUnknown, body.Instances[0].Lesser.Drift)
}

func TestHandleOperatorInstancesDrift_Unauthorized(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db)}

	// Portal user.
	ctx := portalUserCtx()
	_, err := s.handleOperatorInstancesDrift(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.forbidden", appErr.Code)

	// Unauthenticated.
	ctx2 := unauthenticatedCtx()
	_, err = s.handleOperatorInstancesDrift(ctx2)
	require.Error(t, err)
}

// --- #436: Operator MCP remediation endpoint ---

func TestHandleOperatorRemediateMCPDrift_NoWireStale(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserDefaultVersion:     "v1.4.2",
		ManagedLesserBodyDefaultVersion: "v0.3.0",
	}}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			makeInstance("ok1", "v1.4.2", "v0.3.0", now, now),
		}
	}).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorRemediateMCPDrift(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body remediateMCPDriftResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Empty(t, body.CreatedJobIDs)
	require.Equal(t, 0, body.Created)
	require.Equal(t, 0, body.Skipped)
}

func TestHandleOperatorRemediateMCPDrift_SingleWireStale(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()

	// Set up Instance scan + getInstance + no active MCP jobs (GSI2 empty).
	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserBodyDefaultVersion: "v0.3.0",
		ManagedInstanceRoleName:         "ManagedInstanceRole",
	}}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	mcpTime := now.Add(-24 * time.Hour)
	bodyTime := now

	// Instance scan for drift.
	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			{
				Slug:               "wired-stale",
				Owner:              "alice",
				Status:             models.InstanceStatusActive,
				LesserBodyVersion:  "v0.3.0",
				LesserBodyUpdateAt: bodyTime,
				McpWiredAt:         mcpTime,
				UpdatedAt:          bodyTime,
				HostedAccountID:    "123456789012",
				HostedRegion:       "us-east-1",
				HostedBaseDomain:   "wired-stale.greater.website",
			},
		}
	}).Once()

	// GSI2 active jobs: empty (no existing MCP-only jobs).
	tdb.qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Once()

	// getInstance call for the wire-stale slug.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:                           "wired-stale",
			Owner:                          "alice",
			Status:                         models.InstanceStatusActive,
			LesserBodyVersion:              "v0.3.0",
			HostedAccountID:                "123456789012",
			HostedRegion:                   "us-east-1",
			HostedBaseDomain:               "wired-stale.greater.website",
			LesserHostInstanceKeySecretARN: "",
			LesserVersion:                  "v1.4.2",
		}
	}).Once()

	// Audit.
	tdb.qAudit.On("Create").Return(nil).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorRemediateMCPDrift(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body remediateMCPDriftResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Len(t, body.CreatedJobIDs, 1)
	require.Equal(t, 1, body.Created)
	require.Equal(t, 0, body.Skipped)
}

func TestHandleOperatorRemediateMCPDrift_IdempotentSkip(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserBodyDefaultVersion: "v0.3.0",
		ManagedInstanceRoleName:         "ManagedInstanceRole",
	}}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	mcpTime := now.Add(-24 * time.Hour)

	// Instance scan: two wire-stale instances.
	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			{
				Slug:               "ws-1",
				Owner:              "alice",
				Status:             models.InstanceStatusActive,
				LesserBodyVersion:  "v0.3.0",
				LesserBodyUpdateAt: now,
				McpWiredAt:         mcpTime,
				UpdatedAt:          now,
				HostedAccountID:    "123456789012",
				HostedRegion:       "us-east-1",
				HostedBaseDomain:   "ws-1.greater.website",
			},
			{
				Slug:               "ws-2",
				Owner:              "alice",
				Status:             models.InstanceStatusActive,
				LesserBodyVersion:  "v0.3.0",
				LesserBodyUpdateAt: now,
				McpWiredAt:         mcpTime,
				UpdatedAt:          now,
				HostedAccountID:    "123456789012",
				HostedRegion:       "us-east-1",
				HostedBaseDomain:   "ws-2.greater.website",
			},
		}
	}).Once()

	// GSI2 active jobs: ws-1 already has an active MCP-only job.
	tdb.qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{
				ID:           "existing-mcp-1",
				InstanceSlug: "ws-1",
				MCPOnly:      true,
				Status:       models.UpdateJobStatusQueued,
			},
		}
	}).Once()

	// Only ws-2 should get a new job; ws-1 is skipped.
	// getInstance for ws-2.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:                           "ws-2",
			Owner:                          "alice",
			Status:                         models.InstanceStatusActive,
			LesserBodyVersion:              "v0.3.0",
			HostedAccountID:                "123456789012",
			HostedRegion:                   "us-east-1",
			HostedBaseDomain:               "ws-2.greater.website",
			LesserHostInstanceKeySecretARN: "",
			LesserVersion:                  "v1.4.2",
		}
	}).Once()

	// UpdateJob create for ws-2.
	tdb.qUpdate.On("Create").Return(nil).Once()

	// Audit.
	tdb.qAudit.On("Create").Return(nil).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorRemediateMCPDrift(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body remediateMCPDriftResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Len(t, body.CreatedJobIDs, 1)
	require.Equal(t, 1, body.Created)
	require.Equal(t, 1, body.Skipped)
}

func TestHandleOperatorRemediateMCPDrift_Unauthorized(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db)}

	ctx := portalUserCtx()
	_, err := s.handleOperatorRemediateMCPDrift(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.forbidden", appErr.Code)
}

// --- Drift computation unit tests ---

func TestComputeComponentDrift(t *testing.T) {
	t.Parallel()

	if got := computeComponentDrift("v1.4.2", "v1.4.2"); got != stackDriftOK {
		t.Fatalf("same version: got %q, want %q", got, stackDriftOK)
	}
	if got := computeComponentDrift("v1.4.0", "v1.4.2"); got != stackDriftStale {
		t.Fatalf("older version: got %q, want %q", got, stackDriftStale)
	}
	if got := computeComponentDrift("v1.5.0", "v1.4.2"); got != stackDriftOK {
		t.Fatalf("newer version: got %q, want %q", got, stackDriftOK)
	}
	if got := computeComponentDrift("", "v1.4.2"); got != stackDriftUnknown {
		t.Fatalf("empty current: got %q, want %q", got, stackDriftUnknown)
	}
	if got := computeComponentDrift("v1.4.2", ""); got != stackDriftUnknown {
		t.Fatalf("empty target: got %q, want %q", got, stackDriftUnknown)
	}
}

func TestSortVersionStringsDesc(t *testing.T) {
	t.Parallel()

	versions := []string{"v1.4.0", "v1.4.2", "v1.3.9", "v2.0.0", "v1.10.0"}
	sortVersionStringsDesc(versions)
	expected := []string{"v2.0.0", "v1.10.0", "v1.4.2", "v1.4.0", "v1.3.9"}
	for i, v := range versions {
		if v != expected[i] {
			t.Fatalf("position %d: got %q, want %q", i, v, expected[i])
		}
	}
}

func TestCompareVersions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		a, b string
		want int
	}{
		{"v1.4.2", "v1.4.2", 0},
		{"v1.4.2", "v1.4.1", 1},
		{"v1.4.0", "v1.4.2", -1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.10.0", "v1.9.0", 1},
		{"v0.3.0", "v0.2.9", 1},
		{"1.4.2", "v1.4.2", 0}, // v prefix stripped
	}
	for _, tc := range cases {
		got := compareVersions(tc.a, tc.b)
		if got != tc.want {
			t.Fatalf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestComputeFleetDrift_EmptyInstances(t *testing.T) {
	t.Parallel()

	resp := computeFleetDrift([]*models.Instance{}, "v1.4.2", "v0.3.0")
	require.Empty(t, resp.Instances)
	require.Equal(t, 0, resp.Summary.Total)
}

func TestBuildOperatorReleasesResponse_EmptyStringVersionsIgnored(t *testing.T) {
	t.Parallel()

	// Instance with empty LesserVersion should be counted in fleet_total
	// but not attributed to any version.
	instances := []*models.Instance{
		{Slug: "no-version", Owner: "alice", Status: models.InstanceStatusActive},
	}

	resp := buildOperatorReleasesResponse(instances, "v1.4.2", "v0.3.0")
	require.Equal(t, 1, resp.FleetTotal)
	require.Empty(t, resp.Channels[0].Versions)
	require.Empty(t, resp.Channels[1].Versions)
}
