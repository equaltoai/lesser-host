package provisionworker

import (
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func applyManagedUpdateRuntimeConfig(job *models.UpdateJob, inst *models.Instance) (string, string) {
	if job == nil || inst == nil {
		return "internal_error", updateVerifyInternalError
	}
	if !effectiveBodyEnabled(inst.BodyEnabled) {
		job.LesserBodyVersion = ""
	}
	job.TipEnabled = effectiveTipEnabled(inst.TipEnabled)
	job.TipChainID = inst.TipChainID
	job.TipContractAddress = strings.TrimSpace(inst.TipContractAddress)
	if job.TipEnabled && (job.TipChainID <= 0 || strings.TrimSpace(job.TipContractAddress) == "") {
		return "tip_config_incomplete", "tip configuration is incomplete for managed update"
	}
	job.AIEnabled = effectiveLesserAIEnabled(inst.LesserAIEnabled)
	job.AIModerationEnabled = effectiveLesserAIModerationEnabled(inst.LesserAIModerationEnabled)
	job.AINsfwDetectionEnabled = effectiveLesserAINsfwDetectionEnabled(inst.LesserAINsfwDetectionEnabled)
	job.AISpamDetectionEnabled = effectiveLesserAISpamDetectionEnabled(inst.LesserAISpamDetectionEnabled)
	job.AIPiiDetectionEnabled = effectiveLesserAIPiiDetectionEnabled(inst.LesserAIPiiDetectionEnabled)
	job.AIContentDetectionEnabled = effectiveLesserAIContentDetectionEnabled(inst.LesserAIContentDetectionEnabled)
	return "", ""
}
