package trust

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	"github.com/theory-cloud/tabletheory/pkg/core"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"

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
	}

	if !req.ForceRefresh {
		item, err := s.store.GetLinkPreview(ctx.Context(), previewID)
		if err == nil && !item.ExpiresAt.IsZero() && item.ExpiresAt.After(time.Now().UTC()) {
			return apptheory.JSON(http.StatusOK, linkPreviewResponseFromModel(ctx, item, true))
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
		return apptheory.JSON(http.StatusOK, linkPreviewResponseFromModel(ctx, item, false))
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

	return apptheory.JSON(http.StatusOK, linkPreviewResponseFromModel(ctx, item, false))
}

func (s *Server) handleGetLinkPreview(ctx *apptheory.Context) (*apptheory.Response, error) {
	if s == nil || s.store == nil || s.store.DB == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
	}
	if ctx == nil {
		return nil, &apptheory.AppError{Code: "app.internal", Message: "internal error"}
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

	return apptheory.JSON(http.StatusOK, linkPreviewResponseFromModel(ctx, item, true))
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
	body, contentType, etag, err := s.artifacts.getObject(ctx.Context(), key, linkPreviewMaxImageBytes)
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
	if err := s.artifacts.putObject(ctx, key, body, contentType, "public, max-age=86400, immutable"); err != nil {
		return "", ""
	}
	return imageID, key
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
