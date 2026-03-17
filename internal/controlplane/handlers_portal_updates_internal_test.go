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
	require.Equal(t, updateJobKindLesser, out.Kind)
	require.Equal(t, "queued", out.Status)
	require.Equal(t, "note", out.Note)
}

func expectEmptyUpdateJobHistory(t *testing.T, qUpdate *ttmocks.MockQuery) {
	t.Helper()
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Once()
}

func setupActiveManagedUpdateConflictCase(t *testing.T, body []byte) (*Server, *apptheory.Context) {
	t.Helper()

	tdb := newPortalTestDB()
	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)

	s := &Server{store: store.New(tdb.db)}

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid"}
	ctx.Params = map[string]string{"slug": testPortalInstanceSlugDemo}
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
			BodyEnabled:      nil,
		}
	}).Once()

	now := time.Unix(20, 0).UTC()
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{ID: "job-body", InstanceSlug: testPortalInstanceSlugDemo, Status: models.UpdateJobStatusRunning, BodyOnly: true, LesserBodyVersion: "v0.1.11", CreatedAt: now},
		}
	}).Once()

	return s, ctx
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
			Slug:             testPortalInstanceSlugDemo,
			Owner:            "alice",
			HostedAccountID:  "123",
			HostedRegion:     "us-east-1",
			HostedBaseDomain: "demo.example.com",
			LesserVersion:    "v1.2.3",
			UpdateStatus:     models.UpdateJobStatusRunning,
			UpdateJobID:      "job1",
		}
	}).Once()
	expectEmptyUpdateJobHistory(t, qUpdate)

	qUpdate.On("First", mock.AnythingOfType("*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.UpdateJob](t, args, 0)
		*dest = models.UpdateJob{
			ID:            "job1",
			InstanceSlug:  testPortalInstanceSlugDemo,
			Status:        models.UpdateJobStatusRunning,
			LesserVersion: "v1.2.3",
		}
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
	expectEmptyUpdateJobHistory(t, qUpdate)

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

func TestHandlePortalCreateInstanceUpdateJob_DoesNotDefaultLesserBodyVersionForLesserUpdates(t *testing.T) {
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
			ManagedLesserBodyDefaultVersion: "v0.1.3",
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
	expectEmptyUpdateJobHistory(t, qUpdate)

	resp, err := s.handlePortalCreateInstanceUpdateJob(ctx)
	require.NoError(t, err)
	require.Equal(t, 202, resp.Status)

	var parsed updateJobResponse
	require.NoError(t, json.Unmarshal(resp.Body, &parsed))
	require.Equal(t, "", parsed.LesserBodyVersion)
}

func TestHandlePortalCreateInstanceUpdateJob_RejectsMalformedReleaseTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "lesser version", body: `{"lesser_version":"v.1.2.3"}`},
		{name: "lesser body version", body: `{"body_only":true,"lesser_body_version":"v.0.1.3"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			ctx.Request.Body = []byte(tt.body)

			tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
				dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
				*dest = models.Instance{
					Slug:              testPortalInstanceSlugDemo,
					Owner:             "alice",
					HostedAccountID:   "123",
					HostedRegion:      "us-east-1",
					HostedBaseDomain:  "demo.example.com",
					LesserVersion:     "v1.2.3",
					LesserBodyVersion: "v0.1.15",
					UpdateStatus:      models.UpdateJobStatusOK,
				}
			}).Once()
			expectEmptyUpdateJobHistory(t, qUpdate)

			_, err := s.handlePortalCreateInstanceUpdateJob(ctx)
			require.Error(t, err)
			appErr, ok := err.(*apptheory.AppError)
			require.True(t, ok)
			require.Equal(t, "app.bad_request", appErr.Code)
			require.Contains(t, appErr.Message, "must be \"latest\" or a tag like v1.2.3")
		})
	}
}

func TestHandlePortalCreateInstanceUpdateJob_BodyOnlyDefaultsLesserBodyVersion(t *testing.T) {
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
			ManagedLesserBodyDefaultVersion: "v0.1.12",
		},
		store: store.New(tdb.db),
	}

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid"}
	ctx.Params = map[string]string{"slug": testPortalInstanceSlugDemo}
	body, _ := json.Marshal(createUpdateJobRequest{BodyOnly: true})
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
			BodyEnabled:      nil,
		}
	}).Once()
	expectEmptyUpdateJobHistory(t, qUpdate)

	resp, err := s.handlePortalCreateInstanceUpdateJob(ctx)
	require.NoError(t, err)
	require.Equal(t, 202, resp.Status)

	var parsed updateJobResponse
	require.NoError(t, json.Unmarshal(resp.Body, &parsed))
	require.Equal(t, "v0.1.12", parsed.LesserBodyVersion)
	require.True(t, parsed.BodyOnly)
}

func TestHandlePortalCreateInstanceUpdateJob_BodyOnlyUsesBodyVersion(t *testing.T) {
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
	body, _ := json.Marshal(createUpdateJobRequest{LesserBodyVersion: "v0.2.0", BodyOnly: true})
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
			BodyEnabled:      nil,
		}
	}).Once()
	expectEmptyUpdateJobHistory(t, qUpdate)

	resp, err := s.handlePortalCreateInstanceUpdateJob(ctx)
	require.NoError(t, err)
	require.Equal(t, 202, resp.Status)

	var parsed updateJobResponse
	require.NoError(t, json.Unmarshal(resp.Body, &parsed))
	require.Equal(t, "v1.2.3", parsed.LesserVersion)
	require.Equal(t, "v0.2.0", parsed.LesserBodyVersion)
	require.True(t, parsed.BodyOnly)
}

func TestHandlePortalCreateInstanceUpdateJob_BodyOnlyRejectsRotateInstanceKey(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)
	s := &Server{store: store.New(tdb.db)}

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid"}
	ctx.Params = map[string]string{"slug": testPortalInstanceSlugDemo}
	body, _ := json.Marshal(createUpdateJobRequest{LesserBodyVersion: "v0.2.0", BodyOnly: true, RotateInstanceKey: true})
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
			BodyEnabled:      nil,
		}
	}).Once()
	expectEmptyUpdateJobHistory(t, qUpdate)

	_, err := s.handlePortalCreateInstanceUpdateJob(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.bad_request", appErr.Code)
}

func TestHandlePortalCreateInstanceUpdateJob_BodyOnlyRequiresBodyVersionWhenNoDefault(t *testing.T) {
	t.Parallel()

	tdb := newPortalTestDB()
	qUpdate := new(ttmocks.MockQuery)
	tdb.db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qUpdate)
	s := &Server{store: store.New(tdb.db)}

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid"}
	ctx.Params = map[string]string{"slug": testPortalInstanceSlugDemo}
	body, _ := json.Marshal(createUpdateJobRequest{BodyOnly: true})
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
			BodyEnabled:      nil,
		}
	}).Once()
	expectEmptyUpdateJobHistory(t, qUpdate)

	_, err := s.handlePortalCreateInstanceUpdateJob(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.bad_request", appErr.Code)
}

func TestHandlePortalCreateInstanceUpdateJob_ReturnsActiveJobFromInstanceHistory(t *testing.T) {
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
			Slug:             testPortalInstanceSlugDemo,
			Owner:            "alice",
			HostedAccountID:  "123",
			HostedRegion:     "us-east-1",
			HostedBaseDomain: "demo.example.com",
			LesserVersion:    "v1.2.3",
			UpdateStatus:     models.UpdateJobStatusOK,
		}
	}).Once()

	now := time.Unix(20, 0).UTC()
	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{
			{ID: "job-active", InstanceSlug: testPortalInstanceSlugDemo, Status: models.UpdateJobStatusRunning, LesserVersion: "v1.2.3", CreatedAt: now},
		}
	}).Once()

	resp, err := s.handlePortalCreateInstanceUpdateJob(ctx)
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status)

	var parsed updateJobResponse
	require.NoError(t, json.Unmarshal(resp.Body, &parsed))
	require.Equal(t, "job-active", parsed.ID)
	require.Equal(t, models.UpdateJobStatusRunning, parsed.Status)
}

func TestHandlePortalCreateInstanceUpdateJob_MCPOOnlyCreatesIndependentJob(t *testing.T) {
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
	body, _ := json.Marshal(createUpdateJobRequest{MCPOnly: true})
	ctx.Request.Body = body

	tdb.qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:              testPortalInstanceSlugDemo,
			Owner:             "alice",
			HostedAccountID:   "123",
			HostedRegion:      "us-east-1",
			HostedBaseDomain:  "demo.example.com",
			LesserVersion:     "v1.2.3",
			LesserBodyVersion: "v0.1.11",
			BodyEnabled:       nil,
		}
	}).Once()

	qUpdate.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{}
	}).Once()

	resp, err := s.handlePortalCreateInstanceUpdateJob(ctx)
	require.NoError(t, err)
	require.Equal(t, 202, resp.Status)

	var parsed updateJobResponse
	require.NoError(t, json.Unmarshal(resp.Body, &parsed))
	require.Equal(t, updateJobKindMCP, parsed.Kind)
	require.True(t, parsed.MCPOnly)
	require.False(t, parsed.BodyOnly)
	require.Equal(t, "v0.1.11", parsed.LesserBodyVersion)
}

func TestHandlePortalCreateInstanceUpdateJob_ConflictsWhenDifferentActiveKindExists(t *testing.T) {
	t.Parallel()

	s, ctx := setupActiveManagedUpdateConflictCase(t, []byte(`{"lesser_version":"v1.2.3"}`))

	_, err := s.handlePortalCreateInstanceUpdateJob(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.conflict", appErr.Code)
	require.Contains(t, appErr.Message, "cannot start Lesser update to v1.2.3")
	require.Contains(t, appErr.Message, "lesser-body update to v0.1.11")
}

func TestHandlePortalCreateInstanceUpdateJob_ConflictsWhenSameKindVersionDiffers(t *testing.T) {
	t.Parallel()

	s, ctx := setupActiveManagedUpdateConflictCase(t, []byte(`{"body_only":true,"lesser_body_version":"v0.1.12"}`))

	_, err := s.handlePortalCreateInstanceUpdateJob(ctx)
	require.Error(t, err)
	appErr, ok := err.(*apptheory.AppError)
	require.True(t, ok)
	require.Equal(t, "app.conflict", appErr.Code)
	require.Contains(t, appErr.Message, "cannot start lesser-body update to v0.1.12")
	require.Contains(t, appErr.Message, "lesser-body update to v0.1.11")
}

func TestHandlePortalCreateInstanceUpdateJob_TransactWriteFailureReturnsInternalError(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDBStrict()
	qInstance := new(ttmocks.MockQuery)
	qUpdate := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInstance).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qUpdate).Maybe()
	addStandardMockQueryStubs(qInstance)
	addStandardMockQueryStubs(qUpdate)
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(errors.New("fail")).Once()

	s := &Server{
		cfg:   config.Config{ManagedInstanceRoleName: "role"},
		store: store.New(db),
	}

	ctx := &apptheory.Context{AuthIdentity: "alice", RequestID: "rid"}
	ctx.Set(ctxKeyOperatorRole, models.RoleAdmin)
	ctx.Params = map[string]string{"slug": testPortalInstanceSlugDemo}
	ctx.Request.Body = []byte(`{"lesser_version":"v1.0.0"}`)

	qInstance.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:             testPortalInstanceSlugDemo,
			Owner:            "alice",
			HostedAccountID:  "123",
			HostedRegion:     "us-east-1",
			HostedBaseDomain: "demo.example.com",
			LesserVersion:    "v1.0.0",
		}
	}).Once()
	expectEmptyUpdateJobHistory(t, qUpdate)

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
