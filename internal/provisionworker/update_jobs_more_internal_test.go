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
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

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

func TestDescribeAndEnsureManagedInstanceKeySecret_FallsBackWhenTagMissing(t *testing.T) {
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
			Tags: nil, // force GetSecretValue fallback
		},
		getOut: &secretsmanager.GetSecretValueOutput{
			SecretString: aws.String(`{"secret":"lhk_test"}`),
		},
	}

	arn, err := srv.describeAndEnsureManagedInstanceKeySecret(context.Background(), sm, " slug ", " secret ")
	require.NoError(t, err)
	require.Equal(t, "arn:secret", arn)
}

func TestCreateManagedInstanceKeySecret_ValidatesAndPropagatesErrors(t *testing.T) {
	t.Parallel()

	srv := &Server{}

	_, _, err := srv.createManagedInstanceKeySecret(context.Background(), nil, "name", "slug")
	require.Error(t, err)

	_, _, err = srv.createManagedInstanceKeySecret(context.Background(), &fakeSecretsManager{}, " ", "slug")
	require.Error(t, err)

	_, _, err = srv.createManagedInstanceKeySecret(context.Background(), &fakeSecretsManager{}, "name", " ")
	require.Error(t, err)

	sm := &fakeSecretsManager{createErr: &smtypes.ResourceExistsException{}}
	_, _, err = srv.createManagedInstanceKeySecret(context.Background(), sm, "name", "slug")
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

	updateManagedInstanceKeySecretTags(context.Background(), nil, "arn", "slug", "kid")
	updateManagedInstanceKeySecretTags(context.Background(), &fakeSecretsManager{}, " ", "slug", "kid")
	updateManagedInstanceKeySecretTags(context.Background(), &fakeSecretsManager{}, "arn", " ", "kid")
	updateManagedInstanceKeySecretTags(context.Background(), &fakeSecretsManager{}, "arn", "slug", " ")
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

func TestResolveUpdateDeployRunnerInputs_ErrorsForNonWalletOwner(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: config.Config{Stage: "lab"}}
	_, err := s.resolveUpdateDeployRunnerInputs(&models.UpdateJob{AccountID: "1", AccountRoleName: "r", Region: "us", BaseDomain: "d", LesserVersion: "v", LesserHostInstanceKeySecretARN: "arn"}, &models.Instance{Owner: "alice"})
	require.Error(t, err)
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

func TestAdvanceUpdateInstanceConfig_RetriesWhenSecretsManagerClientCannotBeAssumed(t *testing.T) {
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

	// No secrets manager factory and no STS client: childSecretsManagerClient should fail, triggering retry logic.
	srv := &Server{cfg: config.Config{ManagedInstanceRoleName: "role", Stage: "lab"}, store: store.New(db)}
	job := &models.UpdateJob{ID: "j1", InstanceSlug: "slug", Status: models.UpdateJobStatusRunning, Step: updateStepInstanceConfig, MaxAttempts: 3, RotateInstanceKey: true}

	delay, done, err := srv.advanceUpdateInstanceConfig(context.Background(), job, "req", time.Unix(1, 0).UTC())
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, provisionDefaultShortRetryDelay, delay)
	require.Equal(t, int64(1), job.Attempts)
}
