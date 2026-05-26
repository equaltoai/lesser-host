package controlplane

import (
	"encoding/json"
	"strings"
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
	db                *ttmocks.MockExtendedDB
	qInstance         *ttmocks.MockQuery
	qJob              *ttmocks.MockQuery
	qAudit            *ttmocks.MockQuery
	qUpdate           *ttmocks.MockQuery
	createdUpdateJobs *[]*models.UpdateJob
	createdAuditLogs  *[]*models.AuditLogEntry
}

func newOperatorEndpointTestDB() operatorEndpointTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInstance := new(ttmocks.MockQuery)
	qJob := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	qUpdate := new(ttmocks.MockQuery)
	createdUpdateJobs := []*models.UpdateJob{}
	createdAuditLogs := []*models.AuditLogEntry{}

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Run(func(args mock.Arguments) {
		if entry, ok := args.Get(0).(*models.AuditLogEntry); ok && strings.TrimSpace(entry.Action) != "" {
			createdAuditLogs = append(createdAuditLogs, entry)
		}
	}).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Run(func(args mock.Arguments) {
		if job, ok := args.Get(0).(*models.UpdateJob); ok && strings.TrimSpace(job.ID) != "" {
			createdUpdateJobs = append(createdUpdateJobs, job)
		}
	}).Maybe()
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInstance, qJob, qAudit, qUpdate} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		addStandardMockQueryStubs(q)
	}

	// Instance.First and ProvisionJob.First are NOT defaulted here — broad
	// Maybe() expectations shadow the per-test Once() rows that remediation and
	// provision-evidence tests need to consume. Tests that need a GetInstance or
	// ProvisionJob row set up explicit Once() expectations. Tests without a
	// ProvisionJobID never call GetProvisionJob (loadProvisionJobFallback bails
	// early), and non-remediation tests never call getInstance.

	// UpdateJob.All is NOT defaulted here either. Evidence gathering and active
	// job listing both call All on qUpdate, so a broad default shadows explicit
	// per-test rows. Tests that need empty update histories call
	// expectEmptyUpdateJobRows with the exact number of history/list calls.

	return operatorEndpointTestDB{
		db:                db,
		qInstance:         qInstance,
		qJob:              qJob,
		qAudit:            qAudit,
		qUpdate:           qUpdate,
		createdUpdateJobs: &createdUpdateJobs,
		createdAuditLogs:  &createdAuditLogs,
	}
}

func expectUpdateJobRows(t *testing.T, tdb operatorEndpointTestDB, rows []*models.UpdateJob) {
	t.Helper()
	tdb.qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = append([]*models.UpdateJob(nil), rows...)
	}).Once()
}

func expectEmptyUpdateJobRows(t *testing.T, tdb operatorEndpointTestDB, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		expectUpdateJobRows(t, tdb, nil)
	}
}

func createdUpdateJobs(t *testing.T, tdb operatorEndpointTestDB) []*models.UpdateJob {
	t.Helper()
	if tdb.createdUpdateJobs == nil {
		return nil
	}
	return *tdb.createdUpdateJobs
}

func createdAuditLogs(t *testing.T, tdb operatorEndpointTestDB) []*models.AuditLogEntry {
	t.Helper()
	if tdb.createdAuditLogs == nil {
		return nil
	}
	return *tdb.createdAuditLogs
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

func instanceWithProvisionJob(slug, lesserVer, provisionJobID string, updatedAt time.Time) *models.Instance {
	return &models.Instance{
		Slug:             slug,
		Owner:            "alice",
		Status:           models.InstanceStatusActive,
		LesserVersion:    lesserVer,
		ProvisionJobID:   provisionJobID,
		UpdatedAt:        updatedAt,
		HostedAccountID:  "123456789012",
		HostedRegion:     "us-east-1",
		HostedBaseDomain: slug + ".greater.website",
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
	early := now.Add(-24 * time.Hour)

	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			instanceWithProvisionJob("alpha", "v1.4.2", "prov-alpha", now),
			instanceWithProvisionJob("beta", "v1.4.1", "prov-beta", early),
			instanceWithProvisionJob("gamma", "v1.4.2", "prov-gamma", now),
		}
	}).Once()
	expectEmptyUpdateJobRows(t, tdb, 3)

	// Provision jobs: variation — alpha got deployed earlier than instance says.
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		*dest = models.ProvisionJob{
			ID:            "prov-alpha",
			InstanceSlug:  "alpha",
			Status:        models.ProvisionJobStatusOK,
			LesserVersion: "v1.4.2",
			UpdatedAt:     early, // earlier than instance.UpdatedAt
		}
	}).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		*dest = models.ProvisionJob{
			ID:            "prov-beta",
			InstanceSlug:  "beta",
			Status:        models.ProvisionJobStatusOK,
			LesserVersion: "v1.4.1",
			UpdatedAt:     early,
		}
	}).Once()
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		*dest = models.ProvisionJob{
			ID:            "prov-gamma",
			InstanceSlug:  "gamma",
			Status:        models.ProvisionJobStatusOK,
			LesserVersion: "v1.4.2",
			UpdatedAt:     now,
		}
	}).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorReleases(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body operatorReleasesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, 3, body.FleetTotal)
	require.Len(t, body.Channels, 2)

	lesserCh := findChannel(body.Channels, "lesser")
	require.NotNil(t, lesserCh)
	require.Len(t, lesserCh.Versions, 2)
	// v1.4.2 is newest and marked is_latest.
	require.Equal(t, "v1.4.2", lesserCh.Versions[0].Version)
	require.True(t, lesserCh.Versions[0].IsLatest)
	require.Equal(t, 2, lesserCh.Versions[0].Adoption.Instances)
	require.Equal(t, "v1.4.1", lesserCh.Versions[1].Version)
	require.False(t, lesserCh.Versions[1].IsLatest)
	require.Equal(t, 1, lesserCh.Versions[1].Adoption.Instances)
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
			instanceWithProvisionJob("active-a", "v1.4.2", "prov-a", time.Now()),
			{Slug: "disabled-b", Status: models.InstanceStatusDisabled, LesserVersion: "v1.4.1"},
			{Slug: "", Status: models.InstanceStatusActive}, // empty slug, filtered
		}
	}).Once()
	expectEmptyUpdateJobRows(t, tdb, 1)

	// Only active-a gets evidence queries.
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		*dest = models.ProvisionJob{
			ID: "prov-a", InstanceSlug: "active-a",
			Status: models.ProvisionJobStatusOK, LesserVersion: "v1.4.2",
			UpdatedAt: time.Now(),
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

// TestHandleOperatorReleases_UsesProvisionJobTimestamps is the Blocker 1
// regression: Instance-only aggregation would give released_at from
// Instance.UpdatedAt (T2, late), but the ProvisionJob has an earlier
// UpdatedAt (T1, early). The evidence-based path uses T1, proving it
// reads from ProvisionJob, not just Instance fields.
func TestHandleOperatorReleases_UsesProvisionJobTimestamps(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserDefaultVersion: "v1.4.2",
	}}

	earlyProv := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	lateInstance := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			{
				Slug:            "late-marker",
				Owner:           "alice",
				Status:          models.InstanceStatusActive,
				LesserVersion:   "v1.4.0",
				ProvisionJobID:  "prov-late",
				UpdatedAt:       lateInstance,
				HostedAccountID: "123456789012",
			},
		}
	}).Once()
	expectEmptyUpdateJobRows(t, tdb, 1)

	// ProvisionJob has updatedAt=earlyProv (T1), earlier than instance.UpdatedAt (T2).
	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		*dest = models.ProvisionJob{
			ID:            "prov-late",
			InstanceSlug:  "late-marker",
			Status:        models.ProvisionJobStatusOK,
			LesserVersion: "v1.4.0",
			UpdatedAt:     earlyProv,
		}
	}).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorReleases(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body operatorReleasesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, 1, body.FleetTotal)

	lesserCh := findChannel(body.Channels, "lesser")
	require.NotNil(t, lesserCh)
	require.Len(t, lesserCh.Versions, 1)
	require.Equal(t, "v1.4.0", lesserCh.Versions[0].Version)

	// released_at must be from ProvisionJob (T1, early), NOT from Instance (T2, late).
	require.Equal(t, earlyProv.UTC().Format(time.RFC3339), lesserCh.Versions[0].ReleasedAt,
		"released_at should come from ProvisionJob evidence, not Instance.UpdatedAt")
}

func TestHandleOperatorReleases_AggregatesUpdateProvisionAndInstanceEvidence(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserDefaultVersion: "v1.4.2",
	}}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	earlyProv := now.Add(-48 * time.Hour)

	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			// Instance marker is stale; successful update evidence should win.
			{Slug: "from-update", Owner: "alice", Status: models.InstanceStatusActive, LesserVersion: "v1.4.0", UpdatedAt: now},
			// ProvisionJob evidence should win over the instance marker.
			{Slug: "from-provision", Owner: "alice", Status: models.InstanceStatusActive, LesserVersion: "v1.4.0", ProvisionJobID: "prov-evidence", UpdatedAt: now},
			// No job evidence: instance state is the fallback.
			{Slug: "from-instance", Owner: "alice", Status: models.InstanceStatusActive, LesserVersion: "v1.3.9", UpdatedAt: now},
		}
	}).Once()

	expectUpdateJobRows(t, tdb, []*models.UpdateJob{{
		ID:            "upd-lesser-1",
		InstanceSlug:  "from-update",
		Status:        models.UpdateJobStatusOK,
		LesserVersion: "v1.4.2",
		UpdatedAt:     now.Add(-1 * time.Hour),
	}})
	expectEmptyUpdateJobRows(t, tdb, 2)

	tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		*dest = models.ProvisionJob{
			ID:            "prov-evidence",
			InstanceSlug:  "from-provision",
			Status:        models.ProvisionJobStatusOK,
			LesserVersion: "v1.4.1",
			UpdatedAt:     earlyProv,
		}
	}).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorReleases(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body operatorReleasesResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, 3, body.FleetTotal)

	lesserCh := findChannel(body.Channels, "lesser")
	require.NotNil(t, lesserCh)
	require.Len(t, lesserCh.Versions, 3)
	require.Equal(t, 1, findReleaseVersion(t, lesserCh, "v1.4.2").Adoption.Instances,
		"successful UpdateJob evidence must contribute adoption")
	require.Equal(t, 1, findReleaseVersion(t, lesserCh, "v1.4.1").Adoption.Instances,
		"successful ProvisionJob evidence must contribute adoption")
	require.Equal(t, 1, findReleaseVersion(t, lesserCh, "v1.3.9").Adoption.Instances,
		"Instance state remains the fallback when no job evidence exists")
}

func findChannel(channels []operatorReleaseChannel, id string) *operatorReleaseChannel {
	for i := range channels {
		if channels[i].ID == id {
			return &channels[i]
		}
	}
	return nil
}

func findReleaseVersion(t *testing.T, channel *operatorReleaseChannel, version string) *operatorReleaseVersion {
	t.Helper()
	require.NotNil(t, channel)
	for i := range channel.Versions {
		if channel.Versions[i].Version == version {
			return &channel.Versions[i]
		}
	}
	require.Failf(t, "version missing", "version %q not found in channel %q", version, channel.ID)
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
	expectEmptyUpdateJobRows(t, tdb, 2)

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
	expectEmptyUpdateJobRows(t, tdb, 2)

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
	expectEmptyUpdateJobRows(t, tdb, 1)

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorInstancesDrift(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body fleetDriftResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, 1, body.Summary.BodyStale)
}

// TestHandleOperatorInstancesDrift_MCPWireStaleEvidence is the Blocker 2
// doc-shaped regression: body deployed at v0.2.6 (from latest body update job),
// MCP last wired against v0.2.5 (from latest MCP-only update job). Response
// shows wired_against=v0.2.5, current_body=v0.2.6, drift=wire-stale.
// This FAILS with the old timestamp-only heuristic because timestamps alone
// can't distinguish which body version MCP was wired against.
func TestHandleOperatorInstancesDrift_MCPWireStaleEvidence(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserBodyDefaultVersion: "v0.3.0",
	}}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			{
				Slug:               "wire-stale-ev",
				Owner:              "alice",
				Status:             models.InstanceStatusActive,
				LesserBodyVersion:  "v0.2.6",
				LesserBodyUpdateAt: now,
				McpWiredAt:         now,
				UpdatedAt:          now,
				HostedAccountID:    "123456789012",
			},
		}
	}).Once()

	// Body update job deployed v0.2.6.
	// MCP-only update job wired against v0.2.5.
	// Evidence layer takes the latest per kind, so wire-stale shows.
	tdb.qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{
				ID:                "upd-body-1",
				InstanceSlug:      "wire-stale-ev",
				Status:            models.UpdateJobStatusOK,
				BodyOnly:          true,
				LesserBodyVersion: "v0.2.6",
				UpdatedAt:         now.Add(-1 * time.Hour),
			},
			{
				ID:                "upd-mcp-1",
				InstanceSlug:      "wire-stale-ev",
				Status:            models.UpdateJobStatusOK,
				MCPOnly:           true,
				LesserBodyVersion: "v0.2.5",
				UpdatedAt:         now.Add(-2 * time.Hour),
			},
		}
	}).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorInstancesDrift(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body fleetDriftResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Equal(t, 1, body.Summary.Total)
	require.Equal(t, 1, body.Summary.MCPWireStale, "MCP should be wire-stale")

	entry := body.Instances[0]
	require.Equal(t, "v0.2.5", entry.MCP.WiredAgainst,
		"wired_against should be from MCP-only update job (v0.2.5)")
	require.Equal(t, "v0.2.6", entry.MCP.CurrentBody,
		"current_body should be from body update job (v0.2.6)")
	require.Equal(t, stackDriftWireStale, entry.MCP.Drift)
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
	expectEmptyUpdateJobRows(t, tdb, 1)

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
	expectEmptyUpdateJobRows(t, tdb, 1)

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

// TestHandleOperatorRemediateMCPDrift_VersionFromDeployedBody is the Blocker 3
// regression: ManagedLesserBodyDefaultVersion is "v0.4.0" (config target), but
// deployed current body is "v0.2.5" (from drift evidence). The created MCP-only
// UpdateJob must use v0.2.5 (deployed), NOT v0.4.0 (config target).
func TestHandleOperatorRemediateMCPDrift_VersionFromDeployedBody(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserDefaultVersion:     "v1.4.2",
		ManagedLesserBodyDefaultVersion: "v0.4.0", // config target — must NOT be used
		ManagedInstanceRoleName:         "ManagedInstanceRole",
	}}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// Fleets scan: one instance with body v0.2.5 deployed, MCP wired against v0.2.4.
	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			{
				Slug:               "deployed-025",
				Owner:              "alice",
				Status:             models.InstanceStatusActive,
				LesserBodyVersion:  "v0.2.5",
				LesserBodyUpdateAt: now,
				McpWiredAt:         now,
				UpdatedAt:          now,
				HostedAccountID:    "123456789012",
				HostedRegion:       "us-east-1",
				HostedBaseDomain:   "deployed-025.greater.website",
				LesserVersion:      "v1.4.2",
			},
		}
	}).Once()

	// Evidence: body update job deployed v0.2.5, MCP job wired v0.2.4 → wire-stale.
	tdb.qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{
				ID:                "upd-body-1",
				InstanceSlug:      "deployed-025",
				Status:            models.UpdateJobStatusOK,
				BodyOnly:          true,
				LesserBodyVersion: "v0.2.5",
				UpdatedAt:         now.Add(-1 * time.Hour),
			},
			{
				ID:                "upd-mcp-1",
				InstanceSlug:      "deployed-025",
				Status:            models.UpdateJobStatusOK,
				MCPOnly:           true,
				LesserBodyVersion: "v0.2.4",
				UpdatedAt:         now.Add(-2 * time.Hour),
			},
		}
	}).Once()

	// GSI2 active jobs: empty (no existing MCP-only jobs).
	tdb.qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Once()

	// getInstance for remediation.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:                           "deployed-025",
			Owner:                          "alice",
			Status:                         models.InstanceStatusActive,
			LesserBodyVersion:              "v0.2.5",
			HostedAccountID:                "123456789012",
			HostedRegion:                   "us-east-1",
			HostedBaseDomain:               "deployed-025.greater.website",
			LesserHostInstanceKeySecretARN: "",
			LesserVersion:                  "v1.4.2",
		}
	}).Once()

	// Job creation — the created job's LesserBodyVersion must be the
	// deployed body version (v0.2.5), not config target (v0.4.0).
	// We verify this indirectly: the drift entry's Body.Current is v0.2.5,
	// and the handler creates a job (confirmed by AssertCalled below).
	tdb.qUpdate.On("Create").Return(nil).Once()

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

	// Verify the UpdateJob was created. We check Mock.AssertExpectations
	// to confirm qUpdate.Create was called exactly once.
	tdb.qUpdate.AssertCalled(t, "Create")

	jobs := createdUpdateJobs(t, tdb)
	require.Len(t, jobs, 1)
	require.True(t, jobs[0].MCPOnly)
	require.Equal(t, "v0.2.5", jobs[0].LesserBodyVersion,
		"MCP remediation must wire against deployed current_body, not config target v0.4.0")
}

// TestHandleOperatorRemediateMCPDrift_AuditIncludesSlugList is the Blocker 3
// regression: the audit record must include the affected slug list, not just a
// count. Pre-rework, the audit Target was "fleet:mcp-remediation:N-slugs";
// post-rework, it must be "fleet:mcp-remediation:slugs=slug1,slug2;jobs=job1,job2".
func TestHandleOperatorRemediateMCPDrift_AuditIncludesSlugList(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserBodyDefaultVersion: "v0.3.0",
		ManagedInstanceRoleName:         "ManagedInstanceRole",
	}}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// Two wire-stale instances.
	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			{
				Slug:               "ws-audit-1",
				Owner:              "alice",
				Status:             models.InstanceStatusActive,
				LesserBodyVersion:  "v0.3.0",
				LesserBodyUpdateAt: now,
				McpWiredAt:         now,
				UpdatedAt:          now,
				HostedAccountID:    "123456789012",
				HostedRegion:       "us-east-1",
				HostedBaseDomain:   "ws-audit-1.greater.website",
				LesserVersion:      "v1.4.2",
			},
			{
				Slug:               "ws-audit-2",
				Owner:              "alice",
				Status:             models.InstanceStatusActive,
				LesserBodyVersion:  "v0.3.0",
				LesserBodyUpdateAt: now,
				McpWiredAt:         now,
				UpdatedAt:          now,
				HostedAccountID:    "123456789012",
				HostedRegion:       "us-east-1",
				HostedBaseDomain:   "ws-audit-2.greater.website",
				LesserVersion:      "v1.4.2",
			},
		}
	}).Once()

	// Evidence for ws-audit-1: body v0.3.0 deployed, MCP v0.2.9 → wire-stale.
	tdb.qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{
				ID: "b1", InstanceSlug: "ws-audit-1", Status: models.UpdateJobStatusOK,
				BodyOnly: true, LesserBodyVersion: "v0.3.0", UpdatedAt: now.Add(-1 * time.Hour),
			},
			{
				ID: "m1", InstanceSlug: "ws-audit-1", Status: models.UpdateJobStatusOK,
				MCPOnly: true, LesserBodyVersion: "v0.2.9", UpdatedAt: now.Add(-2 * time.Hour),
			},
		}
	}).Once()

	// Evidence for ws-audit-2: body v0.3.0 deployed, MCP v0.2.9 → wire-stale.
	tdb.qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{
				ID: "b2", InstanceSlug: "ws-audit-2", Status: models.UpdateJobStatusOK,
				BodyOnly: true, LesserBodyVersion: "v0.3.0", UpdatedAt: now.Add(-1 * time.Hour),
			},
			{
				ID: "m2", InstanceSlug: "ws-audit-2", Status: models.UpdateJobStatusOK,
				MCPOnly: true, LesserBodyVersion: "v0.2.9", UpdatedAt: now.Add(-2 * time.Hour),
			},
		}
	}).Once()

	// GSI2 active jobs: empty.
	tdb.qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Once()

	// getInstance for ws-audit-1.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug: "ws-audit-1", Owner: "alice", Status: models.InstanceStatusActive,
			LesserBodyVersion: "v0.3.0", HostedAccountID: "123456789012",
			HostedRegion: "us-east-1", HostedBaseDomain: "ws-audit-1.greater.website",
			LesserHostInstanceKeySecretARN: "", LesserVersion: "v1.4.2",
		}
	}).Once()

	// getInstance for ws-audit-2.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug: "ws-audit-2", Owner: "alice", Status: models.InstanceStatusActive,
			LesserBodyVersion: "v0.3.0", HostedAccountID: "123456789012",
			HostedRegion: "us-east-1", HostedBaseDomain: "ws-audit-2.greater.website",
			LesserHostInstanceKeySecretARN: "", LesserVersion: "v1.4.2",
		}
	}).Once()

	// Two UpdateJob creates.
	tdb.qUpdate.On("Create").Return(nil).Once()
	tdb.qUpdate.On("Create").Return(nil).Once()

	// Capture happens in the DB.Model recorder; Create remains a no-op.
	tdb.qAudit.On("Create").Return(nil).Once()

	ctx := operatorAuthenticatedCtx()
	resp, err := s.handleOperatorRemediateMCPDrift(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body remediateMCPDriftResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))
	require.Len(t, body.CreatedJobIDs, 2)
	require.Equal(t, 2, body.Created)
	require.Equal(t, 0, body.Skipped)

	// Verify audit was called.
	tdb.qAudit.AssertCalled(t, "Create")

	audits := createdAuditLogs(t, tdb)
	require.Len(t, audits, 1)
	require.Contains(t, audits[0].Target, "slugs=ws-audit-1,ws-audit-2")
	require.Contains(t, audits[0].Target, ";jobs=")
	for _, id := range body.CreatedJobIDs {
		require.Contains(t, audits[0].Target, id)
	}
}

func TestHandleOperatorRemediateMCPDrift_SingleWireStale(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()

	s := &Server{store: store.New(tdb.db), cfg: config.Config{
		ManagedLesserBodyDefaultVersion: "v0.3.0",
		ManagedInstanceRoleName:         "ManagedInstanceRole",
	}}

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// Instance scan: one wire-stale instance.
	tdb.qInstance.On("All", mock.AnythingOfType("*[]*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Instance](t, args, 0)
		*dest = []*models.Instance{
			{
				Slug:               "wired-stale",
				Owner:              "alice",
				Status:             models.InstanceStatusActive,
				LesserBodyVersion:  "v0.3.0",
				LesserBodyUpdateAt: now,
				McpWiredAt:         now,
				UpdatedAt:          now,
				HostedAccountID:    "123456789012",
				HostedRegion:       "us-east-1",
				HostedBaseDomain:   "wired-stale.greater.website",
				LesserVersion:      "v1.4.2",
			},
		}
	}).Once()

	// Evidence: body v0.3.0 deployed, MCP wired v0.2.9 → wire-stale.
	tdb.qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{
				ID: "b1", InstanceSlug: "wired-stale", Status: models.UpdateJobStatusOK,
				BodyOnly: true, LesserBodyVersion: "v0.3.0", UpdatedAt: now.Add(-1 * time.Hour),
			},
			{
				ID: "m1", InstanceSlug: "wired-stale", Status: models.UpdateJobStatusOK,
				MCPOnly: true, LesserBodyVersion: "v0.2.9", UpdatedAt: now.Add(-2 * time.Hour),
			},
		}
	}).Once()

	// GSI2 active jobs: empty.
	tdb.qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Once()

	// getInstance for wired-stale.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug: "wired-stale", Owner: "alice", Status: models.InstanceStatusActive,
			LesserBodyVersion: "v0.3.0", HostedAccountID: "123456789012",
			HostedRegion: "us-east-1", HostedBaseDomain: "wired-stale.greater.website",
			LesserHostInstanceKeySecretARN: "", LesserVersion: "v1.4.2",
		}
	}).Once()

	// UpdateJob create.
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
				McpWiredAt:         now,
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
				McpWiredAt:         now,
				UpdatedAt:          now,
				HostedAccountID:    "123456789012",
				HostedRegion:       "us-east-1",
				HostedBaseDomain:   "ws-2.greater.website",
			},
		}
	}).Once()

	// Evidence for ws-1: body v0.3.0, MCP v0.2.9 → wire-stale.
	tdb.qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{
				ID: "b1", InstanceSlug: "ws-1", Status: models.UpdateJobStatusOK,
				BodyOnly: true, LesserBodyVersion: "v0.3.0", UpdatedAt: now.Add(-1 * time.Hour),
			},
			{
				ID: "m1", InstanceSlug: "ws-1", Status: models.UpdateJobStatusOK,
				MCPOnly: true, LesserBodyVersion: "v0.2.9", UpdatedAt: now.Add(-2 * time.Hour),
			},
		}
	}).Once()

	// Evidence for ws-2: body v0.3.0, MCP v0.2.9 → wire-stale.
	tdb.qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{
				ID: "b2", InstanceSlug: "ws-2", Status: models.UpdateJobStatusOK,
				BodyOnly: true, LesserBodyVersion: "v0.3.0", UpdatedAt: now.Add(-1 * time.Hour),
			},
			{
				ID: "m2", InstanceSlug: "ws-2", Status: models.UpdateJobStatusOK,
				MCPOnly: true, LesserBodyVersion: "v0.2.9", UpdatedAt: now.Add(-2 * time.Hour),
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

	// Only ws-2 gets remediation. getInstance for ws-2.
	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug: "ws-2", Owner: "alice", Status: models.InstanceStatusActive,
			LesserBodyVersion: "v0.3.0", HostedAccountID: "123456789012",
			HostedRegion: "us-east-1", HostedBaseDomain: "ws-2.greater.website",
			LesserHostInstanceKeySecretARN: "", LesserVersion: "v1.4.2",
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
	tdb.qUpdate.AssertCalled(t, "Index", "gsi2")
	tdb.qUpdate.AssertCalled(t, "Where", "gsi2PK", "=", "UPDATE_ACTIVE")
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

func TestComputeMCPEvidenceDrift(t *testing.T) {
	t.Parallel()

	if got := computeMCPEvidenceDrift("v0.2.5", "v0.2.6"); got != stackDriftWireStale {
		t.Fatalf("different versions: got %q, want %q", got, stackDriftWireStale)
	}
	if got := computeMCPEvidenceDrift("v0.2.6", "v0.2.6"); got != stackDriftOK {
		t.Fatalf("same version: got %q, want %q", got, stackDriftOK)
	}
	if got := computeMCPEvidenceDrift("", "v0.2.6"); got != stackDriftUnknown {
		t.Fatalf("empty wired_against: got %q, want %q", got, stackDriftUnknown)
	}
	if got := computeMCPEvidenceDrift("v0.2.5", ""); got != stackDriftUnknown {
		t.Fatalf("empty current_body: got %q, want %q", got, stackDriftUnknown)
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

func TestComputeFleetDrift_EmptyEvidence(t *testing.T) {
	t.Parallel()

	resp := computeFleetDrift([]*instanceJobsEvidence{}, "v1.4.2", "v0.3.0")
	require.Empty(t, resp.Instances)
	require.Equal(t, 0, resp.Summary.Total)
}

func TestBuildOperatorReleasesResponse_EmptyStringVersionsIgnored(t *testing.T) {
	t.Parallel()

	// Instance with empty LesserVersion should be counted in fleet_total
	// but not attributed to any version.
	inst := &models.Instance{Slug: "no-version", Owner: "alice", Status: models.InstanceStatusActive}
	evidence := []*instanceJobsEvidence{
		{instance: inst},
	}

	resp := buildOperatorReleasesResponse(evidence, "v1.4.2", "v0.3.0")
	require.Equal(t, 1, resp.FleetTotal)
	require.Empty(t, resp.Channels[0].Versions)
	require.Empty(t, resp.Channels[1].Versions)
}

// --- Evidence helper unit tests ---

func TestGatherInstanceJobsEvidence_KeepsNewestSuccessfulPerKind(t *testing.T) {
	t.Parallel()

	tdb := newOperatorEndpointTestDB()
	s := &Server{store: store.New(tdb.db)}
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	inst := &models.Instance{Slug: "newest-wins", Status: models.InstanceStatusActive}

	expectUpdateJobRows(t, tdb, []*models.UpdateJob{
		{
			ID:                "body-new",
			InstanceSlug:      "newest-wins",
			Status:            models.UpdateJobStatusOK,
			BodyOnly:          true,
			LesserBodyVersion: "v0.3.0",
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		{
			ID:                "body-old",
			InstanceSlug:      "newest-wins",
			Status:            models.UpdateJobStatusOK,
			BodyOnly:          true,
			LesserBodyVersion: "v0.2.9",
			CreatedAt:         now.Add(-24 * time.Hour),
			UpdatedAt:         now.Add(-24 * time.Hour),
		},
		{
			ID:                "mcp-new",
			InstanceSlug:      "newest-wins",
			Status:            models.UpdateJobStatusOK,
			MCPOnly:           true,
			LesserBodyVersion: "v0.2.9",
			CreatedAt:         now,
			UpdatedAt:         now,
		},
		{
			ID:                "mcp-old",
			InstanceSlug:      "newest-wins",
			Status:            models.UpdateJobStatusOK,
			MCPOnly:           true,
			LesserBodyVersion: "v0.2.8",
			CreatedAt:         now.Add(-24 * time.Hour),
			UpdatedAt:         now.Add(-24 * time.Hour),
		},
		{
			ID:            "lesser-new",
			InstanceSlug:  "newest-wins",
			Status:        models.UpdateJobStatusOK,
			LesserVersion: "v1.4.2",
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            "lesser-old",
			InstanceSlug:  "newest-wins",
			Status:        models.UpdateJobStatusOK,
			LesserVersion: "v1.4.1",
			CreatedAt:     now.Add(-24 * time.Hour),
			UpdatedAt:     now.Add(-24 * time.Hour),
		},
	})

	ev := gatherInstanceJobsEvidence(operatorAuthenticatedCtx(), s, inst)
	require.NotNil(t, ev)
	require.Equal(t, "body-new", ev.latestBodyUpdate.ID)
	require.Equal(t, "mcp-new", ev.latestMCPUpdate.ID)
	require.Equal(t, "lesser-new", ev.latestLesserUpdate.ID)
}

func TestLesserVersionFromEvidence_UpdateJobPreferred(t *testing.T) {
	t.Parallel()

	now := time.Now()
	ev := &instanceJobsEvidence{
		instance: &models.Instance{LesserVersion: "v1.0.0", UpdatedAt: now},
		latestLesserUpdate: &models.UpdateJob{
			LesserVersion: "v1.2.0",
			UpdatedAt:     now.Add(-1 * time.Hour),
		},
		provisionJob: &models.ProvisionJob{
			LesserVersion: "v0.9.0",
			UpdatedAt:     now.Add(-24 * time.Hour),
		},
	}

	require.Equal(t, "v1.2.0", lesserVersionFromEvidence(ev))
}

func TestLesserVersionFromEvidence_ProvisionFallback(t *testing.T) {
	t.Parallel()

	now := time.Now()
	ev := &instanceJobsEvidence{
		instance: &models.Instance{LesserVersion: "v1.0.0", UpdatedAt: now},
		provisionJob: &models.ProvisionJob{
			LesserVersion: "v0.9.0",
			UpdatedAt:     now.Add(-24 * time.Hour),
		},
	}

	require.Equal(t, "v0.9.0", lesserVersionFromEvidence(ev))
}

func TestLesserVersionFromEvidence_InstanceFallback(t *testing.T) {
	t.Parallel()

	now := time.Now()
	ev := &instanceJobsEvidence{
		instance: &models.Instance{LesserVersion: "v1.0.0", UpdatedAt: now},
	}

	require.Equal(t, "v1.0.0", lesserVersionFromEvidence(ev))
}

func TestMCPWiredAgainstFromEvidence_MCPUpdatePreferred(t *testing.T) {
	t.Parallel()

	ev := &instanceJobsEvidence{
		instance: &models.Instance{LesserBodyVersion: "v0.3.0"},
		latestMCPUpdate: &models.UpdateJob{
			LesserBodyVersion: "v0.2.5",
		},
	}

	require.Equal(t, "v0.2.5", mcpWiredAgainstFromEvidence(ev))
}

func TestMCPWiredAgainstFromEvidence_NoMCPUpdateEvidence(t *testing.T) {
	t.Parallel()

	now := time.Now()
	ev := &instanceJobsEvidence{
		instance: &models.Instance{LesserBodyVersion: "v0.3.0", McpWiredAt: now},
		provisionJob: &models.ProvisionJob{
			McpWiredAt: now.Add(-24 * time.Hour),
		},
	}

	require.Empty(t, mcpWiredAgainstFromEvidence(ev),
		"wired_against should only come from successful MCP-only UpdateJob evidence")
}

func TestBodyVersionFromEvidence_BodyUpdatePreferred(t *testing.T) {
	t.Parallel()

	ev := &instanceJobsEvidence{
		instance: &models.Instance{LesserBodyVersion: "v0.3.0"},
		latestBodyUpdate: &models.UpdateJob{
			LesserBodyVersion: "v0.2.6",
		},
	}

	require.Equal(t, "v0.2.6", bodyVersionFromEvidence(ev))
}

func TestBodyVersionFromEvidence_InstanceFallback(t *testing.T) {
	t.Parallel()

	ev := &instanceJobsEvidence{
		instance: &models.Instance{LesserBodyVersion: "v0.3.0"},
	}

	require.Equal(t, "v0.3.0", bodyVersionFromEvidence(ev))
}
