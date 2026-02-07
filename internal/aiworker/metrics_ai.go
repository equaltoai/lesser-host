package aiworker

import (
	"strings"

	"github.com/equaltoai/lesser-host/internal/metrics"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

const unknownValue = "unknown"

func (s *Server) emitAIJobMetrics(instanceSlug string, module string, status string, usage models.AIUsage, errs []models.AIError, err error) {
	if s == nil {
		return
	}

	stage := strings.TrimSpace(s.cfg.Stage)
	if stage == "" {
		stage = "lab"
	}

	instanceSlug = strings.TrimSpace(instanceSlug)
	if instanceSlug == "" {
		instanceSlug = unknownValue
	}

	module = strings.TrimSpace(module)
	if module == "" {
		module = unknownValue
	}

	status = strings.TrimSpace(status)
	if status == "" {
		if err != nil {
			status = "error"
		} else {
			status = unknownValue
		}
	}

	ms := []metrics.Metric{
		{Name: "AIJobs", Unit: metrics.UnitCount, Value: 1},
		{Name: "AIJobDurationMs", Unit: metrics.UnitMilliseconds, Value: float64(usage.DurationMs)},
	}

	switch status {
	case "ok":
		ms = append(ms, metrics.Metric{Name: "AIJobOK", Unit: metrics.UnitCount, Value: 1})
	case "error":
		ms = append(ms, metrics.Metric{Name: "AIJobErrors", Unit: metrics.UnitCount, Value: 1})
	}

	if err != nil {
		ms = append(ms, metrics.Metric{Name: "AIJobInternalErrors", Unit: metrics.UnitCount, Value: 1})
	}

	llmFallback := false
	for _, e := range errs {
		switch strings.ToLower(strings.TrimSpace(e.Code)) {
		case aiErrorCodeLLMUnavailable, aiErrorCodeLLMFailed, aiErrorCodeLLMMissingOutput:
			llmFallback = true
		}
	}
	if llmFallback {
		ms = append(ms, metrics.Metric{Name: "AILLMFallback", Unit: metrics.UnitCount, Value: 1})
	}

	provider := strings.TrimSpace(usage.Provider)
	if provider == "" {
		provider = unknownValue
	}

	metrics.Emit("lesser-host", map[string]string{
		"Stage":    stage,
		"Service":  ServiceName,
		"Instance": instanceSlug,
		"Module":   module,
		"Status":   status,
		"Provider": provider,
	}, ms, nil)
}
