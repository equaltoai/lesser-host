package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

// setupStackTestDB creates a test Server + AppTheory context + mock DB
// primed for stack queries. Returns the mocks so tests can set expectations
// for UpdateJob and ProvisionJob reads.
type stackTestHarness struct {
	s    *Server
	ctx  *apptheory.Context
	tdb  portalTestDB
	qUpd *ttmocks.MockQuery
}

func newStackTestHarness(t *testing.T, slug, owner string, bodyEnabled *bool) stackTestHarness {
	t.Helper()

	tdb := newPortalTestDB()
	qUpd := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpd).Maybe()
	addStandardMockQueryStubs(qUpd)

	s := &Server{store: store.New(tdb.db)}

	ctx := &apptheory.Context{AuthIdentity: owner}
	ctx.Params = map[string]string{"slug": slug}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:            slug,
			Owner:           owner,
			BodyEnabled:     bodyEnabled,
			ProvisionJobID:  "prov-job-1",
			HostedAccountID: "123456789012",
		}
	}).Maybe()

	return stackTestHarness{s: s, ctx: ctx, tdb: tdb, qUpd: qUpd}
}

func TestHandlePortalGetInstanceStack_OwnerSuccess(t *testing.T) {
	t.Parallel()

	h := newStackTestHarness(t, "demo", "alice", nil)

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// Return two successful update jobs: one lesser, one body.
	h.qUpd.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{
				ID:            "upd-lesser-1",
				InstanceSlug:  "demo",
				Status:        models.UpdateJobStatusOK,
				LesserVersion: "v1.2.7",
				UpdatedAt:     now,
			},
			{
				ID:                "upd-body-1",
				InstanceSlug:      "demo",
				Status:            models.UpdateJobStatusOK,
				BodyOnly:          true,
				LesserBodyVersion: "v0.3.0",
				UpdatedAt:         now.Add(time.Minute),
			},
		}
	}).Once()

	// ProvisionJob fallback: body was provisioned and MCP wired.
	h.tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		*dest = models.ProvisionJob{
			ID:                "prov-job-1",
			InstanceSlug:      "demo",
			Status:            models.ProvisionJobStatusOK,
			LesserVersion:     "v1.2.6",
			BodyEnabled:       true,
			BodyProvisionedAt: now.Add(-24 * time.Hour),
			McpWiredAt:        now.Add(-23 * time.Hour),
			UpdatedAt:         now.Add(-24 * time.Hour),
		}
	}).Once()

	resp, err := h.s.handlePortalGetInstanceStack(h.ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	require.Equal(t, 200, resp.Status)

	var body instanceStackResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))

	require.Equal(t, "demo", body.Slug)
	// Lesser: latest ok update
	require.Equal(t, "v1.2.7", body.Lesser.CurrentVersion)
	require.Equal(t, "upd-lesser-1", body.Lesser.SourceJobID)
	require.Equal(t, stackDriftOK, body.Lesser.Drift)
	// Body: latest ok body update
	require.True(t, body.Body.Enabled)
	require.Equal(t, "v0.3.0", body.Body.CurrentVersion)
	require.Equal(t, "upd-body-1", body.Body.SourceJobID)
	require.Equal(t, stackDriftOK, body.Body.Drift)
	// MCP: provision job timestamp
	require.Equal(t, formatStackTime(now.Add(-23*time.Hour)), body.MCP.WiredAt)
	require.Equal(t, stackDriftUnknown, body.MCP.Drift)
}

func TestHandlePortalGetInstanceStack_OtherSlugDenial(t *testing.T) {
	t.Parallel()

	// Build a dedicated test DB without the default harness instance mock.
	// The harness Maybe() mock would return Owner=alice and bypass the denial.
	db := ttmocks.NewMockExtendedDB()
	db.On("WithContext", mock.Anything).Return(db).Maybe()

	qInstance := new(ttmocks.MockQuery)
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	addStandardMockQueryStubs(qInstance)

	s := &Server{store: store.New(db)}

	ctx := &apptheory.Context{AuthIdentity: "alice"}
	ctx.Params = map[string]string{"slug": "demo"}

	// Instance owned by "bob" → requireInstanceAccess must reject alice.
	qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "bob"}
	}).Once()

	// Crucially: no UpdateJob or ProvisionJob mock expectations should
	// fire — ownership must fail before any stack-state query.

	_, err := s.handlePortalGetInstanceStack(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppTheoryError)
	require.True(t, ok, "expected AppError, got %T: %v", err, err)
	require.Equal(t, "app.forbidden", appErr.Code)
}

func TestHandlePortalGetInstanceStack_NoUpdateYetFallbackToProvisionJob(t *testing.T) {
	t.Parallel()

	h := newStackTestHarness(t, "demo", "alice", nil)

	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

	// No update jobs at all.
	h.qUpd.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Once()

	// Provision job exists with version and body info.
	h.tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		*dest = models.ProvisionJob{
			ID:                "prov-job-1",
			InstanceSlug:      "demo",
			Status:            models.ProvisionJobStatusOK,
			LesserVersion:     "v1.2.6",
			BodyEnabled:       true,
			BodyProvisionedAt: now,
			McpWiredAt:        now.Add(time.Minute),
			UpdatedAt:         now,
		}
	}).Once()

	resp, err := h.s.handlePortalGetInstanceStack(h.ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body instanceStackResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))

	// Lesser falls back to provision job version.
	require.Equal(t, "v1.2.6", body.Lesser.CurrentVersion)
	require.Equal(t, "prov-job-1", body.Lesser.SourceJobID)
	require.Equal(t, stackDriftUnknown, body.Lesser.Drift)

	// Body: enabled with provision timestamp but unknown drift.
	require.True(t, body.Body.Enabled)
	require.Equal(t, formatStackTime(now), body.Body.DeployedAt)
	require.Equal(t, stackDriftUnknown, body.Body.Drift)

	// MCP: provision job wired timestamp.
	require.Equal(t, formatStackTime(now.Add(time.Minute)), body.MCP.WiredAt)
}

func TestHandlePortalGetInstanceStack_BodyNotInstalled(t *testing.T) {
	t.Parallel()

	// Instance created without body provisioning — BodyEnabled nil
	// means body was never configured, and there is no body installation
	// evidence (no successful body update, no BodyProvisionedAt timestamp).
	// The stack contract must report body.enabled=false so the already-merged
	// StackCard shows its "Add agentic" CTA.
	h := newStackTestHarness(t, "demo", "alice", nil)

	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

	// Only a lesser update job exists — no body or MCP updates.
	h.qUpd.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{
				ID:            "upd-lesser-1",
				InstanceSlug:  "demo",
				Status:        models.UpdateJobStatusOK,
				LesserVersion: "v1.2.7",
				UpdatedAt:     now,
			},
		}
	}).Once()

	// No provision job evidence for body — GetProvisionJob returns not-found.
	// The instance record also has BodyProvisionedAt at zero value.
	h.tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(theoryErrors.ErrItemNotFound).Once()

	resp, err := h.s.handlePortalGetInstanceStack(h.ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body instanceStackResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))

	// Lesser has data from the update job.
	require.Equal(t, "v1.2.7", body.Lesser.CurrentVersion)
	require.Equal(t, stackDriftOK, body.Lesser.Drift)

	// Body: not enabled (no installation evidence → StackCard "Add agentic" CTA).
	require.False(t, body.Body.Enabled)
	require.Empty(t, body.Body.CurrentVersion)
	require.Equal(t, stackDriftUnknown, body.Body.Drift)

	// MCP: unknown drift (no body installed → no meaningful wiring info).
	require.Equal(t, stackDriftUnknown, body.MCP.Drift)
}

func TestHandlePortalGetInstanceStack_MCPWireStale(t *testing.T) {
	t.Parallel()

	h := newStackTestHarness(t, "demo", "alice", nil)

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// Body update to v0.4.0, then MCP wired against older v0.3.0.
	h.qUpd.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{
				ID:                "upd-body-2",
				InstanceSlug:      "demo",
				Status:            models.UpdateJobStatusOK,
				BodyOnly:          true,
				LesserBodyVersion: "v0.4.0",
				UpdatedAt:         now,
			},
			{
				ID:                "upd-mcp-1",
				InstanceSlug:      "demo",
				Status:            models.UpdateJobStatusOK,
				MCPOnly:           true,
				LesserBodyVersion: "v0.3.0", // wired against older version
				UpdatedAt:         now.Add(-time.Hour),
			},
		}
	}).Once()

	// Provision job exists (don't actually need it here since update jobs cover everything).
	h.tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		*dest = models.ProvisionJob{
			ID:           "prov-job-1",
			InstanceSlug: "demo",
			Status:       models.ProvisionJobStatusOK,
			BodyEnabled:  true,
		}
	}).Once()

	resp, err := h.s.handlePortalGetInstanceStack(h.ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body instanceStackResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))

	// Body is at v0.4.0.
	require.Equal(t, "v0.4.0", body.Body.CurrentVersion)
	require.Equal(t, stackDriftOK, body.Body.Drift)

	// MCP wired against v0.3.0 → wire-stale against current body v0.4.0.
	require.Equal(t, "v0.3.0", body.MCP.WiredAgainstBodyVersion)
	require.Equal(t, "v0.4.0", body.MCP.CurrentBodyVersion)
	require.Equal(t, stackDriftWireStale, body.MCP.Drift)
}

func TestHandlePortalGetInstanceStack_NewerNonOKJobIgnored(t *testing.T) {
	t.Parallel()

	// Regression: a newer queued/running/error update job must not be
	// reported as the currently deployed version. The stack endpoint
	// must source current-version data from the latest *successful*
	// (status=ok) job per component kind.
	h := newStackTestHarness(t, "demo", "alice", nil)

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// Three jobs: older ok body, older ok lesser, newer error body.
	// The newer error body job must be ignored; current-version data
	// must come from the older ok jobs.
	h.qUpd.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{
				ID:                "upd-body-2",
				InstanceSlug:      "demo",
				Status:            models.UpdateJobStatusError,
				BodyOnly:          true,
				LesserBodyVersion: "v0.5.0", // newer but FAILED — must be ignored
				UpdatedAt:         now,
			},
			{
				ID:                "upd-body-1",
				InstanceSlug:      "demo",
				Status:            models.UpdateJobStatusOK,
				BodyOnly:          true,
				LesserBodyVersion: "v0.3.0", // older but SUCCESSFUL — must be used
				UpdatedAt:         now.Add(-time.Hour),
			},
			{
				ID:            "upd-lesser-1",
				InstanceSlug:  "demo",
				Status:        models.UpdateJobStatusOK,
				LesserVersion: "v1.2.7",
				UpdatedAt:     now.Add(-30 * time.Minute),
			},
		}
	}).Once()

	h.tdb.qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.ProvisionJob](t, args, 0)
		*dest = models.ProvisionJob{
			ID:                "prov-job-1",
			InstanceSlug:      "demo",
			Status:            models.ProvisionJobStatusOK,
			BodyEnabled:       true,
			BodyProvisionedAt: now.Add(-48 * time.Hour),
			UpdatedAt:         now.Add(-48 * time.Hour),
		}
	}).Once()

	resp, err := h.s.handlePortalGetInstanceStack(h.ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var body instanceStackResponse
	require.NoError(t, json.Unmarshal(resp.Body, &body))

	// Lesser: ok update job version, NOT skipped.
	require.Equal(t, "v1.2.7", body.Lesser.CurrentVersion)
	require.Equal(t, "upd-lesser-1", body.Lesser.SourceJobID)
	require.Equal(t, stackDriftOK, body.Lesser.Drift)

	// Body: must use the older OK job (v0.3.0), NOT the newer error job (v0.5.0).
	require.True(t, body.Body.Enabled)
	require.Equal(t, "v0.3.0", body.Body.CurrentVersion)
	require.Equal(t, "upd-body-1", body.Body.SourceJobID)
	require.Equal(t, stackDriftOK, body.Body.Drift)
}

func TestComputeLesserDrift(t *testing.T) {
	t.Parallel()

	okJob := &models.UpdateJob{Status: models.UpdateJobStatusOK}
	if got := computeLesserDrift(okJob); got != stackDriftOK {
		t.Fatalf("computeLesserDrift(ok) = %q, want %q", got, stackDriftOK)
	}

	runningJob := &models.UpdateJob{Status: models.UpdateJobStatusRunning}
	if got := computeLesserDrift(runningJob); got != stackDriftUnknown {
		t.Fatalf("computeLesserDrift(running) = %q, want %q", got, stackDriftUnknown)
	}

	if got := computeLesserDrift(nil); got != stackDriftUnknown {
		t.Fatalf("computeLesserDrift(nil) = %q, want %q", got, stackDriftUnknown)
	}
}

func TestComputeMCPDrift_WireStale(t *testing.T) {
	t.Parallel()

	// MCP wired against v0.3.0, body now at v0.4.0.
	job := &models.UpdateJob{
		Status:            models.UpdateJobStatusOK,
		LesserBodyVersion: "v0.3.0",
	}
	if got := computeMCPDrift(job, "v0.4.0"); got != stackDriftWireStale {
		t.Fatalf("computeMCPDrift(v0.3.0 vs v0.4.0) = %q, want %q", got, stackDriftWireStale)
	}

	// MCP wired against same version.
	job2 := &models.UpdateJob{
		Status:            models.UpdateJobStatusOK,
		LesserBodyVersion: "v0.4.0",
	}
	if got := computeMCPDrift(job2, "v0.4.0"); got != stackDriftOK {
		t.Fatalf("computeMCPDrift(v0.4.0 vs v0.4.0) = %q, want %q", got, stackDriftOK)
	}

	// Unknown when job is not ok.
	runningJob := &models.UpdateJob{
		Status:            models.UpdateJobStatusRunning,
		LesserBodyVersion: "v0.3.0",
	}
	if got := computeMCPDrift(runningJob, "v0.4.0"); got != stackDriftUnknown {
		t.Fatalf("computeMCPDrift(running) = %q, want %q", got, stackDriftUnknown)
	}
}

func TestDeriveDriftSummary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		lesserDrift string
		bodyDrift   string
		mcpDrift    string
		bodyEnabled bool
		want        string
	}{
		{name: "all ok", lesserDrift: "ok", bodyDrift: "ok", mcpDrift: "ok", bodyEnabled: true, want: "up to date"},
		{name: "wire-stale only", lesserDrift: "ok", bodyDrift: "ok", mcpDrift: "wire-stale", bodyEnabled: true, want: "MCP wire-stale"},
		{name: "stale + wire-stale", lesserDrift: "stale", bodyDrift: "ok", mcpDrift: "wire-stale", bodyEnabled: true, want: "stale + MCP wire-stale"},
		{name: "stale only", lesserDrift: "stale", bodyDrift: "ok", mcpDrift: "ok", bodyEnabled: true, want: "stale components"},
		{name: "unknowns", lesserDrift: "unknown", bodyDrift: "unknown", mcpDrift: "unknown", bodyEnabled: true, want: "partial telemetry"},
		{name: "body not installed", lesserDrift: "ok", bodyDrift: "unknown", mcpDrift: "unknown", bodyEnabled: false, want: "partial telemetry"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveDriftSummary(tc.lesserDrift, tc.bodyDrift, tc.mcpDrift, tc.bodyEnabled)
			if got != tc.want {
				t.Fatalf("deriveDriftSummary = %q, want %q", got, tc.want)
			}
		})
	}
}
