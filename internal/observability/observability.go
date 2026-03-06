package observability

import (
	"context"
	"log/slog"
	"os"
	"strings"

	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/hostmetrics"
)

const observabilityUnknownValue = "unknown"

// New constructs observability hooks for apptheory services.
func New(service string) apptheory.ObservabilityHooks {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	return apptheory.ObservabilityHooks{
		Log: func(rec apptheory.LogRecord) {
			emitTrustProxy503BestEffort(service, rec)
			emitCommWebhookMetricsBestEffort(service, rec)

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
		instanceSlug = observabilityUnknownValue
	}

	hostmetrics.Emit("lesser-host", map[string]string{
		"Stage":   stage,
		"Service": strings.TrimSpace(service),
	}, []hostmetrics.Metric{
		{Name: "TrustProxy503", Unit: hostmetrics.UnitCount, Value: 1},
	}, map[string]any{
		"instance": instanceSlug,
		"method":   strings.TrimSpace(rec.Method),
		"path":     strings.TrimSpace(rec.Path),
	})

	hostmetrics.Emit("lesser-host", map[string]string{
		"Stage":    stage,
		"Service":  strings.TrimSpace(service),
		"Instance": instanceSlug,
	}, []hostmetrics.Metric{
		{Name: "TrustProxy503", Unit: hostmetrics.UnitCount, Value: 1},
	}, map[string]any{
		"method": strings.TrimSpace(rec.Method),
		"path":   strings.TrimSpace(rec.Path),
	})
}

func emitCommWebhookMetricsBestEffort(service string, rec apptheory.LogRecord) {
	if strings.TrimSpace(service) != "control-plane-api" {
		return
	}

	path := strings.TrimSpace(rec.Path)
	if !strings.HasPrefix(path, "/webhooks/comm/") {
		return
	}

	stage := strings.TrimSpace(os.Getenv("STAGE"))
	if stage == "" {
		stage = "lab"
	}

	provider, channel := commWebhookProviderAndChannel(path)
	if provider == "" {
		provider = observabilityUnknownValue
	}
	if channel == "" {
		channel = observabilityUnknownValue
	}

	ms := []hostmetrics.Metric{
		{Name: "CommWebhookRequests", Unit: hostmetrics.UnitCount, Value: 1},
	}
	if rec.Status >= 400 && rec.Status < 500 {
		ms = append(ms, hostmetrics.Metric{Name: "CommWebhook4xx", Unit: hostmetrics.UnitCount, Value: 1})
	}
	webhook5xx := rec.Status >= 500
	if rec.Status >= 500 {
		ms = append(ms, hostmetrics.Metric{Name: "CommWebhook5xx", Unit: hostmetrics.UnitCount, Value: 1})
	}

	hostmetrics.Emit("lesser-host", map[string]string{
		"Stage":    stage,
		"Service":  strings.TrimSpace(service),
		"Provider": provider,
		"Channel":  channel,
	}, ms, map[string]any{
		"path":   path,
		"status": rec.Status,
	})

	// Emit an alarm-friendly rollup because CloudWatch metric alarms cannot target SEARCH expressions.
	if webhook5xx {
		hostmetrics.Emit("lesser-host", map[string]string{
			"Stage":   stage,
			"Service": strings.TrimSpace(service),
		}, []hostmetrics.Metric{
			{Name: "CommWebhook5xx", Unit: hostmetrics.UnitCount, Value: 1},
		}, map[string]any{
			"path":   path,
			"status": rec.Status,
		})
	}
}

func commWebhookProviderAndChannel(path string) (provider string, channel string) {
	path = strings.TrimSpace(path)
	switch path {
	case "/webhooks/comm/email/inbound":
		return "migadu", "email"
	case "/webhooks/comm/sms/inbound":
		return "telnyx", "sms"
	case "/webhooks/comm/voice/inbound", "/webhooks/comm/voice/status":
		return "telnyx", "voice"
	default:
		return "", ""
	}
}
