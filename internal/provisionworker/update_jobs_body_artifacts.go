package provisionworker

import (
	"fmt"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) updateBodyFailureS3Key(job *models.UpdateJob) string {
	if job == nil {
		return ""
	}
	return fmt.Sprintf("managed/updates/%s/%s/body-failure.json", strings.TrimSpace(job.InstanceSlug), strings.TrimSpace(job.ID))
}

func (s *Server) updateBodyTemplateCertificationS3Key(job *models.UpdateJob) string {
	if job == nil {
		return ""
	}
	return fmt.Sprintf("managed/updates/%s/%s/body-template-certification.json", strings.TrimSpace(job.InstanceSlug), strings.TrimSpace(job.ID))
}
