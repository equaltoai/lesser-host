package provisionworker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/provisioning"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func managedProvisioningJob(step string) *models.ProvisionJob {
	return &models.ProvisionJob{
		ID:              "job-1",
		InstanceSlug:    "slug",
		Status:          models.ProvisionJobStatusRunning,
		Step:            step,
		MaxAttempts:     2,
		AccountID:       "123456789012",
		AccountRoleName: "lesser-host-instance",
		Region:          "us-east-1",
		BaseDomain:      "slug.example.com",
	}
}

func TestHandleProvisionQueueMessage_UpdateKindCallsProcess(t *testing.T) {
	t.Parallel()

	st, db := newBranchTestStore()
	qJob := new(ttmocks.MockQuery)
	db.On("Model", mock.AnythingOfType("*models.UpdateJob")).Return(qJob).Maybe()
	qJob.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qJob).Maybe()
	qJob.On("ConsistentRead").Return(qJob).Maybe()
	qJob.On("First", mock.AnythingOfType("*models.UpdateJob")).Return(theoryErrors.ErrItemNotFound).Once()

	srv := &Server{cfg: config.Config{ManagedProvisioningEnabled: true}, store: st}
	body, err := json.Marshal(provisioning.JobMessage{Kind: "update_job", JobID: "job-1"})
	require.NoError(t, err)

	require.NoError(t, srv.handleProvisionQueueMessage(&apptheory.EventContext{RequestID: "req"}, events.SQSMessage{Body: string(body)}))
}

func TestRunManagedProvisioningStateMachine_HelperBranches(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()

	t.Run("requires store", func(t *testing.T) {
		err := (&Server{}).runManagedProvisioningStateMachine(context.Background(), &models.ProvisionJob{}, "req", now)
		require.ErrorContains(t, err, "store not initialized")
	})

	t.Run("nil job is ignored", func(t *testing.T) {
		st, _ := newBranchTestStore()
		require.NoError(t, (&Server{store: st}).runManagedProvisioningStateMachine(context.Background(), nil, "req", now))
	})

	t.Run("expired job fails", func(t *testing.T) {
		st, _ := newBranchTestStore()
		srv := &Server{store: st}
		job := &models.ProvisionJob{
			ID:           "job-1",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusQueued,
			ExpiresAt:    now.Add(-time.Second),
		}

		require.NoError(t, srv.runManagedProvisioningStateMachine(context.Background(), job, "req", now))
		require.Equal(t, models.ProvisionJobStatusError, job.Status)
		require.Equal(t, provisionStepFailed, job.Step)
		require.Equal(t, "expired", job.ErrorCode)
	})
}

func TestInitializeManagedProvisionJob_SetsDefaultsAndPreservesValues(t *testing.T) {
	t.Parallel()

	srv := &Server{cfg: config.Config{
		ManagedDefaultRegion:      "us-west-2",
		ManagedParentHostedZoneID: "ZPARENT",
		ManagedInstanceRoleName:   "default-role",
		ManagedParentDomain:       "example.com",
		Stage:                     "LIVE",
	}}

	srv.initializeManagedProvisionJob(nil)

	job := &models.ProvisionJob{InstanceSlug: " slug "}
	srv.initializeManagedProvisionJob(job)
	require.Equal(t, provisionStepQueued, job.Step)
	require.Equal(t, "us-west-2", job.Region)
	require.Equal(t, "live", job.Stage)
	require.Equal(t, "ZPARENT", job.ParentHostedZoneID)
	require.Equal(t, "default-role", job.AccountRoleName)
	require.Equal(t, "slug.example.com", job.BaseDomain)

	job = &models.ProvisionJob{
		InstanceSlug:       "slug",
		Step:               provisionStepDeployWait,
		Region:             "eu-west-1",
		Stage:              "lab",
		ParentHostedZoneID: "ZCUSTOM",
		AccountRoleName:    "custom-role",
		BaseDomain:         "custom.example.com",
	}
	srv.initializeManagedProvisionJob(job)
	require.Equal(t, provisionStepDeployWait, job.Step)
	require.Equal(t, "eu-west-1", job.Region)
	require.Equal(t, "dev", job.Stage)
	require.Equal(t, "ZCUSTOM", job.ParentHostedZoneID)
	require.Equal(t, "custom-role", job.AccountRoleName)
	require.Equal(t, "custom.example.com", job.BaseDomain)
}

func TestManagedInstanceKeySecretName_UsesSlugPrefix(t *testing.T) {
	t.Parallel()

	require.Equal(t, "slug/instance-key", managedInstanceKeySecretName("LIVE", " Slug "))
	require.Equal(t, "slug/instance-key", managedInstanceKeySecretName("", "slug"))
	require.Equal(t, "", managedInstanceKeySecretName("live", " "))
}

func TestStartManagedProvisioningJobIfQueued_Branches(t *testing.T) {
	t.Parallel()

	st, _ := newBranchTestStore()
	srv := &Server{store: st}
	now := time.Unix(200, 0).UTC()

	t.Run("non queued job is ignored", func(t *testing.T) {
		job := &models.ProvisionJob{
			ID:           "job-1",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepDeployStart,
			Note:         "keep me",
		}

		require.NoError(t, srv.startManagedProvisioningJobIfQueued(context.Background(), job, "req", now))
		require.Equal(t, models.ProvisionJobStatusRunning, job.Status)
		require.Equal(t, provisionStepDeployStart, job.Step)
		require.Equal(t, "keep me", job.Note)
	})

	t.Run("queued job starts provisioning", func(t *testing.T) {
		job := &models.ProvisionJob{
			ID:           "job-2",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusQueued,
			BaseDomain:   "slug.example.com",
			Region:       "us-east-1",
		}

		require.NoError(t, srv.startManagedProvisioningJobIfQueued(context.Background(), job, "req", now))
		require.Equal(t, models.ProvisionJobStatusRunning, job.Status)
		require.Equal(t, provisionStepQueued, job.Step)
		require.Equal(t, "starting provisioning", job.Note)
	})
}

func TestAdvanceManagedProvisioningLoop_Branches(t *testing.T) {
	t.Parallel()

	now := time.Unix(300, 0).UTC()

	t.Run("nil server is ignored", func(t *testing.T) {
		var srv *Server
		require.NoError(t, srv.advanceManagedProvisioningLoop(context.Background(), nil, "req", now))
	})

	t.Run("done step exits without requeue", func(t *testing.T) {
		st, _ := newBranchTestStore()
		sqsClient := &fakeSQS{}
		srv := &Server{
			cfg:   config.Config{ProvisionQueueURL: "https://example.com/queue"},
			store: st,
			sqs:   sqsClient,
		}
		job := &models.ProvisionJob{ID: "job-1", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepDone}

		require.NoError(t, srv.advanceManagedProvisioningLoop(context.Background(), job, "req", now))
		require.Empty(t, sqsClient.inputs)
	})

	t.Run("delay requeues job", func(t *testing.T) {
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
		job := managedProvisioningJob(provisionStepDeployWait)
		job.RunID = branchTestRunID
		job.CreatedAt = now

		require.NoError(t, srv.advanceManagedProvisioningLoop(context.Background(), job, "req", now))
		require.Len(t, sqsClient.inputs, 1)
		require.EqualValues(t, provisionDefaultPollDelay/time.Second, sqsClient.inputs[0].DelaySeconds)
	})
}

func TestLoadProvisionJob_BlankAndNotFound(t *testing.T) {
	t.Parallel()

	st, db := newBranchTestStore()
	srv := &Server{store: st}

	got, err := srv.loadProvisionJob(context.Background(), " ")
	require.NoError(t, err)
	require.Nil(t, got)

	qJob := new(ttmocks.MockQuery)
	db.On("Model", mock.AnythingOfType("*models.ProvisionJob")).Return(qJob).Maybe()
	qJob.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qJob).Maybe()
	qJob.On("ConsistentRead").Return(qJob).Maybe()
	qJob.On("First", mock.AnythingOfType("*models.ProvisionJob")).Return(theoryErrors.ErrItemNotFound).Once()

	got, err = srv.loadProvisionJob(context.Background(), "job-1")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestLoadInstance_BlankAndNotFound(t *testing.T) {
	t.Parallel()

	st, db := newBranchTestStore()
	srv := &Server{store: st}

	_, err := srv.loadInstance(context.Background(), " ")
	require.ErrorContains(t, err, "instance slug is required")

	mockBranchInstanceLookup(t, db, nil, theoryErrors.ErrItemNotFound)
	got, err := srv.loadInstance(context.Background(), "slug")
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestAdvanceProvisionChildZone_Branches(t *testing.T) {
	t.Parallel()

	now := time.Unix(400, 0).UTC()

	t.Run("missing account rewinds to allocation", func(t *testing.T) {
		st, _ := newBranchTestStore()
		srv := &Server{store: st}
		job := &models.ProvisionJob{
			ID:           "job-1",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepChildZone,
		}

		delay, done, err := srv.advanceProvisionChildZone(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, provisionStepAccountCreate, job.Step)
		require.Equal(t, noteMissingAccountIDRestart, job.Note)
	})

	t.Run("child zone failure retries then fails", func(t *testing.T) {
		st, _ := newBranchTestStore()
		srv := &Server{store: st}
		job := managedProvisioningJob(provisionStepChildZone)

		delay, done, err := srv.advanceProvisionChildZone(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, provisionDefaultShortRetryDelay, delay)
		require.Equal(t, int64(1), job.Attempts)
		require.Contains(t, job.Note, "failed to ensure child hosted zone; retrying")

		delay, done, err = srv.advanceProvisionChildZone(context.Background(), job, "req", now.Add(time.Second))
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, models.ProvisionJobStatusError, job.Status)
		require.Equal(t, provisionStepFailed, job.Step)
		require.Equal(t, "child_zone_failed", job.ErrorCode)
	})
}

func TestAdvanceProvisionInstanceConfig_Branches(t *testing.T) {
	t.Parallel()

	now := time.Unix(500, 0).UTC()

	t.Run("requires store", func(t *testing.T) {
		delay, done, err := (&Server{}).advanceProvisionInstanceConfig(context.Background(), &models.ProvisionJob{}, "req", now)
		require.ErrorContains(t, err, "store not initialized")
		require.False(t, done)
		require.Zero(t, delay)
	})

	t.Run("nil job is ignored", func(t *testing.T) {
		st, _ := newBranchTestStore()
		delay, done, err := (&Server{store: st}).advanceProvisionInstanceConfig(context.Background(), nil, "req", now)
		require.NoError(t, err)
		require.True(t, done)
		require.Zero(t, delay)
	})

	t.Run("missing account rewinds to allocation", func(t *testing.T) {
		st, _ := newBranchTestStore()
		srv := &Server{store: st}
		job := &models.ProvisionJob{
			ID:           "job-1",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepInstanceConfig,
		}

		delay, done, err := srv.advanceProvisionInstanceConfig(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, provisionStepAccountCreate, job.Step)
		require.Equal(t, noteMissingAccountIDRestart, job.Note)
	})

	t.Run("instance load error retries", func(t *testing.T) {
		st, db := newBranchTestStore()
		mockBranchInstanceLookup(t, db, nil, errors.New("boom"))
		srv := &Server{store: st}
		job := managedProvisioningJob(provisionStepInstanceConfig)
		job.MaxAttempts = 3

		delay, done, err := srv.advanceProvisionInstanceConfig(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, provisionDefaultShortRetryDelay, delay)
		require.Equal(t, int64(1), job.Attempts)
		require.Equal(t, models.ProvisionJobStatusRunning, job.Status)
	})

	t.Run("instance not found fails", func(t *testing.T) {
		st, db := newBranchTestStore()
		mockBranchInstanceLookup(t, db, nil, theoryErrors.ErrItemNotFound)
		srv := &Server{store: st}
		job := managedProvisioningJob(provisionStepInstanceConfig)

		delay, done, err := srv.advanceProvisionInstanceConfig(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, models.ProvisionJobStatusError, job.Status)
		require.Equal(t, provisionStepFailed, job.Step)
		require.Equal(t, "instance_not_found", job.ErrorCode)
	})

	t.Run("instance key secret failure retries", func(t *testing.T) {
		st, db := newBranchTestStore()
		mockBranchInstanceLookup(t, db, &models.Instance{Slug: "slug"}, nil)
		srv := &Server{store: st}
		job := managedProvisioningJob(provisionStepInstanceConfig)
		job.MaxAttempts = 3

		delay, done, err := srv.advanceProvisionInstanceConfig(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, provisionDefaultShortRetryDelay, delay)
		require.Equal(t, int64(1), job.Attempts)
		require.Equal(t, models.ProvisionJobStatusRunning, job.Status)
	})
}
