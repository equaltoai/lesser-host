package renderworker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	apptheory "github.com/theory-cloud/apptheory/runtime"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/trust"
)

type renderStore interface {
	GetRenderArtifact(ctx context.Context, id string) (*models.RenderArtifact, error)
	PutRenderArtifact(ctx context.Context, item *models.RenderArtifact) error
	DeleteRenderArtifact(ctx context.Context, id string) error
	ListExpiredRenderArtifacts(ctx context.Context, now time.Time, limit int) ([]*models.RenderArtifact, error)
}

type artifactStore interface {
	PutObject(ctx context.Context, key string, body []byte, contentType string, cacheControl string) error
	DeleteObject(ctx context.Context, key string) error
}

// Server processes render jobs and retention sweep events.
type Server struct {
	cfg       config.Config
	store     renderStore
	artifacts artifactStore
}

// NewServer constructs a render worker Server.
func NewServer(cfg config.Config, st renderStore, artifactsStore artifactStore) *Server {
	return &Server{
		cfg:       cfg,
		store:     st,
		artifacts: artifactsStore,
	}
}

// Register registers queue handlers and scheduled events with the provided app.
func (s *Server) Register(app *apptheory.App) {
	if app == nil || s == nil {
		return
	}

	queueName := sqsQueueNameFromURL(s.cfg.PreviewQueueURL)
	if queueName != "" {
		app.SQS(queueName, s.handlePreviewQueueMessage)
	}

	ruleName := fmt.Sprintf("%s-%s-retention-sweep", s.cfg.AppName, s.cfg.Stage)
	app.EventBridge(apptheory.EventBridgeRule(ruleName), s.handleRetentionSweep)
}

func (s *Server) handlePreviewQueueMessage(ctx *apptheory.EventContext, msg events.SQSMessage) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}
	if ctx == nil {
		return fmt.Errorf("event context is nil")
	}

	var job rendering.RenderJobMessage
	if err := json.Unmarshal([]byte(msg.Body), &job); err != nil {
		return nil // drop invalid
	}
	if strings.TrimSpace(job.Kind) != "render" {
		return nil
	}

	if job.RenderID == "" || job.NormalizedURL == "" {
		return nil
	}

	return s.processRenderJob(ctx.Context(), ctx.RequestID, job)
}

func (s *Server) processRenderJob(ctx context.Context, requestID string, job rendering.RenderJobMessage) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("store not initialized")
	}
	if s.artifacts == nil {
		return fmt.Errorf("artifact store not initialized")
	}

	now := time.Now().UTC()

	normalized := strings.TrimSpace(job.NormalizedURL)
	renderID := strings.TrimSpace(job.RenderID)

	// Ensure ID is deterministic and correct.
	wantID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, normalized)
	if renderID == "" {
		renderID = wantID
	} else if renderID != wantID {
		renderID = wantID
	}

	retentionDays, classOut := rendering.RetentionForClass(job.RetentionClass)
	if job.RetentionDays > 0 {
		retentionDays = job.RetentionDays
	}
	desiredExpiresAt := rendering.ExpiresAtForRetention(now, retentionDays)

	// Fast path: existing, rendered, and not expired.
	if existing, err := s.store.GetRenderArtifact(ctx, renderID); err == nil {
		if existing.ExpiresAt.Before(desiredExpiresAt) || (existing.RetentionClass != models.RenderRetentionClassEvidence && classOut == models.RenderRetentionClassEvidence) {
			existing.ExpiresAt = maxTime(existing.ExpiresAt, desiredExpiresAt)
			existing.RetentionClass = classOutOrExisting(existing.RetentionClass, classOut)
			existing.RequestID = strings.TrimSpace(requestID)
			existing.RequestedBy = strings.TrimSpace(job.RequestedBy)
			_ = existing.UpdateKeys()
			_ = s.store.PutRenderArtifact(ctx, existing)
		}

		if !existing.RenderedAt.IsZero() && strings.TrimSpace(existing.ThumbnailObjectKey) != "" {
			return nil
		}
	}

	// Fetch HTML with hardened rules.
	_, start, err := trust.NormalizeLinkURL(normalized)
	if err != nil {
		return s.storeRenderError(ctx, renderID, normalized, classOut, desiredExpiresAt, requestID, job.RequestedBy, "invalid_url", "invalid url")
	}
	if err := trust.ValidateOutboundURL(ctx, start); err != nil {
		return s.storeRenderError(ctx, renderID, normalized, classOut, desiredExpiresAt, requestID, job.RequestedBy, "blocked_ssrf", "host is not allowed")
	}

	client := trust.NewPreviewHTTPClient(6 * time.Second)
	finalURL, chain, body, contentType, err := trust.FetchWithRedirects(ctx, client, start, 5, 1024*1024)
	if err != nil {
		code := "fetch_failed"
		msg := "fetch failed"
		if pe, ok := err.(*trust.LinkPreviewError); ok {
			code = pe.Code
			msg = pe.Message
		}
		return s.storeRenderError(ctx, renderID, normalized, classOut, desiredExpiresAt, requestID, job.RequestedBy, code, msg)
	}
	if !strings.Contains(strings.ToLower(contentType), "text/html") {
		// Not HTML: store a minimal artifact without screenshot.
		item := &models.RenderArtifact{
			ID:             renderID,
			PolicyVersion:  rendering.RenderPolicyVersion,
			NormalizedURL:  normalized,
			ResolvedURL:    safeURLString(finalURL),
			RedirectChain:  append([]string(nil), chain...),
			RetentionClass: classOut,
			RequestID:      strings.TrimSpace(requestID),
			RequestedBy:    strings.TrimSpace(job.RequestedBy),
			CreatedAt:      now,
			RenderedAt:     now,
			ExpiresAt:      desiredExpiresAt,
			ErrorCode:      "not_html",
			ErrorMessage:   "content is not html",
		}
		_ = item.UpdateKeys()
		return s.store.PutRenderArtifact(ctx, item)
	}

	// Render screenshot + snapshot with network blocked.
	thumb, snap, textPreview, renderErr := renderHTMLWithChrome(ctx, body)
	if renderErr != nil {
		return s.storeRenderError(ctx, renderID, normalized, classOut, desiredExpiresAt, requestID, job.RequestedBy, "render_failed", "render failed")
	}

	thumbKey := rendering.ThumbnailObjectKey(renderID)
	snapKey := rendering.SnapshotObjectKey(renderID)

	if err := s.artifacts.PutObject(ctx, thumbKey, thumb, "image/jpeg", "public, max-age=86400, immutable"); err != nil {
		return err
	}
	if err := s.artifacts.PutObject(ctx, snapKey, snap, "text/plain; charset=utf-8", "private, max-age=600"); err != nil {
		return err
	}

	item := &models.RenderArtifact{
		ID:                   renderID,
		PolicyVersion:        rendering.RenderPolicyVersion,
		NormalizedURL:        normalized,
		ResolvedURL:          safeURLString(finalURL),
		RedirectChain:        append([]string(nil), chain...),
		ThumbnailObjectKey:   thumbKey,
		ThumbnailContentType: "image/jpeg",
		SnapshotObjectKey:    snapKey,
		SnapshotContentType:  "text/plain; charset=utf-8",
		TextPreview:          textPreview,
		RetentionClass:       classOut,
		RequestID:            strings.TrimSpace(requestID),
		RequestedBy:          strings.TrimSpace(job.RequestedBy),
		CreatedAt:            now,
		RenderedAt:           now,
		ExpiresAt:            desiredExpiresAt,
	}
	_ = item.UpdateKeys()
	return s.store.PutRenderArtifact(ctx, item)
}

func (s *Server) storeRenderError(ctx context.Context, renderID string, normalizedURL string, retentionClass string, expiresAt time.Time, requestID string, requestedBy string, code string, message string) error {
	now := time.Now().UTC()
	item := &models.RenderArtifact{
		ID:             renderID,
		PolicyVersion:  rendering.RenderPolicyVersion,
		NormalizedURL:  normalizedURL,
		RetentionClass: retentionClass,
		ErrorCode:      strings.TrimSpace(code),
		ErrorMessage:   strings.TrimSpace(message),
		RequestID:      strings.TrimSpace(requestID),
		RequestedBy:    strings.TrimSpace(requestedBy),
		CreatedAt:      now,
		RenderedAt:     now,
		ExpiresAt:      expiresAt,
	}
	_ = item.UpdateKeys()
	return s.store.PutRenderArtifact(ctx, item)
}

func (s *Server) handleRetentionSweep(ctx *apptheory.EventContext, _ events.EventBridgeEvent) (any, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	if s.artifacts == nil {
		return nil, fmt.Errorf("artifact store not initialized")
	}
	if ctx == nil {
		return nil, fmt.Errorf("event context is nil")
	}

	now := time.Now().UTC()
	deleted := 0

	for ctx.RemainingMS <= 0 || ctx.RemainingMS >= 5000 {

		items, err := s.store.ListExpiredRenderArtifacts(ctx.Context(), now, 25)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			break
		}

		for _, item := range items {
			if item == nil {
				continue
			}
			_ = s.artifacts.DeleteObject(ctx.Context(), item.ThumbnailObjectKey)
			_ = s.artifacts.DeleteObject(ctx.Context(), item.SnapshotObjectKey)
			_ = s.store.DeleteRenderArtifact(ctx.Context(), item.ID)
			deleted++
		}
	}

	return map[string]any{
		"deleted": deleted,
	}, nil
}

func sqsQueueNameFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func maxTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return b
	}
	return a
}

func classOutOrExisting(existing, desired string) string {
	if strings.TrimSpace(desired) == models.RenderRetentionClassEvidence {
		return models.RenderRetentionClassEvidence
	}
	if strings.TrimSpace(existing) != "" {
		return existing
	}
	return models.RenderRetentionClassBenign
}

func safeURLString(u *url.URL) string {
	if u == nil {
		return ""
	}
	return strings.TrimSpace(u.String())
}
