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
	"github.com/stretchr/testify/require"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestHandleProvisionAccountCreateStatus_Branches(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)
	s := &Server{store: st, org: &fakeOrg{}}

	now := time.Now().UTC()
	job := &models.ProvisionJob{
		ID:           "job1",
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
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, provisionDefaultPollDelay, delay)
	delay, done, err = s.handleProvisionAccountCreateStatus(context.Background(), job, "req", now, &orgtypes.CreateAccountStatus{})
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, provisionDefaultPollDelay, delay)

	// In progress.
	delay, done, err = s.handleProvisionAccountCreateStatus(context.Background(), job, "req", now, &orgtypes.CreateAccountStatus{
		State: orgtypes.CreateAccountStateInProgress,
	})
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, provisionDefaultPollDelay, delay)
	require.Contains(t, job.Note, "in progress")

	// Failed.
	delay, done, err = s.handleProvisionAccountCreateStatus(context.Background(), job, "req", now, &orgtypes.CreateAccountStatus{
		State:         orgtypes.CreateAccountStateFailed,
		FailureReason: orgtypes.CreateAccountFailureReasonEmailAlreadyExists,
	})
	require.NoError(t, err)
	require.False(t, done)
	require.Zero(t, delay)
	require.Equal(t, provisionStepFailed, job.Step)
	require.Equal(t, "account_email_exists_adoption_required", job.ErrorCode)

	// Succeeded but missing account id.
	job.Step = provisionStepAccountCreatePoll
	job.ErrorCode = ""
	delay, done, err = s.handleProvisionAccountCreateStatus(context.Background(), job, "req", now, &orgtypes.CreateAccountStatus{
		State: orgtypes.CreateAccountStateSucceeded,
	})
	require.NoError(t, err)
	require.False(t, done)
	require.Zero(t, delay)
	require.Equal(t, provisionStepFailed, job.Step)
	require.Equal(t, "account_create_failed", job.ErrorCode)

	// Succeeded with account id advances to account move.
	job.Step = provisionStepAccountCreatePoll
	job.ErrorCode = ""
	job.Status = models.ProvisionJobStatusRunning
	delay, done, err = s.handleProvisionAccountCreateStatus(context.Background(), job, "req", now, &orgtypes.CreateAccountStatus{
		State:     orgtypes.CreateAccountStateSucceeded,
		AccountId: aws.String("123456789012"),
	})
	require.NoError(t, err)
	require.False(t, done)
	require.Zero(t, delay)
	require.Equal(t, provisionStepAccountMove, job.Step)
	require.NotEmpty(t, strings.TrimSpace(job.AccountID))

	// Unknown state keeps polling.
	delay, done, err = s.handleProvisionAccountCreateStatus(context.Background(), job, "req", now, &orgtypes.CreateAccountStatus{
		State: orgtypes.CreateAccountState("weird"),
	})
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, provisionDefaultPollDelay, delay)
	require.Contains(t, job.Note, "state")
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
		require.NoError(t, err)
		require.False(t, done)
		require.Greater(t, delay, time.Duration(0))
		require.Equal(t, int64(1), job.Attempts)
		require.Contains(t, job.Note, "retry")
	})

	t.Run("create_error_fails_at_max_attempts", func(t *testing.T) {
		s := makeServer(&fakeOrg{createErr: errors.New("boom")})
		job := &models.ProvisionJob{ID: "j2", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepAccountCreate, MaxAttempts: 1}

		delay, done, err := s.startProvisionAccountCreate(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, provisionStepFailed, job.Step)
		require.Equal(t, "create_account_failed", job.ErrorCode)
	})

	t.Run("empty_request_id_fails", func(t *testing.T) {
		s := makeServer(&fakeOrg{
			createOut: &organizations.CreateAccountOutput{CreateAccountStatus: &orgtypes.CreateAccountStatus{}},
		})
		job := &models.ProvisionJob{ID: "j3", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepAccountCreate, MaxAttempts: 3}

		delay, done, err := s.startProvisionAccountCreate(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, provisionStepFailed, job.Step)
		require.Equal(t, "create_account_failed", job.ErrorCode)
	})

	t.Run("does_not_reuse_existing_account_by_email", func(t *testing.T) {
		s := makeServer(&fakeOrg{
			createOut: &organizations.CreateAccountOutput{
				CreateAccountStatus: &orgtypes.CreateAccountStatus{Id: aws.String("car-123")},
			},
		})
		job := &models.ProvisionJob{ID: "j4", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepAccountCreate, MaxAttempts: 3}

		delay, done, err := s.startProvisionAccountCreate(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Equal(t, provisionDefaultPollDelay, delay)
		require.Equal(t, provisionStepAccountCreatePoll, job.Step)
		require.Equal(t, "car-123", job.AccountRequestID)
		require.Empty(t, job.AccountID)
	})
}

func TestAdvanceProvisionAccountCreate_Branches(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)
	s := &Server{store: st}

	now := time.Now().UTC()

	// Account ID + Request ID present => advance to move (normal post-CreateAccount path, not adoption).
	job := &models.ProvisionJob{ID: "j1", InstanceSlug: "slug", Status: models.ProvisionJobStatusRunning, Step: provisionStepAccountCreate, AccountID: "123", AccountRequestID: "car-1"}
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

func TestValidateAdoptedProvisionAccount_RequiresMatchingOrgAccount(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	makeServer := func(accounts []orgtypes.Account) *Server {
		db := ttmocks.NewMockExtendedDB()
		st := store.New(db)
		return &Server{
			cfg: config.Config{
				ManagedAccountEmailTemplate: "ops+{slug}@example.com",
				ManagedAccountNamePrefix:    "lesser-",
			},
			store: st,
			org: &fakeOrg{listOut: &organizations.ListAccountsOutput{
				Accounts: accounts,
			}},
		}
	}

	t.Run("matches", func(t *testing.T) {
		s := makeServer([]orgtypes.Account{{
			Id:     aws.String("123456789012"),
			Email:  aws.String("ops+slug@example.com"),
			Name:   aws.String("lesser-slug"),
			Status: orgtypes.AccountStatusActive,
		}})
		job := &models.ProvisionJob{
			ID:           "j",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepAccountMove,
			AccountID:    "123456789012",
			AccountEmail: "ops+slug@example.com",
			MaxAttempts:  3,
		}

		delay, done, err := s.validateAdoptedProvisionAccount(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.NotEqual(t, provisionStepFailed, job.Step)
	})

	t.Run("mismatched email fails", func(t *testing.T) {
		s := makeServer([]orgtypes.Account{{
			Id:     aws.String("123456789012"),
			Email:  aws.String("ops+other@example.com"),
			Name:   aws.String("lesser-slug"),
			Status: orgtypes.AccountStatusActive,
		}})
		job := &models.ProvisionJob{
			ID:           "j",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepAccountMove,
			AccountID:    "123456789012",
			AccountEmail: "ops+slug@example.com",
			MaxAttempts:  3,
		}

		delay, done, err := s.validateAdoptedProvisionAccount(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, provisionStepFailed, job.Step)
		require.Equal(t, "account_adoption_invalid", job.ErrorCode)
	})
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
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, provisionStepAccountCreate, job.Step)
		require.Contains(t, job.Note, "missing account request id")
	})

	t.Run("timeout_fails", func(t *testing.T) {
		s := makeServer(&fakeOrg{})
		job := &models.ProvisionJob{
			ID:               "j",
			InstanceSlug:     "slug",
			Status:           models.ProvisionJobStatusRunning,
			Step:             provisionStepAccountCreatePoll,
			AccountRequestID: "req1",
			MaxAttempts:      3,
			CreatedAt:        now.Add(-(provisionMaxAccountCreateAge + 1*time.Minute)),
		}

		delay, done, err := s.advanceProvisionAccountCreatePoll(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, provisionStepFailed, job.Step)
		require.Equal(t, "account_create_timeout", job.ErrorCode)
	})

	t.Run("describe_error_retries_and_then_fails", func(t *testing.T) {
		s := makeServer(&fakeOrg{describeErr: errors.New("boom")})

		job := &models.ProvisionJob{
			ID:               "j",
			InstanceSlug:     "slug",
			Status:           models.ProvisionJobStatusRunning,
			Step:             provisionStepAccountCreatePoll,
			AccountRequestID: "req1",
			MaxAttempts:      2,
			CreatedAt:        now.Add(-1 * time.Minute),
		}
		delay, done, err := s.advanceProvisionAccountCreatePoll(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Greater(t, delay, time.Duration(0))
		require.Equal(t, int64(1), job.Attempts)

		// Next attempt hits max and fails.
		delay, done, err = s.advanceProvisionAccountCreatePoll(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.False(t, done)
		require.Zero(t, delay)
		require.Equal(t, provisionStepFailed, job.Step)
		require.Equal(t, "describe_account_failed", job.ErrorCode)
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

// TestAdvanceProvisionAccountCreate_AdoptedAccountFailsEarly guards the
// CSR-012 fix: when an adopted account (AccountID set, AccountRequestID
// empty) fails validation, the job must be failed at account.create
// before any state transition to account.move or subsequent side effects.
func TestAdvanceProvisionAccountCreate_AdoptedAccountFailsEarly(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	makeServer := func(accounts []orgtypes.Account) *Server {
		db := ttmocks.NewMockExtendedDB()
		st := store.New(db)
		return &Server{
			cfg: config.Config{
				ManagedAccountEmailTemplate: "ops+{slug}@example.com",
				ManagedAccountNamePrefix:    "lesser-",
			},
			store: st,
			org: &fakeOrg{listOut: &organizations.ListAccountsOutput{
				Accounts: accounts,
			}},
		}
	}

	t.Run("adopted_account_validation_fails_at_create_not_move", func(t *testing.T) {
		s := makeServer([]orgtypes.Account{{
			Id:     aws.String("123456789012"),
			Email:  aws.String("ops+other@example.com"),
			Name:   aws.String("lesser-slug"),
			Status: orgtypes.AccountStatusActive,
		}})
		job := &models.ProvisionJob{
			ID:           "j-adopt",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepAccountCreate,
			AccountID:    "123456789012",
			AccountEmail: "ops+slug@example.com",
			MaxAttempts:  3,
			CreatedAt:    now.Add(-1 * time.Minute),
			ExpiresAt:    now.Add(1 * time.Hour),
		}

		delay, done, err := s.advanceProvisionAccountCreate(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.True(t, done)
		require.Zero(t, delay)
		require.Equal(t, models.ProvisionJobStatusError, job.Status)
		require.Equal(t, "account_adoption_invalid", job.ErrorCode)
		require.Equal(t, provisionStepFailed, job.Step)
	})

	t.Run("adopted_account_not_in_org_fails_at_create", func(t *testing.T) {
		s := makeServer(nil)
		job := &models.ProvisionJob{
			ID:           "j-missing",
			InstanceSlug: "slug",
			Status:       models.ProvisionJobStatusRunning,
			Step:         provisionStepAccountCreate,
			AccountID:    "123456789012",
			AccountEmail: "ops+slug@example.com",
			MaxAttempts:  3,
			CreatedAt:    now.Add(-1 * time.Minute),
			ExpiresAt:    now.Add(1 * time.Hour),
		}

		delay, done, err := s.advanceProvisionAccountCreate(context.Background(), job, "req", now)
		require.NoError(t, err)
		require.True(t, done)
		require.Zero(t, delay)
		require.Equal(t, models.ProvisionJobStatusError, job.Status)
		require.Equal(t, "account_adoption_invalid", job.ErrorCode)
		require.Equal(t, provisionStepFailed, job.Step)
	})
}

// TestAdvanceProvisionAccountMove_NoFallthroughAfterValidationFailure guards
// the CSR-012 fix: after validateAdoptedProvisionAccount fails the job via
// failJob, the calling code must check job.Status and return immediately
// rather than proceeding through OU move and assume-role steps.
func TestAdvanceProvisionAccountMove_NoFallthroughAfterValidationFailure(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)
	s := &Server{
		cfg: config.Config{
			ManagedAccountEmailTemplate:       "ops+{slug}@example.com",
			ManagedAccountNamePrefix:          "lesser-",
			ManagedTargetOrganizationalUnitID: "ou-target",
		},
		store: st,
		org: &fakeOrg{listOut: &organizations.ListAccountsOutput{
			Accounts: []orgtypes.Account{{
				Id:     aws.String("123456789012"),
				Email:  aws.String("ops+other@example.com"),
				Name:   aws.String("lesser-slug"),
				Status: orgtypes.AccountStatusActive,
			}},
		}},
	}

	job := &models.ProvisionJob{
		ID:           "j-fallthrough",
		InstanceSlug: "slug",
		Status:       models.ProvisionJobStatusRunning,
		Step:         provisionStepAccountMove,
		AccountID:    "123456789012",
		AccountEmail: "ops+slug@example.com",
		MaxAttempts:  3,
		CreatedAt:    now.Add(-1 * time.Minute),
		ExpiresAt:    now.Add(1 * time.Hour),
	}

	delay, done, err := s.advanceProvisionAccountMove(context.Background(), job, "req", now)
	require.NoError(t, err)
	require.True(t, done)
	require.Zero(t, delay)
	require.Equal(t, models.ProvisionJobStatusError, job.Status)
	require.Equal(t, "account_adoption_invalid", job.ErrorCode)
	require.Equal(t, provisionStepFailed, job.Step)
}
