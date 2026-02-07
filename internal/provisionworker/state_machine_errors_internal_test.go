package provisionworker

import (
	"context"
	"errors"
	"testing"
	"time"

	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestAdvanceProvisionAccountCreatePoll_RestartsWhenRequestIDMissing(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)
	s := &Server{store: st}

	now := time.Now().UTC()
	job := &models.ProvisionJob{
		ID:               "j1",
		InstanceSlug:     "slug",
		Status:           models.ProvisionJobStatusRunning,
		Step:             provisionStepAccountCreatePoll,
		MaxAttempts:      3,
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(1 * time.Hour),
		AccountID:        "",
		AccountRequestID: "",
	}

	delay, done, err := s.advanceProvisionAccountCreatePoll(context.Background(), job, "req", now)
	if err != nil || done || delay != 0 {
		t.Fatalf("unexpected result: delay=%v done=%v err=%v", delay, done, err)
	}
	if job.Step != provisionStepAccountCreate {
		t.Fatalf("expected restart to account.create, got %q", job.Step)
	}
	if job.Note == "" {
		t.Fatalf("expected restart note set")
	}
}

func TestAdvanceProvisionAccountCreatePoll_FailsAfterTimeout(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)
	s := &Server{store: st}

	now := time.Now().UTC()
	job := &models.ProvisionJob{
		ID:               "j1",
		InstanceSlug:     "slug",
		Status:           models.ProvisionJobStatusRunning,
		Step:             provisionStepAccountCreatePoll,
		MaxAttempts:      3,
		CreatedAt:        now.Add(-1 * (provisionMaxAccountCreateAge + time.Minute)),
		UpdatedAt:        now.Add(-1 * time.Minute),
		ExpiresAt:        now.Add(1 * time.Hour),
		AccountRequestID: "req-1",
	}

	delay, done, err := s.advanceProvisionAccountCreatePoll(context.Background(), job, "req", now)
	if err != nil || done || delay != 0 {
		t.Fatalf("unexpected result: delay=%v done=%v err=%v", delay, done, err)
	}
	if job.Status != models.ProvisionJobStatusError || job.Step != provisionStepFailed || job.ErrorCode != "account_create_timeout" {
		t.Fatalf("expected job failed, got %#v", job)
	}
}

func TestAdvanceProvisionAccountCreatePoll_RetriesOnDescribeError(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)
	s := &Server{store: st, org: &fakeOrg{describeErr: errors.New("boom")}}

	now := time.Now().UTC()
	job := &models.ProvisionJob{
		ID:               "j1",
		InstanceSlug:     "slug",
		Status:           models.ProvisionJobStatusRunning,
		Step:             provisionStepAccountCreatePoll,
		MaxAttempts:      3,
		Attempts:         0,
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(1 * time.Hour),
		AccountRequestID: "req-1",
	}

	delay, done, err := s.advanceProvisionAccountCreatePoll(context.Background(), job, "req", now)
	if err != nil || done {
		t.Fatalf("unexpected result: delay=%v done=%v err=%v", delay, done, err)
	}
	if delay != provisionDefaultPollDelay {
		t.Fatalf("expected default poll delay, got %v", delay)
	}
	if job.Attempts != 1 {
		t.Fatalf("expected attempts incremented, got %d", job.Attempts)
	}
}

func TestAdvanceProvisionAccountCreatePoll_FailsAfterMaxAttempts(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)
	s := &Server{store: st, org: &fakeOrg{describeErr: errors.New("boom")}}

	now := time.Now().UTC()
	job := &models.ProvisionJob{
		ID:               "j1",
		InstanceSlug:     "slug",
		Status:           models.ProvisionJobStatusRunning,
		Step:             provisionStepAccountCreatePoll,
		MaxAttempts:      3,
		Attempts:         2,
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(1 * time.Hour),
		AccountRequestID: "req-1",
	}

	delay, done, err := s.advanceProvisionAccountCreatePoll(context.Background(), job, "req", now)
	if err != nil || done || delay != 0 {
		t.Fatalf("unexpected result: delay=%v done=%v err=%v", delay, done, err)
	}
	if job.Status != models.ProvisionJobStatusError || job.Step != provisionStepFailed || job.ErrorCode != "describe_account_failed" {
		t.Fatalf("expected job failed, got %#v", job)
	}
	if job.Attempts != 3 {
		t.Fatalf("expected attempts incremented to 3, got %d", job.Attempts)
	}
}
