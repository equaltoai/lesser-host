package trust

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/rendering"
)

func TestQueueClient_ValidationAndClientInitErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	if _, err := (*queueClient)(nil).sqsClient(ctx); err == nil {
		t.Fatalf("expected error for nil queue client")
	}

	q := newQueueClient("  p  ", "  s  ")
	if q.previewQueueURL != "p" || q.safetyQueueURL != "s" {
		t.Fatalf("expected trimmed urls, got %#v", q)
	}

	// Force init path to skip awsconfig.LoadDefaultConfig by completing the once.
	q.once.Do(func() {})
	if _, err := q.sqsClient(ctx); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected not initialized error, got %v", err)
	}

	q2 := newQueueClient("p", "s")
	q2.err = errors.New("boom")
	q2.once.Do(func() {})
	if _, err := q2.sqsClient(ctx); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected injected error, got %v", err)
	}
}

func TestQueueClient_EnqueueValidatesInputs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	if err := (*queueClient)(nil).enqueueRenderJob(ctx, rendering.RenderJobMessage{}); err == nil {
		t.Fatalf("expected error for nil queue client")
	}
	if err := newQueueClient("", "s").enqueueRenderJob(ctx, rendering.RenderJobMessage{}); err == nil {
		t.Fatalf("expected error for missing preview queue url")
	}

	if err := (*queueClient)(nil).enqueueAIJob(ctx, ai.JobMessage{}); err == nil {
		t.Fatalf("expected error for nil queue client")
	}
	if err := newQueueClient("p", "").enqueueAIJob(ctx, ai.JobMessage{}); err == nil {
		t.Fatalf("expected error for missing safety queue url")
	}
}

