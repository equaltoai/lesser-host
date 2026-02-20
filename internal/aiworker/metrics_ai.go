package aiworker

import (
	"strings"

	"github.com/equaltoai/lesser-host/internal/hostmetrics"
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

	ms := []hostmetrics.Metric{
		{Name: "AIJobs", Unit: hostmetrics.UnitCount, Value: 1},
		{Name: "AIJobDurationMs", Unit: hostmetrics.UnitMilliseconds, Value: float64(usage.DurationMs)},
	}

	switch status {
	case "ok":
		ms = append(ms, hostmetrics.Metric{Name: "AIJobOK", Unit: hostmetrics.UnitCount, Value: 1})
	case "error":
		ms = append(ms, hostmetrics.Metric{Name: "AIJobErrors", Unit: hostmetrics.UnitCount, Value: 1})
	}

	if err != nil {
		ms = append(ms, hostmetrics.Metric{Name: "AIJobInternalErrors", Unit: hostmetrics.UnitCount, Value: 1})
	}

	llmFallback := false
	for _, e := range errs {
		switch strings.ToLower(strings.TrimSpace(e.Code)) {
		case aiErrorCodeLLMUnavailable, aiErrorCodeLLMFailed, aiErrorCodeLLMMissingOutput:
			llmFallback = true
		}
	}
	if llmFallback {
		ms = append(ms, hostmetrics.Metric{Name: "AILLMFallback", Unit: hostmetrics.UnitCount, Value: 1})
	}

	provider := strings.TrimSpace(usage.Provider)
	if provider == "" {
		provider = unknownValue
	}

	hostmetrics.Emit("lesser-host", map[string]string{
		"Stage":    stage,
		"Service":  ServiceName,
		"Instance": instanceSlug,
		"Module":   module,
		"Status":   status,
		"Provider": provider,
	}, ms, nil)
}
