package trust

import (
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

func requireQueueRenderDeps(s *Server, ctx *apptheory.Context) *apptheory.AppError {
	if s == nil || s.store == nil || s.store.DB == nil || s.queues == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	return nil
}

func normalizeQueueRenderURL(raw string) (string, *apptheory.AppError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", &apptheory.AppError{Code: "app.bad_request", Message: "url is required"}
	}
	return raw, nil
}

func desiredQueueRenderRetention(now time.Time, retentionClass string, retentionDays int) (days int, classOut string, expiresAt time.Time) {
	classDays, classOut := rendering.RetentionForClass(retentionClass)
	if retentionDays <= 0 {
		retentionDays = classDays
	}
	return retentionDays, classOut, rendering.ExpiresAtForRetention(now, retentionDays)
}

func maybeExtendRenderArtifact(existing *models.RenderArtifact, desiredExpiresAt time.Time, retentionClass string, requestedBy string, requestID string) bool {
	if existing == nil {
		return false
	}

	updated := false
	if existing.ExpiresAt.Before(desiredExpiresAt) {
		existing.ExpiresAt = desiredExpiresAt
		updated = true
	}
	if existing.RetentionClass != models.RenderRetentionClassEvidence && retentionClass == models.RenderRetentionClassEvidence {
		existing.RetentionClass = retentionClass
		updated = true
	}
	if !updated {
		return false
	}

	existing.RequestID = strings.TrimSpace(requestID)
	existing.RequestedBy = strings.TrimSpace(requestedBy)
	_ = existing.UpdateKeys()
	return true
}

func (s *Server) getRenderArtifactIfExists(ctx *apptheory.Context, renderID string) (*models.RenderArtifact, bool, *apptheory.AppError) {
	if ctx == nil {
		return nil, false, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	existing, err := s.store.GetRenderArtifact(ctx.Context(), renderID)
	if err == nil {
		return existing, true, nil
	}
	if theoryErrors.IsNotFound(err) {
		return nil, false, nil
	}
	return nil, false, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
}

func (s *Server) createRenderArtifactIfNotExists(ctx *apptheory.Context, placeholder *models.RenderArtifact, renderID string) (*models.RenderArtifact, *apptheory.AppError) {
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if err := s.store.DB.WithContext(ctx.Context()).Model(placeholder).IfNotExists().Create(); err != nil {
		if theoryErrors.IsConditionFailed(err) {
			// Raced with another writer; treat as existing.
			if existing, err2 := s.store.GetRenderArtifact(ctx.Context(), renderID); err2 == nil {
				return existing, nil
			}
		}
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to queue render"}
	}
	return placeholder, nil
}

func (s *Server) enqueueRenderJobOrRecordFailure(ctx *apptheory.Context, placeholder *models.RenderArtifact, msg rendering.RenderJobMessage) *apptheory.AppError {
	if err := s.queues.enqueueRenderJob(ctx.Context(), msg); err == nil {
		return nil
	}

	placeholder.ErrorCode = "queue_failed"
	placeholder.ErrorMessage = "failed to enqueue render job"
	_ = placeholder.UpdateKeys()
	_ = s.store.PutRenderArtifact(ctx.Context(), placeholder)
	return &apptheory.AppError{Code: "app.internal", Message: "failed to queue render"}
}

func (s *Server) queueRender(ctx *apptheory.Context, normalizedURL string, retentionClass string, retentionDays int) (*models.RenderArtifact, bool, error) {
	if appErr := requireQueueRenderDeps(s, ctx); appErr != nil {
		return nil, false, appErr
	}

	normalizedURL, appErr := normalizeQueueRenderURL(normalizedURL)
	if appErr != nil {
		return nil, false, appErr
	}

	now := time.Now().UTC()
	retentionDays, retentionClass, desiredExpiresAt := desiredQueueRenderRetention(now, retentionClass, retentionDays)
	renderID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, normalizedURL)

	existing, found, getErr := s.getRenderArtifactIfExists(ctx, renderID)
	if getErr != nil {
		return nil, false, getErr
	}
	if found {
		if maybeExtendRenderArtifact(existing, desiredExpiresAt, retentionClass, ctx.AuthIdentity, ctx.RequestID) {
			_ = s.store.PutRenderArtifact(ctx.Context(), existing)
		}
		return existing, false, nil
	}

	placeholder := &models.RenderArtifact{
		ID:             renderID,
		PolicyVersion:  rendering.RenderPolicyVersion,
		NormalizedURL:  normalizedURL,
		RetentionClass: retentionClass,
		RequestedBy:    strings.TrimSpace(ctx.AuthIdentity),
		RequestID:      strings.TrimSpace(ctx.RequestID),
		CreatedAt:      now,
		ExpiresAt:      desiredExpiresAt,
	}
	_ = placeholder.UpdateKeys()

	created, createErr := s.createRenderArtifactIfNotExists(ctx, placeholder, renderID)
	if createErr != nil {
		return nil, false, createErr
	}
	if created != placeholder {
		return created, false, nil
	}

	msg := rendering.RenderJobMessage{
		Kind:           "render",
		RenderID:       renderID,
		NormalizedURL:  normalizedURL,
		RetentionClass: retentionClass,
		RetentionDays:  retentionDays,
		RequestedBy:    strings.TrimSpace(ctx.AuthIdentity),
		RequestID:      strings.TrimSpace(ctx.RequestID),
	}
	if enqueueErr := s.enqueueRenderJobOrRecordFailure(ctx, placeholder, msg); enqueueErr != nil {
		return placeholder, false, enqueueErr
	}

	return placeholder, true, nil
}
