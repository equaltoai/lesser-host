package provisionworker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestProvisionWorkerServerRegister_CoversQueueHookBranch(t *testing.T) {
	t.Parallel()

	var s *Server
	s.Register(nil)

	app := apptheory.New()
	s = &Server{cfg: config.Config{ProvisionQueueURL: "https://sqs.us-east-1.amazonaws.com/123/q"}}
	s.Register(app)
}

func TestAdvanceManagedProvisioning_UnknownStepFailsJob(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)

	s := &Server{cfg: config.Config{}, store: st}

	now := time.Now().UTC()
	job := &models.ProvisionJob{
		ID:          "job-1",
		InstanceSlug: "slug",
		Status:       models.ProvisionJobStatusRunning,
		Step:         "???",
		MaxAttempts:  3,
		CreatedAt:    now.Add(-1 * time.Minute),
		UpdatedAt:    now.Add(-1 * time.Minute),
		ExpiresAt:    now.Add(1 * time.Hour),
	}

	_, _, err := s.advanceManagedProvisioning(context.Background(), job, "req", now)
	if err != nil || strings.TrimSpace(job.Step) != provisionStepFailed || strings.TrimSpace(job.ErrorCode) != "unknown_step" {
		t.Fatalf("expected job failed unknown_step, got job=%#v err=%v", job, err)
	}
}

func TestAdvanceProvisionAssumeRole_Branches(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)

	now := time.Now().UTC()

	t.Run("missing_account_id_restarts", func(t *testing.T) {
		s := &Server{store: st}
		job := &models.ProvisionJob{ID: "j", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepAssumeRole}
		_, _, err := s.advanceProvisionAssumeRole(context.Background(), job, "req", now)
		if err != nil || job.Step != provisionStepAccountCreate || job.Note != noteMissingAccountIDRestart {
			t.Fatalf("unexpected: job=%#v err=%v", job, err)
		}
	})

	t.Run("timeout_fails", func(t *testing.T) {
		s := &Server{store: st, sts: &fakeSTS{err: errors.New("AccessDenied")}}
		job := &models.ProvisionJob{
			ID:           "j",
			InstanceSlug:  "slug",
			Status:        models.ProvisionJobStatusRunning,
			Step:          provisionStepAssumeRole,
			AccountID:     "123",
			AccountRoleName: "role",
			MaxAttempts:   3,
			CreatedAt:     now.Add(-(provisionMaxAssumeRoleAge + provisionMaxAccountCreateAge + 1*time.Minute)),
			UpdatedAt:     now,
			ExpiresAt:     now.Add(1 * time.Hour),
		}
		_, _, err := s.advanceProvisionAssumeRole(context.Background(), job, "req", now)
		if err != nil || job.Step != provisionStepFailed || job.ErrorCode != "assume_role_timeout" {
			t.Fatalf("expected assume_role_timeout, got job=%#v err=%v", job, err)
		}
	})

	t.Run("assume_role_not_ready_retries", func(t *testing.T) {
		s := &Server{store: st, sts: &fakeSTS{err: errors.New("AccessDenied: not ready")}}
		job := &models.ProvisionJob{
			ID:            "j",
			InstanceSlug:   "slug",
			Status:         models.ProvisionJobStatusRunning,
			Step:           provisionStepAssumeRole,
			AccountID:      "123",
			AccountRoleName: "role",
			MaxAttempts:    3,
			CreatedAt:      now.Add(-1 * time.Minute),
			UpdatedAt:      now,
			ExpiresAt:      now.Add(1 * time.Hour),
		}
		delay, _, err := s.advanceProvisionAssumeRole(context.Background(), job, "req", now)
		if err != nil || delay <= 0 || job.Note == "" || !strings.Contains(job.Note, "assumable") {
			t.Fatalf("unexpected: delay=%v job=%#v err=%v", delay, job, err)
		}
	})

	t.Run("non_retryable_error_backoff", func(t *testing.T) {
		s := &Server{store: st, sts: &fakeSTS{err: errors.New("boom")}}
		job := &models.ProvisionJob{
			ID:            "j",
			InstanceSlug:   "slug",
			Status:         models.ProvisionJobStatusRunning,
			Step:           provisionStepAssumeRole,
			AccountID:      "123",
			AccountRoleName: "role",
			MaxAttempts:    3,
			CreatedAt:      now.Add(-1 * time.Minute),
			UpdatedAt:      now,
			ExpiresAt:      now.Add(1 * time.Hour),
		}
		delay, _, err := s.advanceProvisionAssumeRole(context.Background(), job, "req", now)
		if err != nil || delay <= 0 || job.Attempts != 1 {
			t.Fatalf("unexpected: delay=%v job=%#v err=%v", delay, job, err)
		}
	})
}

func TestAdvanceProvisionParentDelegation_Branches(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)

	now := time.Now().UTC()

	t.Run("missing_parent_zone_fails", func(t *testing.T) {
		s := &Server{store: st}
		job := &models.ProvisionJob{ID: "j", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepParentDelegation}
		_, _, err := s.advanceProvisionParentDelegation(context.Background(), job, "req", now)
		if err != nil || job.Step != provisionStepFailed || job.ErrorCode != "missing_parent_zone" {
			t.Fatalf("expected missing_parent_zone, got job=%#v err=%v", job, err)
		}
	})

	t.Run("missing_child_name_servers_rewinds", func(t *testing.T) {
		s := &Server{store: st}
		job := &models.ProvisionJob{
			ID:              "j",
			InstanceSlug:     "slug",
			Status:           models.ProvisionJobStatusRunning,
			Step:             provisionStepParentDelegation,
			ParentHostedZoneID: "ZPARENT",
			BaseDomain:       "slug.example.com",
			ChildNameServers: nil,
		}
		_, _, err := s.advanceProvisionParentDelegation(context.Background(), job, "req", now)
		if err != nil || job.Step != provisionStepChildZone {
			t.Fatalf("expected rewind to child zone, got job=%#v err=%v", job, err)
		}
	})

	t.Run("upsert_error_retries_then_fails", func(t *testing.T) {
		r53 := &fakeRoute53{changeErr: errors.New("nope")}
		s := &Server{store: st, r53: r53}
		job := &models.ProvisionJob{
			ID:               "j",
			InstanceSlug:      "slug",
			Status:            models.ProvisionJobStatusRunning,
			Step:              provisionStepParentDelegation,
			ParentHostedZoneID: "ZPARENT",
			BaseDomain:        "slug.example.com",
			ChildNameServers:  []string{"ns-1"},
			MaxAttempts:       3,
			CreatedAt:         now.Add(-1 * time.Minute),
			UpdatedAt:         now,
			ExpiresAt:         now.Add(1 * time.Hour),
		}

		delay, _, err := s.advanceProvisionParentDelegation(context.Background(), job, "req", now)
		if err != nil || delay <= 0 || job.Attempts != 1 {
			t.Fatalf("expected retry backoff, got delay=%v job=%#v err=%v", delay, job, err)
		}

		job.Attempts = job.MaxAttempts - 1
		_, _, err = s.advanceProvisionParentDelegation(context.Background(), job, "req", now)
		if err != nil || job.Step != provisionStepFailed || job.ErrorCode != "parent_delegation_failed" {
			t.Fatalf("expected parent_delegation_failed, got job=%#v err=%v", job, err)
		}
	})
}

func TestAdvanceProvisionDeployStart_Branches(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)

	now := time.Now().UTC()

	t.Run("run_already_set_advances_to_wait", func(t *testing.T) {
		s := &Server{store: st}
		job := &models.ProvisionJob{
			ID:          "j",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepDeployStart,
			RunID:        "run1",
			MaxAttempts:  3,
			CreatedAt:    now.Add(-1 * time.Minute),
			UpdatedAt:    now,
			ExpiresAt:    now.Add(1 * time.Hour),
		}
		delay, _, err := s.advanceProvisionDeployStart(context.Background(), job, "req", now)
		if err != nil || delay != provisionDefaultPollDelay || job.Step != provisionStepDeployWait {
			t.Fatalf("unexpected: delay=%v job=%#v err=%v", delay, job, err)
		}
	})

	t.Run("start_runner_error_retries", func(t *testing.T) {
		cb := &fakeCodebuild{startErr: errors.New("boom")}
		s := &Server{
			cfg: config.Config{
				ManagedProvisionRunnerProjectName: "proj",
				ArtifactBucketName:                "bucket",
				ManagedLesserGitHubOwner:          "o",
				ManagedLesserGitHubRepo:           "r",
			},
			store: st,
			cb:    cb,
		}
		job := &models.ProvisionJob{
			ID:           "j",
			InstanceSlug:  "slug",
			Status:        models.ProvisionJobStatusRunning,
			Step:          provisionStepDeployStart,
			AccountID:     "123",
			AccountRoleName: "role",
			BaseDomain:    "slug.example.com",
			Region:        "us-east-1",
			LesserVersion: "v",
			MaxAttempts:   3,
			CreatedAt:     now.Add(-1 * time.Minute),
			UpdatedAt:     now,
			ExpiresAt:     now.Add(1 * time.Hour),
		}
		delay, _, err := s.advanceProvisionDeployStart(context.Background(), job, "req", now)
		if err != nil || delay <= 0 || job.Attempts != 1 || !strings.Contains(job.Note, "retrying") {
			t.Fatalf("expected retry, got delay=%v job=%#v err=%v", delay, job, err)
		}
	})
}

func TestAdvanceProvisionDeployWait_StatusBranches(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)

	now := time.Now().UTC()

	t.Run("timeout_fails", func(t *testing.T) {
		s := &Server{store: st, cb: &fakeCodebuild{}}
		job := &models.ProvisionJob{
			ID:          "j",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepDeployWait,
			RunID:        "run1",
			MaxAttempts:  3,
			CreatedAt:    now.Add(-(provisionMaxDeployAge + 1*time.Minute)),
			UpdatedAt:    now,
			ExpiresAt:    now.Add(1 * time.Hour),
		}
		_, _, err := s.advanceProvisionDeployWait(context.Background(), job, "req", now)
		if err != nil || job.Step != provisionStepFailed || job.ErrorCode != "deploy_timeout" {
			t.Fatalf("expected deploy_timeout, got job=%#v err=%v", job, err)
		}
	})

	t.Run("status_error_retries", func(t *testing.T) {
		cb := &fakeCodebuild{batchErr: errors.New("boom")}
		s := &Server{store: st, cb: cb}
		job := &models.ProvisionJob{
			ID:          "j",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepDeployWait,
			RunID:        "run1",
			MaxAttempts:  3,
			CreatedAt:    now.Add(-1 * time.Minute),
			UpdatedAt:    now,
			ExpiresAt:    now.Add(1 * time.Hour),
		}
		delay, _, err := s.advanceProvisionDeployWait(context.Background(), job, "req", now)
		if err != nil || delay <= 0 || job.Attempts != 1 || !strings.Contains(job.Note, "retrying") {
			t.Fatalf("expected retry delay, got delay=%v job=%#v err=%v", delay, job, err)
		}
	})

	t.Run("in_progress_polls", func(t *testing.T) {
		cb := &fakeCodebuild{batchOut: &codebuild.BatchGetBuildsOutput{Builds: []cbtypes.Build{{BuildStatus: cbtypes.StatusTypeInProgress}}}}
		s := &Server{store: st, cb: cb}
		job := &models.ProvisionJob{
			ID:          "j",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepDeployWait,
			RunID:        "run1",
			MaxAttempts:  3,
			CreatedAt:    now.Add(-1 * time.Minute),
			UpdatedAt:    now,
			ExpiresAt:    now.Add(1 * time.Hour),
		}
		delay, _, err := s.advanceProvisionDeployWait(context.Background(), job, "req", now)
		if err != nil || delay != provisionDefaultPollDelay || job.Step != provisionStepDeployWait {
			t.Fatalf("unexpected: delay=%v job=%#v err=%v", delay, job, err)
		}
	})

	t.Run("failed_includes_deeplink", func(t *testing.T) {
		cb := &fakeCodebuild{batchOut: &codebuild.BatchGetBuildsOutput{Builds: []cbtypes.Build{{
			BuildStatus: cbtypes.StatusTypeFailed,
			Logs:        &cbtypes.LogsLocation{DeepLink: aws.String(" link ")},
		}}}}
		s := &Server{store: st, cb: cb}
		job := &models.ProvisionJob{
			ID:          "j",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepDeployWait,
			RunID:        "run1",
			MaxAttempts:  3,
			CreatedAt:    now.Add(-1 * time.Minute),
			UpdatedAt:    now,
			ExpiresAt:    now.Add(1 * time.Hour),
		}
		_, _, err := s.advanceProvisionDeployWait(context.Background(), job, "req", now)
		if err != nil || job.Step != provisionStepFailed || job.ErrorCode != "deploy_failed" || !strings.Contains(job.ErrorMessage, "CodeBuild") {
			t.Fatalf("expected deploy_failed with link, got job=%#v err=%v", job, err)
		}
	})

	t.Run("unknown_status_polls", func(t *testing.T) {
		cb := &fakeCodebuild{batchOut: &codebuild.BatchGetBuildsOutput{Builds: []cbtypes.Build{{BuildStatus: cbtypes.StatusType("WAT")}}}}
		s := &Server{store: st, cb: cb}
		job := &models.ProvisionJob{
			ID:          "j",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepDeployWait,
			RunID:        "run1",
			MaxAttempts:  3,
			CreatedAt:    now.Add(-1 * time.Minute),
			UpdatedAt:    now,
			ExpiresAt:    now.Add(1 * time.Hour),
		}
		delay, _, err := s.advanceProvisionDeployWait(context.Background(), job, "req", now)
		if err != nil || delay != provisionDefaultPollDelay || !strings.Contains(job.Note, "deploy runner status") {
			t.Fatalf("unexpected: delay=%v job=%#v err=%v", delay, job, err)
		}
	})
}
