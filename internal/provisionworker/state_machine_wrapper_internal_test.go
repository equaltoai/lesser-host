package provisionworker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func TestManagedProvisioningStateMachine_RequeuesAfterStartingAccountCreate(t *testing.T) {
	db := ttmocks.NewMockExtendedDB()
	st := store.New(db)

	org := &fakeOrg{
		createOut: &organizations.CreateAccountOutput{
			CreateAccountStatus: &orgtypes.CreateAccountStatus{
				Id: aws.String("req-1"),
			},
		},
	}
	sqsClient := &fakeSQS{}

	s := NewServer(
		config.Config{
			ProvisionQueueURL:           "https://sqs.us-east-1.amazonaws.com/123/provision",
			ManagedAccountEmailTemplate: "ops+{slug}@example.com",
		},
		st,
		org,
		nil, // route53
		nil, // sts
		sqsClient,
		nil, // codebuild
		nil, // s3
	)

	now := time.Now().UTC()
	job := &models.ProvisionJob{
		ID:           "job-1",
		InstanceSlug: "slug",
		Status:       models.ProvisionJobStatusQueued,
		MaxAttempts:  3,
		CreatedAt:    now,
		UpdatedAt:    now,
		ExpiresAt:    now.Add(1 * time.Hour),
	}

	if err := s.runManagedProvisioningStateMachine(context.Background(), job, "req", now); err != nil {
		t.Fatalf("runManagedProvisioningStateMachine: %v", err)
	}

	if strings.TrimSpace(job.Status) != models.ProvisionJobStatusRunning {
		t.Fatalf("expected running status, got %#v", job)
	}
	if strings.TrimSpace(job.Step) != provisionStepAccountCreatePoll {
		t.Fatalf("expected step account_create_poll, got %#v", job)
	}
	if strings.TrimSpace(job.AccountRequestID) != "req-1" {
		t.Fatalf("expected account request id, got %#v", job)
	}

	if len(sqsClient.inputs) != 1 || sqsClient.inputs[0] == nil {
		t.Fatalf("expected one SQS enqueue, got %#v", sqsClient.inputs)
	}
	in := sqsClient.inputs[0]
	if in.DelaySeconds != int32(provisionDefaultPollDelay.Seconds()) {
		t.Fatalf("expected delay %d, got %d", int32(provisionDefaultPollDelay.Seconds()), in.DelaySeconds)
	}

	var msg map[string]any
	if err := json.Unmarshal([]byte(aws.ToString(in.MessageBody)), &msg); err != nil {
		t.Fatalf("unmarshal message body: %v", err)
	}
	if msg["kind"] != "provision_job" || msg["job_id"] != "job-1" {
		t.Fatalf("unexpected message body: %#v", msg)
	}
}

func TestProvisionWorkerRegisterHelpers(t *testing.T) {
	t.Parallel()

	if got := Register(nil, nil); got != nil {
		t.Fatalf("expected nil")
	}

	app := apptheory.New()
	if got := Register(app, nil); got != app {
		t.Fatalf("expected same app")
	}
}
