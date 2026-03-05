package controlplane

import (
	"encoding/json"
	"errors"
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

func TestUpdateJobResponseFromModel_TrimsFields(t *testing.T) {
	t.Parallel()

	j := &models.UpdateJob{ID: " id ", InstanceSlug: " slug ", Status: " queued ", Note: " note "}
	out := updateJobResponseFromModel(j)
	require.Equal(t, "id", out.ID)
	require.Equal(t, "slug", out.InstanceSlug)
	require.Equal(t, "queued", out.Status)
	require.Equal(t, "note", out.Note)
}

func TestHandlePortalCreateInstanceUpdateJob_ReturnsExistingJobWhenQueued(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)

	s := &Server{store: store.New(tdb.db)}

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid"}
	ctx.Params = map[string]string{"slug": testPortalInstanceSlugDemo}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:         testPortalInstanceSlugDemo,
			Owner:        "alice",
			UpdateStatus: models.UpdateJobStatusRunning,
			UpdateJobID:  "job1",
		}
	}).Once()

	qUpdate.On("First", mock.AnythingOfType("*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.UpdateJob](t, args, 0)
		*dest = models.UpdateJob{ID: "job1", InstanceSlug: testPortalInstanceSlugDemo, Status: models.UpdateJobStatusRunning}
		_ = dest.UpdateKeys()
	}).Once()

	resp, err := s.handlePortalCreateInstanceUpdateJob(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)
	require.NotEmpty(t, resp.Body)
}

func TestHandlePortalCreateInstanceUpdateJob_CreatesNewJob(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)

	s := &Server{
		cfg: config.Config{
			Stage:                   "lab",
			WebAuthnRPID:            "example.com",
			ManagedInstanceRoleName: "role",
		},
		store: store.New(tdb.db),
	}

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid"}
	ctx.Params = map[string]string{"slug": testPortalInstanceSlugDemo}
	body, _ := json.Marshal(createUpdateJobRequest{LesserVersion: "v1.2.3", RotateInstanceKey: true})
	ctx.Request.Body = body

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:                        testPortalInstanceSlugDemo,
			Owner:                       "alice",
			HostedAccountID:             "123",
			HostedRegion:                "us-east-1",
			HostedBaseDomain:            "demo.example.com",
			LesserVersion:               "v1.2.3",
			UpdateStatus:                models.UpdateJobStatusOK,
			UpdateJobID:                 "",
			TranslationEnabled:          nil,
			TipEnabled:                  nil,
			LesserAIEnabled:             nil,
			LesserAIPiiDetectionEnabled: nil,
		}
	}).Once()

	resp, err := s.handlePortalCreateInstanceUpdateJob(ctx)
	require.NoError(t, err)
	require.Equal(t, 202, resp.Status)

	var parsed updateJobResponse
	require.NoError(t, json.Unmarshal(resp.Body, &parsed))
	require.Equal(t, testPortalInstanceSlugDemo, parsed.InstanceSlug)
	require.Equal(t, models.UpdateJobStatusQueued, parsed.Status)
	require.Equal(t, "queued", parsed.Step)
	require.Equal(t, "v1.2.3", parsed.LesserVersion)
	require.True(t, parsed.RotateInstanceKey)
}

func TestHandlePortalCreateInstanceUpdateJob_DefaultsLesserBodyVersion(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)

	s := &Server{
		cfg: config.Config{
			Stage:                           "lab",
			WebAuthnRPID:                    "example.com",
			ManagedInstanceRoleName:         "role",
			ManagedLesserBodyDefaultVersion: "v.0.1.3",
		},
		store: store.New(tdb.db),
	}

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid"}
	ctx.Params = map[string]string{"slug": testPortalInstanceSlugDemo}
	body, _ := json.Marshal(createUpdateJobRequest{LesserVersion: "v1.2.3"})
	ctx.Request.Body = body

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:             testPortalInstanceSlugDemo,
			Owner:            "alice",
			HostedAccountID:  "123",
			HostedRegion:     "us-east-1",
			HostedBaseDomain: "demo.example.com",
			LesserVersion:    "v1.2.3",
			UpdateStatus:     models.UpdateJobStatusOK,
			UpdateJobID:      "",
			BodyEnabled:      nil, // nil defaults to enabled
		}
	}).Once()

	resp, err := s.handlePortalCreateInstanceUpdateJob(ctx)
	require.NoError(t, err)
	require.Equal(t, 202, resp.Status)

	var parsed updateJobResponse
	require.NoError(t, json.Unmarshal(resp.Body, &parsed))
	require.Equal(t, "v.0.1.3", parsed.LesserBodyVersion)
}

func TestHandlePortalCreateInstanceUpdateJob_TransactWriteFailureReturnsInternalError(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()
	qInstance := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	addStandardMockQueryStubs(qInstance)
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(errors.New("fail")).Once()

	s := &Server{
		cfg:   config.Config{ManagedInstanceRoleName: "role"},
		store: store.New(db),
	}

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	ctx.Params = map[string]string{"slug": testPortalInstanceSlugDemo}
	ctx.Request.Body = []byte(`{"lesser_version":"v1"}`)

	qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:             testPortalInstanceSlugDemo,
			Owner:            "alice",
			HostedAccountID:  "123",
			HostedRegion:     "us-east-1",
			HostedBaseDomain: "demo.example.com",
			LesserVersion:    "v1",
		}
	}).Once()

	_, err := s.handlePortalCreateInstanceUpdateJob(ctx)
	require.Error(t, err)
}

func TestHandlePortalListInstanceUpdateJobs_ReturnsJobs(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)

	s := &Server{store: store.New(tdb.db)}

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid"}
	ctx.Params = map[string]string{"slug": testPortalInstanceSlugDemo}
	ctx.Request.Query = map[string][]string{"limit": {"2"}}

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: testPortalInstanceSlugDemo, Owner: "alice"}
	}).Once()

	now := time.Unix(10, 0).UTC()
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{ID: "a", InstanceSlug: testPortalInstanceSlugDemo, Status: models.UpdateJobStatusQueued, CreatedAt: now.Add(-time.Minute)},
			{ID: "b", InstanceSlug: testPortalInstanceSlugDemo, Status: models.UpdateJobStatusQueued, CreatedAt: now},
		}
	}).Once()

	resp, err := s.handlePortalListInstanceUpdateJobs(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var parsed listUpdateJobsResponse
	require.NoError(t, json.Unmarshal(resp.Body, &parsed))
	require.Equal(t, 2, parsed.Count)
	require.Len(t, parsed.Jobs, 2)
	require.Equal(t, "b", parsed.Jobs[0].ID)
}
