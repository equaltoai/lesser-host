package trust

import (
	"strings"

	"github.com/equaltoai/lesser-host/internal/ai"
	"github.com/equaltoai/lesser-host/internal/hostmetrics"
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

	ms := []hostmetrics.Metric{
		{Name: "AIRequests", Unit: hostmetrics.UnitCount, Value: 1},
		{Name: "AICreditsRequested", Unit: hostmetrics.UnitCount, Value: float64(resp.Budget.RequestedCredits)},
		{Name: "AICreditsDebited", Unit: hostmetrics.UnitCount, Value: float64(resp.Budget.DebitedCredits)},
	}

	switch resp.Status {
	case ai.JobStatusOK:
		if resp.Cached {
			ms = append(ms, hostmetrics.Metric{Name: "AICacheHits", Unit: hostmetrics.UnitCount, Value: 1})
		}
	case ai.JobStatusQueued:
		ms = append(ms, hostmetrics.Metric{Name: "AIQueued", Unit: hostmetrics.UnitCount, Value: 1})
	case ai.JobStatusNotCheckedBudget:
		ms = append(ms, hostmetrics.Metric{Name: "AINotChecked", Unit: hostmetrics.UnitCount, Value: 1})
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(resp.Budget.Reason)), "concurrency") {
			ms = append(ms, hostmetrics.Metric{Name: "AIConcurrencyRejected", Unit: hostmetrics.UnitCount, Value: 1})
		}
	case ai.JobStatusError:
		ms = append(ms, hostmetrics.Metric{Name: "AIErrors", Unit: hostmetrics.UnitCount, Value: 1})
	}

	if err != nil {
		ms = append(ms, hostmetrics.Metric{Name: "AIInternalErrors", Unit: hostmetrics.UnitCount, Value: 1})
	}

	hostmetrics.Emit("lesser-host", map[string]string{
		"Stage":    stage,
		"Service":  ServiceName,
		"Instance": instanceSlug,
		"Module":   module,
		"Status":   status,
	}, ms, nil)
}
