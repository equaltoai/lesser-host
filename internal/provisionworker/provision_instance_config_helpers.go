package provisionworker

import (
	"strings"

	"github.com/theory-cloud/tabletheory/pkg/core"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func provisionTranslationEnabled(inst *models.Instance) bool {
	if inst == nil || inst.TranslationEnabled == nil {
		return true
	}
	return *inst.TranslationEnabled
}

func provisionInstanceConfigInstanceUpdate(
	job *models.ProvisionJob,
	inst *models.Instance,
	publicBaseURL string,
	attestationsURL string,
	secretArn string,
	translationEnabled bool,
) func(core.UpdateBuilder) error {
	return func(ub core.UpdateBuilder) error {
		if job != nil {
			setStringIfNotEmpty(ub, "LesserVersion", strings.TrimSpace(job.LesserVersion))
		}
		setHostURLsIfNotEmpty(ub, publicBaseURL, attestationsURL)
		setStringIfNotEmpty(ub, "LesserHostInstanceKeySecretARN", secretArn)

		if inst == nil {
			return nil
		}

		setBoolIfNil(ub, inst.TranslationEnabled, "TranslationEnabled", translationEnabled)
		setBoolIfNil(ub, inst.TipEnabled, "TipEnabled", effectiveTipEnabled(inst.TipEnabled))
		setBoolIfNil(ub, inst.LesserAIEnabled, "LesserAIEnabled", effectiveLesserAIEnabled(inst.LesserAIEnabled))
		setBoolIfNil(ub, inst.LesserAIModerationEnabled, "LesserAIModerationEnabled", effectiveLesserAIModerationEnabled(inst.LesserAIModerationEnabled))
		setBoolIfNil(ub, inst.LesserAINsfwDetectionEnabled, "LesserAINsfwDetectionEnabled", effectiveLesserAINsfwDetectionEnabled(inst.LesserAINsfwDetectionEnabled))
		setBoolIfNil(ub, inst.LesserAISpamDetectionEnabled, "LesserAISpamDetectionEnabled", effectiveLesserAISpamDetectionEnabled(inst.LesserAISpamDetectionEnabled))
		setBoolIfNil(ub, inst.LesserAIPiiDetectionEnabled, "LesserAIPiiDetectionEnabled", effectiveLesserAIPiiDetectionEnabled(inst.LesserAIPiiDetectionEnabled))
		setBoolIfNil(ub, inst.LesserAIContentDetectionEnabled, "LesserAIContentDetectionEnabled", effectiveLesserAIContentDetectionEnabled(inst.LesserAIContentDetectionEnabled))
		return nil
	}
}
