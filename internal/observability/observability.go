package observability

import (
	"context"
	"log/slog"
	"os"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

func New(service string) apptheory.ObservabilityHooks {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	return apptheory.ObservabilityHooks{
		Log: func(rec apptheory.LogRecord) {
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
