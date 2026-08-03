package costtelemetry

import (
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	apptheory "github.com/theory-cloud/apptheory/v3/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
)

func TestHandleScheduledTelemetryReturnsWorkloadSummary(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		AppName: "lesser-host",
		Stage:   "lab",
	}
	srv := NewServer(cfg, nil)

	scheduledAt := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	event := events.EventBridgeEvent{
		ID:         "cost-telemetry-evt-1",
		Source:     "aws.events",
		DetailType: "Scheduled Event",
		Resources: []string{
			"arn:aws:events:us-east-1:123456789012:rule/lesser-host-lab-cost-telemetry-collect",
		},
		Time: scheduledAt,
	}

	out, err := srv.handleScheduledTelemetry(&apptheory.EventContext{
		RequestID:   "req-cost-telemetry",
		RemainingMS: 6000,
	}, event)
	if err != nil {
		t.Fatalf("handleScheduledTelemetry: %v", err)
	}

	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", out)
	}

	scaffold, ok := m["scaffold"].(string)
	if !ok {
		t.Fatalf("expected scaffold string, got %T", m["scaffold"])
	}
	if scaffold == "" {
		t.Fatalf("expected non-empty scaffold message")
	}

	workload, ok := m["workload"].(map[string]any)
	if !ok {
		t.Fatalf("expected workload summary map, got %T", m["workload"])
	}

	// Verify canonical workload summary fields are present.
	wantFields := map[string]any{
		"kind":           "scheduled",
		"event_id":       "cost-telemetry-evt-1",
		"source":         "aws.events",
		"detail_type":    "Scheduled Event",
		"remaining_ms":   6000,
		"scheduled_time": scheduledAt.Format(time.RFC3339),
	}
	for k, want := range wantFields {
		got, ok := workload[k]
		if !ok {
			t.Fatalf("workload missing field %q", k)
		}
		if got != want {
			t.Fatalf("workload[%q] = %v, want %v", k, got, want)
		}
	}
}

func TestHandleScheduledTelemetryNilContext(t *testing.T) {
	t.Parallel()

	srv := NewServer(config.Config{}, nil)
	_, err := srv.handleScheduledTelemetry(nil, events.EventBridgeEvent{})
	if err == nil {
		t.Fatalf("expected error for nil context")
	}
}

func TestHandleScheduledTelemetryNilServer(t *testing.T) {
	t.Parallel()

	var srv *Server
	_, err := srv.handleScheduledTelemetry(&apptheory.EventContext{}, events.EventBridgeEvent{})
	if err == nil {
		t.Fatalf("expected error for nil server")
	}
}

func TestHandleScheduledTelemetryEmptyEvents(t *testing.T) {
	t.Parallel()

	srv := NewServer(config.Config{}, nil)
	out, err := srv.handleScheduledTelemetry(&apptheory.EventContext{
		RemainingMS: 6000,
	}, events.EventBridgeEvent{})
	if err != nil {
		t.Fatalf("handleScheduledTelemetry with empty event: %v", err)
	}

	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", out)
	}

	// Empty events are valid; workload summary fields should be present
	// with zero-values where appropriate.
	workload, ok := m["workload"].(map[string]any)
	if !ok {
		t.Fatalf("expected workload summary map for empty event")
	}
	if _, ok := workload["kind"]; !ok {
		t.Fatalf("expected workload summary to have kind field")
	}
}

func TestNew_ConstructsApp(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	if got := New(); got == nil {
		t.Fatalf("expected app, got nil")
	}
}
