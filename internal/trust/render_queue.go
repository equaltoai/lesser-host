package trust

import (
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func (s *Server) queueRender(ctx *apptheory.Context, normalizedURL string, retentionClass string, retentionDays int) (*models.RenderArtifact, bool, error) {
	if s == nil || s.store == nil || s.store.DB == nil || s.queues == nil {
		return nil, false, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, false, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	normalizedURL = strings.TrimSpace(normalizedURL)
	if normalizedURL == "" {
		return nil, false, &apptheory.AppError{Code: "app.bad_request", Message: "url is required"}
	}

	now := time.Now().UTC()
	classDays, classOut := rendering.RetentionForClass(retentionClass)
	if retentionDays <= 0 {
		retentionDays = classDays
	}
	desiredExpiresAt := rendering.ExpiresAtForRetention(now, retentionDays)

	renderID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, normalizedURL)

	// If already present, extend retention if needed and return (no new queue).
	if existing, err := s.store.GetRenderArtifact(ctx.Context(), renderID); err == nil {
		updated := false
		if existing.ExpiresAt.Before(desiredExpiresAt) {
			existing.ExpiresAt = desiredExpiresAt
			updated = true
		}
		if existing.RetentionClass != models.RenderRetentionClassEvidence && classOut == models.RenderRetentionClassEvidence {
			existing.RetentionClass = classOut
			updated = true
		}
		if updated {
			existing.RequestID = strings.TrimSpace(ctx.RequestID)
			existing.RequestedBy = strings.TrimSpace(ctx.AuthIdentity)
			_ = existing.UpdateKeys()
			_ = s.store.PutRenderArtifact(ctx.Context(), existing)
		}
		return existing, false, nil
	} else if err != nil && !theoryErrors.IsNotFound(err) {
		return nil, false, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	placeholder := &models.RenderArtifact{
		ID:             renderID,
		PolicyVersion:  rendering.RenderPolicyVersion,
		NormalizedURL:  normalizedURL,
		RetentionClass: classOut,
		RequestedBy:    strings.TrimSpace(ctx.AuthIdentity),
		RequestID:      strings.TrimSpace(ctx.RequestID),
		CreatedAt:      now,
		ExpiresAt:      desiredExpiresAt,
	}
	_ = placeholder.UpdateKeys()

	if err := s.store.DB.WithContext(ctx.Context()).Model(placeholder).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			// Raced with another writer; treat as existing.
			if existing, err2 := s.store.GetRenderArtifact(ctx.Context(), renderID); err2 == nil {
				return existing, false, nil
			}
		}
		return nil, false, &apptheory.AppError{Code: "app.internal", Message: "failed to queue render"}
	}

	msg := rendering.RenderJobMessage{
		Kind:           "render",
		RenderID:       renderID,
		NormalizedURL:  normalizedURL,
		RetentionClass: classOut,
		RetentionDays:  retentionDays,
		RequestedBy:    strings.TrimSpace(ctx.AuthIdentity),
		RequestID:      strings.TrimSpace(ctx.RequestID),
	}
	if err := s.queues.enqueueRenderJob(ctx.Context(), msg); err != nil {
		// Best-effort record the enqueue failure.
		placeholder.ErrorCode = "queue_failed"
		placeholder.ErrorMessage = "failed to enqueue render job"
		_ = placeholder.UpdateKeys()
		_ = s.store.PutRenderArtifact(ctx.Context(), placeholder)
		return placeholder, false, &apptheory.AppError{Code: "app.internal", Message: "failed to queue render"}
	}

	return placeholder, true, nil
}
