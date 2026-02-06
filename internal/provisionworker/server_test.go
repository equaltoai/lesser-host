package provisionworker

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/provisioning"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
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
