package provisionworker

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	theoryErrors "github.com/theory-cloud/tabletheory/v2/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/v2/pkg/mocks"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestProcessUpdateJob_DisabledAndMissingConfig(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qJob).Maybe()
	qJob.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qJob).Maybe()
	qJob.On("ConsistentRead").Return(qJob).Maybe()

	var loaded1 *models.UpdateJob
	qJob.On("First", mock.AnythingOfType("*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.UpdateJob](t, args, 0)
		*dest = models.UpdateJob{ID: "j1", InstanceSlug: "slug", Status: models.UpdateJobStatusQueued}
		loaded1 = dest
	}).Once()

	var loaded2 *models.UpdateJob
	qJob.On("First", mock.AnythingOfType("*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.UpdateJob](t, args, 0)
		*dest = models.UpdateJob{ID: "j2", InstanceSlug: "slug", Status: models.UpdateJobStatusQueued}
		loaded2 = dest
	}).Once()

	st := store.New(db)
	srv := &Server{cfg: config.Config{ManagedProvisioningEnabled: false}, store: st}

	require.NoError(t, srv.processUpdateJob(context.Background(), "req", "j1"))
	require.NotNil(t, loaded1)
	require.Equal(t, models.UpdateJobStatusError, loaded1.Status)
	require.Equal(t, updateStepFailed, loaded1.Step)
	require.Equal(t, "disabled", loaded1.ErrorCode)

	srv.cfg.ManagedProvisioningEnabled = true
	require.NoError(t, srv.processUpdateJob(context.Background(), "req", "j2"))
	require.NotNil(t, loaded2)
	require.Equal(t, models.UpdateJobStatusError, loaded2.Status)
	require.Equal(t, updateStepFailed, loaded2.Step)
	require.Equal(t, "missing_config", loaded2.ErrorCode)
}

func TestLoadUpdateJob_BlankAndNotFound(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)
	srv := &Server{store: st}

	got, err := srv.loadUpdateJob(context.Background(), "   ")
	require.NoError(t, err)
	require.Nil(t, got)

	qJob := new(ttmocks.MockQuery)
	db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qJob).Maybe()
	qJob.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qJob).Maybe()
	qJob.On("ConsistentRead").Return(qJob).Maybe()
	qJob.On("First", mock.AnythingOfType("*models.UpdateJob")).Return(theoryErrors.ErrItemNotFound).Once()

	got, err = srv.loadUpdateJob(context.Background(), "j1")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestAdvanceManagedUpdateLoop_InvalidStepFailsJob(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	srv := &Server{store: store.New(db)}

	job := &models.UpdateJob{ID: "j1", InstanceSlug: "slug", Status: models.UpdateJobStatusRunning, Step: "nope"}
	require.NoError(t, srv.advanceManagedUpdateLoop(context.Background(), job, "req", time.Unix(1, 0).UTC()))
	require.Equal(t, models.UpdateJobStatusError, job.Status)
	require.Equal(t, updateStepFailed, job.Step)
	require.Equal(t, "invalid_step", job.ErrorCode)
}

func TestAdvanceUpdateDeployWait_StatusVariants(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)

	t.Run("in_progress timeout fails", func(t *testing.T) {
		t.Parallel()

		cb := &fakeCodebuild{batchOut: &codebuild.BatchGetBuildsOutput{Builds: []cbtypes.Build{{BuildStatus: cbtypes.StatusTypeInProgress}}}}
		srv := &Server{store: st, cb: cb}

		now := time.Unix(1000, 0).UTC()
		job := &models.UpdateJob{
			ID:           "j1",
			InstanceSlug: "slug",
			Status:       models.UpdateJobStatusRunning,
			Step:         updateStepDeployWait,
			RunID:        "run",
			MaxAttempts:  3,
			CreatedAt:    now.Add(-4 * time.Hour),
		}
		delay, done, err := srv.advanceUpdateDeployWait(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, time.Duration(0), delay)
		require.Equal(t, models.UpdateJobStatusError, job.Status)
		require.Equal(t, "deploy_timeout", job.ErrorCode)
	})

	t.Run("failed sets run url and fails", func(t *testing.T) {
		t.Parallel()

		cb := &fakeCodebuild{
			batchOut: &codebuild.BatchGetBuildsOutput{
				Builds: []cbtypes.Build{{
					BuildStatus:  cbtypes.StatusTypeFailed,
					CurrentPhase: aws.String("BUILD"),
					Logs:         &cbtypes.LogsLocation{DeepLink: aws.String(" https://deep ")},
					Phases: []cbtypes.BuildPhase{{
						PhaseType:   cbtypes.BuildPhaseType("BUILD"),
						PhaseStatus: cbtypes.StatusTypeFailed,
						Contexts:    []cbtypes.PhaseContext{{Message: aws.String("unit tests failed")}},
					}},
				}},
			},
		}
		srv := &Server{store: st, cb: cb}

		job := &models.UpdateJob{
			ID:           "j1",
			InstanceSlug: "slug",
			Status:       models.UpdateJobStatusRunning,
			Step:         updateStepDeployWait,
			RunID:        "run",
			MaxAttempts:  3,
		}
		delay, done, err := srv.advanceUpdateDeployWait(context.Background(), job, "req", time.Unix(1, 0).UTC())
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, time.Duration(0), delay)
		require.Equal(t, models.UpdateJobStatusError, job.Status)
		require.Equal(t, "deploy_failed", job.ErrorCode)
		require.Equal(t, updatePhaseDeploy, job.FailedPhase)
		require.Contains(t, job.ErrorMessage, "BUILD: unit tests failed")
		require.Equal(t, job.ErrorMessage, job.Note)
		require.Equal(t, "https://deep", strings.TrimSpace(job.RunURL))
		require.Contains(t, job.DeployError, "unit tests failed")
	})

	t.Run("unknown status requeues", func(t *testing.T) {
		t.Parallel()

		cb := &fakeCodebuild{batchOut: &codebuild.BatchGetBuildsOutput{Builds: []cbtypes.Build{{BuildStatus: cbtypes.StatusType("weird")}}}}
		srv := &Server{store: st, cb: cb}

		job := &models.UpdateJob{
			ID:           "j1",
			InstanceSlug: "slug",
			Status:       models.UpdateJobStatusRunning,
			Step:         updateStepDeployWait,
			RunID:        "run",
			MaxAttempts:  3,
		}
		delay, done, err := srv.advanceUpdateDeployWait(context.Background(), job, "req", time.Unix(1, 0).UTC())
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, provisionDefaultPollDelay, delay)
		require.Equal(t, models.UpdateJobStatusRunning, job.Status)
		require.Contains(t, job.Note, "deploy runner status:")
	})

	t.Run("poll error retries then fails", func(t *testing.T) {
		t.Parallel()

		cb := &fakeCodebuild{batchErr: errors.New("boom")}
		srv := &Server{store: st, cb: cb}

		job := &models.UpdateJob{
			ID:           "j1",
			InstanceSlug: "slug",
			Status:       models.UpdateJobStatusRunning,
			Step:         updateStepDeployWait,
			RunID:        "run",
			MaxAttempts:  2,
		}

		delay, done, err := srv.advanceUpdateDeployWait(context.Background(), job, "req", time.Unix(1, 0).UTC())
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, provisionDefaultPollDelay, delay)
		require.Contains(t, job.Note, "failed to poll deploy runner; retrying")

		delay, done, err = srv.advanceUpdateDeployWait(context.Background(), job, "req", time.Unix(2, 0).UTC())
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, time.Duration(0), delay)
		require.Equal(t, models.UpdateJobStatusError, job.Status)
		require.Equal(t, "deploy_status_failed", job.ErrorCode)
	})
}

func TestAdvanceUpdateBodyDeployWait_SanitizesCommandExecutionFailureDetail(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)
	srv := &Server{
		store: st,
		cb: &fakeCodebuild{
			batchOut: &codebuild.BatchGetBuildsOutput{
				Builds: []cbtypes.Build{{
					BuildStatus:  cbtypes.StatusTypeFailed,
					CurrentPhase: aws.String("BUILD"),
					Logs:         &cbtypes.LogsLocation{DeepLink: aws.String(" https://logs.example/body ")},
					Phases: []cbtypes.BuildPhase{{
						PhaseType:   cbtypes.BuildPhaseType("BUILD"),
						PhaseStatus: cbtypes.StatusTypeFailed,
						Contexts: []cbtypes.PhaseContext{{
							Message: aws.String("COMMAND_EXECUTION_ERROR: Error while executing command: bash ./deploy-lesser-body-from-release.sh --stack-name demo --asset-bucket bucket Reason: exit status 1"),
						}},
					}},
				}},
			},
		},
	}

	job := &models.UpdateJob{
		ID:           "j-body",
		InstanceSlug: "slug",
		Status:       models.UpdateJobStatusRunning,
		Step:         updateStepBodyDeployWait,
		RunID:        "run-body",
		MaxAttempts:  3,
	}

	delay, done, err := srv.advanceUpdateBodyDeployWait(context.Background(), job, "req", time.Unix(1, 0).UTC())
	require.NoError(t, err)
	require.False(t, done)
	require.Zero(t, delay)
	require.Equal(t, models.UpdateJobStatusError, job.Status)
	require.Equal(t, "body_deploy_failed", job.ErrorCode)
	require.Equal(t, updatePhaseBody, job.FailedPhase)
	require.Contains(t, job.ErrorMessage, "BUILD: command execution failed (exit status 1)")
	require.Contains(t, job.ErrorMessage, "CodeBuild: https://logs.example/body")
	require.NotContains(t, job.ErrorMessage, "--stack-name")
	require.Equal(t, job.ErrorMessage, job.Note)
	require.Equal(t, "https://logs.example/body", strings.TrimSpace(job.RunURL))
	require.NotContains(t, job.BodyError, "--asset-bucket")
}

func TestAdvanceUpdateReceiptIngest_RetriesAndFails(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	s3Client := &fakeS3{err: errors.New("nope")}

	srv := &Server{cfg: config.Config{ArtifactBucketName: "bucket"}, store: store.New(db), s3: s3Client}
	job := &models.UpdateJob{ID: "j1", InstanceSlug: "slug", Status: models.UpdateJobStatusRunning, Step: updateStepReceiptIngest, MaxAttempts: 2}

	delay, done, err := srv.advanceUpdateReceiptIngest(context.Background(), job, "req", time.Unix(1, 0).UTC())
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, provisionDefaultShortRetryDelay, delay)
	require.Contains(t, job.Note, "failed to load receipt; retrying")

	delay, done, err = srv.advanceUpdateReceiptIngest(context.Background(), job, "req", time.Unix(2, 0).UTC())
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, time.Duration(0), delay)
	require.Equal(t, models.UpdateJobStatusError, job.Status)
	require.Equal(t, "receipt_load_failed", job.ErrorCode)
}

func TestAdvanceUpdateBodyDeployWait_UsesUploadedBodyFailureArtifact(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)
	srv := &Server{
		cfg:   config.Config{ArtifactBucketName: "artifacts"},
		store: st,
		s3: &fakeS3{byKey: map[string]*s3.GetObjectOutput{
			"managed/updates/slug/j-body-artifact/body-failure.json": {
				Body: io.NopCloser(strings.NewReader(`{"version":1,"status":"failed","lesser_body_version":"v0.2.4","template_path":"lesser-body-managed-dev.template.json","stack_name":"slug-dev-lesser-body","verification_mode":"cloudformation_deploy_no_execute_changeset","detail":"Template format error: Every Default member must be a string."}`)),
			},
			"managed/updates/slug/j-body-artifact/body-state.json": {
				Body: io.NopCloser(strings.NewReader(" ")),
			},
		}},
		cb: &fakeCodebuild{
			batchOut: &codebuild.BatchGetBuildsOutput{
				Builds: []cbtypes.Build{{
					BuildStatus:  cbtypes.StatusTypeFailed,
					CurrentPhase: aws.String("BUILD"),
					Logs:         &cbtypes.LogsLocation{DeepLink: aws.String("https://logs.example/body-artifact")},
					Phases: []cbtypes.BuildPhase{{
						PhaseType:   cbtypes.BuildPhaseType("BUILD"),
						PhaseStatus: cbtypes.StatusTypeFailed,
						Contexts: []cbtypes.PhaseContext{{
							Message: aws.String("COMMAND_EXECUTION_ERROR: Error while executing command: bash ./deploy-lesser-body-from-release.sh Reason: exit status 1"),
						}},
					}},
				}},
			},
		},
	}

	job := &models.UpdateJob{
		ID:           "j-body-artifact",
		InstanceSlug: "slug",
		Status:       models.UpdateJobStatusRunning,
		Step:         updateStepBodyDeployWait,
		RunID:        "run-body-artifact",
		MaxAttempts:  3,
	}

	delay, done, err := srv.advanceUpdateBodyDeployWait(context.Background(), job, "req", time.Unix(2, 0).UTC())
	require.NoError(t, err)
	require.False(t, done)
	require.Zero(t, delay)
	require.Equal(t, models.UpdateJobStatusError, job.Status)
	require.Equal(t, "body_deploy_failed", job.ErrorCode)
	require.Contains(t, job.ErrorMessage, "cloudformation_deploy_no_execute_changeset lesser-body-managed-dev.template.json: Template format error: Every Default member must be a string")
	require.Contains(t, job.ErrorMessage, "CodeBuild: https://logs.example/body-artifact")
	require.NotContains(t, job.ErrorMessage, "command execution failed (exit status 1)")
	require.Contains(t, job.BodyError, "Template format error: Every Default member must be a string")
}

func TestAdvanceUpdateDeployWait_MissingRunnerPastGraceFails(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)
	srv := &Server{
		store: st,
		cb: &fakeCodebuild{
			batchOut: &codebuild.BatchGetBuildsOutput{Builds: nil},
		},
	}

	now := time.Unix(5000, 0).UTC()
	job := &models.UpdateJob{
		ID:           "j1",
		InstanceSlug: "slug",
		Status:       models.UpdateJobStatusRunning,
		Step:         updateStepDeployWait,
		RunID:        "run",
		RunURL:       "https://logs.example/deploy",
		UpdatedAt:    now.Add(-(updateRunnerMissingMaxAge + time.Minute)),
		MaxAttempts:  3,
	}

	delay, done, err := srv.advanceUpdateDeployWait(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.False(t, done)
	require.Zero(t, delay)
	require.Equal(t, models.UpdateJobStatusError, job.Status)
	require.Equal(t, "deploy_runner_missing", job.ErrorCode)
	require.Equal(t, updatePhaseDeploy, job.FailedPhase)
	require.Equal(t, job.ErrorMessage, job.Note)
	require.Contains(t, job.ErrorMessage, "disappeared from CodeBuild")
	require.Contains(t, job.ErrorMessage, "https://logs.example/deploy")
}

func TestProcessActiveUpdateSweep_ReconcilesTerminalRunnerFailure(t *testing.T) {
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

	sweepJob := &models.UpdateJob{
		ID:           "job-sweep",
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
		*dest = []*models.UpdateJob{{ID: sweepJob.ID, InstanceSlug: sweepJob.InstanceSlug, Status: sweepJob.Status, Step: sweepJob.Step, UpdatedAt: sweepJob.UpdatedAt}}
	})

	var loaded *models.UpdateJob
	qJob.On("First", mock.AnythingOfType("*models.UpdateJob")).Return(nil).Once().Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.UpdateJob](t, args, 0)
		*dest = *sweepJob
		loaded = dest
	})

	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Maybe()

	srv := NewServer(config.Config{
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
					Contexts:    []cbtypes.PhaseContext{{Message: aws.String("bundle mismatch")}},
				}},
			}},
		},
	}, nil)

	out, err := srv.processActiveUpdateSweep(context.Background(), "req-sweep", time.Unix(300, 0).UTC())
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, 1, out["active_jobs"])
	require.Equal(t, 1, out["processed"])
	require.Equal(t, 0, out["errors"])
	require.NotNil(t, loaded)
	require.Equal(t, models.UpdateJobStatusError, loaded.Status)
	require.Equal(t, "deploy_failed", loaded.ErrorCode)
	require.Contains(t, loaded.ErrorMessage, "bundle mismatch")
}

func TestProcessActiveUpdateSweep_ReconcilesTerminalBodyRunnerFailure(t *testing.T) {
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

	sweepJob := &models.UpdateJob{
		ID:           "job-body-sweep",
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
		*dest = []*models.UpdateJob{{ID: sweepJob.ID, InstanceSlug: sweepJob.InstanceSlug, Status: sweepJob.Status, Step: sweepJob.Step, UpdatedAt: sweepJob.UpdatedAt}}
	})

	var loaded *models.UpdateJob
	qJob.On("First", mock.AnythingOfType("*models.UpdateJob")).Return(nil).Once().Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.UpdateJob](t, args, 0)
		*dest = *sweepJob
		loaded = dest
	})

	db.On("TransactWrite", mock.Anything, mock.Anything).Return(nil).Maybe()

	srv := NewServer(config.Config{
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

	out, err := srv.processActiveUpdateSweep(context.Background(), "req-sweep", time.Unix(300, 0).UTC())
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, 1, out["active_jobs"])
	require.Equal(t, 1, out["processed"])
	require.Equal(t, 0, out["errors"])
	require.NotNil(t, loaded)
	require.Equal(t, models.UpdateJobStatusError, loaded.Status)
	require.Equal(t, "body_deploy_failed", loaded.ErrorCode)
	require.Equal(t, updatePhaseBody, loaded.FailedPhase)
	require.Contains(t, loaded.ErrorMessage, "command execution failed (exit status 1)")
	require.Contains(t, loaded.ErrorMessage, "CodeBuild: https://logs.example/body")
	require.NotContains(t, loaded.ErrorMessage, "--stack-name")
	require.Contains(t, loaded.BodyError, "command execution failed (exit status 1)")
}

func TestGetSecretsManagerSecretPlaintext_ParsesJSONAndBinary(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	_, err := getSecretsManagerSecretPlaintext(ctx, &fakeSecretsManager{}, " ")
	require.Error(t, err)

	sm := &fakeSecretsManager{getErr: errors.New("nope")}
	_, err = getSecretsManagerSecretPlaintext(ctx, sm, "arn")
	require.Error(t, err)

	sm = &fakeSecretsManager{getOut: &secretsmanager.GetSecretValueOutput{SecretString: aws.String(`{"secret":"lhk_test"}`)}}
	val, err := getSecretsManagerSecretPlaintext(ctx, sm, "arn")
	require.NoError(t, err)
	require.Equal(t, "lhk_test", val)

	sm = &fakeSecretsManager{getOut: &secretsmanager.GetSecretValueOutput{SecretBinary: []byte(`{"secret":"lhk_bin"}`)}}
	val, err = getSecretsManagerSecretPlaintext(ctx, sm, "arn")
	require.NoError(t, err)
	require.Equal(t, "lhk_bin", val)
}

func TestDescribeAndEnsureManagedInstanceKeySecret_RequiresManagedStageTags(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	qKey.On("IfNotExists").Return(qKey).Maybe()
	qKey.On("Create").Return(nil).Maybe()

	srv := &Server{store: store.New(db)}

	sm := &fakeSecretsManager{
		describeOut: &secretsmanager.DescribeSecretOutput{
			ARN:  aws.String("arn:secret"),
			Tags: nil,
		},
		getOut: &secretsmanager.GetSecretValueOutput{
			SecretString: aws.String(`{"secret":"lhk_test"}`),
		},
	}

	_, err := srv.describeAndEnsureManagedInstanceKeySecret(context.Background(), sm, " slug ", " secret ", "lab")
	require.ErrorContains(t, err, "refusing unmanaged")

	sm.describeOut.Tags = []smtypes.Tag{
		{Key: aws.String(managedInstanceKeySecretTagInstanceSlug), Value: aws.String("slug")},
		{Key: aws.String(managedInstanceKeySecretTagKeyID), Value: aws.String(secretValueToKeyID("lhk_test"))},
		{Key: aws.String(managedInstanceKeySecretTagManaged), Value: aws.String("true")},
		{Key: aws.String(managedInstanceKeySecretTagStage), Value: aws.String("lab")},
	}
	arn, err := srv.describeAndEnsureManagedInstanceKeySecret(context.Background(), sm, " slug ", " secret ", "lab")
	require.NoError(t, err)
	require.Equal(t, "arn:secret", arn)

	_, err = srv.describeAndEnsureManagedInstanceKeySecret(context.Background(), sm, " slug ", " secret ", "live")
	require.ErrorContains(t, err, "refusing unmanaged")
}

func TestDescribeAndEnsureManagedInstanceKeySecret_RejectsTagOnlyKeyIDForgery(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	qKey.On("IfNotExists").Return(qKey).Maybe()
	qKey.On("Create").Return(nil).Maybe()

	srv := &Server{store: store.New(db)}
	sm := &fakeSecretsManager{
		describeOut: &secretsmanager.DescribeSecretOutput{
			ARN: aws.String("arn:secret"),
			Tags: []smtypes.Tag{
				{Key: aws.String(managedInstanceKeySecretTagInstanceSlug), Value: aws.String("slug")},
				{Key: aws.String(managedInstanceKeySecretTagKeyID), Value: aws.String(secretValueToKeyID("lhk_attacker"))},
				{Key: aws.String(managedInstanceKeySecretTagManaged), Value: aws.String("true")},
				{Key: aws.String(managedInstanceKeySecretTagStage), Value: aws.String("lab")},
			},
		},
		getOut: &secretsmanager.GetSecretValueOutput{
			SecretString: aws.String(`{"secret":"lhk_real"}`),
		},
	}

	_, err := srv.describeAndEnsureManagedInstanceKeySecret(context.Background(), sm, "slug", "arn:secret", "lab")
	require.ErrorContains(t, err, "mismatched key id tag")
}

func TestEnsureManagedInstanceKeySecret_NonLiveReplacesLegacyUntaggedARN(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	qKey.On("IfNotExists").Return(qKey).Once()
	qKey.On("Create").Return(nil).Once()

	oldArn := "arn:aws:secretsmanager:us-east-1:123456789012:secret:legacy/instance-key"
	newArn := "arn:aws:secretsmanager:us-east-1:123456789012:secret:lab/slug/instance-key"
	canonicalName := managedInstanceKeySecretName("lab", "slug")
	sm := &fakeSecretsManager{
		describeByID: map[string]*secretsmanager.DescribeSecretOutput{
			oldArn: {ARN: aws.String(oldArn), Tags: nil},
		},
		describeErrByID: map[string]error{
			canonicalName: &smtypes.ResourceNotFoundException{},
		},
		createOut: &secretsmanager.CreateSecretOutput{ARN: aws.String(newArn)},
	}

	srv := &Server{
		cfg:   config.Config{Stage: "lab"},
		store: store.New(db),
		smFactory: func(context.Context, string, string, string, string, string) (secretsManagerAPI, error) {
			return sm, nil
		},
	}
	job := &models.ProvisionJob{ID: "job1", InstanceSlug: "slug", AccountID: "123456789012", AccountRoleName: "role", Region: "us-east-1"}
	inst := &models.Instance{Slug: "slug", LesserHostInstanceKeySecretARN: oldArn}

	arn, err := srv.ensureManagedInstanceKeySecret(context.Background(), job, inst)
	require.NoError(t, err)
	require.Equal(t, newArn, arn)
	require.Equal(t, []string{oldArn, canonicalName}, sm.describeInputs)
	require.Len(t, sm.createInputs, 1)
	require.Equal(t, canonicalName, aws.ToString(sm.createInputs[0].Name))
	require.Equal(t, "slug", secretsManagerTagValue(sm.createInputs[0].Tags, managedInstanceKeySecretTagInstanceSlug))
	require.Equal(t, "lab", secretsManagerTagValue(sm.createInputs[0].Tags, managedInstanceKeySecretTagStage))
}

func TestEnsureManagedInstanceKeySecret_LiveRefusesLegacyUntaggedARN(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{"live", "prod", "production"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()

			oldArn := "arn:aws:secretsmanager:us-east-1:123456789012:secret:legacy/instance-key"
			sm := &fakeSecretsManager{
				describeByID: map[string]*secretsmanager.DescribeSecretOutput{
					oldArn: {ARN: aws.String(oldArn), Tags: nil},
				},
			}

			srv := &Server{
				cfg:   config.Config{Stage: stage},
				store: store.New(ttmocks.NewMockExtendedDB()),
				smFactory: func(context.Context, string, string, string, string, string) (secretsManagerAPI, error) {
					return sm, nil
				},
			}
			job := &models.ProvisionJob{ID: "job1", InstanceSlug: "slug", AccountID: "123456789012", AccountRoleName: "role", Region: "us-east-1"}
			inst := &models.Instance{Slug: "slug", LesserHostInstanceKeySecretARN: oldArn}

			_, err := srv.ensureManagedInstanceKeySecret(context.Background(), job, inst)
			require.ErrorContains(t, err, "refusing unmanaged or cross-stage instance key secret")
			require.Equal(t, []string{oldArn}, sm.describeInputs)
			require.Empty(t, sm.createInputs)
		})
	}
}

func TestCreateManagedInstanceKeySecret_ValidatesAndPropagatesErrors(t *testing.T) {
	t.Parallel()

	srv := &Server{}

	_, _, err := srv.createManagedInstanceKeySecret(context.Background(), nil, "name", "slug", "lab")
	require.Error(t, err)

	_, _, err = srv.createManagedInstanceKeySecret(context.Background(), &fakeSecretsManager{}, " ", "slug", "lab")
	require.Error(t, err)

	_, _, err = srv.createManagedInstanceKeySecret(context.Background(), &fakeSecretsManager{}, "name", " ", "lab")
	require.Error(t, err)

	sm := &fakeSecretsManager{createErr: &smtypes.ResourceExistsException{}}
	_, _, err = srv.createManagedInstanceKeySecret(context.Background(), sm, "name", "slug", "lab")
	require.Error(t, err)
}

func TestManagedInstanceSecretsInputsFromJob_Validates(t *testing.T) {
	t.Parallel()

	_, err := managedInstanceSecretsInputsFromJob(nil)
	require.Error(t, err)

	_, err = managedInstanceSecretsInputsFromJob(&models.ProvisionJob{ID: "j"})
	require.Error(t, err)
}

func TestUpdateManagedInstanceKeySecretTags_NoopsForMissingInputs(t *testing.T) {
	t.Parallel()

	updateManagedInstanceKeySecretTags(context.Background(), nil, "arn", "slug", "kid", "lab")
	updateManagedInstanceKeySecretTags(context.Background(), &fakeSecretsManager{}, " ", "slug", "kid", "lab")
	updateManagedInstanceKeySecretTags(context.Background(), &fakeSecretsManager{}, "arn", " ", "kid", "lab")
	updateManagedInstanceKeySecretTags(context.Background(), &fakeSecretsManager{}, "arn", "slug", " ", "lab")
}

func TestGenerateInstanceKeySecret_ReturnsWrappedJSON(t *testing.T) {
	t.Parallel()

	plaintext, keyID, secretJSON, err := generateInstanceKeySecret()
	require.NoError(t, err)
	require.NotEmpty(t, plaintext)
	require.NotEmpty(t, keyID)
	require.True(t, strings.HasPrefix(secretJSON, "{"))
}

func TestUpdateReceiptS3Key_EmptyJobIsEmpty(t *testing.T) {
	t.Parallel()

	s := &Server{}
	require.Equal(t, "", s.updateReceiptS3Key(nil))
}

func TestUpdateVerifyDomain_PrefixesNonLiveStages(t *testing.T) {
	t.Parallel()

	require.Equal(t, "dev.example.com", updateVerifyDomain("example.com", "lab"))
	require.Equal(t, "example.com", updateVerifyDomain("example.com", "live"))
}

func TestUpdateVerifyInstanceUpdate_DoesNotPanicOnNilJob(t *testing.T) {
	t.Parallel()

	fn := updateVerifyInstanceUpdate(nil)
	require.NotNil(t, fn)
	tx := new(ttmocks.MockTransactionBuilder)
	tx.UpdateWithBuilder(&models.Instance{}, fn)
	require.NoError(t, tx.Execute())
}

func TestUpdateVerifyInstanceUpdate_DoesNotWriteLesserVersionForBodyOnly(t *testing.T) {
	t.Parallel()

	fn := updateVerifyInstanceUpdate(&models.UpdateJob{BodyOnly: true, LesserVersion: "v9.9.9"})
	tx := new(ttmocks.MockTransactionBuilder)
	tx.UpdateWithBuilder(&models.Instance{}, fn)
	require.NoError(t, tx.Execute())
	tx.AssertNotCalled(t, "Set", "LesserVersion", "v9.9.9")
}

func TestUpdateInstanceConfigInstanceUpdate_SetsOptionalURLs(t *testing.T) {
	t.Parallel()

	fn := updateInstanceConfigInstanceUpdate(" https://x ", " https://y ", " arn ", &models.UpdateJob{})
	require.NotNil(t, fn)
	tx := new(ttmocks.MockTransactionBuilder)
	tx.UpdateWithBuilder(&models.Instance{}, fn)
	require.NoError(t, tx.Execute())
}

func TestRequeueUpdateJob_UsesSharedRequeueHelper(t *testing.T) {
	t.Parallel()

	srv := &Server{cfg: config.Config{ProvisionQueueURL: "url"}, sqs: &fakeSQS{}}
	require.NoError(t, srv.requeueUpdateJob(context.Background(), "job", -10*time.Second))
}

func TestProcessUpdateJob_DropsNonProcessable(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qJob := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qJob).Maybe()
	qJob.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qJob).Maybe()
	qJob.On("ConsistentRead").Return(qJob).Maybe()

	qJob.On("First", mock.AnythingOfType("*models.UpdateJob")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.UpdateJob](t, args, 0)
		*dest = models.UpdateJob{ID: "j1", InstanceSlug: "slug", Status: models.UpdateJobStatusOK}
	}).Once()

	srv := &Server{cfg: config.Config{ManagedProvisioningEnabled: true}, store: store.New(db)}
	require.NoError(t, srv.processUpdateJob(context.Background(), "req", "j1"))
}

func TestResolveManagedUpdateMetadata_Validation(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{ManagedInstanceRoleName: "role"}}
	_, err := s.resolveManagedUpdateMetadata(&models.UpdateJob{}, &models.Instance{})
	require.Error(t, err)
}

func TestResolveUpdateDeployRunnerInputs_EnforcesTenantBoundary(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{Stage: "lab", ManagedInstanceRoleName: "role"}}
	job := &models.UpdateJob{
		ID:                             "u1",
		InstanceSlug:                   "slug",
		AccountID:                      "123456789012",
		AccountRoleName:                "role",
		Region:                         "us-east-1",
		BaseDomain:                     "slug.example.com",
		LesserVersion:                  "v1.2.3",
		LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
	}
	inst := &models.Instance{
		Slug:             "slug",
		Owner:            "wallet-abc",
		HostedAccountID:  "210987654321",
		HostedRegion:     "us-east-1",
		HostedBaseDomain: "slug.example.com",
	}

	_, err := s.resolveUpdateDeployRunnerInputs(job, inst)
	require.ErrorContains(t, err, "target account does not match")

	inst.HostedAccountID = job.AccountID
	job.AccountRoleName = "other-role"
	_, err = s.resolveUpdateDeployRunnerInputs(job, inst)
	require.ErrorContains(t, err, "target role does not match")
}

func TestResolveUpdateDeployRunnerInputs_ErrorsForNonWalletOwner(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{Stage: "lab", ManagedInstanceRoleName: "r"}}
	_, err := s.resolveUpdateDeployRunnerInputs(
		&models.UpdateJob{InstanceSlug: "slug", AccountID: "123456789012", AccountRoleName: "r", Region: "us", BaseDomain: "d", LesserVersion: "v", LesserHostInstanceKeySecretARN: "arn"},
		&models.Instance{Slug: "slug", Owner: "alice", HostedAccountID: "123456789012", HostedRegion: "us", HostedBaseDomain: "d"},
	)
	require.Error(t, err)
}

func updateStartBuildEnvValue(in *codebuild.StartBuildInput, name string) string {
	if in == nil {
		return ""
	}
	for _, env := range in.EnvironmentVariablesOverride {
		if strings.TrimSpace(aws.ToString(env.Name)) == name {
			return strings.TrimSpace(aws.ToString(env.Value))
		}
	}
	return ""
}

func TestManagedInstanceKeyReceiptValidationBindsUpdateJob(t *testing.T) {
	t.Parallel()

	const keyID = "38ed91d202121369e6ad8f501c2839590ba5427b51cf16422f446d99f031601b"
	baseJob := &models.UpdateJob{
		ID:           "job1",
		InstanceSlug: "theory",
		AccountID:    "922120356241",
		Region:       "us-east-1",
	}
	baseReceipt := managedInstanceKeyReceipt{
		Version:      1,
		Source:       managedInstanceKeyReceiptSourceDeployRunner,
		SecretARN:    "arn:aws:secretsmanager:us-east-1:922120356241:secret:theory/instance-key",
		KeyID:        keyID,
		InstanceSlug: "theory",
		Stage:        "live",
		VerifiedAt:   "2026-06-27T00:00:00Z",
	}

	require.NoError(t, (&Server{cfg: config.Config{Stage: "live"}}).validateManagedInstanceKeyReceipt(baseJob, &baseReceipt))

	tests := []struct {
		name    string
		server  *Server
		job     *models.UpdateJob
		receipt *managedInstanceKeyReceipt
		want    string
	}{
		{
			name:    "nil server",
			server:  nil,
			job:     baseJob,
			receipt: &baseReceipt,
			want:    "server is nil",
		},
		{
			name:    "nil job",
			server:  &Server{cfg: config.Config{Stage: "live"}},
			job:     nil,
			receipt: &baseReceipt,
			want:    "job is nil",
		},
		{
			name:    "missing proof",
			server:  &Server{cfg: config.Config{Stage: "live"}},
			job:     baseJob,
			receipt: nil,
			want:    "managed instance key proof missing",
		},
		{
			name:   "unsupported version",
			server: &Server{cfg: config.Config{Stage: "live"}},
			job:    baseJob,
			receipt: func() *managedInstanceKeyReceipt {
				r := baseReceipt
				r.Version = 2
				return &r
			}(),
			want: "unsupported managed instance key receipt version",
		},
		{
			name:   "invalid source",
			server: &Server{cfg: config.Config{Stage: "live"}},
			job:    baseJob,
			receipt: func() *managedInstanceKeyReceipt {
				r := baseReceipt
				r.Source = "provision-worker"
				return &r
			}(),
			want: "managed instance key receipt source is invalid",
		},
		{
			name:   "slug mismatch",
			server: &Server{cfg: config.Config{Stage: "live"}},
			job:    baseJob,
			receipt: func() *managedInstanceKeyReceipt {
				r := baseReceipt
				r.InstanceSlug = "other"
				return &r
			}(),
			want: "managed instance key receipt slug does not match",
		},
		{
			name:   "missing stage",
			server: &Server{cfg: config.Config{Stage: "live"}},
			job:    baseJob,
			receipt: func() *managedInstanceKeyReceipt {
				r := baseReceipt
				r.Stage = ""
				return &r
			}(),
			want: "managed instance key receipt stage is missing",
		},
		{
			name:   "stage mismatch",
			server: &Server{cfg: config.Config{Stage: "lab"}},
			job:    baseJob,
			receipt: func() *managedInstanceKeyReceipt {
				r := baseReceipt
				r.Stage = "live"
				return &r
			}(),
			want: "managed instance key receipt stage does not match",
		},
		{
			name:   "invalid arn",
			server: &Server{cfg: config.Config{Stage: "live"}},
			job:    baseJob,
			receipt: func() *managedInstanceKeyReceipt {
				r := baseReceipt
				r.SecretARN = "theory/instance-key"
				return &r
			}(),
			want: "managed instance key receipt secret ARN is invalid",
		},
		{
			name:   "account mismatch",
			server: &Server{cfg: config.Config{Stage: "live"}},
			job:    baseJob,
			receipt: func() *managedInstanceKeyReceipt {
				r := baseReceipt
				r.SecretARN = "arn:aws:secretsmanager:us-east-1:000000000000:secret:theory/instance-key"
				return &r
			}(),
			want: "managed instance key receipt secret ARN account does not match",
		},
		{
			name:   "region mismatch",
			server: &Server{cfg: config.Config{Stage: "live"}},
			job:    baseJob,
			receipt: func() *managedInstanceKeyReceipt {
				r := baseReceipt
				r.SecretARN = "arn:aws:secretsmanager:us-west-2:922120356241:secret:theory/instance-key"
				return &r
			}(),
			want: "managed instance key receipt secret ARN region does not match",
		},
		{
			name:   "invalid key id",
			server: &Server{cfg: config.Config{Stage: "live"}},
			job:    baseJob,
			receipt: func() *managedInstanceKeyReceipt {
				r := baseReceipt
				r.KeyID = strings.Repeat("z", 64)
				return &r
			}(),
			want: "managed instance key receipt has invalid key id",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.server.validateManagedInstanceKeyReceipt(tc.job, tc.receipt)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestManagedInstanceKeyReceiptJSONParsing(t *testing.T) {
	t.Parallel()

	receipt, err := managedInstanceKeyReceiptFromJSON("   ")
	require.NoError(t, err)
	require.Nil(t, receipt)

	receipt, err = managedInstanceKeyReceiptFromJSON(`{"managed_instance_key":{"version":1,"source":"deploy-runner-managed-profile"}}`)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	require.Equal(t, 1, receipt.Version)
	require.Equal(t, managedInstanceKeyReceiptSourceDeployRunner, receipt.Source)

	_, err = managedInstanceKeyReceiptFromJSON(`{"managed_instance_key":`)
	require.Error(t, err)
}

func TestStartUpdateDeployRunnerWithMode_DoesNotPreflightTargetAccountAccess(t *testing.T) {
	t.Parallel()

	cb := &fakeCodebuild{startOut: &codebuild.StartBuildOutput{Build: &cbtypes.Build{Id: aws.String("run1")}}}
	stsClient := &fakeSTS{err: errors.New("AccessDenied")}
	srv := &Server{
		cfg: config.Config{
			Stage:                             "live",
			ManagedInstanceRoleName:           "OrganizationAccountAccessRole",
			ManagedProvisionRunnerProjectName: "runner-project",
			ManagedProvisionRunnerRoleARN:     "arn:aws:iam::693925625407:role/runner",
			ArtifactBucketName:                "artifact-bucket",
			ManagedLesserGitHubOwner:          "equaltoai",
			ManagedLesserGitHubRepo:           "lesser",
		},
		cb:  cb,
		sts: stsClient,
		iamFactory: func(context.Context, string, string, string, string, string) (iamAPI, error) {
			t.Fatalf("startUpdateDeployRunnerWithMode must not preflight target-account IAM")
			return nil, errors.New("unexpected target IAM preflight")
		},
	}
	job := &models.UpdateJob{
		ID:                             "job1",
		InstanceSlug:                   "theory",
		AccountID:                      "922120356241",
		AccountRoleName:                "OrganizationAccountAccessRole",
		Region:                         "us-east-1",
		BaseDomain:                     "theory.greater.website",
		LesserVersion:                  "v1.2.3",
		LesserHostBaseURL:              "https://lesser.host",
		LesserHostAttestationsURL:      "https://lesser.host",
		LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:922120356241:secret:theory/instance-key",
		RotateInstanceKey:              true,
	}
	inst := &models.Instance{
		Slug:             "theory",
		Owner:            "wallet-deadbeef",
		HostedAccountID:  "922120356241",
		HostedRegion:     "us-east-1",
		HostedBaseDomain: "theory.greater.website",
	}

	runID, err := srv.startUpdateDeployRunnerWithMode(context.Background(), job, inst, deployRunnerModeLesser, "")
	require.NoError(t, err)
	require.Equal(t, "run1", runID)
	require.Empty(t, stsClient.lastArn)
	require.Len(t, cb.startInputs, 1)
	require.Equal(t, "lesser-host/deploy/theory", updateStartBuildEnvValue(cb.startInputs[0], "DEPLOY_EXTERNAL_ID"))
	require.Equal(t, job.LesserHostInstanceKeySecretARN, updateStartBuildEnvValue(cb.startInputs[0], "LESSER_HOST_INSTANCE_KEY_SECRET_ID"))
	require.Equal(t, "true", updateStartBuildEnvValue(cb.startInputs[0], "LESSER_HOST_INSTANCE_KEY_ROTATE"))
}

func TestApplyManagedInstanceKeyReceiptPersistsHostRecordWithoutTargetRead(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	qKey.On("IfNotExists").Return(qKey).Maybe()
	qKey.On("Create").Return(nil).Once()

	stsClient := &fakeSTS{err: errors.New("AccessDenied")}
	srv := &Server{cfg: config.Config{Stage: "live"}, store: store.New(db), sts: stsClient}
	job := &models.UpdateJob{ID: "job1", InstanceSlug: "theory", RotateInstanceKey: true}
	receiptJSON := `{"managed_instance_key":{"version":1,"source":"deploy-runner-managed-profile","secret_arn":"arn:aws:secretsmanager:us-east-1:922120356241:secret:theory/instance-key","key_id":"38ed91d202121369e6ad8f501c2839590ba5427b51cf16422f446d99f031601b","instance_slug":"theory","stage":"live","rotated":true,"verified_at":"2026-06-27T00:00:00Z"}}`

	require.NoError(t, srv.applyManagedInstanceKeyReceiptJSON(context.Background(), job, receiptJSON))
	require.Equal(t, "arn:aws:secretsmanager:us-east-1:922120356241:secret:theory/instance-key", job.LesserHostInstanceKeySecretARN)
	require.Equal(t, "38ed91d202121369e6ad8f501c2839590ba5427b51cf16422f446d99f031601b", job.RotatedInstanceKeyID)
	require.Empty(t, stsClient.lastArn)
}

func TestVerifyUpdateUsesReceiptProofWhenDirectAssumeDenied(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qKey := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.InstanceKey")).Return(qKey).Maybe()
	qKey.On("IfNotExists").Return(qKey).Maybe()
	qKey.On("Create").Return(nil).Maybe()

	stsClient := &fakeSTS{err: errors.New("AccessDenied")}
	srv := &Server{cfg: config.Config{Stage: "live"}, store: store.New(db), sts: stsClient}
	job := &models.UpdateJob{
		ID:           "job1",
		InstanceSlug: "theory",
		AIEnabled:    true,
		ReceiptJSON:  `{"managed_instance_key":{"version":1,"source":"deploy-runner-managed-profile","secret_arn":"arn:aws:secretsmanager:us-east-1:922120356241:secret:theory/instance-key","key_id":"38ed91d202121369e6ad8f501c2839590ba5427b51cf16422f446d99f031601b","instance_slug":"theory","stage":"live","verified_at":"2026-06-27T00:00:00Z"}}`,
	}

	trustOK, trustErr := srv.verifyUpdateTrustAuth(context.Background(), nil, job)
	require.True(t, trustOK, trustErr)
	require.Empty(t, trustErr)
	aiOK, aiErr := srv.verifyUpdateAI(context.Background(), nil, job)
	require.True(t, aiOK, aiErr)
	require.Empty(t, aiErr)
	require.Equal(t, "arn:aws:secretsmanager:us-east-1:922120356241:secret:theory/instance-key", job.LesserHostInstanceKeySecretARN)
	require.Empty(t, stsClient.lastArn)
}

func TestAdvanceUpdateReceiptIngest_RequiresS3Client(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	srv := &Server{cfg: config.Config{ArtifactBucketName: "bucket"}, store: store.New(db), s3: nil}
	job := &models.UpdateJob{ID: "j1", InstanceSlug: "slug", Status: models.UpdateJobStatusRunning, Step: updateStepReceiptIngest, MaxAttempts: 1}

	delay, done, err := srv.advanceUpdateReceiptIngest(context.Background(), job, "req", time.Unix(1, 0).UTC())
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, time.Duration(0), delay)
	require.Equal(t, "receipt_load_failed", job.ErrorCode)
}

func TestUpdateBootstrapS3Key_Validation(t *testing.T) {
	t.Parallel()

	s := &Server{}
	require.Equal(t, "", s.updateBootstrapS3Key(" "))
}

func TestUpdateInstanceConfigInstanceUpdate_SetsOnlyProvidedURLs(t *testing.T) {
	t.Parallel()

	job := &models.UpdateJob{TranslationEnabled: true}
	fn := updateInstanceConfigInstanceUpdate("", "", "", job)
	require.NotNil(t, fn)
	tx := new(ttmocks.MockTransactionBuilder)
	tx.UpdateWithBuilder(&models.Instance{}, fn)
	require.NoError(t, tx.Execute())
}

func TestAdvanceUpdateQueued_NilJobDone(t *testing.T) {
	t.Parallel()

	delay, done, err := (&Server{}).advanceUpdateQueued(context.Background(), nil, "req", time.Unix(1, 0).UTC())
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, time.Duration(0), delay)
}

func TestRetryUpdateJobOrFail_StopsAtMaxAttempts(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	srv := &Server{store: store.New(db)}

	job := &models.UpdateJob{ID: "j1", InstanceSlug: "slug", Status: models.UpdateJobStatusRunning, MaxAttempts: 1}
	_, _, err := srv.retryUpdateJobOrFail(context.Background(), job, "req", time.Unix(1, 0).UTC(), "c", "m", time.Second, time.Minute)
	require.NoError(t, err)
	require.Equal(t, models.UpdateJobStatusError, job.Status)
	require.Equal(t, "c", job.ErrorCode)
}

func TestAdvanceUpdateDeployStart_WhenSecretARNMissingResetsToInstanceConfig(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "slug", LesserHostInstanceKeySecretARN: " "}
	}).Once()

	srv := &Server{store: store.New(db)}
	job := &models.UpdateJob{ID: "j1", InstanceSlug: "slug", Status: models.UpdateJobStatusRunning, Step: updateStepDeployStart}
	delay, done, err := srv.advanceUpdateDeployStart(context.Background(), job, "req", time.Unix(1, 0).UTC())
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, time.Duration(0), delay)
	require.Equal(t, updateStepInstanceConfig, job.Step)
}

func TestAdvanceUpdateReceiptIngest_SuccessMovesToVerify(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "slug"}
	}).Once()
	st := store.New(db)

	s3Client := &fakeS3{out: &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(`{"app":"x","base_domain":"d"}`))}}
	srv := &Server{cfg: config.Config{ArtifactBucketName: "bucket"}, store: st, s3: s3Client}

	job := &models.UpdateJob{ID: "j1", InstanceSlug: "slug", Status: models.UpdateJobStatusRunning, Step: updateStepReceiptIngest, MaxAttempts: 2}
	delay, done, err := srv.advanceUpdateReceiptIngest(context.Background(), job, "req", time.Unix(1, 0).UTC())
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, time.Duration(0), delay)
	require.Equal(t, updateStepVerify, job.Step)
	require.NotEmpty(t, job.ReceiptJSON)
}

func TestAdvanceUpdateInstanceConfig_RetriesOnInstanceLoadError(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(errors.New("boom")).Once()

	srv := &Server{store: store.New(db)}
	job := &models.UpdateJob{ID: "j1", InstanceSlug: "slug", Status: models.UpdateJobStatusRunning, Step: updateStepInstanceConfig, MaxAttempts: 3}

	delay, done, err := srv.advanceUpdateInstanceConfig(context.Background(), job, "req", time.Unix(1, 0).UTC())
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, provisionDefaultShortRetryDelay, delay)
	require.Equal(t, int64(1), job.Attempts)
}

func TestAdvanceUpdateInstanceConfig_FailsWhenInstanceMetadataMissing(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "slug", HostedRegion: "us-east-1", HostedBaseDomain: "example.com", HostedAccountID: ""}
	}).Once()

	srv := &Server{cfg: config.Config{ManagedInstanceRoleName: "role"}, store: store.New(db)}
	job := &models.UpdateJob{ID: "j1", InstanceSlug: "slug", Status: models.UpdateJobStatusRunning, Step: updateStepInstanceConfig, MaxAttempts: 3}

	delay, done, err := srv.advanceUpdateInstanceConfig(context.Background(), job, "req", time.Unix(1, 0).UTC())
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, time.Duration(0), delay)
	require.Equal(t, models.UpdateJobStatusError, job.Status)
	require.Equal(t, "missing_instance_metadata", job.ErrorCode)
}

func TestAdvanceUpdateInstanceConfig_DoesNotAssumeTargetRoleBeforeRunner(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:             "slug",
			Owner:            "wallet-deadbeef",
			HostedAccountID:  "123",
			HostedRegion:     "us-east-1",
			HostedBaseDomain: "example.com",
		}
	}).Once()

	now := time.Unix(1000, 0).UTC()
	stsClient := &fakeSTS{err: errors.New("NoSuchEntity: role has not propagated")}
	srv := &Server{
		cfg:   config.Config{ManagedInstanceRoleName: "role", Stage: "lab"},
		store: store.New(db),
		sts:   stsClient,
	}
	job := &models.UpdateJob{
		ID:           "j1",
		InstanceSlug: "slug",
		Status:       models.UpdateJobStatusRunning,
		Step:         updateStepInstanceConfig,
		MaxAttempts:  1,
		CreatedAt:    now.Add(-time.Minute),
	}

	delay, done, err := srv.advanceUpdateInstanceConfig(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, time.Duration(0), delay)
	require.Equal(t, models.UpdateJobStatusRunning, job.Status)
	require.Equal(t, updateStepDeployStart, job.Step)
	require.Equal(t, "starting update deploy runner", job.Note)
	require.Equal(t, "lab/slug/instance-key", job.LesserHostInstanceKeySecretARN)
	require.Empty(t, job.ErrorCode)
	require.Empty(t, job.ErrorMessage)
	require.Equal(t, int64(0), job.Attempts)
	require.Empty(t, stsClient.lastArn)
}

func TestAdvanceUpdateInstanceConfig_ExistingSecretARNProceedsOnDirectAssumeAccessDenied(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:                           "theory",
			Owner:                          "wallet-deadbeef",
			Status:                         models.InstanceStatusActive,
			HostedAccountID:                "922120356241",
			HostedRegion:                   "us-east-1",
			HostedBaseDomain:               "theory.greater.website",
			LesserHostInstanceKeySecretARN: "arn:aws:secretsmanager:us-east-1:922120356241:secret:theory/instance-key",
			CreatedAt:                      time.Unix(100, 0).UTC(),
		}
	}).Once()

	callerARN := "arn:aws:sts::693925625407:assumed-role/lesser-host-live-ProvisionWorkerServiceRole89A343D2-sqYFBuJR7NFJ/lesser-host-live-provision-worker"
	targetARN := "arn:aws:iam::922120356241:role/OrganizationAccountAccessRole"
	accessDenied := errors.New("AccessDenied: User: " + callerARN + " is not authorized to perform: sts:AssumeRole on resource: " + targetARN)
	now := time.Unix(2000, 0).UTC()
	stsClient := &fakeSTS{err: accessDenied}
	srv := &Server{
		cfg:   config.Config{ManagedInstanceRoleName: "OrganizationAccountAccessRole", Stage: "live"},
		store: store.New(db),
		sts:   stsClient,
	}
	job := &models.UpdateJob{
		ID:              "CqBkpMZWBBYIiEOeqLeY-Q",
		InstanceSlug:    "theory",
		Status:          models.UpdateJobStatusRunning,
		Step:            updateStepInstanceConfig,
		AccountRoleName: "OrganizationAccountAccessRole",
		MaxAttempts:     10,
		CreatedAt:       now.Add(-time.Minute),
	}

	delay, done, err := srv.advanceUpdateInstanceConfig(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, time.Duration(0), delay)
	require.Equal(t, models.UpdateJobStatusRunning, job.Status)
	require.Equal(t, updateStepDeployStart, job.Step)
	require.Equal(t, "starting update deploy runner", job.Note)
	require.Equal(t, "arn:aws:secretsmanager:us-east-1:922120356241:secret:theory/instance-key", job.LesserHostInstanceKeySecretARN)
	require.Empty(t, job.ErrorCode)
	require.Empty(t, job.ErrorMessage)
	require.Equal(t, int64(0), job.Attempts)
	require.Empty(t, stsClient.lastArn)
	for _, forbidden := range []string{"SecretAccessKey", "SessionToken", "AWS_SECRET_ACCESS_KEY"} {
		require.NotContains(t, job.ErrorMessage, forbidden)
	}
}

func TestAdvanceUpdateInstanceConfig_UsesRunnerSecretEnsurePastOldReadinessDeadline(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:             "slug",
			Owner:            "wallet-deadbeef",
			HostedAccountID:  "123",
			HostedRegion:     "us-east-1",
			HostedBaseDomain: "example.com",
		}
	}).Once()

	now := time.Unix(1000, 0).UTC()
	stsClient := &fakeSTS{err: errors.New("NoSuchEntity: role has not propagated")}
	srv := &Server{
		cfg:   config.Config{ManagedInstanceRoleName: "role", Stage: "lab"},
		store: store.New(db),
		sts:   stsClient,
	}
	job := &models.UpdateJob{
		ID:           "j1",
		InstanceSlug: "slug",
		Status:       models.UpdateJobStatusRunning,
		Step:         updateStepInstanceConfig,
		MaxAttempts:  10,
		CreatedAt:    now.Add(-(provisionMaxAssumeRoleAge + time.Second)),
	}

	delay, done, err := srv.advanceUpdateInstanceConfig(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, time.Duration(0), delay)
	require.Equal(t, models.UpdateJobStatusRunning, job.Status)
	require.Equal(t, updateStepDeployStart, job.Step)
	require.Equal(t, "lab/slug/instance-key", job.LesserHostInstanceKeySecretARN)
	require.Empty(t, job.ErrorCode)
	require.Empty(t, job.ErrorMessage)
	require.Equal(t, int64(0), job.Attempts)
	require.Empty(t, stsClient.lastArn)
}

func TestAdvanceUpdateInstanceConfig_RotationIsDeferredToDeployRunner(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	qInst.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qInst).Maybe()
	qInst.On("ConsistentRead").Return(qInst).Maybe()
	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{
			Slug:             "slug",
			Owner:            "wallet-deadbeef",
			HostedAccountID:  "123",
			HostedRegion:     "us-east-1",
			HostedBaseDomain: "example.com",
		}
	}).Once()

	// No secrets manager factory and no STS client: instance-key ensure/rotation is deferred to the deploy runner.
	srv := &Server{cfg: config.Config{ManagedInstanceRoleName: "role", Stage: "lab"}, store: store.New(db)}
	job := &models.UpdateJob{ID: "j1", InstanceSlug: "slug", Status: models.UpdateJobStatusRunning, Step: updateStepInstanceConfig, MaxAttempts: 3, RotateInstanceKey: true}

	delay, done, err := srv.advanceUpdateInstanceConfig(context.Background(), job, "req", time.Unix(1, 0).UTC())
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, time.Duration(0), delay)
	require.Equal(t, updateStepDeployStart, job.Step)
	require.Equal(t, "lab/slug/instance-key", job.LesserHostInstanceKeySecretARN)
	require.Empty(t, job.RotatedInstanceKeyID)
	require.Equal(t, int64(0), job.Attempts)
}
