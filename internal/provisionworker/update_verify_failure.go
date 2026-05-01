package provisionworker

import (
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func managedUpdateVerificationFailureMessage(job *models.UpdateJob) string {
	if job == nil {
		return "managed update verification failed"
	}
	failures := make([]string, 0, 4)
	if job.VerifyTranslationOK != nil && !*job.VerifyTranslationOK {
		failures = append(failures, "translation: "+verificationFailureDetail(job.VerifyTranslationErr))
	}
	if job.VerifyTrustOK != nil && !*job.VerifyTrustOK {
		failures = append(failures, "trust: "+verificationFailureDetail(job.VerifyTrustErr))
	}
	if job.VerifyTipsOK != nil && !*job.VerifyTipsOK {
		failures = append(failures, "tips: "+verificationFailureDetail(job.VerifyTipsErr))
	}
	if job.VerifyAIOK != nil && !*job.VerifyAIOK {
		failures = append(failures, "ai: "+verificationFailureDetail(job.VerifyAIErr))
	}
	if len(failures) == 0 {
		return "managed update verification failed"
	}
	return "managed update verification failed: " + strings.Join(failures, "; ")
}

func verificationFailureDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return "failed"
	}
	return detail
}
