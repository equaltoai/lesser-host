package provisionworker

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/apptheory/testkit"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

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
	if job.Status != models.ProvisionJobStatusError || job.Step != "failed" {
		t.Fatalf("expected job marked failed, got status=%q step=%q", job.Status, job.Step)
	}
	if job.ErrorCode != "code" || job.ErrorMessage != "msg" {
		t.Fatalf("expected error details set")
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
