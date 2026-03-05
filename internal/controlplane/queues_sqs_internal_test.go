package controlplane

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-host/internal/commworker"
	"github.com/equaltoai/lesser-host/internal/provisioning"
)

func TestControlPlaneQueueClient_ValidationAndClientInitErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	if _, err := (*queueClient)(nil).sqsClient(ctx); err == nil {
		t.Fatalf("expected error for nil queue client")
	}

	q := newQueueClient("  url  ", "  comm  ")
	if q.provisionQueueURL != "url" {
		t.Fatalf("expected trimmed url, got %#v", q)
	}
	if q.commQueueURL != "comm" {
		t.Fatalf("expected trimmed comm url, got %#v", q)
	}

	// Force init path to skip awsconfig.LoadDefaultConfig by completing the once.
	q.once.Do(func() {})
	if _, err := q.sqsClient(ctx); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected not initialized error, got %v", err)
	}

	q2 := newQueueClient("url", "comm")
	q2.err = errors.New("boom")
	q2.once.Do(func() {})
	if _, err := q2.sqsClient(ctx); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected injected error, got %v", err)
	}
}

func TestControlPlaneQueueClient_EnqueueValidatesInputs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	if err := (*queueClient)(nil).enqueueProvisionJob(ctx, provisioning.JobMessage{}); err == nil {
		t.Fatalf("expected error for nil queue client")
	}
	if err := newQueueClient("", "").enqueueProvisionJob(ctx, provisioning.JobMessage{}); err == nil {
		t.Fatalf("expected error for missing queue url")
	}

	if err := (*queueClient)(nil).enqueueCommMessage(ctx, commworker.QueueMessage{}); err == nil {
		t.Fatalf("expected error for nil queue client (comm)")
	}
	if err := newQueueClient("", "").enqueueCommMessage(ctx, commworker.QueueMessage{}); err == nil {
		t.Fatalf("expected error for missing comm queue url")
	}
}
