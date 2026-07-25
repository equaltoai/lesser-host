package costtelemetry

import (
	"fmt"

	"github.com/aws/aws-lambda-go/events"
	apptheory "github.com/theory-cloud/apptheory/v2/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
)

// Server processes scheduled cost telemetry collection events.
// M3.7 scaffold: no business logic yet. M3.8–M3.10 implement
// CloudWatch collection, Cost Explorer integration, and DynamoDB cache.
type Server struct {
	cfg config.Config

	// cloudwatch is the M3.8 CloudWatch metric collector. Nil until
	// wired in production (M3.9/M3.10). The handler is safe when nil.
	cloudwatch CloudWatchCollector
}

// NewServer constructs a cost telemetry worker Server.
// Pass a nil collector to run without metric collection (M3.7 scaffold
// behavior); pass a CloudWatchCollector to enable M3.8+ metric collection.
func NewServer(cfg config.Config, collector CloudWatchCollector) *Server {
	return &Server{
		cfg:        cfg,
		cloudwatch: collector,
	}
}

// Register registers scheduled events with the provided app.
func (s *Server) Register(app *apptheory.App) {
	if app == nil || s == nil {
		return
	}

	ruleName := fmt.Sprintf("%s-%s-cost-telemetry-collect", s.cfg.AppName, s.cfg.Stage)
	app.EventBridge(apptheory.EventBridgeRule(ruleName), s.handleScheduledTelemetry)
}

// handleScheduledTelemetry is the M3.7 scaffold scheduled handler.
// It validates the EventContext, normalizes the scheduled workload,
// and returns a small JSON-compatible summary. No AWS billing /
// CloudWatch / Cost Explorer / DynamoDB business logic yet.
//
// M3.8–M3.10 wire the real collection pipeline here.
func (s *Server) handleScheduledTelemetry(ctx *apptheory.EventContext, event events.EventBridgeEvent) (any, error) {
	if ctx == nil {
		return nil, fmt.Errorf("event context is nil")
	}
	if s == nil {
		return nil, fmt.Errorf("server not initialized")
	}

	workload := apptheory.NormalizeEventBridgeScheduledWorkload(ctx, event)

	return map[string]any{
		"scaffold": "cost-telemetry-worker M3.7 scaffold — no business logic yet",
		"workload": scheduledTelemetryWorkloadSummary(workload),
	}, nil
}

func scheduledTelemetryWorkloadSummary(summary apptheory.EventBridgeScheduledWorkloadSummary) map[string]any {
	return map[string]any{
		"correlation_id":     summary.CorrelationID,
		"correlation_source": summary.CorrelationSource,
		"deadline_unix_ms":   summary.DeadlineUnixMS,
		"detail_type":        summary.DetailType,
		"event_id":           summary.EventID,
		"idempotency_key":    summary.IdempotencyKey,
		"kind":               summary.Kind,
		"remaining_ms":       summary.RemainingMS,
		"run_id":             summary.RunID,
		"scheduled_time":     summary.ScheduledTime,
		"source":             summary.Source,
	}
}
