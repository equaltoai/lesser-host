package provisionworker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	apptheory "github.com/theory-cloud/apptheory/v4/runtime"
	"github.com/theory-cloud/apptheory/v4/testkit"
	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/provisioning"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestSQSQueueNameFromURL(t *testing.T) {
	t.Parallel()

	if got := sqsQueueNameFromURL(""); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := sqsQueueNameFromURL("http://%"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := sqsQueueNameFromURL("not a url"); got != "not a url" {
		t.Fatalf("expected last path segment, got %q", got)
	}
	if got := sqsQueueNameFromURL("https://sqs.us-east-1.amazonaws.com/123/q"); got != "q" {
		t.Fatalf("expected q, got %q", got)
	}
}

func TestProvisionJobProcessable(t *testing.T) {
	t.Parallel()

	if provisionJobProcessable(nil) {
		t.Fatalf("expected false")
	}
	if !provisionJobProcessable(&models.ProvisionJob{Status: models.ProvisionJobStatusQueued}) {
		t.Fatalf("expected true for queued")
	}
	if !provisionJobProcessable(&models.ProvisionJob{Status: " RUNNING "}) {
		t.Fatalf("expected true for running")
	}
	if provisionJobProcessable(&models.ProvisionJob{Status: "ok"}) {
		t.Fatalf("expected false for ok")
	}
}

func TestMissingManagedProvisioningConfig(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{}}
	missing := s.missingManagedProvisioningConfig(&models.ProvisionJob{})
	if len(missing) == 0 {
		t.Fatalf("expected missing config list")
	}
}

func TestHandleProvisionQueueMessage_DropsInvalidAndUnknown(t *testing.T) {
	t.Parallel()

	s := &Server{store: &store.Store{}}

	if err := s.handleProvisionQueueMessage(nil, events.SQSMessage{}); err == nil {
		t.Fatalf("expected error for nil ctx")
	}

	ctx := &apptheory.EventContext{RequestID: "r1"}

	// Invalid JSON is dropped.
	if err := s.handleProvisionQueueMessage(ctx, events.SQSMessage{Body: "{"}); err != nil {
		t.Fatalf("expected nil for invalid json, got %v", err)
	}

	// Unknown kind is dropped.
	body, _ := json.Marshal(provisioning.JobMessage{Kind: "other", JobID: "x"})
	if err := s.handleProvisionQueueMessage(ctx, events.SQSMessage{Body: string(body)}); err != nil {
		t.Fatalf("expected nil for unknown kind, got %v", err)
	}

	// Missing job id is dropped.
	body, _ = json.Marshal(provisioning.JobMessage{Kind: "provision_job"})
	if err := s.handleProvisionQueueMessage(ctx, events.SQSMessage{Body: string(body)}); err != nil {
		t.Fatalf("expected nil for missing job id, got %v", err)
	}
}

func TestFailJob_UpdatesJobAndTransacts(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)

	s := &Server{cfg: config.Config{}, store: st}

	job := &models.ProvisionJob{ID: "j1", InstanceSlug: "slug", Status: models.ProvisionJobStatusQueued}

	now := time.Unix(10, 0).UTC()
	if err := s.failJob(context.Background(), job, "req", now, "code", "msg"); err != nil {
		t.Fatalf("failJob: %v", err)
	}
	if job.Status != models.ProvisionJobStatusError || job.Step != provisionStepFailed {
		t.Fatalf("expected job marked failed, got status=%q step=%q", job.Status, job.Step)
	}
	if job.ErrorCode != "code" || job.ErrorMessage != "msg" {
		t.Fatalf("expected error details set")
	}
	if job.Note != job.ErrorMessage {
		t.Fatalf("expected failure note to mirror error message, got note=%q error=%q", job.Note, job.ErrorMessage)
	}
	if job.RequestID != "req" {
		t.Fatalf("expected request id set")
	}
	if !job.UpdatedAt.Equal(now) {
		t.Fatalf("expected UpdatedAt set")
	}
}

func TestUpdateSweepEventBridge_ReconcilesActiveJob(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qJob).Maybe()
	qJob.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qJob).Maybe()
	qJob.On("Index", "gsi2").Return(qJob).Maybe()
	qJob.On("OrderBy", "gsi2SK", "ASC").Return(qJob).Maybe()
	qJob.On("Limit", mock.Anything).Return(qJob).Maybe()
	qJob.On("ConsistentRead").Return(qJob).Maybe()

	activeJob := &models.UpdateJob{
		ID:           "job-sweep-eventbridge",
		InstanceSlug: "slug",
		Status:       models.UpdateJobStatusRunning,
		Step:         updateStepDeployWait,
		RunID:        "run-1",
		CreatedAt:    time.Unix(100, 0).UTC(),
		UpdatedAt:    time.Unix(101, 0).UTC(),
		ExpiresAt:    time.Now().UTC().Add(time.Hour),
		MaxAttempts:  10,
	}

	qJob.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Once().Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{{ID: activeJob.ID, InstanceSlug: activeJob.InstanceSlug, Status: activeJob.Status, Step: activeJob.Step, UpdatedAt: activeJob.UpdatedAt}}
	})

	var loaded *models.UpdateJob
	qJob.On("First", mock.AnythingOfType("*models.UpdateJob")).Return(nil).Once().Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.UpdateJob](t, args, 0)
		*dest = *activeJob
		loaded = dest
	})

	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Maybe()

	srv := NewServer(config.Config{
		AppName:                           "lesser-host",
		Stage:                             "lab",
		ManagedProvisioningEnabled:        true,
		ManagedInstanceRoleName:           "role",
		ManagedProvisionRunnerProjectName: "project",
		ArtifactBucketName:                "artifacts",
		ManagedLesserGitHubOwner:          "equaltoai",
		ManagedLesserGitHubRepo:           "lesser",
	}, store.New(db), nil, nil, nil, nil, &fakeCodebuild{
		batchOut: &codebuild.BatchGetBuildsOutput{
			Builds: []cbtypes.Build{{
				BuildStatus:  cbtypes.StatusTypeFailed,
				CurrentPhase: aws.String("BUILD"),
				Phases: []cbtypes.BuildPhase{{
					PhaseType:   cbtypes.BuildPhaseType("BUILD"),
					PhaseStatus: cbtypes.StatusTypeFailed,
					Contexts:    []cbtypes.PhaseContext{{Message: aws.String("release contract mismatch")}},
				}},
			}},
		},
	}, nil)

	env := testkit.New()
	app := env.App()
	Register(app, srv)

	ruleName := fmt.Sprintf("%s-%s-update-sweep", srv.cfg.AppName, srv.cfg.Stage)
	event := testkit.EventBridgeEvent(testkit.EventBridgeEventOptions{
		Resources: []string{
			fmt.Sprintf("arn:aws:events:us-east-1:123456789012:rule/%s", ruleName),
		},
	})

	out, err := env.InvokeEventBridge(context.Background(), app, event)
	require.NoError(t, err)

	result, ok := out.(map[string]any)
	require.True(t, ok)
	require.EqualValues(t, 1, result["active_jobs"])
	require.EqualValues(t, 1, result["processed"])
	require.EqualValues(t, 0, result["errors"])
	require.NotNil(t, loaded)
	require.Equal(t, models.UpdateJobStatusError, loaded.Status)
	require.Equal(t, "deploy_failed", loaded.ErrorCode)
	require.Contains(t, loaded.ErrorMessage, "release contract mismatch")
}

func TestUpdateSweepEventBridge_ReconcilesActiveBodyJob(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qJob).Maybe()
	qJob.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qJob).Maybe()
	qJob.On("Index", "gsi2").Return(qJob).Maybe()
	qJob.On("OrderBy", "gsi2SK", "ASC").Return(qJob).Maybe()
	qJob.On("Limit", mock.Anything).Return(qJob).Maybe()
	qJob.On("ConsistentRead").Return(qJob).Maybe()

	activeJob := &models.UpdateJob{
		ID:           "job-body-sweep-eventbridge",
		InstanceSlug: "slug",
		Status:       models.UpdateJobStatusRunning,
		Step:         updateStepBodyDeployWait,
		RunID:        "run-body-1",
		RunURL:       "https://logs.example/body",
		BodyRunURL:   "https://logs.example/body",
		CreatedAt:    time.Unix(100, 0).UTC(),
		UpdatedAt:    time.Unix(101, 0).UTC(),
		ExpiresAt:    time.Now().UTC().Add(time.Hour),
		MaxAttempts:  10,
	}

	qJob.On("All", mock.AnythingOfType("*[]*models.UpdateJob")).Return(nil).Once().Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.UpdateJob](t, args, 0)
		*dest = []*models.UpdateJob{{ID: activeJob.ID, InstanceSlug: activeJob.InstanceSlug, Status: activeJob.Status, Step: activeJob.Step, UpdatedAt: activeJob.UpdatedAt}}
	})

	var loaded *models.UpdateJob
	qJob.On("First", mock.AnythingOfType("*models.UpdateJob")).Return(nil).Once().Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.UpdateJob](t, args, 0)
		*dest = *activeJob
		loaded = dest
	})

	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Maybe()

	srv := NewServer(config.Config{
		AppName:                           "lesser-host",
		Stage:                             "lab",
		ManagedProvisioningEnabled:        true,
		ManagedInstanceRoleName:           "role",
		ManagedProvisionRunnerProjectName: "project",
		ArtifactBucketName:                "artifacts",
		ManagedLesserGitHubOwner:          "equaltoai",
		ManagedLesserGitHubRepo:           "lesser",
	}, store.New(db), nil, nil, nil, nil, &fakeCodebuild{
		batchOut: &codebuild.BatchGetBuildsOutput{
			Builds: []cbtypes.Build{{
				BuildStatus:  cbtypes.StatusTypeFailed,
				CurrentPhase: aws.String("BUILD"),
				Phases: []cbtypes.BuildPhase{{
					PhaseType:   cbtypes.BuildPhaseType("BUILD"),
					PhaseStatus: cbtypes.StatusTypeFailed,
					Contexts: []cbtypes.PhaseContext{{
						Message: aws.String("COMMAND_EXECUTION_ERROR: Error while executing command: bash ./deploy-lesser-body-from-release.sh --stack-name demo Reason: exit status 1"),
					}},
				}},
			}},
		},
	}, nil)

	env := testkit.New()
	app := env.App()
	Register(app, srv)

	ruleName := fmt.Sprintf("%s-%s-update-sweep", srv.cfg.AppName, srv.cfg.Stage)
	event := testkit.EventBridgeEvent(testkit.EventBridgeEventOptions{
		Resources: []string{
			fmt.Sprintf("arn:aws:events:us-east-1:123456789012:rule/%s", ruleName),
		},
	})

	out, err := env.InvokeEventBridge(context.Background(), app, event)
	require.NoError(t, err)

	result, ok := out.(map[string]any)
	require.True(t, ok)
	require.EqualValues(t, 1, result["active_jobs"])
	require.EqualValues(t, 1, result["processed"])
	require.EqualValues(t, 0, result["errors"])
	require.NotNil(t, loaded)
	require.Equal(t, models.UpdateJobStatusError, loaded.Status)
	require.Equal(t, "body_deploy_failed", loaded.ErrorCode)
	require.Contains(t, loaded.ErrorMessage, "command execution failed (exit status 1)")
	require.Contains(t, loaded.ErrorMessage, "CodeBuild: https://logs.example/body")
	require.NotContains(t, loaded.ErrorMessage, "--stack-name")
}

func TestProcessActiveProvisionSweep_RequeuesOnlyStaleUnleasedJobs(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	qJob.On("Where", "SK", "=", models.SKJob).Return(qJob).Once()
	qJob.On("Limit", 100).Return(qJob).Once()

	now := time.Unix(1_000, 0).UTC()
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		*dest = []*models.ProvisionJob{
			testProvisionSweepJob(&models.ProvisionJob{
				ID:           "stale",
				InstanceSlug: "slug",
				Status:       models.ProvisionJobStatusQueued,
				UpdatedAt:    now.Add(-provisionSweepStaleAfter - time.Second),
				ExpiresAt:    now.Add(time.Hour),
			}),
			testProvisionSweepJob(&models.ProvisionJob{
				ID:             "leased",
				InstanceSlug:   "slug",
				Status:         models.ProvisionJobStatusRunning,
				UpdatedAt:      now.Add(-provisionSweepStaleAfter - time.Second),
				LeaseOwner:     "worker",
				LeaseExpiresAt: now.Add(time.Minute),
				ExpiresAt:      now.Add(time.Hour),
			}),
		}
	}).Once()

	sqsClient := &fakeSQS{}
	srv := &Server{
		cfg:   config.Config{ProvisionQueueURL: "https://sqs.us-east-1.amazonaws.com/123/provision"},
		store: store.New(db),
		sqs:   sqsClient,
	}

	out, err := srv.processActiveProvisionSweep(context.Background(), "req-sweep", now)
	require.NoError(t, err)
	require.EqualValues(t, 2, out["active_jobs"])
	require.EqualValues(t, 1, out["processed"])
	require.EqualValues(t, 0, out["errors"])
	require.Len(t, sqsClient.inputs, 1)
	require.Contains(t, aws.ToString(sqsClient.inputs[0].MessageBody), `"job_id":"stale"`)
}

func TestProcessActiveProvisionSweep_FailsExpiredJobs(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	qJob.On("Where", "SK", "=", models.SKJob).Return(qJob).Once()
	qJob.On("Limit", 100).Return(qJob).Once()

	now := time.Unix(1_000, 0).UTC()
	expired := &models.ProvisionJob{
		ID:           "expired",
		InstanceSlug: "slug",
		Status:       models.ProvisionJobStatusRunning,
		UpdatedAt:    now.Add(-time.Hour),
		ExpiresAt:    now.Add(-time.Minute),
	}
	expired = testProvisionSweepJob(expired)
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		*dest = []*models.ProvisionJob{expired}
	}).Once()
	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Once()

	srv := &Server{
		cfg:   config.Config{ProvisionQueueURL: "https://sqs.us-east-1.amazonaws.com/123/provision"},
		store: store.New(db),
		sqs:   &fakeSQS{},
	}

	out, err := srv.processActiveProvisionSweep(context.Background(), "req-sweep", now)
	require.NoError(t, err)
	require.EqualValues(t, 1, out["active_jobs"])
	require.EqualValues(t, 1, out["processed"])
	require.Equal(t, models.ProvisionJobStatusError, expired.Status)
	require.Equal(t, "expired", expired.ErrorCode)
}

func TestProcessActiveProvisionSweep_ValidationAndErrorAccounting(t *testing.T) {
	t.Parallel()

	now := time.Unix(2_000, 0).UTC()
	var nilServer *Server
	_, err := nilServer.processActiveProvisionSweep(context.Background(), "req", now)
	require.ErrorContains(t, err, "store not initialized")

	out, err := (&Server{store: store.New(ttmocks.NewMockExtendedDB())}).processActiveProvisionSweep(context.Background(), "req", now)
	require.NoError(t, err)
	require.Equal(t, "sqs not initialized", out["skipped"])

	dbListErr := ttmocks.NewMockExtendedDB()
	qListErr := new(ttmocks.MockQuery)
	dbListErr.On("WithContext", mock.Anything).Return(dbListErr).Once()
	dbListErr.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qListErr).Once()
	qListErr.On("Where", "SK", "=", models.SKJob).Return(qListErr).Once()
	qListErr.On("Limit", 100).Return(qListErr).Once()
	qListErr.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return((*core.PaginatedResult)(nil), fmt.Errorf("db down")).Once()

	_, err = (&Server{store: store.New(dbListErr), sqs: &fakeSQS{}}).processActiveProvisionSweep(context.Background(), "req", now)
	require.ErrorContains(t, err, "db down")

	dbProcessErr := ttmocks.NewMockExtendedDB()
	qProcessErr := new(ttmocks.MockQuery)
	dbProcessErr.On("WithContext", mock.Anything).Return(dbProcessErr).Once()
	dbProcessErr.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qProcessErr).Once()
	qProcessErr.On("Where", "SK", "=", models.SKJob).Return(qProcessErr).Once()
	qProcessErr.On("Limit", 100).Return(qProcessErr).Once()
	qProcessErr.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		*dest = []*models.ProvisionJob{
			nil,
			testProvisionSweepJob(&models.ProvisionJob{ID: "done", InstanceSlug: "slug", Status: models.ProvisionJobStatusOK}),
			testProvisionSweepJob(&models.ProvisionJob{ID: "stale", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, UpdatedAt: now.Add(-time.Hour)}),
			{PK: "UPDATE_JOB#u1", SK: models.SKJob, ID: "u1", Status: models.ProvisionJobStatusRunning, UpdatedAt: now.Add(-time.Hour)},
		}
	}).Once()

	out, err = (&Server{
		cfg:   config.Config{ProvisionQueueURL: "https://sqs.us-east-1.amazonaws.com/123/provision"},
		store: store.New(dbProcessErr),
		sqs:   &fakeSQS{err: fmt.Errorf("sqs down")},
	}).processActiveProvisionSweep(context.Background(), "req", now)
	require.ErrorContains(t, err, "sqs down")
	require.EqualValues(t, 1, out["active_jobs"])
	require.EqualValues(t, 0, out["processed"])
	require.EqualValues(t, 1, out["errors"])
}

func testProvisionSweepJob(job *models.ProvisionJob) *models.ProvisionJob {
	if job == nil {
		return nil
	}
	if strings.TrimSpace(job.InstanceSlug) == "" {
		job.InstanceSlug = "slug"
	}
	_ = job.UpdateKeys()
	return job
}

func TestListProvisionSweepJobs_MultiPageCursorChain(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	qJob.On("Where", "SK", "=", models.SKJob).Return(qJob).Once()
	// Literal pins: page size 100, resume cursor "sweep-ct-1".
	qJob.On("Limit", 100).Return(qJob).Times(2)
	qJob.On("Cursor", "sweep-ct-1").Return(qJob).Once()
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "sweep-ct-1"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		*dest = []*models.ProvisionJob{testProvisionSweepJob(&models.ProvisionJob{ID: "j1", Status: models.ProvisionJobStatusQueued})}
	}).Once()
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		*dest = []*models.ProvisionJob{
			testProvisionSweepJob(&models.ProvisionJob{ID: "j2", Status: models.ProvisionJobStatusQueued}),
			testProvisionSweepJob(&models.ProvisionJob{ID: "j3", Status: models.ProvisionJobStatusQueued}),
		}
	}).Once()

	srv := &Server{store: store.New(db)}
	items, err := srv.listProvisionSweepJobs(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.Equal(t, "j1", items[0].ID)
	require.Equal(t, "j2", items[1].ID)
	require.Equal(t, "j3", items[2].ID)
	qJob.AssertExpectations(t)
	qJob.AssertNotCalled(t, "Scan", mock.Anything)
}

func TestListProvisionSweepJobs_ExactPageSizeMultiple(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	qJob.On("Where", "SK", "=", models.SKJob).Return(qJob).Once()
	qJob.On("Limit", 100).Return(qJob).Times(2)
	qJob.On("Cursor", "sweep-ct-2").Return(qJob).Once()
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "sweep-ct-2"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		page := make([]*models.ProvisionJob, 0, 100)
		for i := 0; i < 100; i++ {
			page = append(page, testProvisionSweepJob(&models.ProvisionJob{ID: fmt.Sprintf("sweep-job-%03d", i), Status: models.ProvisionJobStatusQueued}))
		}
		*dest = page
	}).Once()
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		page := make([]*models.ProvisionJob, 0, 100)
		for i := 100; i < 200; i++ {
			page = append(page, testProvisionSweepJob(&models.ProvisionJob{ID: fmt.Sprintf("sweep-job-%03d", i), Status: models.ProvisionJobStatusQueued}))
		}
		*dest = page
	}).Once()

	srv := &Server{store: store.New(db)}
	items, err := srv.listProvisionSweepJobs(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 200)
	qJob.AssertExpectations(t)
}

func TestListProvisionSweepJobs_EmptyTable(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	qJob.On("Where", "SK", "=", models.SKJob).Return(qJob).Once()
	qJob.On("Limit", 100).Return(qJob).Once()
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		*dest = nil
	}).Once()

	srv := &Server{store: store.New(db)}
	items, err := srv.listProvisionSweepJobs(context.Background())
	require.NoError(t, err)
	require.Empty(t, items)
	qJob.AssertExpectations(t)
}

func TestListProvisionSweepJobs_CapExhaustionFailsClosed(t *testing.T) {
	t.Parallel()

	// The sweep walk's cap is 20 pages (page >= 20): exactly twenty pages are
	// read then the sweep fails closed — never a silently truncated sweep.
	// Exact call counts kill the cap-removed and off-by-one mutations.
	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	qJob.On("Where", "SK", "=", models.SKJob).Return(qJob).Once()
	qJob.On("Limit", 100).Return(qJob).Times(20)
	qJob.On("Cursor", mock.Anything).Return(qJob).Times(19)
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "keep-going"}, nil).Times(20)

	srv := &Server{store: store.New(db)}
	items, err := srv.listProvisionSweepJobs(context.Background())
	require.Nil(t, items)
	require.ErrorContains(t, err, "exceeded 20 pages")
	qJob.AssertExpectations(t)
	qJob.AssertNotCalled(t, "Scan", mock.Anything)
}

func TestProcessActiveProvisionSweep_MultiPageCursorChain(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	qJob.On("Where", "SK", "=", models.SKJob).Return(qJob).Once()
	qJob.On("Limit", 100).Return(qJob).Times(2)
	qJob.On("Cursor", "sweep-ct-3").Return(qJob).Once()

	now := time.Unix(3_000, 0).UTC()
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "sweep-ct-3"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		*dest = []*models.ProvisionJob{testProvisionSweepJob(&models.ProvisionJob{
			ID: "m1", Status: models.ProvisionJobStatusQueued,
			UpdatedAt: now.Add(-provisionSweepStaleAfter - time.Second), ExpiresAt: now.Add(time.Hour),
		})}
	}).Once()
	qJob.On("AllPaginated", mock.AnythingOfType("*[]*models.ProvisionJob")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.ProvisionJob](t, args, 0)
		*dest = []*models.ProvisionJob{testProvisionSweepJob(&models.ProvisionJob{
			ID: "m2", Status: models.ProvisionJobStatusQueued,
			UpdatedAt: now.Add(-provisionSweepStaleAfter - time.Second), ExpiresAt: now.Add(time.Hour),
		})}
	}).Once()

	sqsClient := &fakeSQS{}
	srv := &Server{
		cfg:   config.Config{ProvisionQueueURL: "https://sqs.us-east-1.amazonaws.com/123/provision"},
		store: store.New(db),
		sqs:   sqsClient,
	}

	out, err := srv.processActiveProvisionSweep(context.Background(), "req-sweep-multi", now)
	require.NoError(t, err)
	require.EqualValues(t, 2, out["active_jobs"])
	require.EqualValues(t, 2, out["processed"])
	require.EqualValues(t, 0, out["errors"])
	require.Len(t, sqsClient.inputs, 2)
	qJob.AssertExpectations(t)
}
