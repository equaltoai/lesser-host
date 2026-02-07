package provisionworker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestHandleProvisionAccountCreateStatus_Branches(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)
	s := &Server{store: st}

	now := time.Now().UTC()
	job := &models.ProvisionJob{
		ID:          "job1",
		InstanceSlug: "slug",
		Status:       models.ProvisionJobStatusRunning,
		Step:         provisionStepAccountCreatePoll,
		MaxAttempts:  3,
		CreatedAt:    now.Add(-1 * time.Minute),
		UpdatedAt:    now.Add(-1 * time.Minute),
		ExpiresAt:    now.Add(1 * time.Hour),
	}

	// Nil/empty state is a no-op poll.
	delay, done, err := s.handleProvisionAccountCreateStatus(context.Background(), job, "req", now, nil)
	if err != nil || done || delay != provisionDefaultPollDelay {
		t.Fatalf("unexpected nil status: delay=%v done=%v err=%v", delay, done, err)
	}
	delay, done, err = s.handleProvisionAccountCreateStatus(context.Background(), job, "req", now, &orgtypes.CreateAccountStatus{})
	if err != nil || done || delay != provisionDefaultPollDelay {
		t.Fatalf("unexpected empty state: delay=%v done=%v err=%v", delay, done, err)
	}

	// In progress.
	delay, done, err = s.handleProvisionAccountCreateStatus(context.Background(), job, "req", now, &orgtypes.CreateAccountStatus{
		State: orgtypes.CreateAccountStateInProgress,
	})
	if err != nil || done || delay != provisionDefaultPollDelay || !strings.Contains(job.Note, "in progress") {
		t.Fatalf("unexpected in progress: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
	}

	// Failed.
	delay, done, err = s.handleProvisionAccountCreateStatus(context.Background(), job, "req", now, &orgtypes.CreateAccountStatus{
		State:         orgtypes.CreateAccountStateFailed,
		FailureReason: orgtypes.CreateAccountFailureReasonEmailAlreadyExists,
	})
	if err != nil || done || delay != 0 || job.Step != provisionStepFailed || job.ErrorCode != "account_create_failed" {
		t.Fatalf("unexpected failed: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
	}

	// Succeeded but missing account id.
	job.Step = provisionStepAccountCreatePoll
	job.ErrorCode = ""
	delay, done, err = s.handleProvisionAccountCreateStatus(context.Background(), job, "req", now, &orgtypes.CreateAccountStatus{
		State: orgtypes.CreateAccountStateSucceeded,
	})
	if err != nil || done || delay != 0 || job.Step != provisionStepFailed || job.ErrorCode != "account_create_failed" {
		t.Fatalf("unexpected succeeded missing id: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
	}

	// Succeeded with account id advances to account move.
	job.Step = provisionStepAccountCreatePoll
	job.ErrorCode = ""
	job.Status = models.ProvisionJobStatusRunning
	delay, done, err = s.handleProvisionAccountCreateStatus(context.Background(), job, "req", now, &orgtypes.CreateAccountStatus{
		State:     orgtypes.CreateAccountStateSucceeded,
		AccountId: aws.String("123456789012"),
	})
	if err != nil || done || delay != 0 || job.Step != provisionStepAccountMove || strings.TrimSpace(job.AccountID) == "" {
		t.Fatalf("unexpected succeeded: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
	}

	// Unknown state keeps polling.
	delay, done, err = s.handleProvisionAccountCreateStatus(context.Background(), job, "req", now, &orgtypes.CreateAccountStatus{
		State: orgtypes.CreateAccountState("weird"),
	})
	if err != nil || done || delay != provisionDefaultPollDelay || !strings.Contains(job.Note, "state") {
		t.Fatalf("unexpected default state: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
	}
}

func TestStartProvisionAccountCreate_ErrorBranches(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	makeServer := func(org organizationsAPI) *Server {
		db := ttmocks.NewMockExtendedDB()
		st := store.New(db)
		return &Server{
			cfg: config.Config{
				ManagedAccountEmailTemplate: "ops+{slug}@example.com",
				ManagedAccountNamePrefix:    "lesser-",
				ManagedInstanceRoleName:     "role",
			},
			store: st,
			org:   org,
		}
	}

	t.Run("create_error_retries", func(t *testing.T) {
		s := makeServer(&fakeOrg{createErr: errors.New("boom")})
		job := &models.ProvisionJob{ID: "j1", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepAccountCreate, MaxAttempts: 3}

		delay, done, err := s.startProvisionAccountCreate(context.Background(), job, "req", now)
		if err != nil || done || delay <= 0 || job.Attempts != 1 || !strings.Contains(job.Note, "retry") {
			t.Fatalf("unexpected retry: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
		}
	})

	t.Run("create_error_fails_at_max_attempts", func(t *testing.T) {
		s := makeServer(&fakeOrg{createErr: errors.New("boom")})
		job := &models.ProvisionJob{ID: "j2", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepAccountCreate, MaxAttempts: 1}

		delay, done, err := s.startProvisionAccountCreate(context.Background(), job, "req", now)
		if err != nil || done || delay != 0 || job.Step != provisionStepFailed || job.ErrorCode != "create_account_failed" {
			t.Fatalf("unexpected fail: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
		}
	})

	t.Run("empty_request_id_fails", func(t *testing.T) {
		s := makeServer(&fakeOrg{
			createOut: &organizations.CreateAccountOutput{CreateAccountStatus: &orgtypes.CreateAccountStatus{}},
		})
		job := &models.ProvisionJob{ID: "j3", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepAccountCreate, MaxAttempts: 3}

		delay, done, err := s.startProvisionAccountCreate(context.Background(), job, "req", now)
		if err != nil || done || delay != 0 || job.Step != provisionStepFailed || job.ErrorCode != "create_account_failed" {
			t.Fatalf("unexpected empty req id: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
		}
	})
}

func TestAdvanceProvisionAccountCreate_Branches(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)
	s := &Server{store: st}

	now := time.Now().UTC()

	// Account ID present => advance to move.
	job := &models.ProvisionJob{ID: "j1", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepAccountCreate, AccountID: "123"}
	delay, done, err := s.advanceProvisionAccountCreate(context.Background(), job, "req", now)
	if err != nil || done || delay != 0 || job.Step != provisionStepAccountMove {
		t.Fatalf("unexpected account id branch: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
	}

	// Request ID present => advance to poll.
	job2 := &models.ProvisionJob{ID: "j2", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepAccountCreate, AccountRequestID: "req1"}
	delay, done, err = s.advanceProvisionAccountCreate(context.Background(), job2, "req", now)
	if err != nil || done || delay != provisionDefaultPollDelay || job2.Step != provisionStepAccountCreatePoll {
		t.Fatalf("unexpected request id branch: delay=%v done=%v job=%#v err=%v", delay, done, job2, err)
	}
}

func TestAdvanceProvisionAccountCreatePoll_RestartTimeoutAndDescribeError(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	makeServer := func(org organizationsAPI) *Server {
		db := ttmocks.NewMockExtendedDB()
		st := store.New(db)
		return &Server{
			store: st,
			org:   org,
		}
	}

	t.Run("missing_request_id_restarts", func(t *testing.T) {
		s := makeServer(&fakeOrg{})
		job := &models.ProvisionJob{ID: "j", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepAccountCreatePoll}

		delay, done, err := s.advanceProvisionAccountCreatePoll(context.Background(), job, "req", now)
		if err != nil || done || delay != 0 || job.Step != provisionStepAccountCreate || !strings.Contains(job.Note, "missing account request id") {
			t.Fatalf("unexpected restart: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
		}
	})

	t.Run("timeout_fails", func(t *testing.T) {
		s := makeServer(&fakeOrg{})
		job := &models.ProvisionJob{
			ID:             "j",
			InstanceSlug:    "slug",
			Status:          models.ProvisionJobStatusRunning,
			Step:            provisionStepAccountCreatePoll,
			AccountRequestID: "req1",
			MaxAttempts:     3,
			CreatedAt:       now.Add(-(provisionMaxAccountCreateAge + 1*time.Minute)),
		}

		delay, done, err := s.advanceProvisionAccountCreatePoll(context.Background(), job, "req", now)
		if err != nil || done || delay != 0 || job.Step != provisionStepFailed || job.ErrorCode != "account_create_timeout" {
			t.Fatalf("unexpected timeout: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
		}
	})

	t.Run("describe_error_retries_and_then_fails", func(t *testing.T) {
		s := makeServer(&fakeOrg{describeErr: errors.New("boom")})

		job := &models.ProvisionJob{
			ID:              "j",
			InstanceSlug:     "slug",
			Status:           models.ProvisionJobStatusRunning,
			Step:             provisionStepAccountCreatePoll,
			AccountRequestID: "req1",
			MaxAttempts:      2,
			CreatedAt:        now.Add(-1 * time.Minute),
		}
		delay, done, err := s.advanceProvisionAccountCreatePoll(context.Background(), job, "req", now)
		if err != nil || done || delay <= 0 || job.Attempts != 1 {
			t.Fatalf("unexpected retry: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
		}

		// Next attempt hits max and fails.
		delay, done, err = s.advanceProvisionAccountCreatePoll(context.Background(), job, "req", now)
		if err != nil || done || delay != 0 || job.Step != provisionStepFailed || job.ErrorCode != "describe_account_failed" {
			t.Fatalf("unexpected fail: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
		}
	})
}

func TestMoveProvisionAccountToTargetOU_Branches(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	t.Run("missing_account_id_restarts", func(t *testing.T) {
		db := ttmocks.NewMockExtendedDB()
		st := store.New(db)
		s := &Server{store: st, org: &fakeOrg{}}

		job := &models.ProvisionJob{ID: "j", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepAccountMove}
		_, done, err := s.moveProvisionAccountToTargetOU(context.Background(), job, "req", now, "ou-target")
		if err != nil || done || job.Step != provisionStepAccountCreate || job.Note != noteMissingAccountIDRestart {
			t.Fatalf("unexpected: job=%#v err=%v", job, err)
		}
	})

	t.Run("list_parents_error_retries", func(t *testing.T) {
		db := ttmocks.NewMockExtendedDB()
		st := store.New(db)
		s := &Server{store: st, org: &fakeOrg{parentsErr: errors.New("boom")}}

		job := &models.ProvisionJob{ID: "j", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepAccountMove, AccountID: "123", MaxAttempts: 3}
		delay, done, err := s.moveProvisionAccountToTargetOU(context.Background(), job, "req", now, "ou-target")
		if err != nil || done || delay <= 0 || job.Attempts != 1 {
			t.Fatalf("unexpected retry: delay=%v done=%v job=%#v err=%v", delay, done, job, err)
		}
	})
}

