package provisionworker

import (
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func updateJobPhaseDetail(phase string, currentPhase string, failureDetail string) string {
	currentPhase = normalizeOperatorVisibleFailureWhitespace(currentPhase)
	failureDetail = sanitizeOperatorVisibleFailureDetail(failureDetail)
	if currentPhase != "" && failureDetail != "" {
		return currentPhase + ": " + failureDetail
	}
	if currentPhase != "" {
		return currentPhase
	}
	return failureDetail
}

func updateJobPhaseRunURL(job *models.UpdateJob, phase string) string {
	if job == nil {
		return ""
	}
	switch strings.TrimSpace(phase) {
	case updatePhaseDeploy:
		return strings.TrimSpace(job.DeployRunURL)
	case updatePhaseBody:
		return strings.TrimSpace(job.BodyRunURL)
	case updatePhaseMCP:
		return strings.TrimSpace(job.MCPRunURL)
	default:
		return ""
	}
}
