package provisionworker

import (
	"context"
	"strings"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) bestUpdateBodyFailureDetail(ctx context.Context, job *models.UpdateJob, fallback string) string {
	if s == nil || job == nil {
		return fallback
	}
	bucket := strings.TrimSpace(s.cfg.ArtifactBucketName)
	key := s.updateBodyFailureS3Key(job)
	if bucket == "" || key == "" {
		return fallback
	}

	raw, artifact, err := s.loadManagedLesserBodyTemplateArtifactFromS3(ctx, bucket, key)
	if err == nil {
		if detail := renderManagedLesserBodyArtifactFailureDetail(artifact); detail != "" {
			return detail
		}
	}
	if strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw)
	}
	return fallback
}

func renderManagedLesserBodyArtifactFailureDetail(artifact *managedLesserBodyTemplateArtifact) string {
	if artifact == nil {
		return ""
	}
	detail := strings.TrimSpace(artifact.Detail)
	templatePath := strings.TrimSpace(artifact.TemplatePath)
	verificationMode := strings.TrimSpace(artifact.VerificationMode)

	prefix := ""
	switch {
	case verificationMode != "" && templatePath != "":
		prefix = verificationMode + " " + templatePath
	case verificationMode != "":
		prefix = verificationMode
	case templatePath != "":
		prefix = templatePath
	}

	switch {
	case prefix != "" && detail != "":
		return prefix + ": " + detail
	case detail != "":
		return detail
	default:
		return prefix
	}
}
