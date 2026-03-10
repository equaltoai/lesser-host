package provisionworker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const branchTestRunID = "run-1"

func newBranchTestStore() (*store.Store, *ttmocks.MockExtendedDB) {
	db := ttmocks.NewMockExtendedDB()
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	return store.New(db), db
}

func mockBranchInstanceLookup(t *testing.T, db *ttmocks.MockExtendedDB, inst *models.Instance, err error) {
	t.Helper()

	qInst := new(ttmocks.MockQuery)
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()

	if err != nil {
		qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(err).Maybe()
		return
	}

	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		if inst != nil {
			*dest = *inst
		}
	}).Maybe()
}

func managedUpdateRunnerJob(step string) *models.UpdateJob {
	return &models.UpdateJob{
		ID:                             "job-1",
		InstanceSlug:                   "slug",
		Status:                         models.UpdateJobStatusRunning,
		Step:                           step,
		MaxAttempts:                    2,
		AccountID:                      "123456789012",
		AccountRoleName:                "lesser-host-instance",
		Region:                         "us-east-1",
		BaseDomain:                     "slug.example.com",
		LesserVersion:                  "v1.2.3",
		LesserBodyVersion:              "body-v1.2.3",
		LesserHostBaseURL:              "https://lab.example.com",
		LesserHostAttestationsURL:      "https://lab.example.com",
		LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
	}
}

func managedUpdateRunnerInstance() *models.Instance {
	return &models.Instance{
		Slug:  "slug",
		Owner: "wallet-deadbeef",
	}
}

func assertUpdateRunnerWaitBranches(
	t *testing.T,
	now time.Time,
	step string,
	fn func(*Server, context.Context, *models.UpdateJob, string, time.Time) (time.Duration, bool, error),
	successRunURL string,
	successStep string,
	successNote string,
	timeoutCode string,
	failedRunURL string,
	failedCode string,
) {
	t.Helper()

	t.Run("succeeded advances to next step", func(t *testing.T) {
		st, _ := newBranchTestStore()
		srv := &Server{
			store: st,
			cb: &fakeCodebuild{
				batchOut: &codebuild.BatchGetBuildsOutput{
					Builds: []cbtypes.Build{{
						BuildStatus: cbtypes.StatusTypeSucceeded,
						Logs:        &cbtypes.LogsLocation{DeepLink: aws.String(" " + successRunURL + " ")},
					}},
				},
			},
		}
		job := managedUpdateRunnerJob(step)
		job.RunID = branchTestRunID

		delay, done, err := fn(srv, context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, successStep, job.Step)
		require.Equal(t, successNote, job.Note)
		require.Empty(t, job.RunID)
		require.Equal(t, successRunURL, job.RunURL)
	})

	t.Run("timed out in progress fails", func(t *testing.T) {
		st, _ := newBranchTestStore()
		srv := &Server{
			store: st,
			cb: &fakeCodebuild{
				batchOut: &codebuild.BatchGetBuildsOutput{
					Builds: []cbtypes.Build{{BuildStatus: cbtypes.StatusTypeInProgress}},
				},
			},
		}
		job := managedUpdateRunnerJob(step)
		job.RunID = branchTestRunID
		job.CreatedAt = now.Add(-(provisionMaxDeployAge + time.Minute))

		delay, done, err := fn(srv, context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, models.UpdateJobStatusError, job.Status)
		require.Equal(t, timeoutCode, job.ErrorCode)
	})

	t.Run("failed status captures run url and fails", func(t *testing.T) {
		st, _ := newBranchTestStore()
		srv := &Server{
			store: st,
			cb: &fakeCodebuild{
				batchOut: &codebuild.BatchGetBuildsOutput{
					Builds: []cbtypes.Build{{
						BuildStatus: cbtypes.StatusTypeFailed,
						Logs:        &cbtypes.LogsLocation{DeepLink: aws.String(failedRunURL)},
					}},
				},
			},
		}
		job := managedUpdateRunnerJob(step)
		job.RunID = branchTestRunID

		delay, done, err := fn(srv, context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, models.UpdateJobStatusError, job.Status)
		require.Equal(t, failedCode, job.ErrorCode)
		require.Equal(t, failedRunURL, job.RunURL)
	})
}

func TestRunManagedUpdateStateMachine_HelperBranches(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()

	t.Run("requires store", func(t *testing.T) {
		err := (&Server{}).runManagedUpdateStateMachine(context.Background(), &models.UpdateJob{}, "req", now)
		require.ErrorContains(t, err, "store not initialized")
	})

	t.Run("nil and expired jobs", func(t *testing.T) {
		st, _ := newBranchTestStore()
		srv := &Server{store: st}
		require.NoError(t, srv.runManagedUpdateStateMachine(context.Background(), nil, "req", now))

		job := &models.UpdateJob{
			ID:           "job-1",
			InstanceSlug: "slug",
			Status:       models.UpdateJobStatusQueued,
			ExpiresAt:    now.Add(-time.Second),
		}

		require.NoError(t, srv.runManagedUpdateStateMachine(context.Background(), job, "req", now))
		require.Equal(t, models.UpdateJobStatusError, job.Status)
		require.Equal(t, updateStepFailed, job.Step)
		require.Equal(t, "expired", job.ErrorCode)
	})
}

func TestInitializeManagedUpdateJob_SetsDefaultsAndPreservesValues(t *testing.T) {
	t.Parallel()

	srv := &Server{cfg: config.Config{ManagedInstanceRoleName: "default-role"}}

	srv.initializeManagedUpdateJob(nil)

	job := &models.UpdateJob{}
	srv.initializeManagedUpdateJob(job)
	require.Equal(t, updateStepQueued, job.Step)
	require.Equal(t, int64(10), job.MaxAttempts)
	require.Equal(t, "default-role", job.AccountRoleName)

	job = &models.UpdateJob{Step: updateStepVerify, MaxAttempts: 4, AccountRoleName: "custom-role"}
	srv.initializeManagedUpdateJob(job)
	require.Equal(t, updateStepVerify, job.Step)
	require.Equal(t, int64(4), job.MaxAttempts)
	require.Equal(t, "custom-role", job.AccountRoleName)
}

func TestStartManagedUpdateJobIfQueued_Branches(t *testing.T) {
	t.Parallel()

	st, _ := newBranchTestStore()
	srv := &Server{store: st}
	now := time.Unix(200, 0).UTC()

	t.Run("non queued job is ignored", func(t *testing.T) {
		job := &models.UpdateJob{
			ID:           "job-1",
			InstanceSlug: "slug",
			Status:       models.UpdateJobStatusRunning,
			Step:         updateStepVerify,
			Note:         "keep me",
		}

		require.NoError(t, srv.startManagedUpdateJobIfQueued(context.Background(), job, "req", now))
		require.Equal(t, models.UpdateJobStatusRunning, job.Status)
		require.Equal(t, updateStepVerify, job.Step)
		require.Equal(t, "keep me", job.Note)
	})

	t.Run("queued job starts and seeds queued step", func(t *testing.T) {
		job := &models.UpdateJob{
			ID:           "job-2",
			InstanceSlug: "slug",
			Status:       models.UpdateJobStatusQueued,
		}

		require.NoError(t, srv.startManagedUpdateJobIfQueued(context.Background(), job, "req", now))
		require.Equal(t, models.UpdateJobStatusRunning, job.Status)
		require.Equal(t, updateStepQueued, job.Step)
		require.Equal(t, "starting update", job.Note)
	})
}

func TestAdvanceManagedUpdateLoop_Branches(t *testing.T) {
	t.Parallel()

	now := time.Unix(300, 0).UTC()

	t.Run("requires store", func(t *testing.T) {
		err := (&Server{}).advanceManagedUpdateLoop(context.Background(), &models.UpdateJob{}, "req", now)
		require.ErrorContains(t, err, "store not initialized")
	})

	t.Run("nil job is ignored", func(t *testing.T) {
		st, _ := newBranchTestStore()
		require.NoError(t, (&Server{store: st}).advanceManagedUpdateLoop(context.Background(), nil, "req", now))
	})

	t.Run("non processable job stops without requeue", func(t *testing.T) {
		st, _ := newBranchTestStore()
		sqsClient := &fakeSQS{}
		srv := &Server{
			cfg:   config.Config{ProvisionQueueURL: "https://example.com/queue"},
			store: st,
			sqs:   sqsClient,
		}
		job := &models.UpdateJob{ID: "job-1", InstanceSlug: "slug", Status: models.UpdateJobStatusOK, Step: updateStepVerify}

		require.NoError(t, srv.advanceManagedUpdateLoop(context.Background(), job, "req", now))
		require.Empty(t, sqsClient.inputs)
	})

	t.Run("done step exits immediately", func(t *testing.T) {
		st, _ := newBranchTestStore()
		srv := &Server{store: st}
		job := &models.UpdateJob{ID: "job-1", InstanceSlug: "slug", Status: models.UpdateJobStatusRunning, Step: updateStepDone}

		require.NoError(t, srv.advanceManagedUpdateLoop(context.Background(), job, "req", now))
	})

	t.Run("delayed step requeues", func(t *testing.T) {
		st, _ := newBranchTestStore()
		sqsClient := &fakeSQS{}
		srv := &Server{
			cfg:   config.Config{ProvisionQueueURL: "https://example.com/queue"},
			store: st,
			sqs:   sqsClient,
			cb: &fakeCodebuild{
				batchOut: &codebuild.BatchGetBuildsOutput{
					Builds: []cbtypes.Build{{BuildStatus: cbtypes.StatusTypeInProgress}},
				},
			},
		}
		job := &models.UpdateJob{
			ID:           "job-1",
			InstanceSlug: "slug",
			Status:       models.UpdateJobStatusRunning,
			Step:         updateStepDeployWait,
			RunID:        branchTestRunID,
			MaxAttempts:  2,
			CreatedAt:    now,
		}

		require.NoError(t, srv.advanceManagedUpdateLoop(context.Background(), job, "req", now))
		require.Len(t, sqsClient.inputs, 1)
		require.EqualValues(t, provisionDefaultPollDelay/time.Second, sqsClient.inputs[0].DelaySeconds)
	})
}

func TestAdvanceUpdateDone_ReturnsDone(t *testing.T) {
	t.Parallel()

	delay, done, err := (&Server{}).advanceUpdateDone(context.Background(), &models.UpdateJob{}, "req", time.Unix(1, 0).UTC())
	require.NoError(t, err)
	require.True(t, done)
	require.Zero(t, delay)
}

func TestAdvanceUpdateBodyDeployStart_Branches(t *testing.T) {
	t.Parallel()

	now := time.Unix(400, 0).UTC()

	t.Run("skips when body version missing", func(t *testing.T) {
		st, _ := newBranchTestStore()
		srv := &Server{store: st}
		job := managedUpdateRunnerJob(updateStepBodyDeployStart)
		job.LesserBodyVersion = ""
		job.RunID = "stale-run"

		delay, done, err := srv.advanceUpdateBodyDeployStart(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, updateStepVerify, job.Step)
		require.Equal(t, "verifying deployment", job.Note)
		require.Empty(t, job.RunID)
	})

	t.Run("existing run id advances to wait", func(t *testing.T) {
		st, _ := newBranchTestStore()
		srv := &Server{store: st}
		job := managedUpdateRunnerJob(updateStepBodyDeployStart)
		job.RunID = branchTestRunID

		delay, done, err := srv.advanceUpdateBodyDeployStart(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, provisionDefaultPollDelay, delay)
		require.Equal(t, updateStepBodyDeployWait, job.Step)
		require.Equal(t, "lesser-body deploy runner already started", job.Note)
	})

	t.Run("another worker claim retries without starting codebuild", func(t *testing.T) {
		db := ttmocks.NewMockExtendedDBStrict()
		db.On("WithContext", mock.Anything).Return(db).Maybe()
		st := store.New(db)
		db.On("TransactWrite", mock.Anything, mock.Anything).Return(theoryErrors.ErrConditionFailed).Once()
		mockBranchInstanceLookup(t, db, managedUpdateRunnerInstance(), nil)
		cb := &fakeCodebuild{}
		srv := &Server{
			cfg:   config.Config{ManagedProvisionRunnerProjectName: "project", Stage: "lab"},
			store: st,
			cb:    cb,
		}
		job := managedUpdateRunnerJob(updateStepBodyDeployStart)

		delay, done, err := srv.advanceUpdateBodyDeployStart(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, provisionDefaultShortRetryDelay, delay)
		require.Equal(t, updateStepBodyDeployStart, job.Step)
		require.Empty(t, cb.startInputs)
	})

	t.Run("instance load error retries", func(t *testing.T) {
		st, db := newBranchTestStore()
		mockBranchInstanceLookup(t, db, nil, errors.New("boom"))
		srv := &Server{store: st}
		job := managedUpdateRunnerJob(updateStepBodyDeployStart)
		job.MaxAttempts = 3

		delay, done, err := srv.advanceUpdateBodyDeployStart(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, provisionDefaultShortRetryDelay, delay)
		require.Equal(t, int64(1), job.Attempts)
		require.Equal(t, models.UpdateJobStatusRunning, job.Status)
	})

	t.Run("runner start error retries then fails", func(t *testing.T) {
		assertUpdateRunnerStartRetriesThenFails(
			t,
			now,
			updateStepBodyDeployStart,
			func(srv *Server, job *models.UpdateJob, runAt time.Time) (time.Duration, bool, error) {
				return srv.advanceUpdateBodyDeployStart(context.Background(), job, "req", runAt)
			},
			"failed to start lesser-body deploy runner; retrying",
			"body_deploy_start_failed",
		)
	})
}

func TestAdvanceUpdateRunnerClaimed_Branches(t *testing.T) {
	t.Parallel()

	now := time.Unix(650, 0).UTC()

	t.Run("recent claim backs off without mutating the job", func(t *testing.T) {
		st, _ := newBranchTestStore()
		srv := &Server{store: st}
		job := managedUpdateRunnerJob(updateStepDeployClaimed)
		job.UpdatedAt = now.Add(-time.Minute)

		delay, done, err := srv.advanceUpdateDeployClaimed(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, provisionDefaultShortRetryDelay, delay)
		require.Equal(t, models.UpdateJobStatusRunning, job.Status)
		require.Equal(t, updateStepDeployClaimed, job.Step)
	})

	t.Run("stale claim fails loudly instead of relaunching", func(t *testing.T) {
		st, _ := newBranchTestStore()
		srv := &Server{store: st}
		job := managedUpdateRunnerJob(updateStepDeployClaimed)
		job.UpdatedAt = now.Add(-(updateRunnerStartClaimMaxAge + time.Second))

		delay, done, err := srv.advanceUpdateDeployClaimed(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, models.UpdateJobStatusError, job.Status)
		require.Equal(t, updateStepFailed, job.Step)
		require.Equal(t, "deploy_start_claim_stale", job.ErrorCode)
		require.Contains(t, job.ErrorMessage, "refusing to launch a duplicate deploy runner")
	})
}

func TestAdvanceUpdateBodyDeployWait_Branches(t *testing.T) {
	t.Parallel()

	assertUpdateRunnerWaitBranches(
		t,
		time.Unix(500, 0).UTC(),
		updateStepBodyDeployWait,
		(*Server).advanceUpdateBodyDeployWait,
		"https://logs.example/body",
		updateStepBodyReceiptIngest,
		"ingesting lesser-body receipt",
		"body_deploy_timeout",
		"https://logs.example/body-fail",
		"body_deploy_failed",
	)
}

func TestAdvanceUpdateDeployMcpStart_Branches(t *testing.T) {
	t.Parallel()

	now := time.Unix(600, 0).UTC()

	t.Run("existing run id advances to wait", func(t *testing.T) {
		st, _ := newBranchTestStore()
		srv := &Server{store: st}
		job := managedUpdateRunnerJob(updateStepDeployMcpStart)
		job.RunID = branchTestRunID

		delay, done, err := srv.advanceUpdateDeployMcpStart(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, provisionDefaultPollDelay, delay)
		require.Equal(t, updateStepDeployMcpWait, job.Step)
		require.Equal(t, "MCP wiring deploy runner already started", job.Note)
	})

	t.Run("runner start error retries then fails", func(t *testing.T) {
		assertUpdateRunnerStartRetriesThenFails(
			t,
			now,
			updateStepDeployMcpStart,
			func(srv *Server, job *models.UpdateJob, runAt time.Time) (time.Duration, bool, error) {
				return srv.advanceUpdateDeployMcpStart(context.Background(), job, "req", runAt)
			},
			"failed to start MCP wiring deploy runner; retrying",
			"mcp_deploy_start_failed",
		)
	})
}

func TestAdvanceUpdateDeployMcpWait_Branches(t *testing.T) {
	t.Parallel()

	assertUpdateRunnerWaitBranches(
		t,
		time.Unix(700, 0).UTC(),
		updateStepDeployMcpWait,
		(*Server).advanceUpdateDeployMcpWait,
		"https://logs.example/mcp",
		updateStepMCPReceiptIngest,
		"ingesting MCP wiring receipt",
		"mcp_deploy_timeout",
		"https://logs.example/mcp-fail",
		"mcp_deploy_failed",
	)
}

func assertUpdateRunnerStartRetriesThenFails(
	t *testing.T,
	now time.Time,
	step string,
	run func(*Server, *models.UpdateJob, time.Time) (time.Duration, bool, error),
	wantRetryNote string,
	wantErrorCode string,
) {
	t.Helper()

	st, db := newBranchTestStore()
	mockBranchInstanceLookup(t, db, managedUpdateRunnerInstance(), nil)
	srv := &Server{
		cfg:   config.Config{ManagedProvisionRunnerProjectName: "project", Stage: "lab"},
		store: st,
		cb:    &fakeCodebuild{startErr: errors.New("boom")},
	}
	job := managedUpdateRunnerJob(step)

	delay, done, err := run(srv, job, now)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, provisionDefaultShortRetryDelay, delay)
	require.Equal(t, int64(1), job.Attempts)
	require.Contains(t, job.Note, wantRetryNote)

	delay, done, err = run(srv, job, now.Add(time.Second))
	require.NoError(t, err)
	require.False(t, done)
	require.Zero(t, delay)
	require.Equal(t, models.UpdateJobStatusError, job.Status)
	require.Equal(t, updateStepFailed, job.Step)
	require.Equal(t, wantErrorCode, job.ErrorCode)
}
