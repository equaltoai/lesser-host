package trust

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

	"github.com/equaltoai/lesser-host/internal/rendering"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type linkPreviewRequest struct {
	URL          string `json:"url"`
	ForceRefresh bool   `json:"force_refresh,omitempty"`
}

type linkPreviewResponse struct {
	Status string `json:"status"` // ok | blocked | error
	Cached bool   `json:"cached"`

	ID            string `json:"id"`
	PolicyVersion string `json:"policy_version"`

	NormalizedURL string   `json:"normalized_url"`
	ResolvedURL   string   `json:"resolved_url,omitempty"`
	RedirectChain []string `json:"redirect_chain,omitempty"`

	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`

	ImageID  string `json:"image_id,omitempty"`
	ImageURL string `json:"image_url,omitempty"`

	Render *renderArtifactResponse `json:"render,omitempty"`

	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`

	FetchedAt time.Time `json:"fetched_at,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

func (s *Server) handleLinkPreview(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	var req linkPreviewRequest
	if err := parseJSON(ctx, &req); err != nil {
		return nil, err
	}

	normalized, _, err := normalizeLinkURL(req.URL)
	if err != nil {
		if pe, ok := err.(*linkPreviewError); ok && pe.Code == "invalid_url" {
			return nil, &apptheory.AppError{Code: "app.bad_request", Message: pe.Message}
		}
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid url"}
	}

	previewID := linkPreviewID(linkPreviewPolicyVersion, normalized)

	// Respect per-instance config toggle (default: enabled).
	renderPolicy := "suspicious"
	{
		var inst models.Instance
		err := s.store.DB.WithContext(ctx.Context()).
			Model(&models.Instance{}).
			Where("PK", "=", "INSTANCE#"+instanceSlug).
			Where("SK", "=", models.SKMetadata).
			First(&inst)
		if err != nil && !theoryErrors.IsNotFound(err) {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
		}
		if err == nil && inst.HostedPreviewsEnabled != nil && !*inst.HostedPreviewsEnabled {
			return apptheory.JSON(http.StatusOK, linkPreviewResponse{
				Status:        "disabled",
				Cached:        false,
				ID:            previewID,
				PolicyVersion: linkPreviewPolicyVersion,
				NormalizedURL: normalized,
				ErrorCode:     "disabled",
				ErrorMessage:  "hosted previews disabled for instance",
				FetchedAt:     time.Now().UTC(),
				ExpiresAt:     time.Now().UTC().Add(5 * time.Minute),
			})
		}
		if err == nil {
			rp := strings.ToLower(strings.TrimSpace(inst.RenderPolicy))
			if rp == "always" || rp == "suspicious" {
				renderPolicy = rp
			}
		}
	}

	if !req.ForceRefresh {
		item, err := s.store.GetLinkPreview(ctx.Context(), previewID)
		if err == nil && !item.ExpiresAt.IsZero() && item.ExpiresAt.After(time.Now().UTC()) {
			resp := linkPreviewResponseFromModel(ctx, item, true)
			s.maybeAttachPreviewRender(ctx, instanceSlug, renderPolicy, normalized, &resp)
			return apptheory.JSON(http.StatusOK, resp)
		}
		if err != nil && !theoryErrors.IsNotFound(err) {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
		}
	}

	fetched, fetchErr := fetchLinkPreview(ctx.Context(), nil, normalized)

	now := time.Now().UTC()
	item := &models.LinkPreview{
		ID:            previewID,
		PolicyVersion: linkPreviewPolicyVersion,
		NormalizedURL: strings.TrimSpace(normalized),
		ResolvedURL:   strings.TrimSpace(fetched.ResolvedURL),
		RedirectChain: append([]string(nil), fetched.RedirectChain...),
		Title:         strings.TrimSpace(fetched.Title),
		Description:   strings.TrimSpace(fetched.Description),
		FetchedAt:     now,
		ExpiresAt:     now.Add(linkPreviewCacheTTL),
		StoredAt:      now,
		StoredBy:      instanceSlug,
		RequestID:     strings.TrimSpace(ctx.RequestID),
		SourceType:    "trust-api",
	}

	if fetchErr != nil {
		if pe, ok := fetchErr.(*linkPreviewError); ok {
			item.ErrorCode = pe.Code
			item.ErrorMessage = pe.Message
		} else {
			item.ErrorCode = "fetch_failed"
			item.ErrorMessage = "fetch failed"
		}
		if err := item.UpdateKeys(); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
		}
		audit := &models.AuditLogEntry{
			Actor:     instanceSlug,
			Action:    "link_preview.fetch",
			Target:    "link_preview:" + previewID,
			RequestID: strings.TrimSpace(ctx.RequestID),
			CreatedAt: now,
		}
		_ = audit.UpdateKeys()
		if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
			tx.Put(item)
			tx.Put(audit)
			return nil
		}); err != nil {
			return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to store preview"}
		}
		resp := linkPreviewResponseFromModel(ctx, item, false)
		s.maybeAttachPreviewRender(ctx, instanceSlug, renderPolicy, normalized, &resp)
		return apptheory.JSON(http.StatusOK, resp)
	}

	// Fetch and store image if available.
	if strings.TrimSpace(fetched.ImageURL) != "" && s.artifacts != nil {
		imageID, objectKey := s.tryStorePreviewImage(ctx.Context(), fetched.ImageURL)
		if imageID != "" && objectKey != "" {
			item.ImageID = imageID
			item.ImageObjectKey = objectKey
		}
	}

	if err := item.UpdateKeys(); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	audit := &models.AuditLogEntry{
		Actor:     instanceSlug,
		Action:    "link_preview.fetch",
		Target:    "link_preview:" + previewID,
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}
	_ = audit.UpdateKeys()
	if err := s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.Put(item)
		tx.Put(audit)
		return nil
	}); err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "failed to store preview"}
	}

	resp := linkPreviewResponseFromModel(ctx, item, false)
	s.maybeAttachPreviewRender(ctx, instanceSlug, renderPolicy, normalized, &resp)
	return apptheory.JSON(http.StatusOK, resp)
}

func (s *Server) handleGetLinkPreview(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	instanceSlug := strings.TrimSpace(ctx.AuthIdentity)
	if instanceSlug == "" {
		return nil, &apptheory.AppError{Code: "app.unauthorized", Message: "unauthorized"}
	}

	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "id is required"}
	}

	item, err := s.store.GetLinkPreview(ctx.Context(), id)
	if theoryErrors.IsNotFound(err) {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "preview not found"}
	}
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	renderPolicy := "suspicious"
	{
		var inst models.Instance
		err := s.store.DB.WithContext(ctx.Context()).
			Model(&models.Instance{}).
			Where("PK", "=", "INSTANCE#"+instanceSlug).
			Where("SK", "=", models.SKMetadata).
			First(&inst)
		if err == nil {
			rp := strings.ToLower(strings.TrimSpace(inst.RenderPolicy))
			if rp == "always" || rp == "suspicious" {
				renderPolicy = rp
			}
		}
	}

	resp := linkPreviewResponseFromModel(ctx, item, true)
	s.maybeAttachPreviewRender(ctx, instanceSlug, renderPolicy, strings.TrimSpace(item.NormalizedURL), &resp)
	return apptheory.JSON(http.StatusOK, resp)
}

var imageIDRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Server) handleGetLinkPreviewImage(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.artifacts == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}

	imageID := strings.TrimSpace(ctx.Param("imageId"))
	if !imageIDRE.MatchString(imageID) {
		return nil, &apptheory.AppError{Code: "app.bad_request", Message: "invalid image id"}
	}

	key := linkPreviewImageObjectKey(imageID)
	body, contentType, etag, err := s.artifacts.GetObject(ctx.Context(), key, linkPreviewMaxImageBytes)
	if err != nil {
		return nil, &apptheory.AppError{Code: "app.not_found", Message: "image not found"}
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = http.DetectContentType(body)
	}

	resp := apptheory.Binary(http.StatusOK, body, contentType)
	if resp.Headers == nil {
		resp.Headers = map[string][]string{}
	}
	resp.Headers["cache-control"] = []string{"public, max-age=86400, immutable"}
	if strings.TrimSpace(etag) != "" {
		resp.Headers["etag"] = []string{etag}
	}
	return resp, nil
}

func (s *Server) tryStorePreviewImage(ctx context.Context, rawImageURL string) (string, string) {
	rawImageURL = strings.TrimSpace(rawImageURL)
	if rawImageURL == "" || s == nil || s.artifacts == nil {
		return "", ""
	}

	imgNormalized, imgURL, err := normalizeLinkURL(rawImageURL)
	if err != nil {
		return "", ""
	}
	if err := validateOutboundURL(ctx, nil, imgURL); err != nil {
		return "", ""
	}

	client := newPreviewHTTPClient(linkPreviewFetchTimeout)
	_, _, body, contentType, err := fetchWithRedirects(ctx, nil, client, imgURL, linkPreviewMaxRedirects, linkPreviewMaxImageBytes)
	if err != nil {
		return "", ""
	}

	if strings.TrimSpace(contentType) == "" {
		contentType = http.DetectContentType(body)
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return "", ""
	}

	imageID := imageIDFromNormalizedURL(imgNormalized)
	key := linkPreviewImageObjectKey(imageID)
	if err := s.artifacts.PutObject(ctx, key, body, contentType, "public, max-age=86400, immutable"); err != nil {
		return "", ""
	}
	return imageID, key
}

func (s *Server) maybeAttachPreviewRender(ctx *apptheory.Context, instanceSlug string, renderPolicy string, normalizedURL string, resp *linkPreviewResponse) {
	if s == nil || s.store == nil || s.store.DB == nil || ctx == nil || resp == nil {
		return
	}
	if strings.TrimSpace(normalizedURL) == "" {
		return
	}
	if strings.TrimSpace(resp.Status) != "ok" {
		return
	}

	renderPolicy = strings.ToLower(strings.TrimSpace(renderPolicy))
	if renderPolicy != "always" && renderPolicy != "suspicious" {
		renderPolicy = "suspicious"
	}

	analysis := analyzeLinkSafetyBasic(ctx.Context(), nil, normalizedURL)
	risk := strings.ToLower(strings.TrimSpace(analysis.Risk))
	if !shouldRenderLink(renderPolicy, risk) {
		return
	}

	renderID := rendering.RenderArtifactID(rendering.RenderPolicyVersion, normalizedURL)

	// Cache hit path (no debit).
	if existing, err := s.store.GetRenderArtifact(ctx.Context(), renderID); err == nil && existing != nil {
		r := renderArtifactResponseFromModel(ctx, existing, true)
		resp.Render = &r
		return
	}

	// Best-effort: only queue if budget allows and the queue is configured.
	if strings.TrimSpace(s.cfg.PreviewQueueURL) == "" || s.queues == nil {
		return
	}

	now := time.Now().UTC()
	month := now.Format("2006-01")

	pk := fmt.Sprintf("INSTANCE#%s", instanceSlug)
	sk := fmt.Sprintf("BUDGET#%s", month)

	var budget models.InstanceBudgetMonth
	err := s.store.DB.WithContext(ctx.Context()).
		Model(&models.InstanceBudgetMonth{}).
		Where("PK", "=", pk).
		Where("SK", "=", sk).
		ConsistentRead().
		First(&budget)
	if err != nil {
		return
	}

	remaining := budget.IncludedCredits - budget.UsedCredits
	if remaining < linkRenderCreditCost {
		return
	}

	update := &models.InstanceBudgetMonth{
		InstanceSlug: instanceSlug,
		Month:        month,
		UpdatedAt:    now,
	}
	_ = update.UpdateKeys()

	auditBudget := &models.AuditLogEntry{
		Actor:     instanceSlug,
		Action:    "budget.debit",
		Target:    fmt.Sprintf("instance_budget:%s:%s", instanceSlug, month),
		RequestID: strings.TrimSpace(ctx.RequestID),
		CreatedAt: now,
	}
	_ = auditBudget.UpdateKeys()

	err = s.store.DB.TransactWrite(ctx.Context(), func(tx core.TransactionBuilder) error {
		tx.UpdateWithBuilder(update, func(ub core.UpdateBuilder) error {
			ub.Add("UsedCredits", linkRenderCreditCost)
			ub.Set("UpdatedAt", now)
			return nil
		},
			tabletheory.IfExists(),
			tabletheory.ConditionExpression(
				"if_not_exists(usedCredits, :zero) + :delta <= if_not_exists(includedCredits, :zero)",
				map[string]any{
					":zero":  int64(0),
					":delta": linkRenderCreditCost,
				},
			),
		)
		tx.Put(auditBudget)
		return nil
	})
	if err != nil {
		return
	}

	retentionClass := retentionClassForRisk(risk)
	artifact, queued, err := s.queueRender(ctx, normalizedURL, retentionClass, 0)
	if err != nil || artifact == nil {
		return
	}

	if queued {
		audit := &models.AuditLogEntry{
			Actor:     instanceSlug,
			Action:    "render.queue",
			Target:    fmt.Sprintf("render:%s", strings.TrimSpace(artifact.ID)),
			RequestID: strings.TrimSpace(ctx.RequestID),
			CreatedAt: now,
		}
		_ = audit.UpdateKeys()
		_ = s.store.DB.WithContext(ctx.Context()).Model(audit).Create()
	}

	r := renderArtifactResponseFromModel(ctx, artifact, !queued)
	resp.Render = &r
}

func linkPreviewResponseFromModel(ctx *apptheory.Context, item *models.LinkPreview, cached bool) linkPreviewResponse {
	resp := linkPreviewResponse{
		Cached: cached,

		ID:            strings.TrimSpace(item.ID),
		PolicyVersion: strings.TrimSpace(item.PolicyVersion),
		NormalizedURL: strings.TrimSpace(item.NormalizedURL),
		ResolvedURL:   strings.TrimSpace(item.ResolvedURL),
		RedirectChain: append([]string(nil), item.RedirectChain...),
		Title:         strings.TrimSpace(item.Title),
		Description:   strings.TrimSpace(item.Description),
		ImageID:       strings.TrimSpace(item.ImageID),
		ErrorCode:     strings.TrimSpace(item.ErrorCode),
		ErrorMessage:  strings.TrimSpace(item.ErrorMessage),
		FetchedAt:     item.FetchedAt,
		ExpiresAt:     item.ExpiresAt,
	}

	if resp.ErrorCode != "" {
		if resp.ErrorCode == "blocked_ssrf" {
			resp.Status = "blocked"
		} else if resp.ErrorCode == "disabled" {
			resp.Status = "disabled"
		} else {
			resp.Status = "error"
		}
	} else {
		resp.Status = "ok"
	}

	if resp.ImageID != "" {
		base := requestBaseURL(ctx)
		path := "/api/v1/previews/images/" + resp.ImageID
		if base != "" {
			resp.ImageURL = base + path
		} else {
			resp.ImageURL = path
		}
	}
	return resp
}

func requestBaseURL(ctx *apptheory.Context) string {
	if ctx == nil {
		return ""
	}
	host := strings.TrimSpace(firstHeaderValue(ctx.Request.Headers, "x-forwarded-host"))
	if host == "" {
		host = strings.TrimSpace(firstHeaderValue(ctx.Request.Headers, "host"))
	}
	if host == "" {
		return ""
	}
	proto := strings.TrimSpace(firstHeaderValue(ctx.Request.Headers, "x-forwarded-proto"))
	if proto == "" {
		proto = "https"
	}
	return proto + "://" + host
}

func linkPreviewImageObjectKey(imageID string) string {
	return "link-previews/images/" + strings.TrimSpace(imageID)
}

func imageIDFromNormalizedURL(normalized string) string {
	sum := sha256.Sum256([]byte("img:" + normalized))
	return hex.EncodeToString(sum[:])
}
