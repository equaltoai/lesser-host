package observability

import (
	"context"
	"log/slog"
	"os"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/metrics"
)

// New constructs observability hooks for apptheory services.
func New(service string) apptheory.ObservabilityHooks {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	return apptheory.ObservabilityHooks{
		Log: func(rec apptheory.LogRecord) {
			emitTrustProxy503BestEffort(service, rec)

			level := slog.LevelInfo
			switch rec.Level {
			case "warn":
				level = slog.LevelWarn
			case "error":
				level = slog.LevelError
			}

			logger.Log(
				context.Background(),
				level,
				rec.Event,
				slog.String("service", service),
				slog.String("request_id", rec.RequestID),
				slog.String("tenant_id", rec.TenantID),
				slog.String("method", rec.Method),
				slog.String("path", rec.Path),
				slog.Int("status", rec.Status),
				slog.String("error_code", rec.ErrorCode),
			)
		},
		Metric: func(rec apptheory.MetricRecord) {
			logger.Log(
				context.Background(),
				slog.LevelInfo,
				"metric",
				slog.String("service", service),
				slog.String("name", rec.Name),
				slog.Int("value", rec.Value),
				slog.Any("tags", rec.Tags),
			)
		},
	}
}

func emitTrustProxy503BestEffort(service string, rec apptheory.LogRecord) {
	if strings.TrimSpace(service) != "trust-api" || rec.Status != 503 {
		return
	}

	stage := strings.TrimSpace(os.Getenv("STAGE"))
	if stage == "" {
		stage = "lab"
	}

	instanceSlug := strings.TrimSpace(rec.TenantID)
	if instanceSlug == "" {
		instanceSlug = "unknown"
	}

	metrics.Emit("lesser-host", map[string]string{
		"Stage":    stage,
		"Service":  strings.TrimSpace(service),
		"Instance": instanceSlug,
	}, []metrics.Metric{
		{Name: "TrustProxy503", Unit: metrics.UnitCount, Value: 1},
	}, map[string]any{
		"method": strings.TrimSpace(rec.Method),
		"path":   strings.TrimSpace(rec.Path),
	})
}
