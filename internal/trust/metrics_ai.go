package trust

import (
	"strings"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/metrics"
)

const unknownValue = "unknown"

func (s *Server) emitAIRequestMetrics(instanceSlug string, module string, resp ai.Response, err error) {
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

	status := strings.TrimSpace(string(resp.Status))
	if status == "" {
		if err != nil {
			status = statusError
		} else {
			status = unknownValue
		}
	}

	ms := []metrics.Metric{
		{Name: "AIRequests", Unit: metrics.UnitCount, Value: 1},
		{Name: "AICreditsRequested", Unit: metrics.UnitCount, Value: float64(resp.Budget.RequestedCredits)},
		{Name: "AICreditsDebited", Unit: metrics.UnitCount, Value: float64(resp.Budget.DebitedCredits)},
	}

	switch resp.Status {
	case ai.JobStatusOK:
		if resp.Cached {
			ms = append(ms, metrics.Metric{Name: "AICacheHits", Unit: metrics.UnitCount, Value: 1})
		}
	case ai.JobStatusQueued:
		ms = append(ms, metrics.Metric{Name: "AIQueued", Unit: metrics.UnitCount, Value: 1})
	case ai.JobStatusNotCheckedBudget:
		ms = append(ms, metrics.Metric{Name: "AINotChecked", Unit: metrics.UnitCount, Value: 1})
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(resp.Budget.Reason)), "concurrency") {
			ms = append(ms, metrics.Metric{Name: "AIConcurrencyRejected", Unit: metrics.UnitCount, Value: 1})
		}
	case ai.JobStatusError:
		ms = append(ms, metrics.Metric{Name: "AIErrors", Unit: metrics.UnitCount, Value: 1})
	}

	if err != nil {
		ms = append(ms, metrics.Metric{Name: "AIInternalErrors", Unit: metrics.UnitCount, Value: 1})
	}

	metrics.Emit("lesser-host", map[string]string{
		"Stage":    stage,
		"Service":  ServiceName,
		"Instance": instanceSlug,
		"Module":   module,
		"Status":   status,
	}, ms, nil)
}
